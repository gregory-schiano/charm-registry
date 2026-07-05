#!/usr/bin/env python3
"""Resolve the app-image resource for charm-registry from artifacts.build.yaml."""

from __future__ import annotations

import pathlib

import yaml


def find_artifacts_build() -> pathlib.Path:
    here = pathlib.Path.cwd()
    for path in (here, *here.parents):
        candidate = path / "artifacts.build.yaml"
        if candidate.is_file():
            return candidate
    raise FileNotFoundError("artifacts.build.yaml not found")


def main() -> None:
    path = find_artifacts_build()
    with path.open() as fh:
        artifacts = yaml.safe_load(fh)

    image = None
    for rock in artifacts.get("rocks", []):
        if rock.get("name") != "charm-registry":
            continue
        for build in rock.get("builds", []):
            if build.get("arch") not in {"amd64", "x86_64"}:
                continue
            image = build.get("image") or build.get("file")
            if image:
                break
        if image:
            break

    if not image:
        raise RuntimeError(f"charm-registry image not found in {path}")
    if image.endswith(".rock"):
        raise RuntimeError(f"charm-registry image was not uploaded to an OCI registry: {image}")
    print(image)


if __name__ == "__main__":
    main()
