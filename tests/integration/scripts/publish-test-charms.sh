#!/bin/bash
# Publish the built charm-registry charm into the local snap registry with
# charmcraft, and mirror the Terraform dependency charms from Charmhub using
# charm-registryctl's sync management.
#
# Runs as root from the spread suite prepare hook, after
# setup-snap-registry.sh has started the registry and written the REGISTRY_*
# contract to /etc/profile.d.
set -euo pipefail

PROJECT_DIR="${PROJECT_DIR:?PROJECT_DIR must be set (spread environment)}"
ENV_FILE=/etc/profile.d/zz-charm-registry-registry.sh
export PATH="/snap/bin:/usr/local/bin:$PATH"

if [ -z "${REGISTRY_API_URL:-}" ] && [ -f "$ENV_FILE" ]; then
    # shellcheck disable=SC1090
    . "$ENV_FILE"
fi
: "${REGISTRY_API_URL:?REGISTRY_API_URL must be set (run setup-snap-registry.sh first)}"
: "${REGISTRY_TOKEN:?REGISTRY_TOKEN must be set (run setup-snap-registry.sh first)}"

CHARM_NAME="${CHARM_NAME:-charm-registry}"
CHANNEL="${CHANNEL:-latest/edge}"

# Charms the Terraform module deploys from the local registry, mirrored from
# upstream Charmhub as name:track pairs.
DEPENDENCIES=(
    "gateway-api-integrator:1"
    "ingress-configurator:latest"
    "postgresql-k8s:14"
    "self-signed-certificates:1"
)

if ! command -v charmcraft >/dev/null 2>&1; then
    snap install charmcraft --classic
fi

cd "$PROJECT_DIR"
CHARM_FILE="$(realpath "$(opcli artifacts path "$CHARM_NAME" --type charm)")"
if [ ! -f "$CHARM_FILE" ]; then
    echo "ERROR: built charm not found; run 'opcli artifacts build' or 'opcli artifacts fetch' first" >&2
    exit 1
fi
CHARM_PROJECT_DIR="$PROJECT_DIR/charm"
if [ ! -f "$CHARM_PROJECT_DIR/charmcraft.yaml" ]; then
    echo "ERROR: charm project not found at $CHARM_PROJECT_DIR" >&2
    exit 1
fi

# Point charmcraft at the local registry; the dev token is accepted through
# the Macaroon authorization scheme charmcraft uses for store requests.
export CHARMCRAFT_STORE_API_URL="$REGISTRY_API_URL"
export CHARMCRAFT_UPLOAD_URL="$REGISTRY_API_URL"
export CHARMCRAFT_REGISTRY_URL="${REGISTRY_OCI_URL:-$REGISTRY_API_URL}"
CHARMCRAFT_AUTH="$(printf '%s' "$REGISTRY_TOKEN" | base64 -w0)"
export CHARMCRAFT_AUTH

register_log="$(mktemp)"
cd "$CHARM_PROJECT_DIR"
if ! charmcraft register "$CHARM_NAME" >"$register_log" 2>&1; then
    if grep -qi "already" "$register_log"; then
        echo "Charm $CHARM_NAME is already registered"
    else
        cat "$register_log" >&2
        rm -f "$register_log"
        exit 1
    fi
fi
rm -f "$register_log"

latest_revision() {
    curl -fsS \
        -H "Authorization: Bearer ${REGISTRY_TOKEN}" \
        "${REGISTRY_API_URL}/v1/charm/${1}/revisions" |
        python3 -c 'import json, sys; print(max(int(item["revision"]) for item in json.load(sys.stdin)["revisions"]))'
}

charmcraft upload "$CHARM_FILE" --name "$CHARM_NAME"
revision="$(latest_revision "$CHARM_NAME")"
cd "$PROJECT_DIR"
charmcraft release "$CHARM_NAME" --revision "$revision" --channel "$CHANNEL"
echo "Published $CHARM_NAME revision $revision from $CHARM_FILE to $CHANNEL"

go build -o "$PROJECT_DIR/.bin/charm-registryctl" ./cmd/charm-registryctl

export CHARM_REGISTRY_URL="$REGISTRY_API_URL"
export CHARM_REGISTRY_TOKEN="$REGISTRY_TOKEN"
for dependency in "${DEPENDENCIES[@]}"; do
    name="${dependency%%:*}"
    track="${dependency##*:}"
    "$PROJECT_DIR/.bin/charm-registryctl" sync add "$name" --track "$track"
    "$PROJECT_DIR/.bin/charm-registryctl" sync run "$name"
done
"$PROJECT_DIR/.bin/charm-registryctl" sync wait --timeout 30m
