#!/usr/bin/env python3
"""Publish a built charm into a charm-registry instance."""

from __future__ import annotations

import argparse
import json
import sys
import urllib.error
import urllib.request
import uuid
from pathlib import Path


def request(
    base_url: str,
    method: str,
    path: str,
    token: str,
    body: bytes | None = None,
    content_type: str = "application/json",
) -> tuple[int, dict]:
    req = urllib.request.Request(
        base_url.rstrip("/") + path,
        data=body,
        method=method,
        headers={
            "Authorization": f"Bearer {token}",
            "Content-Type": content_type,
        },
    )
    try:
        with urllib.request.urlopen(req, timeout=120) as resp:
            raw = resp.read()
            return resp.status, json.loads(raw or b"{}")
    except urllib.error.HTTPError as exc:
        raw = exc.read()
        try:
            payload = json.loads(raw or b"{}")
        except json.JSONDecodeError:
            payload = {"error": raw.decode(errors="replace")}
        return exc.code, payload


def upload(base_url: str, token: str, charm_file: Path) -> str:
    boundary = "----charmregistry" + uuid.uuid4().hex
    data = charm_file.read_bytes()
    body = b"".join(
        [
            f"--{boundary}\r\n".encode(),
            (
                'Content-Disposition: form-data; name="binary"; '
                f'filename="{charm_file.name}"\r\n'
            ).encode(),
            b"Content-Type: application/octet-stream\r\n\r\n",
            data,
            f"\r\n--{boundary}--\r\n".encode(),
        ]
    )
    status, payload = request(
        base_url,
        "POST",
        "/unscanned-upload/",
        token,
        body=body,
        content_type=f"multipart/form-data; boundary={boundary}",
    )
    if status != 200:
        raise RuntimeError(f"upload failed with HTTP {status}: {payload}")
    upload_id = payload.get("upload_id") or payload.get("upload-id")
    if not upload_id:
        raise RuntimeError(f"upload response did not include upload id: {payload}")
    return str(upload_id)


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--registry-url", required=True)
    parser.add_argument("--token", required=True)
    parser.add_argument("--charm-name", required=True)
    parser.add_argument("--channel", required=True)
    parser.add_argument("--charm-file", required=True, type=Path)
    args = parser.parse_args()

    if not args.charm_file.is_file():
        raise SystemExit(f"charm file not found: {args.charm_file}")

    status, payload = request(
        args.registry_url,
        "POST",
        "/v1/charm",
        args.token,
        json.dumps({"name": args.charm_name, "type": "charm"}).encode(),
    )
    if status not in {201, 409}:
        raise RuntimeError(f"register failed with HTTP {status}: {payload}")

    upload_id = upload(args.registry_url, args.token, args.charm_file)
    status, payload = request(
        args.registry_url,
        "POST",
        f"/v1/charm/{args.charm_name}/revisions",
        args.token,
        json.dumps({"upload-id": upload_id}).encode(),
    )
    if status != 201:
        raise RuntimeError(f"push revision failed with HTTP {status}: {payload}")

    status, payload = request(
        args.registry_url,
        "GET",
        f"/v1/charm/{args.charm_name}/revisions",
        args.token,
    )
    if status != 200:
        raise RuntimeError(f"list revisions failed with HTTP {status}: {payload}")
    revisions = payload.get("revisions") or []
    if not revisions:
        raise RuntimeError(f"no revisions found after push: {payload}")
    revision = max(int(item["revision"]) for item in revisions)

    status, payload = request(
        args.registry_url,
        "POST",
        f"/v1/charm/{args.charm_name}/releases",
        args.token,
        json.dumps([{"revision": revision, "channel": args.channel}]).encode(),
    )
    if status != 201:
        raise RuntimeError(f"release failed with HTTP {status}: {payload}")

    print(f"published {args.charm_name} revision {revision} to {args.channel}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
