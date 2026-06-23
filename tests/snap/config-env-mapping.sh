#!/bin/sh
set -eu

repo_root="$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)"
tmpdir="$(mktemp -d)"
trap 'rm -rf "$tmpdir"' EXIT

mkdir -p "$tmpdir/bin" "$tmpdir/snap/bin" "$tmpdir/snap/etc/charm-registry" "$tmpdir/common/certs"
cp "$repo_root/snap/local/config-env.map" "$tmpdir/snap/etc/charm-registry/config-env.map"
cp "$repo_root/snap/local/charm-registry-snap-helpers" "$tmpdir/snap/bin/charm-registry-snap-helpers"

cat >"$tmpdir/bin/snapctl" <<'SNAPCTL'
#!/bin/sh
if [ "$1" != "get" ]; then
	exit 1
fi
case "$2" in
	public-api-url) printf '%s\n' 'https://api.example.test:8443' ;;
	tls.enabled) printf '%s\n' 'true' ;;
	tls.cert-file) printf '%s\n' '/custom/api.crt' ;;
	tls.key-file) printf '%s\n' '/custom/api.key' ;;
	oci.tls.cert-file) printf '%s\n' '/custom/oci.crt' ;;
	oci.tls.key-file) printf '%s\n' '/custom/oci.key' ;;
	storage.s3.access-key-id) printf '%s\n' 'access-id' ;;
	storage.s3.secret-access-key) printf '%s\n' 'secret-key' ;;
	insecure-dev-auth) printf '%s\n' 'true' ;;
	charmhub.max-artifact-bytes) printf '%s\n' '128MB' ;;
	limits.max-archive-file-bytes) printf '%s\n' '32MB' ;;
	limits.max-upload-bytes) printf '%s\n' '128MB' ;;
	oci.project-prefix) printf '%s\n' 'team' ;;
	*) exit 0 ;;
esac
SNAPCTL
chmod +x "$tmpdir/bin/snapctl"

cat >"$tmpdir/snap/bin/charm-registry" <<'APP'
#!/bin/sh
env | LC_ALL=C sort
APP
chmod +x "$tmpdir/snap/bin/charm-registry"

output="$(env -i PATH="$tmpdir/bin:/usr/bin:/bin" SNAP="$tmpdir/snap" SNAP_COMMON="$tmpdir/common" \
	"$repo_root/snap/local/charm-registry-wrapper")"

assert_env() {
	name="$1"
	want="$2"
	if ! printf '%s\n' "$output" | grep -Fx "$name=$want" >/dev/null; then
		printf 'missing expected env: %s=%s\n' "$name" "$want" >&2
		printf 'actual output:\n%s\n' "$output" >&2
		exit 1
	fi
}

assert_env CHARM_REGISTRY_PUBLIC_API_URL https://api.example.test:8443
assert_env CHARM_REGISTRY_API_TLS_CERT_FILE /custom/api.crt
assert_env CHARM_REGISTRY_API_TLS_KEY_FILE /custom/api.key
assert_env CHARM_REGISTRY_OCI_TLS_CERT_FILE /custom/oci.crt
assert_env CHARM_REGISTRY_OCI_TLS_KEY_FILE /custom/oci.key
assert_env CHARM_REGISTRY_S3_ACCESS_KEY_ID access-id
assert_env CHARM_REGISTRY_S3_SECRET_ACCESS_KEY secret-key
assert_env CHARM_REGISTRY_ENABLE_INSECURE_DEV_AUTH true
assert_env CHARM_REGISTRY_CHARMHUB_MAX_ARTIFACT_BYTES 128MB
assert_env CHARM_REGISTRY_MAX_ARCHIVE_FILE_BYTES 32MB
assert_env CHARM_REGISTRY_MAX_UPLOAD_BYTES 128MB

printf '%s' "CHARM_REGISTRY_OCI_PROJECT_PREFIX oci.project-prefix always" >>"$tmpdir/snap/etc/charm-registry/config-env.map"
output_with_appended_row="$(env -i PATH="$tmpdir/bin:/usr/bin:/bin" SNAP="$tmpdir/snap" SNAP_COMMON="$tmpdir/common" \
	"$repo_root/snap/local/charm-registry-wrapper")"
if ! printf '%s\n' "$output_with_appended_row" | grep -Fx 'CHARM_REGISTRY_OCI_PROJECT_PREFIX=team' >/dev/null; then
	printf 'mapping reader failed after appending a trailing-newline-free row\n' >&2
	exit 1
fi

printf 'snap config env mapping ok\n'
