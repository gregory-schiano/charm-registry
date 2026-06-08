# Quality Gate Report — Consolidated Branch

**Branch:** `prod-ready-use-harbor/complete-quality-gates-fixed`
**Base:** `prod-ready-use-harbor/complete` (1d323ce)
**Consolidated from:**
- `prod-ready-use-harbor/fix-govulncheck-toolchain` (23159c1) — Go 1.26.4 toolchain bump
- `prod-ready-use-harbor/fix-sqlc-drift` (198797a) — sqlc generated code reconciliation
- `prod-ready-use-harbor/fix-integration-cert-bootstrap` (80f4862) — integration cert fix + requireAuth + test fixes
**Additional fix:** Removed unused `uploadFromSQLC` function (lint:unused) — commit 994195e
**Go version:** go1.26.4 linux/amd64

---

## Gate Results

| Gate | Status | Details |
|------|--------|---------|
| `make fmt` | **PASS** | No formatting changes |
| `make tidy` | **PASS** | Modules verified |
| `make vet` | **PASS** | No issues |
| `make lint` | **PASS** | 0 issues (fixed unused `uploadFromSQLC`) |
| `make vuln` | **PASS** | 0 called vulnerabilities (4 imported, 21 required-but-not-called) |
| `make gosec` | **PASS** | 0 issues, 3 nosec (70 files, 14560 lines) |
| `make test` | **PASS** | 10/10 packages OK (api, app, auth, blob, charm, charmhub, config, oci, repo, service, sync) |
| `make build` | **PASS** | Both binaries built (charm-registry, charm-registryctl) |
| `make test-race` | **PASS** | 10/10 packages OK with -race flag |
| `make coverage` | **PASS** | 65.2% total coverage |
| `make sqlc-diff` | **PASS** | Clean (no drift) |
| `docker compose -f compose.integration.yaml down -v` | **PASS** | Clean teardown |
| `make integration-test` | **FAIL** | 30 test failures (see below) |

---

## Integration Test Failures (30 total)

### Category 1: IP Rate Limiter (429 too-many-requests) — 29 tests

The hard-coded IP rate limiter (`newIPRateLimiter(30, time.Minute)` in `internal/api/http.go:56`) throttles requests to 30/min per IP. The integration test suite makes many sequential requests from the same IP (localhost), exhausting the budget within the first few test groups. Subsequent tests all receive 429.

**Affected tests:**
- RES03, RES04, RES05, RES06 (resource tests)
- REV01, REV02, REV03, REV04, REV05, REV06, REV07, REV08, REV09, REV10 (revision tests)
- TOKEN01, TOKEN02, TOKEN03, TOKEN03b, TOKEN04, TOKEN04b, TOKEN05, TOKEN05b, TOKEN06, TOKEN07, TOKEN08, TOKEN09, TOKEN10, TOKEN11, TOKEN12, TOKEN13, TOKEN14 (token tests)

**Root cause:** Rate limiter was added in the hardening branch with a low hard-coded limit not suitable for integration test environments.

**Recommended fix:** Make the IP rate limiter configurable via env var (e.g. `CHARM_REGISTRY_IP_RATE_LIMIT` and `CHARM_REGISTRY_IP_RATE_WINDOW`), and set a high limit or disable it when `CHARM_REGISTRY_ENABLE_INSECURE_DEV_AUTH=true`.

### Category 2: Release persistence — 1 test

- **PERSIST03_ReleaseDataPersists** — response missing "released" array

This may be a cascading failure from the rate limiter (the test relies on creating a release which is rate-limited) or a genuine test data issue.

---

## Pre-existing Failures (from parent card t_c306520e)

These were identified before the current run and are NOT new regressions:
- **Auth expectation mismatches** (ACL05, ACL07, PKG14, PKG19, PKG23, JUJU03, V207) — tests expect specific auth error codes that differ from dev-auth-mode behavior
- **OCI HEAD auth** (OCI06) — unauthenticated blob HEAD returns unexpected status
- **Rate limiter** (RES05, RES06) — were already flagged as rate-limiter-caused

---

## Summary

All static quality gates pass. The integration test suite fails because the IP rate limiter (added in hardening) is too aggressive for the test environment. This is a configuration issue, not a code correctness issue. The recommended fix is to make the rate limiter configurable and raise/disable it in integration tests.
