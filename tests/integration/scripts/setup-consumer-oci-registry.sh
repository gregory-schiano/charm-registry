#!/bin/bash
# Make the terraform-deployed registry's pod-IP OCI endpoint consumable from
# this host and its Canonical K8s node. The lifecycle test configures the charm
# to advertise IP-based API/storage/OCI URLs, so no host or CoreDNS overrides
# are needed. The embedded OCI listener is plain HTTP, so configure
# containers/image (used by charmcraft/skopeo) and K8s containerd to treat that
# exact registry host as insecure.
#
# Usage: setup-consumer-oci-registry.sh <oci-registry-url>
# Invoked with sudo from the Terraform integration test once the deployed app
# pod IP exists; tests/integration suite restore reverts everything.
set -euo pipefail

OCI_REGISTRY_URL="${1:?usage: $0 <oci-registry-url>}"

MARKER="# charm-registry-tf-itest"
STATE_FILE="/var/lib/charm-registry-tf-itest-containerd-hosts"
REGISTRIES_CONF="/etc/containers/registries.conf.d/charm-registry-tf-itest.conf"

if [[ "$OCI_REGISTRY_URL" != http://* ]]; then
    echo "ERROR: expected an http:// OCI registry URL, got: ${OCI_REGISTRY_URL}" >&2
    exit 1
fi

registry_host="${OCI_REGISTRY_URL#http://}"
registry_host="${registry_host%%/*}"
if [ -z "$registry_host" ]; then
    echo "ERROR: cannot derive registry host from ${OCI_REGISTRY_URL}" >&2
    exit 1
fi

mkdir -p "$(dirname "$REGISTRIES_CONF")"
cat >"$REGISTRIES_CONF" <<EOF
[[registry]]
location = "$registry_host"
insecure = true
# $MARKER
EOF

install_containerd_host() {
    local root="$1"
    local host_dir="${root}/${registry_host}"
    case "$root" in
    # Always create the default containerd roots; the k8s snap reads
    # /etc/containerd/hosts.d even when the directory does not exist yet.
    /etc/containerd/*) ;;
    *)
        if [ ! -d "$(dirname "$root")" ]; then
            return
        fi
        ;;
    esac
    mkdir -p "$host_dir"
    cat >"${host_dir}/hosts.toml" <<EOF
server = "${OCI_REGISTRY_URL}"

[host."${OCI_REGISTRY_URL}"]
  capabilities = ["pull", "resolve", "push"]
  # $MARKER
EOF
    echo "$host_dir" >>"$STATE_FILE"
    echo "Installed insecure OCI registry config for ${registry_host} into ${host_dir}"
}

rm -f "$STATE_FILE"
# Canonical K8s containerd reads hosts.d (see the k8s snap registry docs);
# classic containerd defaults to certs.d. Cover both across known roots.
for certs_root in \
    /etc/containerd/certs.d \
    /etc/containerd/hosts.d \
    /ck8s/k8s-containerd/etc/containerd/certs.d \
    /ck8s/k8s-containerd/etc/containerd/hosts.d \
    /var/snap/k8s/common/etc/containerd/certs.d \
    /var/snap/k8s/common/etc/containerd/hosts.d; do
    install_containerd_host "$certs_root"
done

if systemctl is-active --quiet containerd 2>/dev/null; then
    systemctl restart containerd
fi
if systemctl is-active --quiet snap.k8s.containerd.service 2>/dev/null; then
    systemctl restart snap.k8s.containerd.service
fi
if snap services k8s.containerd >/dev/null 2>&1; then
    snap restart k8s.containerd || true
fi

echo "Consumer OCI registry configured for ${OCI_REGISTRY_URL}"
