"""Jubilant fixtures for charm-registry charm integration tests."""

from __future__ import annotations

import logging
import os
import pathlib
import re
import shutil
import subprocess
import time
from collections.abc import Iterator
from typing import Any

import jubilant
import pytest

logger = logging.getLogger(__name__)

REPO_ROOT = pathlib.Path(__file__).resolve().parents[3]
DEFAULT_S3_BUCKET = "charm-registry-artifacts"
DEFAULT_S3_REGION = "us-east-1"
DEFAULT_RGW_PORT = "8081"


def _run(cmd: list[str], **kwargs: Any) -> subprocess.CompletedProcess[str]:
    logger.info("RUN: %s", " ".join(cmd))
    kwargs.setdefault("check", True)
    kwargs.setdefault("capture_output", True)
    kwargs.setdefault("text", True)
    return subprocess.run(cmd, **kwargs)


def _require_cli(name: str) -> str:
    path = shutil.which(name)
    if path is None:
        pytest.skip(f"Prerequisite CLI tool {name!r} not found on PATH")
    return path


def _first_host_ip() -> str:
    """Return a non-loopback host address reachable from the local K8s cluster."""
    result = _run(["hostname", "-I"])
    for address in result.stdout.split():
        if not address.startswith("127."):
            return address
    return "127.0.0.1"


def _microceph_node_name(status: str) -> str:
    """Extract the first MicroCeph node name from ``microceph status`` output."""
    for line in status.splitlines():
        match = re.match(r"^-\s+([^\s(]+)", line.strip())
        if match:
            return match.group(1)
    pytest.fail(f"Could not determine MicroCeph node name from status:\n{status}")


def _microceph_has_rgw(status: str) -> bool:
    """Return whether MicroCeph status reports an RGW service."""
    return any("Services:" in line and "rgw" in line for line in status.splitlines())


def _external_s3_config() -> dict[str, str] | None:
    """Return externally supplied S3 config, if all required values are present."""
    endpoint = os.environ.get("JUB_S3_ENDPOINT")
    access_key = os.environ.get("JUB_S3_ACCESS_KEY")
    secret_key = os.environ.get("JUB_S3_SECRET_KEY")
    if not (endpoint and access_key and secret_key):
        return None
    return {
        "endpoint": endpoint.rstrip("/"),
        "bucket": os.environ.get("JUB_S3_BUCKET", DEFAULT_S3_BUCKET),
        "region": os.environ.get("JUB_S3_REGION", DEFAULT_S3_REGION),
        "access_key": access_key,
        "secret_key": secret_key,
        "path": os.environ.get("JUB_S3_PATH", ""),
        "uri_style": os.environ.get("JUB_S3_URI_STYLE", "path"),
        "microceph": "false",
    }


@pytest.fixture(scope="session")
def s3_config() -> dict[str, str]:
    """Provision or return S3-compatible storage for charm integration tests."""
    external = _external_s3_config()
    if external:
        logger.info("Using externally supplied S3 endpoint: %s", external["endpoint"])
        return external

    _require_cli("snap")
    access_key = os.environ.get("JUB_S3_ACCESS_KEY", "charm-registry")
    secret_key = os.environ.get("JUB_S3_SECRET_KEY", "charm-registry-secret")
    bucket = os.environ.get("JUB_S3_BUCKET", DEFAULT_S3_BUCKET)
    region = os.environ.get("JUB_S3_REGION", DEFAULT_S3_REGION)
    port = os.environ.get("JUB_MICROCEPH_RGW_PORT", DEFAULT_RGW_PORT)
    host = os.environ.get("JUB_MICROCEPH_RGW_HOST", _first_host_ip())
    endpoint = os.environ.get("JUB_S3_ENDPOINT", f"http://{host}:{port}").rstrip("/")

    snap_list = subprocess.run(
        ["snap", "list", "microceph"],
        check=False,
        capture_output=True,
    )
    if snap_list.returncode != 0:
        _run(["sudo", "snap", "install", "microceph"])
    _run(["sudo", "snap", "refresh", "--hold", "microceph"])

    status_result = subprocess.run(
        ["sudo", "microceph", "status"],
        check=False,
        capture_output=True,
        text=True,
    )
    if status_result.returncode != 0:
        _run(["sudo", "microceph", "cluster", "bootstrap"])
        _run(["sudo", "microceph", "disk", "add", "loop,4G,3"])
    else:
        logger.info("Reusing existing MicroCeph cluster:\n%s", status_result.stdout)
        if "Disks: 0" in status_result.stdout:
            _run(["sudo", "microceph", "disk", "add", "loop,4G,3"])

    for _ in range(60):
        status_result = _run(["sudo", "microceph", "status"])
        if "Disks: 3" in status_result.stdout or " osd" in status_result.stdout:
            break
        time.sleep(5)
    status = status_result.stdout
    node_name = _microceph_node_name(status)
    if not _microceph_has_rgw(status):
        _run(
            [
                "sudo",
                "microceph",
                "enable",
                "rgw",
                "--target",
                node_name,
                "--port",
                port,
            ]
        )

    create_user = _run(
        [
            "sudo",
            "microceph.radosgw-admin",
            "user",
            "create",
            "--uid",
            "charm-registry",
            "--display-name",
            "charm-registry integration tests",
            "--access-key",
            access_key,
            "--secret-key",
            secret_key,
        ],
        check=False,
    )
    if create_user.returncode != 0:
        logger.info("Reusing existing RGW user charm-registry: %s", create_user.stderr)

    for _ in range(60):
        rgw_check = subprocess.run(
            ["curl", "--max-time", "2", "-sS", "-o", "/dev/null", endpoint],
            check=False,
            capture_output=True,
        )
        if rgw_check.returncode == 0:
            break
        time.sleep(2)
    else:
        pytest.fail(f"MicroCeph RGW endpoint did not become reachable: {endpoint}")

    logger.info("MicroCeph RGW endpoint for charm workload: %s", endpoint)
    return {
        "endpoint": endpoint,
        "bucket": bucket,
        "region": region,
        "access_key": access_key,
        "secret_key": secret_key,
        "path": os.environ.get("JUB_S3_PATH", ""),
        "uri_style": os.environ.get("JUB_S3_URI_STYLE", "path"),
        "microceph": "true",
    }


