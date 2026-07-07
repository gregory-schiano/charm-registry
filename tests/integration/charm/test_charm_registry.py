"""Jubilant integration tests for the charm-registry charm."""

from __future__ import annotations

import io
import json
import logging
import os
import socket
import subprocess
import time
import zipfile
from collections.abc import Iterator
from contextlib import contextmanager
from typing import Any

import jubilant
import psycopg
import requests

logger = logging.getLogger(__name__)


def _auth_header(subject: str, username: str) -> str:
    """Return a development auth header accepted by the integration deployment."""
    return f"Bearer dev:{subject}:{username}"


def _build_charm_archive(name: str) -> bytes:
    """Build a minimal charm archive for API-level artifact assertions."""
    metadata = f"""name: {name}
summary: Integration test charm
description: A charm for integration testing
"""
    manifest = """bases:
  - name: ubuntu
    channel: "22.04"
    architectures:
      - amd64
"""
    archive = io.BytesIO()
    with zipfile.ZipFile(archive, "w") as charm_zip:
        charm_zip.writestr("metadata.yaml", metadata)
        charm_zip.writestr("manifest.yaml", manifest)
    return archive.getvalue()


def _api_request(
    deployed: dict[str, Any],
    method: str,
    path: str,
    *,
    auth: str,
    **kwargs: Any,
) -> requests.Response:
    """Issue an API request against the deployed registry."""
    resp = requests.request(
        method,
        f"{deployed['api_url']}{path}",
        headers={"Authorization": auth},
        timeout=30,
        **kwargs,
    )
    return resp


def _register_charm(deployed: dict[str, Any], name: str, auth: str) -> str:
    """Register a charm package and return its package ID."""
    resp = _api_request(
        deployed,
        "POST",
        "/v1/charm",
        auth=auth,
        json={"name": name, "type": "charm"},
    )
    assert resp.status_code == 201, resp.text
    package_id = resp.json().get("id")
    assert package_id
    return package_id


def _upload_charm(
    deployed: dict[str, Any], name: str, archive: bytes, auth: str
) -> str:
    """Upload a charm archive and return its upload ID."""
    resp = _api_request(
        deployed,
        "POST",
        "/unscanned-upload/",
        auth=auth,
        files={"binary": (f"{name}.charm", archive, "application/zip")},
    )
    assert resp.status_code == 200, resp.text
    upload_id = resp.json().get("upload_id")
    assert upload_id
    return upload_id


def _push_revision(
    deployed: dict[str, Any], name: str, upload_id: str, auth: str
) -> None:
    """Push a charm revision from an upload ID."""
    resp = _api_request(
        deployed,
        "POST",
        f"/v1/charm/{name}/revisions",
        auth=auth,
        json={"upload-id": upload_id},
    )
    assert resp.status_code == 201, resp.text


def _microceph_bucket_object_count(bucket: str) -> int:
    """Return the number of RGW objects in a MicroCeph bucket."""
    result = subprocess.run(
        [
            "sudo",
            "microceph.radosgw-admin",
            "bucket",
            "stats",
            "--bucket",
            bucket,
            "--format",
            "json",
        ],
        check=True,
        capture_output=True,
        text=True,
    )
    stats = json.loads(result.stdout)
    usage = stats.get("usage", {}).get("rgw.main", {})
    return int(usage.get("num_objects", 0))


def _free_local_port() -> int:
    """Return an available local TCP port for a short-lived port-forward."""
    with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as sock:
        sock.bind(("127.0.0.1", 0))
        return int(sock.getsockname()[1])


def _k8s_namespace(juju: jubilant.Juju) -> str:
    """Return the Kubernetes namespace backing the current Juju model."""
    model = json.loads(juju.cli("show-model", "--format", "json"))
    model_data = next(iter(model.values()))
    return str(model_data["model-name"])


def _postgres_service(juju: jubilant.Juju, database_app: str) -> tuple[str, str]:
    """Return the Kubernetes namespace and service for the PostgreSQL primary."""
    model_namespace = _k8s_namespace(juju)
    services = json.loads(
        subprocess.check_output(
            ["kubectl", "get", "service", "--all-namespaces", "-o", "json"],
            text=True,
        )
    )
    candidate_names = {
        f"{database_app}-primary",
        f"{database_app}-endpoints",
        database_app,
    }
    candidates: list[tuple[str, str]] = []
    for item in services.get("items", []):
        metadata = item.get("metadata", {})
        name = metadata.get("name", "")
        namespace = metadata.get("namespace", "")
        labels = metadata.get("labels", {})
        if name in candidate_names or labels.get("app.juju.is/name") == database_app:
            candidates.append((namespace, name))

    for namespace, name in candidates:
        if namespace == model_namespace and name == f"{database_app}-primary":
            return namespace, name
    for namespace, name in candidates:
        if name == f"{database_app}-primary":
            return namespace, name
    if candidates:
        return candidates[0]
    raise RuntimeError(f"could not find Kubernetes service for {database_app!r}")


