"""Jubilant integration tests for the charm-registry charm."""

from __future__ import annotations

import logging
import os
import subprocess
from typing import Any

import jubilant
import requests

logger = logging.getLogger(__name__)


class TestCharmDeployment:
    """Verify the charm deploys and serves the registry API."""

    def test_application_active(self, deployed: dict[str, Any]):
        """Application reports active status in Juju."""
        juju: jubilant.Juju = deployed["juju"]
        status = juju.status()
        app_status = status.apps[deployed["app"]].application_status

        logger.info("Application status: %s %s", app_status.current, app_status.message)
        assert app_status.current == "active"

    def test_postgresql_relation_active(self, deployed: dict[str, Any]):
        """The deployment uses the PostgreSQL relation instead of SQLite-only mode."""
        juju: jubilant.Juju = deployed["juju"]
        status = juju.status()
        database_status = status.apps[deployed["database_app"]].application_status
        relation_status = juju.cli("status", "--relations")

        logger.info(
            "PostgreSQL status: %s %s",
            database_status.current,
            database_status.message,
        )
        assert database_status.current == "active"
        assert f"{deployed['app']}:postgresql" in relation_status
        assert f"{deployed['database_app']}:database" in relation_status

    def test_self_signed_certificates_active(self, deployed: dict[str, Any]):
        """Self-signed certificates are issued to the ingress applications."""
        juju: jubilant.Juju = deployed["juju"]
        relation_status = juju.cli("status", "--relations")
        task = juju.run(
            f"{deployed['certificates_app']}/0",
            "get-ca-certificate",
            wait=60,
        )
        task.raise_on_failure()

        logger.info("CA certificate output:\n%s", task.stdout)
        assert "BEGIN CERTIFICATE" in task.stdout
        assert f"{deployed['certificates_app']}:certificates" in relation_status
        assert f"{deployed['api_ingress_app']}:certificates" in relation_status
        assert f"{deployed['oci_ingress_app']}:certificates" in relation_status

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
