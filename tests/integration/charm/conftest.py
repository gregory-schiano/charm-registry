"""Jubilant fixtures for charm-registry charm integration tests."""

from __future__ import annotations

import logging
import os
import pathlib
import shutil
import subprocess
import time
from collections.abc import Iterator
from typing import Any

import jubilant
import pytest

logger = logging.getLogger(__name__)

REPO_ROOT = pathlib.Path(__file__).resolve().parents[3]


def _run(cmd: list[str], **kwargs: Any) -> subprocess.CompletedProcess[str]:
    logger.info("RUN: %s", " ".join(cmd))
    return subprocess.run(cmd, check=True, capture_output=True, text=True, **kwargs)


def _require_cli(name: str) -> str:
    path = shutil.which(name)
    if path is None:
        pytest.skip(f"Prerequisite CLI tool {name!r} not found on PATH")
    return path


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
) -> dict[str, Any]:
    """Deploy charm-registry with its mandatory ingress relations."""
    del functional_test_binary
    app = "charm-registry"
    api_ingress_app = "ingress-api"
    oci_ingress_app = "ingress-oci"
    ingress_charm = os.environ.get("JUB_INGRESS_CHARM", "traefik-k8s")
    ingress_channel = os.environ.get("JUB_INGRESS_CHANNEL", "latest/stable")

    logger.info("Deploying %s as %s", ingress_charm, api_ingress_app)
    juju.deploy(ingress_charm, api_ingress_app, channel=ingress_channel, trust=True)
    logger.info("Deploying %s as %s", ingress_charm, oci_ingress_app)
    juju.deploy(ingress_charm, oci_ingress_app, channel=ingress_channel, trust=True)

    logger.info("Deploying %s from %s", app, charm_file)
    juju.deploy(
        charm=str(charm_file),
        app=app,
        resources={"app-image": app_image},
        config={
            "admin-usernames": "admin",
            "app-secret-key": "integration-test-secret",
            "enable-insecure-dev-auth": True,
        },
    )

    juju.integrate(f"{app}:ingress", f"{api_ingress_app}:ingress")
    juju.integrate(f"{app}:oci-ingress", f"{oci_ingress_app}:ingress")

    logger.info("Waiting for active/idle deployment")
    juju.wait(jubilant.all_active, timeout=15 * 60, delay=10, successes=3)
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
        "api_url": api_url,
        "oci_url": oci_url,
    }
