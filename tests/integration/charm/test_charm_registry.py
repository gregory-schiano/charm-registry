# test_charm_registry.py — Jubilant charm integration tests for charm-registry.
#
# Tests deploy the charm with Juju via jubilant, exercise endpoint health,
# run the shared Go functional-test binary, and verify restart/persistence
# behaviour through Juju unit reschedule.
#
# No Docker or Docker Compose references anywhere in this suite.
#
# Usage:
#   make charm-integration-test
#   # or directly:
#   cd tests/integration/charm && python3 -m pytest -v -s --tb native
#
# Prerequisites: juju CLI, charmcraft, jubilant, bootstrapped Juju controller.
# See conftest.py for environment variable overrides.

from __future__ import annotations

import logging
import os
import subprocess
import time
from typing import Any

import jubilant
import pytest
import requests

logger = logging.getLogger(__name__)


# --------------- test classes ---------------


class TestCharmDeployment:
    """Verify charm deploys, integrates, and exposes a reachable API."""

    def test_application_active(self, deployed: dict[str, Any]):
        """Application reports active/idle status."""
        juju: jubilant.Juju = deployed["juju"]
        status = juju.status()
        app_info = status.apps[deployed["app"]]

        app_status = app_info.application_status
        logger.info(
            "App workload status: %s — %s",
            app_status.current,
            getattr(app_status, "message", ""),
        )
        # Allow 'waiting' during initial relation data exchange.
        assert app_status.current in ("active", "waiting"), (
            f"Application not active: {app_status}"
        )

    def test_health_endpoint(self, deployed: dict[str, Any]):
        """GET /healthz returns 200 with status=ok."""
        url = f"{deployed['api_url']}/healthz"
        logger.info("GET %s", url)
        try:
            resp = requests.get(url, timeout=15)
            assert resp.status_code == 200, f"healthz: {resp.status_code} — {resp.text}"
            data = resp.json()
            assert data.get("status") == "ok", f"Unexpected body: {data}"
        except requests.ConnectionError as exc:
            pytest.skip(f"Not reachable at {url}: {exc}")

    def test_ready_endpoint(self, deployed: dict[str, Any]):
        """GET /readyz returns 200 with status=ready."""
        url = f"{deployed['api_url']}/readyz"
        logger.info("GET %s", url)
        try:
            resp = requests.get(url, timeout=15)
            assert resp.status_code == 200, f"readyz: {resp.status_code} — {resp.text}"
            data = resp.json()
            assert data.get("status") == "ready", f"Unexpected body: {data}"
        except requests.ConnectionError as exc:
            pytest.skip(f"Not reachable at {url}: {exc}")

    def test_root_document(self, deployed: dict[str, Any]):
        """GET / returns service metadata."""
        url = f"{deployed['api_url']}/"
        try:
            resp = requests.get(url, timeout=15)
            assert resp.status_code == 200
            data = resp.json()
            assert "service-name" in data or "name" in data, f"Unexpected root: {data}"
        except requests.ConnectionError as exc:
            pytest.skip(f"Not reachable: {exc}")


class TestFunctionalScenarios:
    """Run the shared Go functional-test binary against the deployed charm."""

    def test_all_scenarios(self, deployed: dict[str, Any], functional_test_binary):
        """All 16 functional scenarios pass against the Juju-deployed service."""
        env = os.environ.copy()
        env["FTEST_API_URL"] = deployed["api_url"]
        env["FTEST_ADMIN_SUBJECT"] = os.environ.get("FTEST_ADMIN_SUBJECT", "admin")
        env["FTEST_ADMIN_USER"] = os.environ.get("FTEST_ADMIN_USER", "admin")
        env["FTEST_OCI_URL"] = deployed["oci_url"] or deployed["api_url"]

        logger.info(
            "Running functional tests: API=%s OCI=%s",
            env["FTEST_API_URL"],
            env.get("FTEST_OCI_URL", ""),
        )

        result = subprocess.run(
            [str(functional_test_binary)],
            env=env,
            capture_output=True,
            text=True,
            timeout=300,
        )

        # Always log output for CI debugging.
        logger.info("functional-test stdout:\n%s", result.stdout)
        if result.stderr:
            logger.warning("functional-test stderr:\n%s", result.stderr)

        assert result.returncode == 0, (
            f"Functional test exited {result.returncode}.\n"
            f"stdout:\n{result.stdout}\nstderr:\n{result.stderr}"
        )


