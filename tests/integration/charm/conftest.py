# conftest.py — Jubilant charm integration test fixtures.
#
# Creates a temporary Juju model via jubilant, packs the local charm,
# deploys it with required relations (PostgreSQL, S3-integrator, Traefik
# ingress), and tears the model down after the test session.
#
# Prerequisites (checked once at session start):
#   - juju CLI 3.x+
#   - lxc / microk8s  (a Juju k8s or machine controller must be bootstrapped)
#   - charmcraft      (for local charm pack)
#   - jubilant        (pip install jubilant)
#
# Environment variables that override defaults:
#   JUB_MODEL           – existing model to use instead of creating a temp one
#   JUB_POSTGRES_CHARM  – PostgreSQL charm override (default: postgresql-k8s)
#   JUB_POSTGRES_CHANNEL – charm channel (default: 14/stable)
#   JUB_TRAEFIK_CHARM   – ingress charm override (default: traefik-k8s)
#   JUB_TRAEFIK_CHANNEL – ingress channel (default: latest/stable)
#   JUB_S3_INTEGRATOR   – S3-providing charm (default: s3-integrator)
#   JUB_S3_CHANNEL      – s3-integrator channel (default: latest/stable)
#   JUB_S3_ENDPOINT     – S3 endpoint for s3-integrator config
#   JUB_S3_BUCKET       – S3 bucket name
#   JUB_S3_REGION       – S3 region (default: us-east-1)
#   JUB_S3_ACCESS_KEY / JUB_S3_SECRET_KEY – S3 credentials
#   JUB_API_URL          – override the discovered API URL
#   JUB_OCI_URL          – override the discovered OCI registry URL

from __future__ import annotations

import logging
import os
import pathlib
import shutil
import subprocess
import time

import jubilant
import pytest

logger = logging.getLogger(__name__)

REPO_ROOT = pathlib.Path(__file__).resolve().parents[3]


def _run(cmd: list[str], **kw) -> subprocess.CompletedProcess:
    """Run a command, log it, raise on failure."""
    logger.info("RUN: %s", " ".join(cmd))
    return subprocess.run(cmd, check=True, capture_output=True, text=True, **kw)


def _require_cli(name: str) -> str:
    path = shutil.which(name)
    if path is None:
        pytest.skip(f"Prerequisite CLI tool '{name}' not found on PATH")
    return path


# --------------- session-scoped fixtures ---------------


@pytest.fixture(scope="session")
def repo_root() -> pathlib.Path:
    return REPO_ROOT


@pytest.fixture(scope="session")
def charm_file(repo_root) -> pathlib.Path:
    """Pack the local charm once per session and return the .charm path."""
    _require_cli("charmcraft")
    charm_dir = repo_root / "charm"

    _run(["charmcraft", "pack", "--project-dir", str(charm_dir)])

    charms = sorted(charm_dir.glob("*.charm"), key=os.path.getmtime, reverse=True)
    if not charms:
        pytest.fail("charmcraft pack produced no .charm file")

    charm_path = charms[0]
    logger.info("Packed charm: %s", charm_path)
    return charm_path


@pytest.fixture(scope="session")
def functional_test_binary(repo_root) -> pathlib.Path:
    """Build the shared functional-test binary once per session."""
    bin_dir = repo_root / ".bin"
    bin_dir.mkdir(exist_ok=True)
    binary = bin_dir / "functional-test"
    _run(
        ["go", "build", "-o", str(binary), "./cmd/functional-test"],
        cwd=str(repo_root),
    )
    logger.info("Functional test binary: %s", binary)
    return binary


@pytest.fixture(scope="session")
def juju():
    """Provide a jubilant.Juju() instance scoped to a temporary test model.

    If JUB_MODEL is set, reuses that model without destroying it on teardown.
    Otherwise creates a temp model and destroys it when the session ends.
    """
    _require_cli("juju")

    existing = os.environ.get("JUB_MODEL")
    if existing:
        logger.info("Reusing existing model: %s", existing)
        yield jubilant.Juju(model=existing)
        return

    model_name = f"itest-{int(time.time())}"
    subprocess.run(
        ["juju", "add-model", model_name],
        capture_output=True,
        text=True,
        check=True,
    )
    logger.info("Created temp model: %s", model_name)

    try:
        yield jubilant.Juju(model=model_name)
    finally:
        logger.info("Destroying temp model: %s", model_name)
        subprocess.run(
            ["juju", "destroy-model", "--no-prompt", "--force", model_name],
            capture_output=True,
            text=True,
        )


