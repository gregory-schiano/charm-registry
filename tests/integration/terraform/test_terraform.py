"""Terraform deployment integration tests.

The host-side services these tests rely on are provisioned by the spread
suite prepare hooks (see ``spread.yaml`` and ``tests/integration/scripts/``):
a running local snap registry (spellbook) whose endpoints arrive through the
``REGISTRY_API_URL`` / ``REGISTRY_OCI_URL`` environment contract, with the
built charm published to it and the Terraform dependency charms mirrored from
Charmhub. The tests only consume that contract: they render tfvars, apply the
Terraform module against the local registry (via the ``tflib`` wrapper), and
assert on the deployment with jubilant.

On top of the deployed stack, the lifecycle test drives the full publish and
consume path *through the Juju-deployed registry itself*: charmcraft
register/upload/upload-resource/release of two revisions, a consumer Juju
model whose ``charmhub-url`` points at the deployment, a pinned deploy of the
first revision, and ``juju refresh`` to the channel head.
"""

from __future__ import annotations

import base64
import json
import os
import pathlib
import shlex
import subprocess
import zipfile

import jubilant
import pytest

from tflib import Terraform

ROOT = pathlib.Path(__file__).resolve().parents[3]
CHARMCRAFT_UPLOAD_PROJECT_DIR = ROOT / ".bin" / "charmcraft-upload-project"
TERRAFORM_DIR = ROOT / "terraform"
SCRIPTS_DIR = ROOT / "tests" / "integration" / "scripts"
FIXTURES_DIR = ROOT / "tests" / "integration" / "fixtures"
MODEL_NAME = "charm-registry-tf"
CONSUMER_MODEL_NAME = "charm-registry-tf-consumer"
API_HOSTNAME = "api.charm-registry-tf.test"
OCI_HOSTNAME = "oci.charm-registry-tf.test"
APP_SECRET_KEY = "integration-test-secret"
DEV_TOKEN = "dev:admin:admin"
LIFECYCLE_CHARM = "itest-lifecycle"
LIFECYCLE_IMAGE_SOURCE = "docker://busybox:1.36.1"


def run(
    *args: str | os.PathLike[str],
    cwd: pathlib.Path = ROOT,
    env: dict[str, str] | None = None,
    check: bool = True,
) -> subprocess.CompletedProcess[str]:
    command = [str(arg) for arg in args]
    merged_env = os.environ.copy()
    if env:
        merged_env.update(env)
    print("+", " ".join(shlex.quote(part) for part in command))
    return subprocess.run(
        command,
        cwd=cwd,
        env=merged_env,
        check=check,
        text=True,
        stdout=None,
        stderr=None,
    )


def output(*args: str | os.PathLike[str], cwd: pathlib.Path = ROOT) -> str:
    command = [str(arg) for arg in args]
    print("+", " ".join(shlex.quote(part) for part in command))
    return subprocess.check_output(command, cwd=cwd, text=True).strip()


def registry_urls() -> tuple[str, str]:
    """Return the API and OCI URLs of the prepared local snap registry."""
    api_url = os.environ.get("REGISTRY_API_URL")
    oci_url = os.environ.get("REGISTRY_OCI_URL")
    if not (api_url and oci_url):
        raise RuntimeError(
            "REGISTRY_API_URL and REGISTRY_OCI_URL must be set (in CI the spread"
            " suite prepare hook tests/integration/scripts/setup-snap-registry.sh"
            " provides them)."
        )
    return api_url.rstrip("/"), oci_url.rstrip("/")


def stack_tfvars(app_image: str, registry_api_url: str) -> dict:
    return {
        "model_name": MODEL_NAME,
        "model_config": {
            "test-mode": "true",
            "automatically-retry-hooks": "false",
            "update-status-hook-interval": "1m",
            "charmhub-url": registry_api_url,
        },
        "app_charm_name": "charm-registry",
        "app_channel": "latest/edge",
        "app_image": app_image,
        "app_secret_key": APP_SECRET_KEY,
        "admin_usernames": "admin",
        "enable_insecure_dev_auth": True,
        "extra_app_config": {
            "max-archive-file-bytes": "64MB",
            "rate-limit-ip-limit": 0,
            "rate-limit-token-limit": 0,
        },
        "gateway_class": "ck-gateway",
        "api_hostname": API_HOSTNAME,
        "oci_hostname": OCI_HOSTNAME,
    }