@contextmanager
def _postgres_port_forward(
    juju: jubilant.Juju,
    database_app: str,
) -> Iterator[int]:
    """Forward the PostgreSQL service to localhost and yield the local port."""
    namespace, service = _postgres_service(juju, database_app)
    local_port = _free_local_port()
    proc = subprocess.Popen(
        [
            "kubectl",
            "-n",
            namespace,
            "port-forward",
            f"service/{service}",
            f"{local_port}:5432",
        ],
        stdout=subprocess.PIPE,
        stderr=subprocess.STDOUT,
        text=True,
    )
    try:
        deadline = time.monotonic() + 30
        while time.monotonic() < deadline:
            if proc.poll() is not None:
                break
            try:
                with socket.create_connection(("127.0.0.1", local_port), timeout=1):
                    yield local_port
                    return
            except OSError:
                time.sleep(0.5)
        output = proc.stdout.read() if proc.stdout is not None else ""
        raise RuntimeError("PostgreSQL port-forward did not become ready:\n" + output)
    finally:
        proc.terminate()
        try:
            proc.wait(timeout=10)
        except subprocess.TimeoutExpired:
            proc.kill()
            proc.wait(timeout=10)


def _postgres_query_package_count(
    juju: jubilant.Juju,
    database_app: str,
    package_name: str,
) -> int:
    """Query PostgreSQL directly for package rows matching ``package_name``."""
    password_task = juju.run(
        f"{database_app}/0",
        "get-password",
        {"username": "operator"},
        wait=60,
    )
    password_task.raise_on_failure()
    password = password_task.results.get("password")
    assert password

    with _postgres_port_forward(juju, database_app) as port:
        with psycopg.connect(
            host="127.0.0.1",
            port=port,
            dbname="postgres",
            user="operator",
            password=str(password),
            connect_timeout=10,
        ) as conn:
            with conn.cursor() as cur:
                cur.execute(
                    "select datname from pg_database "
                    "where datistemplate = false and datname <> 'postgres'"
                )
                databases = [str(row[0]) for row in cur.fetchall()]

        for database in databases:
            try:
                with psycopg.connect(
                    host="127.0.0.1",
                    port=port,
                    dbname=database,
                    user="operator",
                    password=str(password),
                    connect_timeout=10,
                ) as conn:
                    with conn.cursor() as cur:
                        cur.execute(
                            "select count(*) from packages where name = %s",
                            (package_name,),
                        )
                        row = cur.fetchone()
                        if row is not None:
                            return int(row[0])
            except psycopg.errors.UndefinedTable:
                continue
    return 0


