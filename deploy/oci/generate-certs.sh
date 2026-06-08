#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
CERT_DIR="${ROOT_DIR}/certs"
HOSTS_FILE="${CERT_DIR}/oci.hosts"

if [[ -f "${ROOT_DIR}/.env" ]]; then
	set -a
	# shellcheck disable=SC1091
	. "${ROOT_DIR}/.env"
	set +a
fi

public_url="${CHARM_REGISTRY_PUBLIC_REGISTRY_URL:-https://localhost:5000}"
internal_url="${CHARM_REGISTRY_OCI_INTERNAL_URL:-https://127.0.0.1:5000}"

url_host() {
	local raw="$1"
	raw="${raw#*://}"
	raw="${raw%%/*}"
	raw="${raw%%:*}"
	printf '%s\n' "${raw}"
}

is_ip() {
	[[ "$1" =~ ^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+$ || "$1" == *:* ]]
}

public_host="$(url_host "${public_url}")"
internal_host="$(url_host "${internal_url}")"

hosts=(
	"localhost"
	"127.0.0.1"
	"::1"
)
for host in "${public_host}" "${internal_host}"; do
	if [[ -n "${host}" ]]; then
		hosts+=("${host}")
	fi
done

unique_hosts="$(printf '%s\n' "${hosts[@]}" | awk 'NF && !seen[$0]++')"
mkdir -p "${CERT_DIR}"

if [[ -f "${CERT_DIR}/oci.crt" && -f "${CERT_DIR}/oci.key" && -f "${HOSTS_FILE}" ]] &&
	[[ "$(cat "${HOSTS_FILE}")" == "${unique_hosts}" ]]; then
	exit 0
fi

san=""
while IFS= read -r host; do
	if is_ip "${host}"; then
		san="${san}IP:${host},"
	else
		san="${san}DNS:${host},"
	fi
done <<<"${unique_hosts}"
san="${san%,}"

openssl req -x509 -newkey rsa:4096 -sha256 -days 365 -nodes \
	-keyout "${CERT_DIR}/oci.key" \
	-out "${CERT_DIR}/oci.crt" \
	-subj "/CN=${public_host:-localhost}" \
	-addext "subjectAltName=${san}" >/dev/null 2>&1

chmod 0644 "${CERT_DIR}/oci.crt"
# The compose image runs as distroless nonroot (uid/gid 65532), while the
# generated files are owned by the host user. Restrict the private key to
# owner + group; production deployments should mount a tighter secret.
chmod 0640 "${CERT_DIR}/oci.key"
printf '%s\n' "${unique_hosts}" >"${HOSTS_FILE}"
