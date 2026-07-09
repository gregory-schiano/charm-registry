#!/bin/bash
# Provision a single-node MicroCeph RGW endpoint for the charm integration
# suite and export the S3 contract for pytest.
#
# Runs as root from the spread suite prepare hook. Publishes the resulting
# JUB_S3_* values through /etc/profile.d so the pytest process (started via a
# `runuser -l ubuntu` login shell) inherits them; the tests only consume these
# variables and never provision host services themselves.
set -euo pipefail

ENV_FILE=/etc/profile.d/zz-charm-registry-s3.sh

ACCESS_KEY="${JUB_S3_ACCESS_KEY:-charm-registry}"
SECRET_KEY="${JUB_S3_SECRET_KEY:-charm-registry-secret}"
BUCKET="${JUB_S3_BUCKET:-charm-registry-artifacts}"
OCI_BUCKET="${JUB_OCI_S3_BUCKET:-charm-registry-oci}"
REGION="${JUB_S3_REGION:-us-east-1}"
RGW_PORT="${JUB_MICROCEPH_RGW_PORT:-8081}"

first_host_ip() {
    local address
    for address in $(hostname -I); do
        case "$address" in
        127.*) ;;
        *)
            echo "$address"
            return 0
            ;;
        esac
    done
    echo "127.0.0.1"
}

RGW_HOST="${JUB_MICROCEPH_RGW_HOST:-$(first_host_ip)}"
ENDPOINT="${JUB_S3_ENDPOINT:-http://${RGW_HOST}:${RGW_PORT}}"
ENDPOINT="${ENDPOINT%/}"

if ! snap list microceph >/dev/null 2>&1; then
    snap install microceph
fi
snap refresh --hold microceph

if ! microceph status >/dev/null 2>&1; then
    microceph cluster bootstrap
    microceph disk add loop,2G,3
elif microceph status | grep -q "Disks: 0"; then
    microceph disk add loop,2G,3
fi

for _ in $(seq 1 60); do
    status="$(microceph status)"
    if echo "$status" | grep -Eq "Disks: 3| osd"; then
        break
    fi
    sleep 5
done

node_name="$(microceph status | awk '/^- / {gsub(/\(.*/, "", $2); print $2; exit}')"
if [ -z "$node_name" ]; then
    echo "ERROR: could not determine MicroCeph node name" >&2
    microceph status >&2
    exit 1
fi

if ! microceph status | grep "Services:" | grep -q "rgw"; then
    microceph enable rgw --target "$node_name" --port "$RGW_PORT"
fi

if ! microceph.radosgw-admin user create \
    --uid charm-registry \
    --display-name "charm-registry integration tests" \
    --access-key "$ACCESS_KEY" \
    --secret-key "$SECRET_KEY"; then
    echo "Reusing existing RGW user charm-registry"
fi

for _ in $(seq 1 60); do
    if curl --max-time 2 -sS -o /dev/null "$ENDPOINT"; then
        echo "MicroCeph RGW endpoint is reachable: $ENDPOINT"
        break
    fi
    sleep 2
done
if ! curl --max-time 2 -sS -o /dev/null "$ENDPOINT"; then
    echo "ERROR: MicroCeph RGW endpoint did not become reachable: $ENDPOINT" >&2
    exit 1
fi

if ! command -v aws >/dev/null 2>&1; then
    apt-get update
    if ! DEBIAN_FRONTEND=noninteractive apt-get install -y awscli; then
        snap install aws-cli --classic
    fi
fi

ensure_bucket() {
    local bucket="$1"
    if microceph.radosgw-admin bucket stats --bucket "$bucket" >/dev/null 2>&1; then
        echo "Reusing existing RGW bucket $bucket"
        return
    fi

    create_args=(--endpoint-url "$ENDPOINT" s3api create-bucket --bucket "$bucket")
    if [ "$REGION" != "us-east-1" ]; then
        create_args+=(--create-bucket-configuration "LocationConstraint=$REGION")
    fi
    create_log="$(mktemp)"
    if ! AWS_ACCESS_KEY_ID="$ACCESS_KEY" \
        AWS_SECRET_ACCESS_KEY="$SECRET_KEY" \
        AWS_DEFAULT_REGION="$REGION" \
        AWS_EC2_METADATA_DISABLED=true \
        AWS_PAGER="" \
        aws "${create_args[@]}" >"$create_log" 2>&1; then
        cat "$create_log" >&2
        rm -f "$create_log"
        exit 1
    fi
    rm -f "$create_log"
    echo "Created RGW bucket $bucket"
}

ensure_bucket "$BUCKET"
ensure_bucket "$OCI_BUCKET"

cat >"$ENV_FILE" <<EOF
export JUB_S3_ENDPOINT="$ENDPOINT"
export JUB_S3_BUCKET="$BUCKET"
export JUB_OCI_S3_BUCKET="$OCI_BUCKET"
export JUB_S3_REGION="$REGION"
export JUB_S3_ACCESS_KEY="$ACCESS_KEY"
export JUB_S3_SECRET_KEY="$SECRET_KEY"
export JUB_S3_URI_STYLE="${JUB_S3_URI_STYLE:-path}"
export JUB_S3_MICROCEPH="true"
EOF
chmod 0644 "$ENV_FILE"
echo "Wrote S3 test environment to $ENV_FILE"
