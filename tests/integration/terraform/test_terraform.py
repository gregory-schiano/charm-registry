"""Terraform deployment integration tests.

The host-side services these tests rely on are provisioned by the spread
suite prepare hooks (see ``spread.yaml`` and ``tests/integration/scripts/``):
a running local snap registry (spellbook) whose endpoints arrive through the
``REGISTRY_API_URL`` / ``REGISTRY_OCI_URL`` environment contract, with the
built charm published to it and the Terraform dependency charms mirrored from
Charmhub. The tests only consume that contract: they render tfvars, apply the
Terraform module against the local registry, and assert on the deployment.

On top of the deployed stack, the lifecycle test drives the full publish and
consume path *through the Juju-deployed registry itself*: charmcraft
register/upload/upload-resource/release, a consumer Juju model whose
``charmhub-url`` points at the deployment, ``juju deploy``, a second charm and
image revision, and ``juju refresh``.
"""

from __future__ import annotations

import base64
import io
import json
import os
import pathlib
import shlex
import subprocess
import time
import zipfile

import pytest

from oci_image import build_oci_image, machine_arch, write_oci_archive

ROOT = pathlib.Path(__file__).resolve().parents[3]
CHARM_PROJECT_DIR = ROOT / "charm"
TERRAFORM_DIR = ROOT / "terraform"
SCRIPTS_DIR = ROOT / "tests" / "integration" / "scripts"
MODEL_NAME = "charm-registry-tf"
CONSUMER_MODEL_NAME = "charm-registry-tf-consumer"
API_HOSTNAME = "api.charm-registry-tf.test"
OCI_HOSTNAME = "oci.charm-registry-tf.test"
APP_SECRET_KEY = "integration-test-secret"
DEV_TOKEN = "dev:admin:admin"
LIFECYCLE_CHARM = "itest-lifecycle"


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


def cleanup() -> None:
    if (TERRAFORM_DIR / "terraform.tfvars").is_file():
        run("terraform", "destroy", "-auto-approve", cwd=TERRAFORM_DIR, check=False)
    (TERRAFORM_DIR / "terraform.tfvars").unlink(missing_ok=True)
    run("rm", "-rf", str(TERRAFORM_DIR / ".terraform"), check=False)


def write_tfvars(app_image: str, registry_api_url: str) -> None:
    (TERRAFORM_DIR / "terraform.tfvars").write_text(
        f"""
model_name = "{MODEL_NAME}"
model_config = {{
  "test-mode"                   = "true"
  "automatically-retry-hooks"   = "false"
  "update-status-hook-interval" = "1m"
  "charmhub-url"                = "{registry_api_url}"
}}
app_charm_name = "charm-registry"
app_channel = "latest/edge"
app_image = "{app_image}"
app_secret_key = "{APP_SECRET_KEY}"
admin_usernames = "admin"
enable_insecure_dev_auth = true
extra_app_config = {{
  "max-archive-file-bytes" = "64MB"
  "rate-limit-ip-limit" = 0
  "rate-limit-token-limit" = 0
}}
gateway_class = "ck-gateway"
api_hostname = "{API_HOSTNAME}"
oci_hostname = "{OCI_HOSTNAME}"
""".lstrip()
    )


def _status(model: str) -> dict:
    return json.loads(output("juju", "status", "-m", model, "--format", "json"))


def _unit_address(status: dict, app: str) -> str:
    return status["applications"][app]["units"][f"{app}/0"]["address"]


@pytest.fixture(scope="module")
def terraform_stack(resource_images: dict[str, str]) -> dict:
    """Apply the Terraform module once for this module's tests."""
    app_image = resource_images["app-image"]
    if app_image.endswith(".rock"):
        raise RuntimeError(
            f"charm-registry image was not uploaded to an OCI registry: {app_image}"
        )
    registry_api_url, _ = registry_urls()

    cleanup()
    try:
        (ROOT / ".bin").mkdir(exist_ok=True)
        run(
            "go",
            "build",
            "-o",
            str(ROOT / ".bin" / "functional-test"),
            "./cmd/functional-test",
        )

        run("kubectl", "wait", "--for=condition=Ready", "node", "--all", "--timeout=5m")

        write_tfvars(app_image, registry_api_url)
        run("terraform", "init", "-input=false", cwd=TERRAFORM_DIR)
        run("terraform", "validate", cwd=TERRAFORM_DIR)
        run("terraform", "apply", "-auto-approve", cwd=TERRAFORM_DIR)

        run(
            "juju",
            "wait-for",
            "application",
            "-m",
            MODEL_NAME,
            "charm-registry",
            "--timeout=30m",
        )
        run("juju", "status", "-m", MODEL_NAME, "--relations", "--color=false")

        address = _unit_address(_status(MODEL_NAME), "charm-registry")
        yield {
            "address": address,
            "api_url": f"http://{address}:8080",
            "oci_url": f"http://{address}:5000",
        }
    finally:
        run(
            "juju",
            "destroy-model",
            "--no-prompt",
            "--force",
            CONSUMER_MODEL_NAME,
            check=False,
        )
        cleanup()


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


