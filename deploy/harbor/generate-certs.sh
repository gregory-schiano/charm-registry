#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
CERT_DIR="${ROOT_DIR}/certs"

if [[ -f "${CERT_DIR}/ca.crt" && -f "${CERT_DIR}/registry.crt" && -f "${CERT_DIR}/registry.key" ]]; then
    exit 0
fi

mkdir -p "${CERT_DIR}"

docker run --rm \
    -v "${CERT_DIR}:/certs" \
    alpine:3.21 \
    sh -euc '
        apk add --no-cache openssl >/dev/null 2>&1
        openssl genrsa -out /certs/ca.key 4096
        openssl req -new -x509 -days 3650 \
          -key /certs/ca.key \
          -out /certs/ca.crt \
          -subj "/CN=Local Charm Registry CA"
        openssl genrsa -out /certs/registry.key 2048
        openssl req -new \
          -key /certs/registry.key \
          -out /certs/registry.csr \
          -subj "/CN=localhost"
        printf "subjectAltName=DNS:localhost,IP:127.0.0.1\n" > /tmp/san.ext
        openssl x509 -req -days 3650 \
          -in /certs/registry.csr \
          -CA /certs/ca.crt -CAkey /certs/ca.key -CAcreateserial \
          -out /certs/registry.crt \
          -extfile /tmp/san.ext
        chmod 644 /certs/ca.crt /certs/registry.crt
        chmod 600 /certs/ca.key /certs/registry.key
    '
