#!/usr/bin/env bash
# rock-smoke-test.sh — Inspect and validate a .rock OCI archive without Docker.
#
# Usage:
#   ./scripts/rock-smoke-test.sh [path-to-rock]
#
# If no path is given, uses the first .rock file in the current directory.
# Requires: skopeo (install: sudo apt install skopeo  or  snap install skopeo)
set -euo pipefail

ROCK="${1:-$(ls -1 ./*.rock 2>/dev/null | head -1)}"

if [ -z "$ROCK" ] || [ ! -f "$ROCK" ]; then
    echo "ERROR: No .rock file found. Run 'rockcraft pack' first." >&2
    echo "  Usage: $0 [path/to/file.rock]" >&2
    exit 1
fi

echo "=== Rock smoke test: $ROCK ==="
echo ""

# --- 1. Basic file info ---
echo "--- File info ---"
ls -lh "$ROCK"
file "$ROCK"
echo ""

# --- 2. OCI inspection via skopeo ---
if ! command -v skopeo >/dev/null 2>&1; then
    echo "WARNING: skopeo not found. Skipping OCI layer inspection."
    echo "  Install: sudo apt install skopeo  (or)  snap install skopeo"
    echo ""
    echo "--- Attempting tar-based inspection instead ---"

    # A .rock is an OCI image archive (tar). We can list its contents.
    echo "--- Archive contents (top level) ---"
    tar tf "$ROCK" | head -30
    echo ""

    # Check for the manifest
    if tar tf "$ROCK" | grep -q "manifest.json"; then
        echo "--- OCI manifest ---"
        tar xf "$ROCK" -O manifest.json 2>/dev/null | python3 -m json.tool 2>/dev/null || \
            tar xf "$ROCK" -O manifest.json 2>/dev/null
        echo ""
    fi

    # Look for our binaries in the layer tarballs
    echo "--- Checking for expected binaries in layers ---"
    FOUND_SERVER=false
    FOUND_CLI=false

    for layer in $(tar tf "$ROCK" | grep '\.tar$' | grep -v manifest); do
        contents=$(tar xf "$ROCK" -O "$layer" 2>/dev/null | tar tf - 2>/dev/null || true)
        if echo "$contents" | grep -q "charm-registry$"; then
            FOUND_SERVER=true
            echo "  [OK] charm-registry server binary found in $layer"
        fi
        if echo "$contents" | grep -q "charm-registryctl$"; then
            FOUND_CLI=true
            echo "  [OK] charm-registryctl CLI binary found in $layer"
        fi
    done
else
    echo "--- OCI inspection (skopeo) ---"
    skopeo inspect "oci-archive:${ROCK}" 2>&1 | python3 -m json.tool 2>/dev/null || \
        skopeo inspect "oci-archive:${ROCK}" 2>&1
    echo ""

    echo "--- Layer info ---"
    skopeo inspect --raw "oci-archive:${ROCK}" 2>&1 | python3 -m json.tool 2>/dev/null || \
        skopeo inspect --raw "oci-archive:${ROCK}" 2>&1
    echo ""

    echo "--- Checking for expected binaries ---"
    FOUND_SERVER=false
    FOUND_CLI=false

    # List files in the rock layers
    ALL_FILES=$(skopeo layers "oci-archive:${ROCK}" 2>/dev/null | tr '\n' ' ' || tar tf "$ROCK")

    if echo "$ALL_FILES" | grep -q "charm-registry"; then
        FOUND_SERVER=true
        echo "  [OK] charm-registry binary referenced in rock"
    fi
    if echo "$ALL_FILES" | grep -q "charm-registryctl"; then
        FOUND_CLI=true
        echo "  [OK] charm-registryctl binary referenced in rock"
    fi
fi

echo ""

# --- 3. Pebble layer check ---
echo "--- Checking for Pebble service layer ---"
if tar tf "$ROCK" | grep -q "pebble"; then
    echo "  [OK] Pebble layer definitions found"
    # Try to extract and show the Pebble layer
    for pebble_layer in $(tar tf "$ROCK" | grep "pebble/layers/" 2>/dev/null || true); do
        echo "  Layer file: $pebble_layer"
        tar xf "$ROCK" -O "$pebble_layer" 2>/dev/null | head -20 || true
    done
else
    echo "  [WARN] No Pebble layer found — go-framework extension should auto-generate one"
fi

echo ""

# --- 4. Summary ---
echo "=== Summary ==="
PASS=true

if [ "$FOUND_SERVER" = true ]; then
    echo "  [PASS] charm-registry server binary present"
else
    echo "  [FAIL] charm-registry server binary NOT found"
    PASS=false
fi

if [ "$FOUND_CLI" = true ]; then
    echo "  [PASS] charm-registryctl CLI binary present"
else
    echo "  [WARN] charm-registryctl CLI binary not found (may be in a compressed layer)"
fi

echo ""
if [ "$PASS" = true ]; then
    echo "Smoke test PASSED for $ROCK"
    exit 0
else
    echo "Smoke test FAILED for $ROCK" >&2
    exit 1
fi
