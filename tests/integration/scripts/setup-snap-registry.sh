#!/bin/bash
# Stand up the spellbook snap as a local charm registry for the Terraform
# integration suite and export the registry contract for pytest.
#
# Runs as root from the spread suite prepare hook. Installs the built snap
# (located via opcli's artifacts.build.yaml), configures and starts it, trusts
# its OCI certificate in the local Kubernetes containerd, and publishes the
# REGISTRY_* values through /etc/profile.d for the pytest process.
set -euo pipefail

PROJECT_DIR="${PROJECT_DIR:?PROJECT_DIR must be set (spread environment)}"
ENV_FILE=/etc/profile.d/zz-charm-registry-registry.sh
export PATH="/snap/bin:/usr/local/bin:$PATH"

if ! command -v terraform >/dev/null 2>&1; then
    snap install terraform --classic
fi

host_ip() {
    ip -4 route get 1.1.1.1 | awk '{for (i = 1; i < NF; i++) if ($i == "src") {print $(i + 1); exit}}'
}

REGISTRY_HOST="$(host_ip)"
if [ -z "$REGISTRY_HOST" ]; then
    echo "ERROR: unable to determine host IP" >&2
    exit 1
fi
REGISTRY_API_URL="http://${REGISTRY_HOST}:8080"
REGISTRY_OCI_URL="https://${REGISTRY_HOST}:5000"
REGISTRY_TOKEN="dev:admin:admin"

cd "$PROJECT_DIR"
SNAP_FILE="$(opcli artifacts path spellbook --type snap)"
if [ ! -f "$SNAP_FILE" ]; then
    echo "ERROR: built spellbook snap not found; run 'opcli artifacts build' or 'opcli artifacts fetch' first" >&2
    exit 1
fi

snap install --dangerous "$SNAP_FILE"
snap set spellbook \
    admin.usernames=admin \
    insecure-dev-auth=true \
    oci.secret-key=integration-test-oci-secret \
    limits.max-archive-file-bytes=64MB \
    "public-api-url=${REGISTRY_API_URL}" \
    "public-storage-url=${REGISTRY_API_URL}" \
    "public-registry-url=${REGISTRY_OCI_URL}" \
    rate-limit.ip-limit=0 \
    rate-limit.token-limit=0

# The snap configure hook regenerates the self-signed OCI certificate when the
# public registry host changes, but the workload does not hot-reload TLS files.
# Restart after snap set so Kubernetes/containerd receives the same CA that the
# registry is actually serving.
snap restart spellbook.charm-registry || snap start spellbook.charm-registry

if ! grep -Fxq "$REGISTRY_HOST" /var/snap/spellbook/common/certs/oci.hosts; then
    echo "ERROR: generated OCI certificate does not cover ${REGISTRY_HOST}" >&2
    echo "Generated hosts:" >&2
    cat /var/snap/spellbook/common/certs/oci.hosts >&2 || true
    exit 1
fi

for _ in $(seq 1 120); do
    if curl -sf "${REGISTRY_API_URL}/healthz" >/dev/null; then
        echo "charm-registry snap is healthy at ${REGISTRY_API_URL}"
        break
    fi
    sleep 1
done
if ! curl -sf "${REGISTRY_API_URL}/healthz" >/dev/null; then
    snap logs spellbook.charm-registry -n 100 || true
    echo "ERROR: snap registry did not become healthy" >&2
    exit 1
fi

CHARM_REGISTRY_PUBLIC_REGISTRY_URL="$REGISTRY_OCI_URL" \
    CHARM_REGISTRY_K8S_OCI_CA_FILE=/var/snap/spellbook/common/certs/oci.crt \
    bash "$PROJECT_DIR/deploy/k8s/install-oci-cert.sh"

cat >"$ENV_FILE" <<EOF
export REGISTRY_API_URL="$REGISTRY_API_URL"
export REGISTRY_OCI_URL="$REGISTRY_OCI_URL"
export REGISTRY_TOKEN="$REGISTRY_TOKEN"
EOF
chmod 0644 "$ENV_FILE"
echo "Wrote registry test environment to $ENV_FILE"
