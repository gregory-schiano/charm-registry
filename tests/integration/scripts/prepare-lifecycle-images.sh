#!/bin/bash
# Materialize OCI image archives consumed by the Terraform lifecycle tests.
set -euo pipefail

PROJECT_DIR="${PROJECT_DIR:?PROJECT_DIR must be set (spread environment)}"
IMAGE_SOURCE="${LIFECYCLE_IMAGE_SOURCE:-docker://public.ecr.aws/docker/library/busybox:1.36.1}"
IMAGE_NAME="${LIFECYCLE_CHARM:-itest-lifecycle}"
AUTH_FILE="$PROJECT_DIR/.bin/skopeo-auth.json"

if ! command -v skopeo >/dev/null 2>&1; then
    echo "ERROR: skopeo is required to prepare lifecycle image archives" >&2
    exit 1
fi

mkdir -p "$PROJECT_DIR/.bin"
if [ ! -f "$AUTH_FILE" ]; then
    printf '{"auths": {}}\n' >"$AUTH_FILE"
fi

for tag in r1 r2; do
    image_file="$PROJECT_DIR/.bin/${IMAGE_NAME}-image-${tag}.tar"
    if [ -f "$image_file" ]; then
        echo "Reusing lifecycle image archive $image_file"
        continue
    fi

    for attempt in 1 2 3; do
        echo "Preparing lifecycle image archive $image_file from $IMAGE_SOURCE (attempt $attempt/3)"
        if skopeo copy \
            --insecure-policy \
            --authfile "$AUTH_FILE" \
            --retry-times 3 \
            "$IMAGE_SOURCE" \
            "oci-archive:${image_file}:${tag}"; then
            break
        fi
        rm -f "$image_file"
        if [ "$attempt" -eq 3 ]; then
            exit 1
        fi
        sleep $((attempt * 10))
    done
done
