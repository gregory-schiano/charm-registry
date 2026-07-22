#!/usr/bin/env python3
"""Regenerate the committed integration-test fixtures.

Builds the charm artifacts the Terraform lifecycle test publishes through the
Juju-deployed registry:

- ``itest-lifecycle_r1.charm`` / ``itest-lifecycle_r2.charm`` — a minimal
  sidecar charm (dispatch only sets active status; the workload container's
  entrypoint is the pebble binary Juju mounts into it). One universal archive
  per revision, declaring both amd64 and arm64.
The lifecycle test materializes a tiny public OCI image as a local
``oci-archive`` at runtime and uploads it through ``charmcraft upload-resource``.

Output is byte-for-byte deterministic (pinned zip timestamps), so CI
can regenerate and diff to prove the committed fixtures match this script.
Uses only the standard library. Run from anywhere:

    python3 tests/integration/scripts/generate-test-fixtures.py
"""

from __future__ import annotations

import io
import pathlib
import sys
import zipfile

FIXTURES_DIR = pathlib.Path(__file__).resolve().parents[1] / "fixtures"
CHARM_NAME = "itest-lifecycle"
ARCHITECTURES = ("amd64", "arm64")
REVISIONS = {"r1": "revision one", "r2": "revision two"}

# Fixed timestamp for zip entries (the zip epoch) so archives are reproducible.
ZIP_EPOCH = (1980, 1, 1, 0, 0, 0)


def build_test_charm(name: str, note: str) -> bytes:
    """Build a minimal deployable sidecar charm archive (deterministic)."""
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
    architectures = "\n".join(f"      - {arch}" for arch in ARCHITECTURES)
    manifest = f"""bases:
  - name: ubuntu
    channel: "22.04"
    architectures:
{architectures}
"""
    charmcraft = f"""{metadata}type: charm
base: ubuntu@22.04
platforms:
  amd64:
  arm64:
parts:
  charm:
    plugin: dump
    source: .
"""
    dispatch = f'#!/bin/sh\nstatus-set active "{note}" || true\n'

    archive = io.BytesIO()
    with zipfile.ZipFile(archive, "w") as charm_zip:
        for filename, content, mode in (
            ("metadata.yaml", metadata, 0o100644),
            ("manifest.yaml", manifest, 0o100644),
            ("charmcraft.yaml", charmcraft, 0o100644),
            ("dispatch", dispatch, 0o100755),
        ):
            info = zipfile.ZipInfo(filename, date_time=ZIP_EPOCH)
            info.external_attr = mode << 16
            charm_zip.writestr(info, content)
    return archive.getvalue()


def main() -> int:
    FIXTURES_DIR.mkdir(parents=True, exist_ok=True)
    written: list[pathlib.Path] = []

    for tag, note in REVISIONS.items():
        charm_path = FIXTURES_DIR / f"{CHARM_NAME}_{tag}.charm"
        charm_path.write_bytes(build_test_charm(CHARM_NAME, note))
        written.append(charm_path)

    for path in written:
        print(
            f"wrote {path.relative_to(FIXTURES_DIR.parents[2])} ({path.stat().st_size} bytes)"
        )
    return 0


if __name__ == "__main__":
    sys.exit(main())
