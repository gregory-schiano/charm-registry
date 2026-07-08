#!/bin/bash
# Make the terraform-deployed registry's ingress hostnames consumable from
# this host and its Canonical K8s node:
#   - /etc/hosts entries map both hostnames to the gateway load-balancer IP
#     (CoreDNS forwards unresolved names to the host, so pods — including the
#     Juju controller — resolve them too);
#   - the deployment's self-signed CA is trusted system-wide (charmcraft's
#     bundled skopeo pushes images over HTTPS) and by the K8s containerd
#     (image pulls for the consumer model).
#
# Usage: setup-consumer-dns-cert.sh <api-hostname> <oci-hostname> <gateway-ip> <ca-file>
# Invoked with sudo from the Terraform integration test once the gateway IP
# and CA exist; tests/integration suite restore reverts everything.
set -euo pipefail

API_HOSTNAME="${1:?usage: $0 <api-hostname> <oci-hostname> <gateway-ip> <ca-file>}"
OCI_HOSTNAME="${2:?usage: $0 <api-hostname> <oci-hostname> <gateway-ip> <ca-file>}"
GATEWAY_IP="${3:?usage: $0 <api-hostname> <oci-hostname> <gateway-ip> <ca-file>}"
CA_FILE="${4:?usage: $0 <api-hostname> <oci-hostname> <gateway-ip> <ca-file>}"

MARKER="# charm-registry-tf-itest"

if [ ! -f "$CA_FILE" ]; then
    echo "ERROR: CA file not found: $CA_FILE" >&2
    exit 1
fi

# Refresh the /etc/hosts entries (idempotent via marker).
sed -i "\#${MARKER}#d" /etc/hosts
{
    echo "${GATEWAY_IP} ${API_HOSTNAME} ${MARKER}"
    echo "${GATEWAY_IP} ${OCI_HOSTNAME} ${MARKER}"
} >>/etc/hosts

# Trust the deployment CA system-wide (charmcraft/skopeo HTTPS pushes).
install -m 0644 "$CA_FILE" /usr/local/share/ca-certificates/charm-registry-tf-itest.crt
update-ca-certificates

# Trust the CA in the K8s containerd for consumer-model image pulls.
CHARM_REGISTRY_PUBLIC_REGISTRY_URL="https://${OCI_HOSTNAME}" \
    CHARM_REGISTRY_K8S_OCI_CA_FILE="$CA_FILE" \
    bash "$(dirname "$0")/../../../deploy/k8s/install-oci-cert.sh"

echo "Consumer DNS and CA trust configured for ${API_HOSTNAME} / ${OCI_HOSTNAME} -> ${GATEWAY_IP}"
