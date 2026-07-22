#!/bin/sh
set -eu

ROOT_DIR="$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)"
CONFIGURE_HOOK="$ROOT_DIR/snap/hooks/configure"

fail() {
	echo "FAIL: $*" >&2
	exit 1
}

run_configure() {
	work_dir="$(mktemp -d)"
	mkdir -p "$work_dir/bin" "$work_dir/snap/usr/bin" "$work_dir/snap/bin" "$work_dir/common" "$work_dir/config"
	cp "$ROOT_DIR/snap/local/charm-registry-snap-helpers" "$work_dir/snap/bin/charm-registry-snap-helpers"

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

	cat >"$work_dir/snap/usr/bin/openssl" <<'OPENSSL'
#!/bin/sh
set -eu
out=""
keyout=""
while [ "$#" -gt 0 ]; do
	case "$1" in
		-out) shift; out="$1" ;;
		-keyout) shift; keyout="$1" ;;
	esac
	shift || true
done
[ -n "$out" ] && : >"$out"
[ -n "$keyout" ] && : >"$keyout"
OPENSSL
	chmod +x "$work_dir/snap/usr/bin/openssl"

	while [ "$#" -gt 0 ]; do
		key="$1"
		value="$2"
		shift 2
		printf '%s' "$value" >"$work_dir/config/$key"
	done

	SNAP="$work_dir/snap" \
	SNAP_COMMON="$work_dir/common" \
	SNAP_TEST_CONFIG="$work_dir/config" \
	PATH="$work_dir/bin:$PATH" \
		"$CONFIGURE_HOOK" >/"$work_dir/stdout" 2>/"$work_dir/stderr"
	status=$?
	cat "$work_dir/stderr" >&2
	rm -rf "$work_dir"
	return "$status"
}

assert_accepts() {
	name="$1"
	shift
	if ! run_configure "$@"; then
		fail "$name should be accepted"
	fi
}

assert_rejects() {
	name="$1"
	shift
	if run_configure "$@"; then
		fail "$name should be rejected"
	fi
}

assert_accepts "unset rate limit options"
assert_accepts "valid rate limit options" \
	rate-limit.ip-limit 100 \
	rate-limit.ip-window 1m \
	rate-limit.token-limit 5 \
	rate-limit.token-window 30s
assert_accepts "zero disables limit" rate-limit.ip-limit 0 rate-limit.token-limit 0
assert_accepts "valid byte limits" \
	limits.max-archive-file-bytes 32MB \
	limits.max-upload-bytes 128MB \
	charmhub.max-artifact-bytes 1GB

assert_rejects "negative IP limit" rate-limit.ip-limit -1
assert_rejects "non-integer IP limit" rate-limit.ip-limit ten
assert_rejects "negative token limit" rate-limit.token-limit -1
assert_rejects "non-integer token limit" rate-limit.token-limit ten
assert_rejects "zero IP window" rate-limit.ip-window 0s
assert_rejects "negative IP window" rate-limit.ip-window -1s
assert_rejects "missing unit IP window" rate-limit.ip-window 60
assert_rejects "non-duration token window" rate-limit.token-window minute
assert_rejects "zero archive file byte limit" limits.max-archive-file-bytes 0MB
assert_rejects "negative upload byte limit" limits.max-upload-bytes -1MB
assert_rejects "non-integer charmhub artifact byte limit" charmhub.max-artifact-bytes 64MiB

echo "configure hook rate-limit validation tests passed"