class TestCharmDeployment:
    """Verify the charm deploys and serves the registry API."""

    def test_application_active(self, deployed: dict[str, Any]):
        """Application reports active status in Juju."""
        juju: jubilant.Juju = deployed["juju"]
        status = juju.status()
        app_status = status.apps[deployed["app"]].app_status

        logger.info("Application status: %s %s", app_status.current, app_status.message)
        assert app_status.current == "active"

    def test_postgresql_relation_active(self, deployed: dict[str, Any]):
        """The deployment uses the PostgreSQL relation instead of SQLite-only mode."""
        juju: jubilant.Juju = deployed["juju"]
        status = juju.status()
        database_status = status.apps[deployed["database_app"]].app_status
        relation_status = juju.cli("status", "--relations")

        logger.info(
            "PostgreSQL status: %s %s",
            database_status.current,
            database_status.message,
        )
        assert database_status.current == "active"
        assert f"{deployed['app']}:postgresql" in relation_status
        assert f"{deployed['database_app']}:database" in relation_status

    def test_s3_relation_active(self, deployed: dict[str, Any]):
        """The deployment uses the S3 relation for artifact storage."""
        juju: jubilant.Juju = deployed["juju"]
        status = juju.status()
        s3_status = status.apps[deployed["s3_app"]].app_status
        relation_status = juju.cli("status", "--relations")

        logger.info("S3 integrator status: %s %s", s3_status.current, s3_status.message)
        assert s3_status.current == "active"
        assert f"{deployed['app']}:s3" in relation_status
        assert f"{deployed['s3_app']}:s3-credentials" in relation_status

    def test_self_signed_certificates_active(self, deployed: dict[str, Any]):
        """Self-signed certificates are issued to the Gateway API integrator."""
        juju: jubilant.Juju = deployed["juju"]
        relation_status = juju.cli("status", "--relations")
        task = juju.run(
            f"{deployed['certificates_app']}/0",
            "get-ca-certificate",
            wait=60,
        )
        task.raise_on_failure()
        ca_certificate = task.results.get("ca-certificate", "")

        logger.info("CA certificate output:\n%s", ca_certificate)
        assert "BEGIN CERTIFICATE" in ca_certificate
        assert f"{deployed['certificates_app']}:certificates" in relation_status
        assert f"{deployed['gateway_app']}:certificates" in relation_status

    def test_gateway_api_ingress_active(self, deployed: dict[str, Any]):
        """Both ingress-configurator applications are related to Gateway API."""
        juju: jubilant.Juju = deployed["juju"]
        relation_status = juju.cli("status", "--relations")

        assert f"{deployed['api_ingress_app']}:gateway-route" in relation_status
        assert f"{deployed['oci_ingress_app']}:gateway-route" in relation_status
        assert f"{deployed['gateway_app']}:gateway-route" in relation_status

    def test_health_endpoint(self, deployed: dict[str, Any]):
        """GET /healthz returns status=ok."""
        resp = requests.get(f"{deployed['api_url']}/healthz", timeout=15)

        assert resp.status_code == 200, resp.text
        assert resp.json().get("status") == "ok"

    def test_ready_endpoint(self, deployed: dict[str, Any]):
        """GET /readyz returns status=ready."""
        resp = requests.get(f"{deployed['api_url']}/readyz", timeout=15)

        assert resp.status_code == 200, resp.text
        assert resp.json().get("status") == "ready"

    def test_root_document(self, deployed: dict[str, Any]):
        """GET / returns the service metadata document."""
        resp = requests.get(f"{deployed['api_url']}/", timeout=15)

        assert resp.status_code == 200, resp.text
        assert resp.json().get("service-name") == "private-charm-registry"

    def test_s3_artifact_round_trip_and_postgresql_row(self, deployed: dict[str, Any]):
        """A pushed charm artifact is stored in RGW and indexed in PostgreSQL."""
        auth = _auth_header("s3-user", "admin")
        name = f"itest-s3-artifact-{os.getpid()}-{time.time_ns()}"
        archive = _build_charm_archive(name)
        package_id = _register_charm(deployed, name, auth)

        before_objects = None
        if deployed["s3_config"].get("microceph") == "true":
            before_objects = _microceph_bucket_object_count(
                deployed["s3_config"]["bucket"]
            )

        upload_id = _upload_charm(deployed, name, archive, auth)
        _push_revision(deployed, name, upload_id, auth)

        resp = _api_request(
            deployed,
            "GET",
            f"/api/v1/charms/download/{package_id}_1.charm",
            auth=auth,
        )
        assert resp.status_code == 200, resp.text
        assert resp.content == archive

        if before_objects is not None:
            after_objects = _microceph_bucket_object_count(
                deployed["s3_config"]["bucket"]
            )
            assert after_objects > before_objects

        count = _postgres_query_package_count(
            deployed["juju"],
            deployed["database_app"],
            name,
        )
        assert count == 1


class TestFunctionalScenarios:
    """Run the shared endpoint-level scenarios against the deployed charm."""

    def test_all_scenarios(self, deployed: dict[str, Any], functional_test_binary):
        """The shared functional scenario binary passes against the Juju deployment."""
        env = os.environ.copy()
        env.update(
            {
                "FTEST_API_URL": deployed["api_url"],
                "FTEST_ADMIN_SUBJECT": "admin",
                "FTEST_ADMIN_USER": "admin",
                "FTEST_OCI_URL": deployed["oci_url"],
            }
        )

        result = subprocess.run(
            [str(functional_test_binary)],
            env=env,
            capture_output=True,
            text=True,
            timeout=5 * 60,
            check=False,
        )

        logger.info("functional-test stdout:\n%s", result.stdout)
        if result.stderr:
            logger.warning("functional-test stderr:\n%s", result.stderr)

        assert result.returncode == 0, (
            f"functional-test exited {result.returncode}\n"
            f"stdout:\n{result.stdout}\nstderr:\n{result.stderr}"
        )