@pytest.fixture(scope="session")
def repo_root() -> pathlib.Path:
    """Return the repository root."""
    return REPO_ROOT


@pytest.fixture(scope="session")
def functional_test_binary(repo_root: pathlib.Path) -> pathlib.Path:
    """Build the shared functional-test binary once per test session."""
    _require_cli("go")
    bin_dir = repo_root / ".bin"
    bin_dir.mkdir(exist_ok=True)
    binary = bin_dir / "functional-test"
    _run(["go", "build", "-o", str(binary), "./cmd/functional-test"], cwd=repo_root)
    return binary


@pytest.fixture(scope="session")
def charm_file(request: pytest.FixtureRequest, repo_root: pathlib.Path) -> pathlib.Path:
    """Return the built charm artifact.

    In charm-ci this comes from opcli's ``charm_paths`` fixture. When running
    locally without opcli, fall back to packing the charm from ``charm/``.
    """
    try:
        charm_paths = request.getfixturevalue("charm_paths")
    except pytest.FixtureLookupError:
        _require_cli("charmcraft")
        charm_dir = repo_root / "charm"
        _run(["charmcraft", "pack", "--project-dir", str(charm_dir)])
        charms = sorted(charm_dir.glob("*.charm"), key=os.path.getmtime, reverse=True)
        if not charms:
            pytest.fail("charmcraft pack produced no .charm file")
        charm_path = charms[0]
    else:
        charm_path = pathlib.Path(charm_paths["charm-registry"].path)

    logger.info("Charm artifact: %s", charm_path)
    return charm_path


@pytest.fixture(scope="session")
def app_image(request: pytest.FixtureRequest) -> str:
    """Return the OCI image reference for the charm's app-image resource."""
    try:
        charm_resource_images = request.getfixturevalue("charm_resource_images")
    except pytest.FixtureLookupError:
        image = os.environ.get("JUB_APP_IMAGE", "")
    else:
        image = charm_resource_images["charm-registry"]["app-image"]

    if not image:
        pytest.skip("No app-image resource available; set JUB_APP_IMAGE for local runs")
    logger.info("app-image resource: %s", image)
    return image


@pytest.fixture(scope="session")
def juju() -> Iterator[jubilant.Juju]:
    """Provide a temporary Juju model."""
    _require_cli("juju")
    existing = os.environ.get("JUB_MODEL")
    if existing:
        logger.info("Reusing existing Juju model: %s", existing)
        yield jubilant.Juju(model=existing)
        return

    with jubilant.temp_model() as juju_model:
        yield juju_model


