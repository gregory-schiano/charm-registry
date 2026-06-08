#!/usr/bin/env bash
# wait-for-healthy.sh — Poll the charm-registry health endpoint until it
# returns 200 or the timeout expires. Used by the integration-test Makefile
# target to ensure the compose stack is ready before running tests.
set -euo pipefail

API_URL="${ITEST_API_URL:-http://localhost:18080}"
MAX_ATTEMPTS="${ITEST_MAX_ATTEMPTS:-60}"
SLEEP_SEC="${ITEST_SLEEP_SEC:-2}"

echo "Waiting for ${API_URL}/healthz to return 200..."

attempt=0
while [ $((attempt)) -lt "${MAX_ATTEMPTS}" ]; do
	if curl -sf -o /dev/null "${API_URL}/healthz" 2>/dev/null; then
		echo "Service is healthy after $((attempt * SLEEP_SEC))s"
		exit 0
	fi
	attempt=$((attempt + 1))
	sleep "${SLEEP_SEC}"
done

echo "ERROR: Service did not become healthy within $((MAX_ATTEMPTS * SLEEP_SEC))s" >&2
echo "Checking docker compose status..."
docker compose -f compose.integration.yaml ps 2>/dev/null || true
docker compose -f compose.integration.yaml logs charm-registry-itest --tail 50 2>/dev/null || true
exit 1
