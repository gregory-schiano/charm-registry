"""Helpers to build and move minimal OCI images in integration tests.

Shared by the charm and Terraform pytest suites (made importable by
``tests/integration/conftest.py``). Images are synthetic single-layer
artifacts: enough for the embedded OCI registry's data path and for a Juju
sidecar workload container, whose entrypoint is the pebble binary that Juju
mounts into the container (so the image itself needs no executable content).
"""

from __future__ import annotations

import gzip
import hashlib
import io
import json
import platform
import tarfile

OCI_CONFIG_MEDIA_TYPE = "application/vnd.oci.image.config.v1+json"
OCI_LAYER_MEDIA_TYPE = "application/vnd.oci.image.layer.v1.tar+gzip"
OCI_MANIFEST_MEDIA_TYPE = "application/vnd.oci.image.manifest.v1+json"

_GO_ARCH = {"x86_64": "amd64", "aarch64": "arm64"}


def machine_arch() -> str:
    """Return the current machine architecture as a Go/OCI arch string."""
    machine = platform.machine()
    return _GO_ARCH.get(machine, machine)


def sha256_digest(data: bytes) -> str:
    return "sha256:" + hashlib.sha256(data).hexdigest()


def build_oci_image(name: str, *, arch: str | None = None) -> dict[str, bytes]:
    """Build a minimal single-layer OCI image (config, layer, manifest blobs)."""
    payload = f"charm-registry integration test {name}\n".encode()
    tar_buffer = io.BytesIO()
    with tarfile.open(fileobj=tar_buffer, mode="w") as tar:
        info = tarfile.TarInfo("etc/charm-registry-itest")
        info.size = len(payload)
        tar.addfile(info, io.BytesIO(payload))
    layer_buffer = io.BytesIO()
    with gzip.GzipFile(fileobj=layer_buffer, mode="wb", mtime=0) as gz:
        gz.write(tar_buffer.getvalue())
    layer = layer_buffer.getvalue()
    config = json.dumps(
        {
            "architecture": arch or machine_arch(),
            "os": "linux",
            "config": {},
            "rootfs": {
                "type": "layers",
                "diff_ids": [sha256_digest(tar_buffer.getvalue())],
            },
        }
    ).encode()
    manifest = json.dumps(
        {
            "schemaVersion": 2,
            "mediaType": OCI_MANIFEST_MEDIA_TYPE,
            "config": {
                "mediaType": OCI_CONFIG_MEDIA_TYPE,
                "digest": sha256_digest(config),
                "size": len(config),
            },
            "layers": [
                {
                    "mediaType": OCI_LAYER_MEDIA_TYPE,
                    "digest": sha256_digest(layer),
                    "size": len(layer),
                }
            ],
        }
    ).encode()
    return {"config": config, "layer": layer, "manifest": manifest}


def push_oci_image(
    oci_url: str,
    repository: str,
    username: str,
    password: str,
    image: dict[str, bytes],
    *,
    tag: str | None = None,
    verify: bool | str = True,
) -> str:
    """Push a built OCI image via the Distribution v2 API; return its digest."""
    import requests

    session = requests.Session()
    session.auth = (username, password)
    for blob in (image["config"], image["layer"]):
        start = session.post(
            f"{oci_url}/v2/{repository}/blobs/uploads/", timeout=30, verify=verify
        )
        assert start.status_code == 202, start.text
        location = requests.compat.urljoin(oci_url, start.headers["Location"])
        separator = "&" if "?" in location else "?"
        finish = session.put(
            f"{location}{separator}digest={sha256_digest(blob)}",
            data=blob,
            headers={"Content-Type": "application/octet-stream"},
            timeout=60,
            verify=verify,
        )
        assert finish.status_code == 201, finish.text

    digest = sha256_digest(image["manifest"])
    manifest_put = session.put(
        f"{oci_url}/v2/{repository}/manifests/{digest}",
        data=image["manifest"],
        headers={"Content-Type": OCI_MANIFEST_MEDIA_TYPE},
        timeout=30,
        verify=verify,
    )
    assert manifest_put.status_code == 201, manifest_put.text
    if tag:
        tag_put = session.put(
            f"{oci_url}/v2/{repository}/manifests/{tag}",
            data=image["manifest"],
            headers={"Content-Type": OCI_MANIFEST_MEDIA_TYPE},
            timeout=30,
            verify=verify,
        )
        assert tag_put.status_code == 201, tag_put.text
    return digest


def pull_oci_manifest(
    oci_url: str,
    repository: str,
    username: str,
    password: str,
    digest: str,
    *,
    verify: bool | str = True,
) -> bytes:
    """Pull an OCI manifest back from the embedded registry."""
    import requests

    resp = requests.get(
        f"{oci_url}/v2/{repository}/manifests/{digest}",
        auth=(username, password),
        headers={"Accept": OCI_MANIFEST_MEDIA_TYPE},
        timeout=30,
        verify=verify,
    )
    assert resp.status_code == 200, resp.text
    return resp.content
