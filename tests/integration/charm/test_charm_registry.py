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
