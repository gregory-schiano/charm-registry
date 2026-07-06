#!/usr/bin/env python3
"""Mirror Terraform dependency charms into a charm-registry instance."""

from __future__ import annotations

import argparse
import json
import sys
import time
import urllib.error
import urllib.request

DEPENDENCIES = {
    "gateway-api-integrator": "1",
    "ingress-configurator": "latest",
    "postgresql-k8s": "14",
    "self-signed-certificates": "1",
}


def request(
    base_url: str, method: str, path: str, token: str, body: dict | None = None
) -> tuple[int, dict]:
    data = None if body is None else json.dumps(body).encode()
    req = urllib.request.Request(
        base_url.rstrip("/") + path,
        data=data,
        method=method,
        headers={
            "Authorization": f"Bearer {token}",
            "Content-Type": "application/json",
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


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--registry-url", required=True)
    parser.add_argument("--token", required=True)
    parser.add_argument("--timeout", type=int, default=1800)
    args = parser.parse_args()

    for name, track in DEPENDENCIES.items():
        status, payload = request(
            args.registry_url,
            "POST",
            "/v1/admin/charmhub-sync",
            args.token,
            {"name": name, "track": track},
        )
        if status not in {202, 409}:
            raise RuntimeError(f"sync add {name} failed with HTTP {status}: {payload}")
        status, payload = request(
            args.registry_url,
            "POST",
            f"/v1/admin/charmhub-sync/{name}/run",
            args.token,
        )
        if status not in {202, 404}:
            raise RuntimeError(f"sync run {name} failed with HTTP {status}: {payload}")

    deadline = time.time() + args.timeout
    while time.time() < deadline:
        status, payload = request(
            args.registry_url,
            "GET",
            "/v1/admin/charmhub-sync",
            args.token,
        )
        if status != 200:
            raise RuntimeError(f"sync list failed with HTTP {status}: {payload}")
        rules = {
            (rule.get("name"), rule.get("track")): rule
            for rule in payload.get("rules", [])
        }
        pending = []
        for name, track in DEPENDENCIES.items():
            rule = rules.get((name, track))
            if not rule:
                pending.append(f"{name}:{track}:missing")
                continue
            state = rule.get("status")
            if state == "ok":
                continue
            if state in {"error", "delete-error"}:
                raise RuntimeError(f"sync failed for {name}:{track}: {rule}")
            pending.append(f"{name}:{track}:{state}")
        if not pending:
            print("all dependency sync rules completed")
            return 0
        print("waiting for dependency sync:", ", ".join(pending))
        time.sleep(15)

    raise TimeoutError("timed out waiting for dependency charm sync")


if __name__ == "__main__":
    sys.exit(main())