@pytest.fixture(scope="module")
def terraform_stack(resource_images: dict[str, str]) -> dict:
    """Apply Terraform once and leave resources for the ephemeral VM to discard."""
    app_image = resource_images["app-image"]
    if app_image.endswith(".rock"):
        raise RuntimeError(
            f"charm-registry image was not uploaded to an OCI registry: {app_image}"
        )
    registry_api_url, _ = registry_urls()

    functional_test = ROOT / ".bin" / "functional-test"
    if not os.access(functional_test, os.X_OK):
        raise RuntimeError(
            f"functional-test binary not found or not executable at {functional_test}; "
            "the spread prepare hook must build it before pytest starts"
        )

    tf = Terraform(TERRAFORM_DIR)
    tf.clean()
    tf.write_vars(stack_tfvars(app_image, registry_api_url))
    tf.init()
    tf.validate()
    tf.apply()

    juju = jubilant.Juju(model=MODEL_NAME)
    juju.wait(
        lambda status: jubilant.all_active(status, "charm-registry"),
        error=jubilant.any_error,
        timeout=30 * 60,
        delay=10,
    )
    print(juju.cli("status", "--relations", "--color=false"))

    status = juju.status()
    address = status.apps["charm-registry"].units["charm-registry/0"].address
    yield {
        "juju": juju,
        "address": address,
        "api_url": f"http://{address}:8080",
        "oci_url": f"http://{address}:5000",
    }


def test_terraform_stack_uses_private_charm_registry(terraform_stack: dict) -> None:
    run(
        ROOT / ".bin" / "functional-test",
        env={
            "FTEST_API_URL": terraform_stack["api_url"],
            "FTEST_OCI_URL": terraform_stack["oci_url"],
            "FTEST_ADMIN_SUBJECT": "admin",
            "FTEST_ADMIN_USER": "admin",
            "FTEST_OCI_CERT_PATH": "",
        },
    )


# ---------------------------------------------------------------------------
#  Charm lifecycle through the Juju-deployed registry
# ---------------------------------------------------------------------------


def _configure_ip_public_urls(juju: jubilant.Juju, api_url: str, oci_url: str) -> None:
    """Make the registry advertise pod-IP URLs reachable without DNS."""
    juju.cli(
        "config",
        "charm-registry",
        f"public-api-url={api_url}",
        f"public-storage-url={api_url}",
        f"public-registry-url={oci_url}",
    )
    juju.wait(
        lambda status: jubilant.all_active(status, "charm-registry"),
        error=jubilant.any_error,
        timeout=5 * 60,
        delay=10,
    )


def _max_revision(payload: object) -> int:
    revisions: list[int] = []

    def collect(value: object) -> None:
        if isinstance(value, dict):
            revision = value.get("revision")
            if isinstance(revision, int):
                revisions.append(revision)
            elif isinstance(revision, str) and revision.isdigit():
                revisions.append(int(revision))
            for child in value.values():
                collect(child)
        elif isinstance(value, list):
            for child in value:
                collect(child)

    collect(payload)
    assert revisions, f"no revisions found in charmcraft output: {payload!r}"
    return max(revisions)


def _charmcraft_json(
    *args: str,
    cwd: pathlib.Path,
    env: dict[str, str],
) -> object:
    command = ["charmcraft", *args, "--format", "json"]
    merged_env = os.environ.copy()
    merged_env.update(env)
    print("+", " ".join(shlex.quote(part) for part in command))
    return json.loads(
        subprocess.check_output(command, cwd=cwd, env=merged_env, text=True)
    )


def _latest_revision(charm: str, cwd: pathlib.Path, env: dict[str, str]) -> int:
    return _max_revision(_charmcraft_json("revisions", charm, cwd=cwd, env=env))


def _latest_resource_revision(
    charm: str, resource: str, cwd: pathlib.Path, env: dict[str, str]
) -> int:
    return _max_revision(
        _charmcraft_json("resource-revisions", charm, resource, cwd=cwd, env=env)
    )