def _build_test_charm(name: str, note: str) -> bytes:
    """Build a minimal deployable sidecar charm archive.

    The workload container's entrypoint is the pebble binary Juju mounts into
    it, so the synthetic OCI image needs no executable content; the dispatch
    script only reports active status.
    """
    metadata = f"""name: {name}
summary: Lifecycle integration test charm
description: Deployable test charm published through the private registry
containers:
  app:
    resource: app-image
resources:
  app-image:
    type: oci-image
    description: Test workload image
"""
    manifest = f"""bases:
  - name: ubuntu
    channel: "22.04"
    architectures:
      - {machine_arch()}
"""
    dispatch = f'#!/bin/sh\nstatus-set active "{note}" || true\n'
    archive = io.BytesIO()
    with zipfile.ZipFile(archive, "w") as charm_zip:
        charm_zip.writestr("metadata.yaml", metadata)
        charm_zip.writestr("manifest.yaml", manifest)
        dispatch_info = zipfile.ZipInfo("dispatch")
        dispatch_info.external_attr = 0o100755 << 16
        charm_zip.writestr(dispatch_info, dispatch)
    return archive.getvalue()


def _gateway_lb_ip() -> str:
    """Return the load-balancer IP assigned to the deployment's gateway."""
    services = json.loads(
        output("kubectl", "get", "service", "--all-namespaces", "-o", "json")
    )
    candidates: list[tuple[str, str]] = []
    for item in services.get("items", []):
        if item.get("spec", {}).get("type") != "LoadBalancer":
            continue
        for ingress in (
            item.get("status", {}).get("loadBalancer", {}).get("ingress", [])
        ):
            ip = ingress.get("ip")
            if ip:
                candidates.append((item["metadata"]["namespace"], ip))
    for namespace, ip in candidates:
        if namespace == MODEL_NAME:
            return ip
    if candidates:
        return candidates[0][1]
    raise RuntimeError("no LoadBalancer service with an assigned IP found")


def _deployment_ca(path: pathlib.Path) -> pathlib.Path:
    """Fetch the deployment's self-signed CA certificate to *path*."""
    result = json.loads(
        output(
            "juju",
            "run",
            "-m",
            MODEL_NAME,
            "self-signed-certificates/0",
            "get-ca-certificate",
            "--format",
            "json",
        )
    )
    unit_result = next(iter(result.values()))
    ca = unit_result["results"]["ca-certificate"]
    path.write_text(ca)
    return path


def _api_ingress_url() -> str:
    """Return the public API URL the registry advertises via its ingress."""
    unit = json.loads(
        output(
            "juju",
            "show-unit",
            "-m",
            MODEL_NAME,
            "charm-registry/0",
            "--format",
            "json",
        )
    )
    for relation in next(iter(unit.values())).get("relation-info", []):
        if relation.get("endpoint") != "ingress":
            continue
        raw = relation.get("application-data", {}).get("ingress")
        if not raw:
            continue
        return json.loads(raw)["url"].rstrip("/")
    raise RuntimeError("could not determine the API ingress URL from relation data")


def _registry_api(
    api_url: str, method: str, path: str, body: dict | None = None
) -> dict:
    import requests

    resp = requests.request(
        method,
        f"{api_url}{path}",
        headers={"Authorization": f"Bearer {DEV_TOKEN}"},
        json=body,
        timeout=30,
    )
    assert resp.status_code < 300, f"{method} {path}: {resp.status_code} {resp.text}"
    return resp.json() if resp.content else {}


def _latest_revision(api_url: str, charm: str) -> int:
    payload = _registry_api(api_url, "GET", f"/v1/charm/{charm}/revisions")
    revisions = payload.get("revisions") or []
    assert revisions, f"no revisions found for {charm}"
    return max(int(item["revision"]) for item in revisions)


def _latest_resource_revision(api_url: str, charm: str, resource: str) -> int:
    payload = _registry_api(
        api_url, "GET", f"/v1/charm/{charm}/resources/{resource}/revisions"
    )
    revisions = payload.get("revisions") or []
    assert revisions, f"no resource revisions found for {charm}:{resource}"
    return max(int(item["revision"]) for item in revisions)


