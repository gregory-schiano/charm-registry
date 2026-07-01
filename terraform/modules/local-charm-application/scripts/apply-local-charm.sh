#!/usr/bin/env bash
set -euo pipefail

config_file="$(mktemp)"
trap 'rm -f "${config_file}"' EXIT

python3 - "${APP_NAME}" "${APP_CONFIG_JSON}" >"${config_file}" <<'PY'
import json
import sys

app_name = sys.argv[1]
config = json.loads(sys.argv[2])
print(json.dumps({app_name: config}))
PY

if juju status "${APP_NAME}" -m "${MODEL_NAME}" --format json >/dev/null 2>&1; then
  juju refresh "${APP_NAME}" \
    -m "${MODEL_NAME}" \
    --path "${CHARM_FILE}" \
    --resource "app-image=${APP_IMAGE}" \
    --config "${config_file}"
else
  juju deploy "${CHARM_FILE}" "${APP_NAME}" \
    -m "${MODEL_NAME}" \
    --resource "app-image=${APP_IMAGE}" \
    --config "${config_file}"
fi

juju wait-for application "${APP_NAME}" -m "${MODEL_NAME}" --timeout="${WAIT_TIMEOUT}"