@pytest.fixture(scope="session")
def deployed(juju, charm_file, functional_test_binary):
    """Deploy charm-registry with PostgreSQL, S3, and ingress.

    Yields a dict with deployment metadata once everything is active/idle.
    """
    app = "charm-registry"
    pg_app = os.environ.get("JUB_POSTGRES_CHARM", "postgresql-k8s")
    pg_channel = os.environ.get("JUB_POSTGRES_CHANNEL", "14/stable")
    ingress_charm = os.environ.get("JUB_TRAEFIK_CHARM", "traefik-k8s")
    ingress_channel = os.environ.get("JUB_TRAEFIK_CHANNEL", "latest/stable")
    # Two traefik apps: one fronting the API/storage endpoints, one fronting
    # the embedded OCI registry. Both ingress relations are mandatory and are
    # the sole source of the workload's public URLs.
    api_ingress_app = "ingress-api"
    oci_ingress_app = "ingress-oci"
    s3_app_name = os.environ.get("JUB_S3_INTEGRATOR", "s3-integrator")
    s3_channel = os.environ.get("JUB_S3_CHANNEL", "latest/stable")

    # --- Deploy supporting charms ---

    logger.info("Deploying %s (channel: %s)", pg_app, pg_channel)
    juju.deploy(pg_app, channel=pg_channel, trust=True)

    logger.info("Deploying %s as %s and %s (channel: %s)",
                ingress_charm, api_ingress_app, oci_ingress_app, ingress_channel)
    juju.deploy(ingress_charm, api_ingress_app, channel=ingress_channel, trust=True)
    juju.deploy(ingress_charm, oci_ingress_app, channel=ingress_channel, trust=True)

    # S3-integrator: optional when env vars not supplied.
    s3_configured = bool(
        os.environ.get("JUB_S3_ENDPOINT") and os.environ.get("JUB_S3_ACCESS_KEY")
    )
    if s3_configured:
        s3_config = {
            "endpoint": os.environ["JUB_S3_ENDPOINT"],
            "bucket": os.environ.get("JUB_S3_BUCKET", "charm-registry-test"),
            "region": os.environ.get("JUB_S3_REGION", "us-east-1"),
            "access-key": os.environ["JUB_S3_ACCESS_KEY"],
            "secret-key": os.environ["JUB_S3_SECRET_KEY"],
            "path": "test",
        }
        juju.deploy(s3_app_name, channel=s3_channel, config=s3_config)

    # --- Deploy the local charm-registry ---

    logger.info("Deploying local charm from %s", charm_file)
    juju.deploy(
        charm_file,
        app,
        config={
            "enable-insecure-dev-auth": True,
        },
    )

    # --- Create relations ---

    juju.integrate(f"{app}:postgresql", f"{pg_app}:database")
    juju.integrate(f"{app}:ingress", f"{api_ingress_app}:ingress")
    juju.integrate(f"{app}:oci-ingress", f"{oci_ingress_app}:ingress")

    if s3_configured:
        juju.integrate(f"{app}:s3", f"{s3_app_name}:s3")
        juju.integrate(f"{app}:oci-s3", f"{s3_app_name}:s3")

    # --- Wait for active/idle ---

    logger.info("Waiting for active/idle (timeout 600s)...")
    juju.wait(jubilant.all_active, timeout=600.0, successes=3)

    # Extra settle time for workload startup and relation data exchange.
    time.sleep(15)

    # --- Discover the API endpoint ---

    status = juju.status()
    unit_info = status.apps[app].units.get(f"{app}/0")
    address = unit_info.address if unit_info else ""

    api_url = os.environ.get("JUB_API_URL", f"http://{address}:8080")

    # OCI URL: check the OCI traefik units for an address. The workload's
    # public URLs come from the ingress relations; no config is needed.
    oci_url = os.environ.get("JUB_OCI_URL", "")
    if not oci_url and oci_ingress_app in status.apps:
        traefik_units = status.apps[oci_ingress_app].units
        if traefik_units:
            traefik_addr = next(iter(traefik_units.values())).address
            if traefik_addr:
                oci_url = f"http://{traefik_addr}:80"

    logger.info("Deployed endpoints — api: %s, oci: %s", api_url, oci_url)

    yield {
        "model": juju.model,
        "app": app,
        "unit": f"{app}/0",
        "api_url": api_url,
        "oci_url": oci_url,
        "s3_configured": s3_configured,
        "juju": juju,
    }
