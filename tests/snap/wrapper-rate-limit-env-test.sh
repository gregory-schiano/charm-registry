#!/bin/sh
set -eu

ROOT_DIR="$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)"
WRAPPER="$ROOT_DIR/snap/local/charm-registry-wrapper"

work_dir="$(mktemp -d)"
trap 'rm -rf "$work_dir"' EXIT

mkdir -p "$work_dir/bin" "$work_dir/snap/bin" "$work_dir/common" "$work_dir/config"

cat >"$work_dir/bin/snapctl" <<'SNAPCTL'
#!/bin/sh
set -eu
if [ "$1" = "get" ]; then
	key="$2"
	if [ -f "$SNAP_TEST_CONFIG/$key" ]; then
		cat "$SNAP_TEST_CONFIG/$key"
	fi
	exit 0
fi
echo "unexpected snapctl invocation: $*" >&2
exit 2
SNAPCTL
chmod +x "$work_dir/bin/snapctl"

cat >"$work_dir/snap/bin/charm-registry" <<'REGISTRY'
#!/bin/sh
set -eu
printf '%s\n' \
	"CHARM_REGISTRY_IP_RATE_LIMIT=$CHARM_REGISTRY_IP_RATE_LIMIT" \
	"CHARM_REGISTRY_IP_RATE_WINDOW=$CHARM_REGISTRY_IP_RATE_WINDOW" \
	"CHARM_REGISTRY_TOKEN_RATE_LIMIT=$CHARM_REGISTRY_TOKEN_RATE_LIMIT" \
	"CHARM_REGISTRY_TOKEN_RATE_WINDOW=$CHARM_REGISTRY_TOKEN_RATE_WINDOW"
REGISTRY
chmod +x "$work_dir/snap/bin/charm-registry"

printf '120' >"$work_dir/config/rate-limit.ip-limit"
printf '30s' >"$work_dir/config/rate-limit.ip-window"
printf '5' >"$work_dir/config/rate-limit.token-limit"
printf '1m' >"$work_dir/config/rate-limit.token-window"

output="$(SNAP="$work_dir/snap" \
	SNAP_COMMON="$work_dir/common" \
	SNAP_TEST_CONFIG="$work_dir/config" \
	PATH="$work_dir/bin:$PATH" \
	"$WRAPPER")"

expected='CHARM_REGISTRY_IP_RATE_LIMIT=120
CHARM_REGISTRY_IP_RATE_WINDOW=30s
CHARM_REGISTRY_TOKEN_RATE_LIMIT=5
CHARM_REGISTRY_TOKEN_RATE_WINDOW=1m'

if [ "$output" != "$expected" ]; then
	echo "unexpected wrapper env output" >&2
	echo "expected:" >&2
	echo "$expected" >&2
	echo "actual:" >&2
	echo "$output" >&2
	exit 1
fi

echo "wrapper rate-limit env mapping test passed"
