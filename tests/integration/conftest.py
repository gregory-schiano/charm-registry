"""Shared pytest setup for the charm and Terraform integration suites.

Both suites live in sibling directories under ``tests/integration/`` and are
run as separate spread suites; this conftest (loaded by pytest for any test
path below it) makes the shared helper modules in this directory importable.
"""

from __future__ import annotations

import pathlib
import sys

sys.path.insert(0, str(pathlib.Path(__file__).resolve().parent))