@pytest.fixture(scope="session")
def deployed(
    juju: jubilant.Juju,
    charm_file: pathlib.Path,
    app_image: str,
    functional_test_binary: pathlib.Path,
    s3_config: dict[str, str],
) -> dict[str, Any]:
    """Deploy charm-registry with Gateway API-backed ingress relations."""
    del functional_test_binary
    app = "charm-registry"
    api_ingress_app = "ingress-api"
    oci_ingress_app = "ingress-oci"
    gateway_app = "gateway-api-integrator"
    database_app = "postgresql"
    s3_app = "s3-integrator"
    certificates_app = "self-signed-certificates"
    ingress_charm = os.environ.get("JUB_INGRESS_CHARM", "ingress-configurator")
    ingress_channel = os.environ.get("JUB_INGRESS_CHANNEL", "latest/edge")
    gateway_charm = os.environ.get("JUB_GATEWAY_CHARM", "gateway-api-integrator")
    gateway_channel = os.environ.get("JUB_GATEWAY_CHANNEL", "1/edge")
    gateway_class = os.environ.get("JUB_GATEWAY_CLASS", "ck-gateway")
    api_hostname = os.environ.get("JUB_API_HOSTNAME", "api.charm-registry.test")
    oci_hostname = os.environ.get("JUB_OCI_HOSTNAME", "oci.charm-registry.test")
    postgresql_charm = os.environ.get("JUB_POSTGRESQL_CHARM", "postgresql-k8s")
    postgresql_channel = os.environ.get("JUB_POSTGRESQL_CHANNEL", "14/stable")
    s3_charm = os.environ.get("JUB_S3_INTEGRATOR_CHARM", "s3-integrator")
    s3_channel = os.environ.get("JUB_S3_INTEGRATOR_CHANNEL", "2/stable")
    certificates_charm = os.environ.get(
        "JUB_CERTIFICATES_CHARM",
        "self-signed-certificates",
    )
    certificates_channel = os.environ.get("JUB_CERTIFICATES_CHANNEL", "1/stable")

    logger.info("Deploying %s as %s", gateway_charm, gateway_app)
    juju.deploy(
        gateway_charm,
        gateway_app,
        channel=gateway_channel,
        config={"gateway-class": gateway_class},
        trust=True,
    )
    logger.info("Deploying %s as %s", ingress_charm, api_ingress_app)
    juju.deploy(
        ingress_charm,
        api_ingress_app,
        channel=ingress_channel,
        config={"hostname": api_hostname},
        trust=True,
    )
    logger.info("Deploying %s as %s", ingress_charm, oci_ingress_app)
    juju.deploy(
        ingress_charm,
        oci_ingress_app,
        channel=ingress_channel,
        config={"hostname": oci_hostname},
        trust=True,
    )
    logger.info("Deploying %s as %s", postgresql_charm, database_app)
    juju.deploy(postgresql_charm, database_app, channel=postgresql_channel, trust=True)
    logger.info("Deploying %s as %s", s3_charm, s3_app)
    juju.deploy(
        s3_charm,
        s3_app,
        channel=s3_channel,
        config={
            "bucket": s3_config["bucket"],
            "endpoint": s3_config["endpoint"],
            "path": s3_config["path"],
            "region": s3_config["region"],
            "s3-uri-style": s3_config["uri_style"],
        },
    )
    secret_name = f"s3-integrator-credentials-{int(time.time())}"
    secret_result = juju.cli(
        "add-secret",
        secret_name,
        f"access-key={s3_config['access_key']}",
        f"secret-key={s3_config['secret_key']}",
    )
    secret_uri = secret_result.strip().splitlines()[-1].strip()
    juju.cli("grant-secret", secret_name, s3_app)
    juju.cli("config", s3_app, f"credentials={secret_uri}")

    logger.info("Deploying %s as %s", certificates_charm, certificates_app)
    juju.deploy(certificates_charm, certificates_app, channel=certificates_channel)

    logger.info("Deploying %s from %s", app, charm_file)
    juju.deploy(
        charm=str(charm_file),
        app=app,
        resources={"app-image": app_image},
        config={
            "admin-usernames": "admin",
            "app-secret-key": "integration-test-secret",
            "enable-insecure-dev-auth": True,
            "max-archive-file-bytes": "64MB",
            "rate-limit-ip-limit": 0,
            "rate-limit-token-limit": 0,
        },
    )

    juju.integrate(f"{app}:ingress", f"{api_ingress_app}:ingress")
    juju.integrate(f"{app}:oci-ingress", f"{oci_ingress_app}:ingress")
    juju.integrate(f"{app}:postgresql", f"{database_app}:database")
    juju.integrate(f"{app}:s3", f"{s3_app}:s3-credentials")
    juju.integrate(
        f"{certificates_app}:certificates",
        f"{gateway_app}:certificates",
    )
    juju.integrate(f"{api_ingress_app}:gateway-route", f"{gateway_app}:gateway-route")
    juju.integrate(f"{oci_ingress_app}:gateway-route", f"{gateway_app}:gateway-route")

    logger.info("Waiting for active/idle deployment")
    juju.wait(jubilant.all_active, timeout=30 * 60, delay=10, successes=3)
    time.sleep(10)

    status = juju.status()
    unit = status.apps[app].units.get(f"{app}/0")
    if unit is None or not unit.address:
        pytest.fail("charm-registry/0 has no address")

    api_url = os.environ.get("JUB_API_URL", f"http://{unit.address}:8080")
    oci_url = os.environ.get("JUB_OCI_URL", f"http://{unit.address}:5000")

    logger.info("Deployed endpoints: api=%s oci=%s", api_url, oci_url)
    return {
        "juju": juju,
        "app": app,
        "unit": f"{app}/0",
        "api_ingress_app": api_ingress_app,
        "oci_ingress_app": oci_ingress_app,
        "gateway_app": gateway_app,
        "database_app": database_app,
        "s3_app": s3_app,
        "s3_config": s3_config,
        "certificates_app": certificates_app,
        "api_url": api_url,
        "oci_url": oci_url,
    }