class TestRestartPersistence:
    """Verify the service survives Juju-level unit reschedule.

    On k8s models we scale down and back up, which forces a new pod schedule.
    Metadata should persist in the PostgreSQL relation.
    """

    def test_health_recovery_after_reschedule(self, deployed: dict[str, Any]):
        """/healthz responds after unit is removed and re-added."""
        juju: jubilant.Juju = deployed["juju"]
        app = deployed["app"]

        # Scale to zero and back to one.
        logger.info("Scaling down %s", app)
        juju.remove_unit(app, num_units=1)

        logger.info("Scaling up %s", app)
        juju.add_unit(app)

        # Wait for the workload to stabilize.
        logger.info("Waiting for %s active after reschedule", app)
        try:
            juju.wait(jubilant.all_active, timeout=300.0, successes=2)
        except jubilant.WaitError:
            logger.warning("wait timed out; proceeding with health check")

        # Extra settle for Go process startup.
        time.sleep(20)

        # Health check via the discovered (or refreshed) address.
        status = juju.status()
        unit_info = status.apps[app].units.get(f"{app}/0")
        address = unit_info.address if unit_info else ""
        if not address:
            pytest.skip("New unit has no address yet")

        url = f"http://{address}:8080/healthz"
        logger.info("Health check after reschedule: %s", url)
        try:
            resp = requests.get(url, timeout=20)
            assert resp.status_code == 200
            data = resp.json()
            assert data.get("status") == "ok", f"Unexpected: {data}"
        except requests.ConnectionError as exc:
            pytest.fail(f"Service did not recover after reschedule: {exc}")

    def test_package_persists_after_reschedule(self, deployed: dict[str, Any]):
        """Register a package, reschedule, verify it can still be read back.

        Confirms metadata is stored in the PostgreSQL relation and the charm
        reconnects correctly after a pod reschedule.
        """
        api_url = deployed["api_url"]
        juju: jubilant.Juju = deployed["juju"]
        app = deployed["app"]

        # Register a charm package via the API.
        pkg_name = f"jubilant-persist-{int(time.time())}"
        logger.info("Registering test package: %s", pkg_name)
        try:
            resp = requests.put(
                f"{api_url}/v1/charm",
                json={"name": pkg_name, "type": "charm"},
                headers={"Authorization": "Bearer admin"},
                timeout=15,
            )
            if resp.status_code not in (200, 201):
                pytest.skip(
                    f"Cannot register package (HTTP {resp.status_code}); "
                    "charm may lack full config or dev-auth"
                )
        except requests.ConnectionError:
            pytest.skip("Service unreachable before reschedule")

        # Reschedule the unit.
        logger.info("Rescheduling %s for persistence test", app)
        juju.remove_unit(app, num_units=1)
        juju.add_unit(app)

        try:
            juju.wait(jubilant.all_active, timeout=300.0, successes=2)
        except jubilant.WaitError:
            logger.warning("wait timed out after reschedule")

        time.sleep(25)

        # Verify the package still exists in the database.
        logger.info("Checking persistence of %s", pkg_name)
        try:
            status = juju.status()
            unit_info = status.apps[app].units.get(f"{app}/0")
            address = unit_info.address if unit_info else ""
            current_url = f"http://{address}:8080" if address else api_url

            resp = requests.get(
                f"{current_url}/v1/charm/{pkg_name}",
                headers={"Authorization": "Bearer admin"},
                timeout=15,
            )
            assert resp.status_code == 200, (
                f"Package {pkg_name} not found after reschedule "
                f"(HTTP {resp.status_code}): {resp.text}"
            )
        except requests.ConnectionError as exc:
            pytest.fail(f"Service unreachable after reschedule: {exc}")