def _charmcraft_yaml_from_charm(charm_file: pathlib.Path) -> str:
    """Build a charmcraft.yaml upload context from the charm archive metadata."""
    with zipfile.ZipFile(charm_file) as charm_zip:
        names = set(charm_zip.namelist())
        if "charmcraft.yaml" in names:
            return charm_zip.read("charmcraft.yaml").decode()

        import yaml

        metadata = yaml.safe_load(charm_zip.read("metadata.yaml")) or {}
        manifest = yaml.safe_load(charm_zip.read("manifest.yaml")) or {}

    bases = manifest.get("bases") or []
    if not bases:
        raise RuntimeError(f"{charm_file} does not contain manifest bases")

    base = bases[0]
    architectures = {
        architecture
        for base_entry in bases
        for architecture in base_entry.get("architectures", [])
    }
    if not architectures:
        raise RuntimeError(f"{charm_file} does not contain manifest architectures")

    charmcraft_yaml = {"name": metadata.pop("name"), "type": "charm"}
    charmcraft_yaml.update(metadata)
    charmcraft_yaml["base"] = f"{base['name']}@{base['channel']}"
    charmcraft_yaml["platforms"] = {
        architecture: {} for architecture in sorted(architectures)
    }
    charmcraft_yaml["parts"] = {"charm": {"plugin": "dump", "source": "."}}
    return yaml.safe_dump(charmcraft_yaml, sort_keys=False)


def _charmcraft_upload_project(charm_file: pathlib.Path) -> pathlib.Path:
    """Return a charmcraft project matching the charm used by upload commands."""
    CHARMCRAFT_UPLOAD_PROJECT_DIR.mkdir(parents=True, exist_ok=True)
    (CHARMCRAFT_UPLOAD_PROJECT_DIR / "charmcraft.yaml").write_text(
        _charmcraft_yaml_from_charm(charm_file)
    )
    return CHARMCRAFT_UPLOAD_PROJECT_DIR


def _lifecycle_image_archive(tag: str) -> pathlib.Path:
    """Materialize a tiny public image as a local OCI archive for Charmcraft."""
    image_file = ROOT / ".bin" / f"{LIFECYCLE_CHARM}-image-{tag}.tar"
    image_file.parent.mkdir(parents=True, exist_ok=True)
    run(
        "skopeo",
        "copy",
        "--insecure-policy",
        LIFECYCLE_IMAGE_SOURCE,
        f"oci-archive:{image_file}:{tag}",
    )
    return image_file


def _publish_lifecycle_revision(
    charmcraft_env: dict[str, str], tag: str
) -> tuple[int, int]:
    """Publish one charm + image revision with charmcraft; return revisions.

    The charm is a committed fixture; the image archive is materialized from a
    tiny public image at runtime so Charmcraft owns the upload path.
    """
    charm_file = FIXTURES_DIR / f"{LIFECYCLE_CHARM}_{tag}.charm"
    assert charm_file.is_file(), (
        f"missing committed charm fixture for {tag!r}; run "
        "tests/integration/scripts/generate-test-fixtures.py"
    )
    image_file = _lifecycle_image_archive(tag)
    charmcraft_cwd = _charmcraft_upload_project(charm_file)

    run(
        "charmcraft",
        "upload",
        str(charm_file),
        "--name",
        LIFECYCLE_CHARM,
        cwd=charmcraft_cwd,
        env=charmcraft_env,
    )
    run(
        "charmcraft",
        "upload-resource",
        LIFECYCLE_CHARM,
        "app-image",
        "--image",
        f"oci-archive:{image_file}",
        cwd=charmcraft_cwd,
        env=charmcraft_env,
    )
    charm_revision = _latest_revision(LIFECYCLE_CHARM, charmcraft_cwd, charmcraft_env)
    resource_revision = _latest_resource_revision(
        LIFECYCLE_CHARM, "app-image", charmcraft_cwd, charmcraft_env
    )
    run(
        "charmcraft",
        "release",
        LIFECYCLE_CHARM,
        "--revision",
        str(charm_revision),
        "--channel",
        "latest/edge",
        "--resource",
        f"app-image:{resource_revision}",
        cwd=charmcraft_cwd,
        env=charmcraft_env,
    )
    return charm_revision, resource_revision


