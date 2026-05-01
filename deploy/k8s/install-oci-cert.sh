#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
env_registry_url="${CHARM_REGISTRY_PUBLIC_REGISTRY_URL:-}"
env_cert_file="${CHARM_REGISTRY_K8S_OCI_CA_FILE:-}"
env_certs_root="${CHARM_REGISTRY_K8S_CONTAINERD_CERTS_DIR:-}"

if [[ -f "${ROOT_DIR}/.env" ]]; then
	set -a
	# shellcheck disable=SC1091
	. "${ROOT_DIR}/.env"
	set +a
fi

if [[ -n "${env_registry_url}" ]]; then
	CHARM_REGISTRY_PUBLIC_REGISTRY_URL="${env_registry_url}"
fi
if [[ -n "${env_cert_file}" ]]; then
	CHARM_REGISTRY_K8S_OCI_CA_FILE="${env_cert_file}"
fi
if [[ -n "${env_certs_root}" ]]; then
	CHARM_REGISTRY_K8S_CONTAINERD_CERTS_DIR="${env_certs_root}"
fi

registry_url="${CHARM_REGISTRY_PUBLIC_REGISTRY_URL:-https://localhost:5000}"
registry_host="${registry_url#*://}"
registry_host="${registry_host%%/*}"
cert_file="${CHARM_REGISTRY_K8S_OCI_CA_FILE:-${ROOT_DIR}/certs/oci.crt}"
certs_root="${CHARM_REGISTRY_K8S_CONTAINERD_CERTS_DIR:-/ck8s/k8s-containerd/etc/containerd/certs.d}"
target_dir="${certs_root}/${registry_host}"
target_ca="${target_dir}/ca.crt"
target_hosts="${target_dir}/hosts.toml"

if [[ -z "${registry_host}" ]]; then
	echo "ERROR: cannot derive registry host from CHARM_REGISTRY_PUBLIC_REGISTRY_URL=${registry_url}" >&2
	exit 1
fi
if [[ "${registry_url}" != https://* ]]; then
	echo "ERROR: CHARM_REGISTRY_PUBLIC_REGISTRY_URL must be https for CA installation: ${registry_url}" >&2
	exit 1
fi
if [[ ! -f "${cert_file}" ]]; then
	echo "ERROR: ${cert_file} not found. Run 'make generate-cert' first." >&2
	exit 1
fi

sudo mkdir -p "${target_dir}"
sudo cp "${cert_file}" "${target_ca}"
sudo chmod 0644 "${target_ca}"
cat <<EOF | sudo tee "${target_hosts}" >/dev/null
server = "${registry_url}"

[host."${registry_url}"]
  capabilities = ["pull", "resolve"]
  ca = "${target_ca}"
EOF

if snap services k8s 2>/dev/null | grep -q '^k8s\.containerd'; then
	sudo snap restart k8s.containerd
else
	echo "WARNING: k8s.containerd snap service not found; restart your Kubernetes containerd manually." >&2
fi

echo "Installed OCI registry CA for ${registry_host} into ${target_dir}"