def _publish_lifecycle_revision(
    stack: dict, charmcraft_env: dict[str, str], note: str, tag: str
) -> tuple[int, int]:
    """Publish one charm + image revision with charmcraft; return revisions."""
    charm_file = ROOT / ".bin" / f"{LIFECYCLE_CHARM}-{tag}.charm"
    charm_file.write_bytes(_build_test_charm(LIFECYCLE_CHARM, note))
    image_file = ROOT / ".bin" / f"{LIFECYCLE_CHARM}-{tag}-image.tar"
    write_oci_archive(build_oci_image(f"{LIFECYCLE_CHARM}-{tag}"), image_file)

    run(
        "charmcraft",
        "upload",
        str(charm_file),
        "--name",
        LIFECYCLE_CHARM,
        cwd=CHARM_PROJECT_DIR,
        env=charmcraft_env,
    )
    run(
        "charmcraft",
        "upload-resource",
        LIFECYCLE_CHARM,
        "app-image",
        "--image",
        f"oci-archive:{image_file}",
        cwd=CHARM_PROJECT_DIR,
        env=charmcraft_env,
    )
    charm_revision = _latest_revision(stack["api_url"], LIFECYCLE_CHARM)
    resource_revision = _latest_resource_revision(
        stack["api_url"], LIFECYCLE_CHARM, "app-image"
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
        env=charmcraft_env,
    )
    return charm_revision, resource_revision


def _wait_for_consumer_revision(charm_revision: int, timeout: int = 600) -> None:
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        status = _status(CONSUMER_MODEL_NAME)
        app = status["applications"].get(LIFECYCLE_CHARM, {})
        current_revision = app.get("charm-rev")
        app_status = app.get("application-status", {}).get("current")
        if current_revision == charm_revision and app_status == "active":
            return
        time.sleep(15)
    raise TimeoutError(
        f"{LIFECYCLE_CHARM} did not reach active charm revision {charm_revision}"
    )


def test_charm_lifecycle_through_deployed_registry(terraform_stack: dict) -> None:
    """Publish, deploy, revise, and refresh a charm via the deployed registry."""
    stack = terraform_stack

    # Host plumbing: hostname resolution to the gateway (CoreDNS falls back to
    # the host resolver, so the Juju controller resolves these too) and CA
    # trust for charmcraft's HTTPS image push and containerd's image pulls.
    gateway_ip = _gateway_lb_ip()
    ca_file = _deployment_ca(ROOT / ".bin" / "charm-registry-tf-ca.crt")
    run(
        "sudo",
        str(SCRIPTS_DIR / "setup-consumer-dns-cert.sh"),
        API_HOSTNAME,
        OCI_HOSTNAME,
        gateway_ip,
        str(ca_file),
    )

    charmcraft_env = {
        "CHARMCRAFT_STORE_API_URL": stack["api_url"],
        "CHARMCRAFT_UPLOAD_URL": stack["api_url"],
        "CHARMCRAFT_REGISTRY_URL": f"https://{OCI_HOSTNAME}",
        "CHARMCRAFT_AUTH": base64.b64encode(DEV_TOKEN.encode()).decode(),
    }

    api_public_url = _api_ingress_url()
    if not api_public_url.startswith("http://"):
        pytest.fail(
            f"The registry advertises {api_public_url!r}; the Juju controller"
            " cannot trust the deployment's self-signed CA for charm downloads,"
            " so the consumer flow requires the API ingress to serve plain HTTP."
        )

    try:
        run(
            "charmcraft",
            "register",
            LIFECYCLE_CHARM,
            cwd=CHARM_PROJECT_DIR,
            env=charmcraft_env,
        )
        charm_revision, _ = _publish_lifecycle_revision(
            stack, charmcraft_env, "revision one", "r1"
        )
        assert charm_revision == 1

        run(
            "juju",
            "add-model",
            CONSUMER_MODEL_NAME,
            "--config",
            f"charmhub-url={api_public_url}",
            "--config",
            "test-mode=true",
        )
        run(
            "juju",
            "deploy",
            "-m",
            CONSUMER_MODEL_NAME,
            LIFECYCLE_CHARM,
            "--channel",
            "latest/edge",
        )
        run(
            "juju",
            "wait-for",
            "application",
            "-m",
            CONSUMER_MODEL_NAME,
            LIFECYCLE_CHARM,
            "--timeout=15m",
        )
        _wait_for_consumer_revision(charm_revision)

        charm_revision, resource_revision = _publish_lifecycle_revision(
            stack, charmcraft_env, "revision two", "r2"
        )
        assert charm_revision == 2
        assert resource_revision == 2

        run("juju", "refresh", "-m", CONSUMER_MODEL_NAME, LIFECYCLE_CHARM)
        _wait_for_consumer_revision(charm_revision)
    finally:
        run("juju", "status", "-m", CONSUMER_MODEL_NAME, "--color=false", check=False)
        run(
            "juju",
            "destroy-model",
            "--no-prompt",
            "--force",
            CONSUMER_MODEL_NAME,
            check=False,
        )
