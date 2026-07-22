#!/bin/sh
set -eu

repo_root="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"

tmp="$(mktemp -d)"
cleanup() {
	rm -rf "$tmp"
}
trap cleanup EXIT INT TERM

fail() {
	printf 'FAIL: %s\n' "$1" >&2
	exit 1
}

assert_file_contains() {
	file="$1"
	text="$2"
	grep -F "$text" "$file" >/dev/null 2>&1 || fail "$file does not contain: $text"
}

for script in \
	"$repo_root/snap/hooks/configure" \
	"$repo_root/snap/local/charm-registry-wrapper"; do
	assert_file_contains "$script" '. "$SNAP/bin/charm-registry-snap-helpers"'
	if grep -E '^(config_get|config_bool|url_host|is_ip)\(\)' "$script" >/dev/null 2>&1; then
		fail "$script still defines shared snap helper functions inline"
	fi
done

assert_file_contains "$repo_root/snap/snapcraft.yaml" 'charm-registry-snap-helpers: bin/charm-registry-snap-helpers'

fake_bin="$tmp/bin"
fake_snap="$tmp/snap"
common="$tmp/common"
mkdir -p "$fake_bin" "$fake_snap/bin" "$fake_snap/usr/bin" "$common/certs"
cp "$repo_root/snap/local/charm-registry-snap-helpers" "$fake_snap/bin/charm-registry-snap-helpers"
mkdir -p "$fake_snap/etc/charm-registry"
cp "$repo_root/snap/local/config-env.map" "$fake_snap/etc/charm-registry/config-env.map"

cat >"$fake_bin/snapctl" <<'EOF'
#!/bin/sh
set -eu
if [ "$1" != "get" ]; then
	exit 1
fi
case "$2" in
	public-api-url) printf '%s\n' "${SNAPCTL_PUBLIC_API_URL:-}" ;;
	public-storage-url) printf '%s\n' "${SNAPCTL_PUBLIC_STORAGE_URL:-}" ;;
	public-registry-url) printf '%s\n' "${SNAPCTL_PUBLIC_REGISTRY_URL:-}" ;;
	oci.internal-url) printf '%s\n' "${SNAPCTL_OCI_INTERNAL_URL:-}" ;;
	tls.enabled) printf '%s\n' "${SNAPCTL_TLS_ENABLED:-}" ;;
	tls.cert-file) printf '%s\n' "${SNAPCTL_TLS_CERT_FILE:-}" ;;
	tls.key-file) printf '%s\n' "${SNAPCTL_TLS_KEY_FILE:-}" ;;
	listen) printf '%s\n' "${SNAPCTL_LISTEN:-}" ;;
	*) printf '%s\n' "" ;;
esac
EOF
chmod +x "$fake_bin/snapctl"

cat >"$fake_snap/usr/bin/openssl" <<'EOF'
#!/bin/sh
set -eu
key_file=""
cert_file=""
while [ "$#" -gt 0 ]; do
	case "$1" in
		-keyout) shift; key_file="$1" ;;
		-out) shift; cert_file="$1" ;;
	esac
	shift || true
done
[ -n "$key_file" ] || exit 1
[ -n "$cert_file" ] || exit 1
printf 'fake key\n' >"$key_file"
printf 'fake cert\n' >"$cert_file"
EOF
chmod +x "$fake_snap/usr/bin/openssl"

cat >"$fake_snap/bin/charm-registry" <<'EOF'
#!/bin/sh
set -eu
env | sort >"$WRAPPER_ENV_OUT"
EOF
chmod +x "$fake_snap/bin/charm-registry"

# Verify the shared helpers preserve config lookup, bool parsing, URL host parsing, and IP detection.
PATH="$fake_bin:$PATH" SNAPCTL_TLS_ENABLED=yes SNAP="$fake_snap" sh -c '
	. "$SNAP/bin/charm-registry-snap-helpers"
	[ "$(config_get tls.enabled)" = yes ]
	config_bool tls.enabled false
	config_bool missing true
	! config_bool missing false
	[ "$(url_host https://example.test:8443/path)" = example.test ]
	is_ip 127.0.0.1
	is_ip ::1
	! is_ip example.test
'

# Verify the wrapper still maps snap config values to application environment variables.
WRAPPER_ENV_OUT="$tmp/wrapper.env" \
PATH="$fake_bin:$PATH" \
SNAP="$fake_snap" \
SNAP_COMMON="$common" \
SNAPCTL_LISTEN=":9090" \
SNAPCTL_PUBLIC_API_URL="https://registry.example:8443/api" \
SNAPCTL_TLS_ENABLED=yes \
SNAPCTL_TLS_CERT_FILE="/custom/cert.pem" \
SNAPCTL_TLS_KEY_FILE="/custom/key.pem" \
sh "$repo_root/snap/local/charm-registry-wrapper"
assert_file_contains "$tmp/wrapper.env" 'CHARM_REGISTRY_LISTEN=:9090'
assert_file_contains "$tmp/wrapper.env" 'CHARM_REGISTRY_PUBLIC_API_URL=https://registry.example:8443/api'
assert_file_contains "$tmp/wrapper.env" 'CHARM_REGISTRY_API_TLS_CERT_FILE=/custom/cert.pem'
assert_file_contains "$tmp/wrapper.env" 'CHARM_REGISTRY_API_TLS_KEY_FILE=/custom/key.pem'
assert_file_contains "$tmp/wrapper.env" "CHARM_REGISTRY_SQLITE_PATH=$common/data/registry.sqlite"

# Verify the configure hook still removes registry TLS files when disabled and always prepares OCI TLS files.
printf 'old cert\n' >"$common/certs/registry.crt"
printf 'old key\n' >"$common/certs/registry.key"
printf 'old hosts\n' >"$common/certs/registry.hosts"
PATH="$fake_bin:$PATH" \
SNAP="$fake_snap" \
SNAP_COMMON="$common" \
SNAPCTL_TLS_ENABLED=false \
SNAPCTL_PUBLIC_REGISTRY_URL="https://oci.example:5000" \
SNAPCTL_OCI_INTERNAL_URL="https://127.0.0.1:5000" \
sh "$repo_root/snap/hooks/configure"
[ ! -e "$common/certs/registry.crt" ] || fail "registry cert was not removed when tls.enabled=false"
[ -f "$common/certs/oci.crt" ] || fail "OCI cert was not generated"
[ -f "$common/certs/oci.key" ] || fail "OCI key was not generated"
assert_file_contains "$common/certs/oci.hosts" 'oci.example'
assert_file_contains "$common/certs/oci.hosts" '127.0.0.1'

printf 'PASS: snap shell helper consolidation verified\n'
