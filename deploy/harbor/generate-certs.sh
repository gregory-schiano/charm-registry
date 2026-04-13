#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
CERT_DIR="${ROOT_DIR}/certs"
ENV_FILE="${ROOT_DIR}/.env"

if [[ -f "${ENV_FILE}" ]]; then
    set -a
    # shellcheck disable=SC1090
    . "${ENV_FILE}"
    set +a
fi

declare -A dns_hosts=()
declare -A ip_hosts=()

extract_host_from_url() {
    local url="${1:-}"
    local authority
    if [[ -z "${url}" ]]; then
        return 0
    fi
    authority="${url#*://}"
    authority="${authority%%/*}"
    authority="${authority%%\?*}"
    authority="${authority%%\#*}"
    authority="${authority##*@}"
    if [[ "${authority}" == \[* ]]; then
        authority="${authority%%]*}"
        authority="${authority#[}"
        printf '%s\n' "${authority}"
        return 0
    fi
    if [[ "${authority}" == *:* ]]; then
        printf '%s\n' "${authority%%:*}"
        return 0
    fi
    printf '%s\n' "${authority}"
}

is_ip_address() {
    local host="${1:-}"
    [[ "${host}" =~ ^([0-9]{1,3}\.){3}[0-9]{1,3}$ || "${host}" == *:* ]]
}

add_cert_host() {
    local host="${1:-}"
    host="${host#[}"
    host="${host%]}"
    if [[ -z "${host}" ]]; then
        return 0
    fi
    if is_ip_address "${host}"; then
        ip_hosts["${host}"]=1
    else
        dns_hosts["${host}"]=1
    fi
}

collect_cert_hosts() {
    add_cert_host "localhost"
    add_cert_host "127.0.0.1"
    add_cert_host "$(extract_host_from_url "${CHARM_REGISTRY_PUBLIC_REGISTRY_URL:-}")"
    add_cert_host "$(extract_host_from_url "${CHARM_REGISTRY_HARBOR_URL:-}")"
}

cert_covers_desired_hosts() {
    local san_text
    if [[ ! -f "${CERT_DIR}/registry.crt" ]]; then
        return 1
    fi
    san_text="$(openssl x509 -in "${CERT_DIR}/registry.crt" -noout -ext subjectAltName 2>/dev/null || true)"
    if [[ -z "${san_text}" ]]; then
        return 1
    fi
    local host
    for host in "${dns_host_list[@]}"; do
        [[ "${san_text}" == *"DNS:${host}"* ]] || return 1
    done
    for host in "${ip_host_list[@]}"; do
        [[ "${san_text}" == *"IP Address:${host}"* ]] || return 1
    done
}

collect_cert_hosts
mapfile -t dns_host_list < <(printf '%s\n' "${!dns_hosts[@]}" | sort)
mapfile -t ip_host_list < <(printf '%s\n' "${!ip_hosts[@]}" | sort)

common_name="localhost"
if [[ "${#dns_host_list[@]}" -gt 0 ]]; then
    common_name="${dns_host_list[0]}"
elif [[ "${#ip_host_list[@]}" -gt 0 ]]; then
    common_name="${ip_host_list[0]}"
fi

san_entries=()
for host in "${dns_host_list[@]}"; do
    san_entries+=("DNS:${host}")
done
for host in "${ip_host_list[@]}"; do
    san_entries+=("IP:${host}")
done
san_line="$(IFS=,; printf '%s' "${san_entries[*]}")"

if [[ -f "${CERT_DIR}/ca.crt" && -f "${CERT_DIR}/ca.key" && -f "${CERT_DIR}/registry.key" ]] && cert_covers_desired_hosts; then
    exit 0
fi

mkdir -p "${CERT_DIR}"

docker run --rm \
    -e COMMON_NAME="${common_name}" \
    -e SAN_LINE="${san_line}" \
    -v "${CERT_DIR}:/certs" \
    alpine:3.21 \
    sh -euc '
        apk add --no-cache openssl >/dev/null 2>&1
        if [ ! -f /certs/ca.key ] || [ ! -f /certs/ca.crt ]; then
          openssl genrsa -out /certs/ca.key 4096
          openssl req -new -x509 -days 3650 \
            -key /certs/ca.key \
            -out /certs/ca.crt \
            -subj "/CN=Local Charm Registry CA"
        fi
        rm -f /certs/registry.key /certs/registry.csr /certs/registry.crt
        openssl genrsa -out /certs/registry.key 2048
        openssl req -new \
          -key /certs/registry.key \
          -out /certs/registry.csr \
          -subj "/CN=${COMMON_NAME}"
        printf "subjectAltName=%s\n" "${SAN_LINE}" > /tmp/san.ext
        openssl x509 -req -days 3650 \
          -in /certs/registry.csr \
          -CA /certs/ca.crt -CAkey /certs/ca.key -CAcreateserial \
          -out /certs/registry.crt \
          -extfile /tmp/san.ext
        chmod 644 /certs/ca.crt /certs/registry.crt
        chmod 600 /certs/ca.key /certs/registry.key
    '