def _consumer_at_revision(revision: int):
    """Ready predicate: the lifecycle charm is active at *revision*."""

    def ready(status: jubilant.Status) -> bool:
        app = status.apps.get(LIFECYCLE_CHARM)
        return (
            app is not None
            and app.charm_rev == revision
            and jubilant.all_active(status, LIFECYCLE_CHARM)
        )

    return ready


def _print_registry_workload_logs() -> None:
    """Dump the deployed registry's workload logs and effective pebble plan."""
    for command in (
        ("kubectl", "logs", "-n", MODEL_NAME, "charm-registry-0", "-c", "app", "--tail=400"),
        (
            "kubectl", "exec", "-n", MODEL_NAME, "charm-registry-0", "-c", "app", "--",
            "/charm/bin/pebble", "plan",
        ),
    ):
        try:
            print(output(*command))
        except subprocess.CalledProcessError as exc:
            print(f"{' '.join(command)} failed: {exc}")


def _print_consumer_k8s_diagnostics() -> None:
    namespace = CONSUMER_MODEL_NAME
    for command in (
        ("kubectl", "get", "pods", "-n", namespace, "-o", "wide"),
        ("kubectl", "describe", "pods", "-n", namespace),
        ("kubectl", "get", "events", "-n", namespace, "--sort-by=.lastTimestamp"),
    ):
        try:
            print(output(*command))
        except subprocess.CalledProcessError as exc:
            print(f"{' '.join(command)} failed: {exc}")


def test_charm_lifecycle_through_deployed_registry(terraform_stack: dict) -> None:
    """Publish, deploy, revise, and refresh a charm via the deployed registry."""
    stack = terraform_stack
    stack_juju: jubilant.Juju = stack["juju"]

    _configure_ip_public_urls(stack_juju, stack["api_url"], stack["oci_url"])
    run("sudo", str(SCRIPTS_DIR / "setup-consumer-oci-registry.sh"), stack["oci_url"])

    charmcraft_env = {
        "CHARMCRAFT_STORE_API_URL": stack["api_url"],
        "CHARMCRAFT_UPLOAD_URL": stack["api_url"],
        "CHARMCRAFT_REGISTRY_URL": stack["oci_url"],
        "CHARMCRAFT_AUTH": base64.b64encode(DEV_TOKEN.encode()).decode(),
        "CHARMCRAFT_ENABLE_EXPERIMENTAL_EXTENSIONS": "1",
    }

    consumer = jubilant.Juju()
    try:
        # Publish both revisions up front; interleaving publishes with the
        # consumer deploy is unnecessary: deploy pins revision 1 explicitly,
        # then refresh follows the channel to its head (revision 2).
        run(
            "charmcraft",
            "register",
            LIFECYCLE_CHARM,
            cwd=_charmcraft_upload_project(
                FIXTURES_DIR / f"{LIFECYCLE_CHARM}_r1.charm"
            ),
            env=charmcraft_env,
        )
        revision_1, resource_1 = _publish_lifecycle_revision(charmcraft_env, "r1")
        assert revision_1 == 1
        revision_2, resource_2 = _publish_lifecycle_revision(charmcraft_env, "r2")
        assert revision_2 == 2
        assert resource_2 > resource_1

        consumer.add_model(
            CONSUMER_MODEL_NAME,
            config={"charmhub-url": stack["api_url"], "test-mode": True},
        )
        consumer.deploy(
            LIFECYCLE_CHARM,
            channel="latest/edge",
            revision=revision_1,
            resources={"app-image": str(resource_1)},
        )
        consumer.wait(
            _consumer_at_revision(revision_1),
            error=jubilant.any_error,
            timeout=15 * 60,
            delay=10,
        )

        consumer.refresh(LIFECYCLE_CHARM)
        consumer.wait(
            _consumer_at_revision(revision_2),
            error=jubilant.any_error,
            timeout=10 * 60,
            delay=10,
        )
    finally:
        if consumer.model:
            try:
                print(consumer.cli("status", "--color=false"))
            except jubilant.CLIError:
                pass
            _print_consumer_k8s_diagnostics()
        _print_registry_workload_logs()
