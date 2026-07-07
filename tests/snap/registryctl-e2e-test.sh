#!/bin/sh
# End-to-end checks for the packaged admin CLI (spellbook.charm-registryctl)
# against a running, dev-auth-enabled charm-registry snap.
#
# Requires: the snap installed, started, and healthy (the snap spread task
# prepare does this), and curl on PATH.
set -eu

API_URL="${FTEST_API_URL:-http://localhost:8080}"
TOKEN="dev:${FTEST_ADMIN_SUBJECT:-admin}:${FTEST_ADMIN_USER:-admin}"
CTL="${REGISTRYCTL_BIN:-spellbook.charm-registryctl}"
RULE_NAME="registryctl-e2e-rule-$$"
CHARM_NAME="registryctl-e2e-charm-$$"

fail() {
	echo "FAIL: $*" >&2
	exit 1
}

ctl() {
	"$CTL" --url "$API_URL" --token "$TOKEN" "$@"
}

api() {
	method="$1"
	path="$2"
	body="${3:-}"
	if [ -n "$body" ]; then
		curl -sS -o /tmp/registryctl-e2e-body -w '%{http_code}' \
			-X "$method" "$API_URL$path" \
			-H "Authorization: Bearer $TOKEN" \
			-H "Content-Type: application/json" \
			-d "$body"
	else
		curl -sS -o /tmp/registryctl-e2e-body -w '%{http_code}' \
			-X "$method" "$API_URL$path" \
			-H "Authorization: Bearer $TOKEN"
	fi
}

echo "=== registryctl: rejects missing confirmation ==="
if ctl unregister "$CHARM_NAME" 2>/dev/null; then
	fail "unregister without --yes should have failed"
fi

echo "=== registryctl: sync list (empty) ==="
ctl sync list | grep -q "NAME" || fail "sync list did not print a header"

echo "=== registryctl: sync wait with no pending rules ==="
ctl sync wait --timeout 30s | grep -q "all sync rules completed" ||
	fail "sync wait did not complete with no rules"

echo "=== registryctl: sync add/list/remove round trip ==="
ctl sync add "$RULE_NAME" --track latest --base ubuntu@24.04 --arch amd64 |
	grep -q "scheduled sync for $RULE_NAME track latest" || fail "sync add output unexpected"
ctl sync list | grep "$RULE_NAME" | grep -q "ubuntu@24.04" || fail "added rule missing from sync list"
ctl sync remove "$RULE_NAME" --track latest |
	grep -q "scheduled removal for $RULE_NAME track latest" || fail "sync remove output unexpected"
# Removal is asynchronous. The initial add also schedules a sync, so re-mark
# the rule for removal while polling to avoid a sync failure racing the delete.
for i in $(seq 1 30); do
	if ! ctl sync list | grep -q "$RULE_NAME"; then
		break
	fi
	ctl sync remove "$RULE_NAME" --track latest >/dev/null || true
	[ "$i" -eq 30 ] && fail "removed rule still present in sync list after 60s"
	sleep 2
done

echo "=== registryctl: unregister removes a registered charm ==="
status="$(api POST /v1/charm "{\"name\":\"$CHARM_NAME\",\"type\":\"charm\"}")"
[ "$status" = "201" ] || fail "register $CHARM_NAME: HTTP $status ($(cat /tmp/registryctl-e2e-body))"
ctl unregister "$CHARM_NAME" --yes | grep -q "unregistered $CHARM_NAME" || fail "unregister output unexpected"
status="$(api GET "/v1/charm/$CHARM_NAME")"
[ "$status" = "404" ] || fail "charm still present after unregister: HTTP $status"

echo "PASS: registryctl e2e checks"
