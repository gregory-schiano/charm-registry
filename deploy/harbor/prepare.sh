#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
ENV_FILE="${ROOT_DIR}/.env"

if [[ -f "${ENV_FILE}" ]]; then
    set -a
    # shellcheck disable=SC1090
    . "${ENV_FILE}"
    set +a
fi

HARBOR_VERSION="${HARBOR_VERSION:-v2.14.1}"
VENDOR_DIR="${ROOT_DIR}/deploy/harbor/vendor"
INSTALLER_DIR="${VENDOR_DIR}/harbor"
TARBALL="${VENDOR_DIR}/harbor-online-installer-${HARBOR_VERSION}.tgz"
TEMPLATE="${ROOT_DIR}/deploy/harbor/harbor.yml.tmpl"
CERT_FILE="${ROOT_DIR}/certs/registry.crt"
KEY_FILE="${ROOT_DIR}/certs/registry.key"
DATA_VOLUME="${INSTALLER_DIR}/data"
LOG_LOCATION="${DATA_VOLUME}/log"
HARBOR_YML="${INSTALLER_DIR}/harbor.yml"
HARBOR_HTTP_PORT="${HARBOR_HTTP_PORT:-9081}"
HARBOR_HTTPS_PORT="${HARBOR_HTTPS_PORT:-9443}"
HARBOR_EXTERNAL_URL="${CHARM_REGISTRY_HARBOR_URL:-https://localhost:${HARBOR_HTTPS_PORT}}"

mkdir -p "${VENDOR_DIR}"

if [[ ! -d "${INSTALLER_DIR}" ]]; then
    if [[ ! -f "${TARBALL}" ]]; then
        curl -fsSL \
            "https://github.com/goharbor/harbor/releases/download/${HARBOR_VERSION}/harbor-online-installer-${HARBOR_VERSION}.tgz" \
            -o "${TARBALL}"
    fi
    tar -xzf "${TARBALL}" -C "${VENDOR_DIR}"
fi

mkdir -p "${DATA_VOLUME}" "${LOG_LOCATION}"

cp "${TEMPLATE}" "${HARBOR_YML}"

sed -i \
    -e "s|__CERT_FILE__|${CERT_FILE}|g" \
    -e "s|__KEY_FILE__|${KEY_FILE}|g" \
    -e "s|__HARBOR_ADMIN_PASSWORD__|${CHARM_REGISTRY_HARBOR_ADMIN_PASSWORD:-Harbor12345}|g" \
    -e "s|__HARBOR_DATABASE_PASSWORD__|${HARBOR_DATABASE_PASSWORD:-root123}|g" \
    -e "s|__HARBOR_HTTP_PORT__|${HARBOR_HTTP_PORT}|g" \
    -e "s|__HARBOR_HTTPS_PORT__|${HARBOR_HTTPS_PORT}|g" \
    -e "s|__HARBOR_EXTERNAL_URL__|${HARBOR_EXTERNAL_URL}|g" \
    -e "s|__DATA_VOLUME__|${DATA_VOLUME}|g" \
    -e "s|__LOG_LOCATION__|${LOG_LOCATION}|g" \
    -e "s|__HARBOR_S3_ACCESS_KEY_ID__|${HARBOR_S3_ACCESS_KEY_ID:-harbor-registry}|g" \
    -e "s|__HARBOR_S3_SECRET_ACCESS_KEY__|${HARBOR_S3_SECRET_ACCESS_KEY:-harbor-registry-secret}|g" \
    -e "s|__HARBOR_S3_REGION__|${HARBOR_S3_REGION:-us-east-1}|g" \
    -e "s|__HARBOR_S3_REGION_ENDPOINT__|${HARBOR_S3_REGION_ENDPOINT:-http://minio:9000}|g" \
    -e "s|__HARBOR_S3_BUCKET__|${HARBOR_S3_BUCKET:-harbor-registry}|g" \
    -e "s|__HARBOR_S3_SECURE__|${HARBOR_S3_SECURE:-false}|g" \
    "${HARBOR_YML}"

(
    cd "${INSTALLER_DIR}"
    ./prepare
)

docker run --rm \
    -v "${INSTALLER_DIR}:/work" \
    python:3.12-alpine \
    sh -euc '
        python - <<'"'"'PY'"'"'
from pathlib import Path

compose_path = Path("/work/docker-compose.yml")
content = compose_path.read_text()
content = content.replace(
    "networks:\n  harbor:\n    external: false\n",
    "networks:\n  harbor:\n    external: true\n    name: charm-registry-shared\n",
)
content = content.replace("container_name: ", "container_name: charm-registry-")
content = content.replace(
    "    networks:\n      - harbor\n    ports:\n",
    "    networks:\n      harbor:\n        aliases:\n          - harbor-proxy\n    ports:\n",
    1,
)
lines = content.splitlines()
for idx, line in enumerate(lines):
    if line.strip().endswith(":/var/lib/postgresql/data:z"):
        lines[idx] = "      - harbor-db-data:/var/lib/postgresql/data"
        break
content = "\n".join(lines) + "\n"
if "volumes:\n  harbor-db-data:\n" not in content:
    content = content.replace(
        "networks:\n  harbor:\n    external: true\n    name: charm-registry-shared\n",
        "volumes:\n  harbor-db-data:\n\nnetworks:\n  harbor:\n    external: true\n    name: charm-registry-shared\n",
    )
compose_path.write_text(content)
PY
        chmod u+w /work/docker-compose.yml /work/harbor.yml
        chmod -R a+rX,u+w /work/common
        chown -R 999:999 /work/data/database
        chmod 700 /work/data/database
        find /work/data/database -type d -exec chmod 700 {} +
        find /work/data/database -type f -exec chmod 600 {} +
    '
