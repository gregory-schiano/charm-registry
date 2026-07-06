"""Terraform deployment integration tests."""

from __future__ import annotations

import os
import pathlib
import json
import shlex
import subprocess
import sys
import time


ROOT = pathlib.Path(__file__).resolve().parents[3]
TERRAFORM_DIR = ROOT / "terraform"
MODEL_NAME = "charm-registry-tf"
API_HOSTNAME = "api.charm-registry-tf.test"
OCI_HOSTNAME = "oci.charm-registry-tf.test"
APP_SECRET_KEY = "integration-test-secret"


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


def find_snap() -> pathlib.Path:
    snaps = sorted(ROOT.glob("**/spellbook_*.snap"))
    if not snaps:
        raise FileNotFoundError("no spellbook snap artifact found")
    return snaps[0]


def host_ip() -> str:
    route = output("ip", "-4", "route", "get", "1.1.1.1")
    parts = route.split()
    if "src" not in parts:
        raise RuntimeError(f"unable to determine host IP from route: {route}")
    return parts[parts.index("src") + 1]


def wait_for_snap_health(api_url: str) -> None:
    for attempt in range(1, 121):
        result = run("curl", "-sf", f"{api_url}/healthz", check=False)
        if result.returncode == 0:
            print(f"charm-registry snap is healthy after {attempt}s")
            return
        time.sleep(1)
    run("sudo", "snap", "logs", "spellbook.charm-registry", "-n", "100", check=False)
    raise TimeoutError("snap registry did not become healthy")


def cleanup() -> None:
    if (TERRAFORM_DIR / "terraform.tfvars").is_file():
        run("terraform", "destroy", "-auto-approve", cwd=TERRAFORM_DIR, check=False)
    (TERRAFORM_DIR / "terraform.tfvars").unlink(missing_ok=True)
    run("rm", "-rf", str(TERRAFORM_DIR / ".terraform"), check=False)
    run("sudo", "snap", "remove", "--purge", "spellbook", check=False)


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
  "rate-limit-ip-limit" = 0
  "rate-limit-token-limit" = 0
}}
gateway_class = "ck-gateway"
api_hostname = "{API_HOSTNAME}"
oci_hostname = "{OCI_HOSTNAME}"
""".lstrip()
    )


def test_terraform_stack_uses_private_charm_registry(
    charm_path: str,
    resource_images: dict[str, str],
) -> None:
    app_image = resource_images["app-image"]
    if app_image.endswith(".rock"):
        raise RuntimeError(
            f"charm-registry image was not uploaded to an OCI registry: {app_image}"
        )

    cleanup()
    try:
        if not shutil_which("terraform"):
            run("sudo", "snap", "install", "terraform", "--classic")

        (ROOT / ".bin").mkdir(exist_ok=True)
        run(
            "go",
            "build",
            "-o",
            str(ROOT / ".bin" / "functional-test"),
            "./cmd/functional-test",
        )

        registry_host = host_ip()
        registry_api_url = f"http://{registry_host}:8080"
        registry_oci_url = f"https://{registry_host}:5000"

        run("sudo", "snap", "install", "--dangerous", find_snap())
        run(
            "sudo",
            "snap",
            "set",
            "spellbook",
            "admin.usernames=admin",
            "insecure-dev-auth=true",
            "oci.secret-key=integration-test-oci-secret",
            f"public-api-url={registry_api_url}",
            f"public-storage-url={registry_api_url}",
            f"public-registry-url={registry_oci_url}",
            "rate-limit.ip-limit=0",
            "rate-limit.token-limit=0",
        )
        run("sudo", "snap", "start", "spellbook.charm-registry")
        wait_for_snap_health(registry_api_url)

        run(
            "bash",
            str(ROOT / "deploy/k8s/install-oci-cert.sh"),
            env={
                "CHARM_REGISTRY_PUBLIC_REGISTRY_URL": registry_oci_url,
                "CHARM_REGISTRY_K8S_OCI_CA_FILE": "/var/snap/spellbook/common/certs/oci.crt",
            },
        )
        run("kubectl", "wait", "--for=condition=Ready", "node", "--all", "--timeout=5m")

        token = "dev:admin:admin"
        run(
            sys.executable,
            str(ROOT / "tests/integration/terraform/publish_charm.py"),
            "--registry-url",
            registry_api_url,
            "--token",
            token,
            "--charm-name",
            "charm-registry",
            "--channel",
            "latest/edge",
            "--charm-file",
            charm_path,
        )
        run(
            sys.executable,
            str(ROOT / "tests/integration/terraform/sync_dependencies.py"),
            "--registry-url",
            registry_api_url,
            "--token",
            token,
        )

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

        unit_address = output(
            "juju",
            "status",
            "-m",
            MODEL_NAME,
            "--format",
            "json",
        )
        status = json.loads(unit_address)
        address = status["applications"]["charm-registry"]["units"]["charm-registry/0"][
            "address"
        ]
        run(
            ROOT / ".bin" / "functional-test",
            env={
                "FTEST_API_URL": f"http://{address}:8080",
                "FTEST_OCI_URL": f"http://{address}:5000",
                "FTEST_ADMIN_SUBJECT": "admin",
                "FTEST_ADMIN_USER": "admin",
                "FTEST_OCI_CERT_PATH": "",
            },
        )
    finally:
        cleanup()


def shutil_which(name: str) -> str | None:
    for directory in os.environ.get("PATH", "").split(os.pathsep):
        candidate = pathlib.Path(directory) / name
        if candidate.is_file() and os.access(candidate, os.X_OK):
            return str(candidate)
    return None
