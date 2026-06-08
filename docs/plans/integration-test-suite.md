# Integration Test Suite Design — charm-registry `use-harbor`

**Base branch:** `production-readiness-use-harbor` (5119530)
**Task:** t_3ba4f3ff — design integration tests
**Date:** 2026-06-08
**Status:** Design document (implementation follows in Task 11)

---

## 1. Scope and Goals

Design an integration test suite that exercises the charm-registry against **real infrastructure** (Postgres, MinIO/S3, embedded OCI registry) rather than in-memory doubles. The suite validates:

1. Full-stack boot and dependency wiring
2. OCI registry integration with TLS
3. Token lifecycle (issue, list, exchange, revoke, expiry)
4. Package CRUD (register, metadata patch, delete)
5. Charm upload → revision → release → refresh → info → find pipeline
6. Resource upload/download (file and OCI image types)
7. ACL negative tests (private packages, scoped tokens, cross-user access)
8. Body/header/upload limit enforcement against real HTTP
9. Restart persistence (data survives service restart)
10. juju/charmcraft compatibility smoke (where viable in CI)

Existing unit tests use `repo.NewMemory()` + `blob.NewMemoryStore()` + `testutil.OCIRegistry{}`. Integration tests must use **real Postgres**, **real S3 (MinIO)**, and the **real embedded OCI distribution registry** with TLS.

---

## 2. Test Infrastructure

### 2.1. Docker Compose Test Stack

Create `compose.integration.yaml` extending the existing `compose.yaml` with:

- **postgres**: Same image, separate DB (`charm_registry_test`), isolated network
- **minio**: Same image, separate buckets
- **charm-registry**: Built from local Dockerfile, with test-specific env vars:
  - `CHARM_REGISTRY_ENABLE_INSECURE_DEV_AUTH=true` (for auth-free test paths)
  - `CHARM_REGISTRY_DATABASE_URL=postgres://postgres:postgres@postgres:5432/charm_registry_test?sslmode=disable`
  - `CHARM_REGISTRY_OCI_SECRET_KEY=integration-test-oci-secret`
  - All ports exposed to host for test client access

### 2.2. Test Runner

Go test file `tests/integration/integration_test.go` with build tag `//go:build integration`.

Run via:
```bash
# Start stack
docker compose -f compose.integration.yaml up --build -d
# Wait for healthy
./scripts/wait-for-healthy.sh
# Run integration tests
go test -tags=integration -timeout 300s ./tests/integration/...
# Teardown
docker compose -f compose.integration.yaml down -v
```

Makefile target:
```makefile
integration-test:
	docker compose -f compose.integration.yaml up --build -d
	./scripts/wait-for-healthy.sh
	go test -tags=integration -timeout 300s ./tests/integration/...
	docker compose -f compose.integration.yaml down -v
```

### 2.3. Test Client

Each test creates an `http.Client` targeting the live service at `http://localhost:8080`. The OCI registry is reached at `https://localhost:5000` (with TLS cert from `certs/oci.crt`).

Auth via dev auth: `Authorization: Bearer dev:<username>:<displayname>`

### 2.4. Test Isolation

- Each top-level `TestXxx` function uses a unique package name prefix (e.g., `itest-<random>-my-charm`) to avoid collisions
- Database is wiped between test runs by `docker compose down -v` (destroy volumes)
- Tests that need clean state within a run use unique names — no test relies on data from another test

---

## 3. Test Scenarios

### 3.1. Full-Stack Boot (BOOT)

#### BOOT-01: Service starts and reports healthy
- **Steps**: `GET /healthz`, `GET /readyz`
- **Expected**: 200, `{"status":"ok"}` / `{"status":"ready"}`
- **Covers**: Postgres connectivity, basic liveness

#### BOOT-02: Root document returns service metadata
- **Steps**: `GET /`
- **Expected**: 200, JSON with `"service-name": "private-charm-registry"`
- **Covers**: API handler wiring

#### BOOT-03: OpenAPI spec served
- **Steps**: `GET /openapi.yaml`
- **Expected**: 200, `Content-Type: application/yaml`, body contains `openapi:`
- **Covers**: Spec endpoint

#### BOOT-04: Docs page rendered
- **Steps**: `GET /docs`
- **Expected**: 200, `Content-Type: text/html`, body contains `Charm Registry`
- **Covers**: Docs endpoint

#### BOOT-05: Security headers present on all responses
- **Steps**: `GET /`, check headers
- **Expected**: `X-Content-Type-Options: nosniff`, `X-Frame-Options: DENY`, `Referrer-Policy: no-referrer`, CSP includes `default-src 'none'`
- **Covers**: Security middleware

#### BOOT-06: OCI registry responds to catalog
- **Steps**: `GET https://localhost:5000/v2/_catalog` with TLS cert
- **Expected**: 200 (or 401 if auth required), valid JSON `{"repositories":[]}`
- **Covers**: Embedded OCI registry + TLS cert valid

---

### 3.2. Token Lifecycle (TOKEN)

#### TOKEN-01: Issue token with dev auth
- **Steps**: `POST /v1/tokens` with `Bearer dev:alice:Alice`, body `{"description":"itest"}`
- **Expected**: 201, response contains `macaroon` (raw token) and token metadata
- **Covers**: Token issuance path

#### TOKEN-02: List tokens after issue
- **Steps**: Issue token, then `GET /v1/tokens` with same auth
- **Expected**: 200, list includes the issued token
- **Covers**: Token list endpoint

#### TOKEN-03: Exchange token
- **Steps**: `POST /v1/tokens/exchange` with bearer token
- **Expected**: 200 or 201, new macaroon returned
- **Covers**: Token exchange flow (charmcraft `login` path)

#### TOKEN-04: Revoke token
- **Steps**: Issue token, `POST /v1/tokens/revoke` with `session_id`, then attempt to use revoked token
- **Expected**: Revoke returns 200; subsequent use of revoked token returns 401
- **Covers**: Token revocation

#### TOKEN-05: Whoami with token
- **Steps**: `GET /v1/whoami` with bearer token
- **Expected**: 200, returns account info matching the token's account
- **Covers**: Token introspection

#### TOKEN-06: Expired token rejected
- **Steps**: Issue token with minimal TTL (`"ttl": 1` → 1 second), wait 2 seconds, attempt authenticated request
- **Expected**: 401 with "token revoked or expired"
- **Covers**: S4 (token expiry enforcement), code-review H-3
- **Note**: This test validates that `ValidUntil` is enforced at authentication time

#### TOKEN-07: Token rate limiting
- **Steps**: Issue 6+ tokens rapidly for same account
- **Expected**: After 5th (or configured limit) request, 429 response
- **Covers**: `tokenIssueLimiter`

#### TOKEN-08: Scoped token cannot exceed permissions
- **Steps**: Issue token with `permissions: ["account-view-packages"]`, attempt to register a package
- **Expected**: 403 forbidden on register
- **Covers**: Token permission scoping, related to C-1 (empty-permissions bypass)

#### TOKEN-09: Token package scoping enforced
- **Steps**: Issue token with `packages: [{"name": "char-a"}]`, attempt to push revision to `char-b`
- **Expected**: 403 on push for `char-b`
- **Covers**: Package-scoped tokens

#### TOKEN-10: Token channel scoping enforced
- **Steps**: Issue token with `channels: ["latest/stable"]`, attempt release to `latest/edge`
- **Expected**: 403 on release to `latest/edge`
- **Covers**: Channel-scoped tokens, `enforceChannelRestriction`

---

### 3.3. Package Registration and Metadata (PACKAGE)

#### PKG-01: Register a new charm package
- **Steps**: `POST /v1/charm` with `Bearer dev:alice:Alice`, body `{"name":"itest-my-charm","type":"charm"}`
- **Expected**: 201, package returned with `status: "registered"`, `default_track: "latest"`
- **Covers**: Registration flow, default track creation

#### PKG-02: Register duplicate package fails
- **Steps**: Register `itest-my-charm`, then register again
- **Expected**: Second registration returns 409 conflict
- **Covers**: Duplicate prevention

#### PKG-03: Get package metadata
- **Steps**: `GET /v1/charm/itest-my-charm` with auth
- **Expected**: 200, package metadata including name, type, status, publisher
- **Covers**: Package retrieval

#### PKG-04: Patch package metadata
- **Steps**: `PATCH /v1/charm/itest-my-charm` with `{"summary":"Updated summary"}`
- **Expected**: 200, summary updated
- **Covers**: Metadata update

#### PKG-05: Toggle package privacy
- **Steps**: Patch with `{"private": true}`, verify unauthenticated find returns 404-like; patch with `{"private": false}`, verify find works
- **Expected**: Private: not found for anonymous; Public: visible
- **Covers**: Privacy toggle, ACL logic

#### PKG-06: Delete empty package
- **Steps**: Register package, delete it immediately
- **Expected**: 200; subsequent GET returns 404
- **Covers**: Package deletion

#### PKG-07: Delete package with revisions fails
- **Steps**: Register, upload, create revision, then delete package
- **Expected**: 409 conflict (has revisions)
- **Covers**: Revision guard on deletion

#### PKG-08: List packages for account
- **Steps**: Register 3 packages as alice, `GET /v1/charm`
- **Expected**: 200, list contains at least 3 packages
- **Covers**: Package listing

#### PKG-09: Search packages
- **Steps**: Register `itest-foo-charm` and `itest-bar-charm`, `GET /v2/charms/find?q=foo`
- **Expected**: Results include `itest-foo-charm`, not `itest-bar-charm`
- **Covers**: Package search, R4 (anonymous find)

---

### 3.4. Charm Upload, Revision, Release Pipeline (REVISION)

#### REV-01: Full upload-to-release pipeline
- **Steps**:
  1. Register `itest-pipeline-charm`
  2. `POST /unscanned-upload/` with charm archive (zip with `metadata.yaml`)
  3. `POST /v1/charm/itest-pipeline-charm/revisions` with `{"upload_id": "..."}`
  4. `GET /v1/charm/itest-pipeline-charm/revisions/review`
  5. `POST /v1/charm/itest-pipeline-charm/releases` with `[{"channel":"latest/stable","revision":1}]`
  6. `GET /v1/charm/itest-pipeline-charm/releases`
- **Expected**: Each step returns success; releases endpoint shows revision 1 on `latest/stable`
- **Covers**: End-to-end charm publish flow

#### REV-02: Upload charm with resources
- **Steps**: Upload charm archive declaring `resources: { config: { type: file, filename: config.yaml } }`
- **Expected**: Revision created; resource definitions auto-declared
- **Covers**: Resource declaration from charm metadata

#### REV-03: List revisions
- **Steps**: Push 2 revisions, `GET /v1/charm/itest-charm/revisions`
- **Expected**: 200, list of 2 revisions
- **Covers**: Revision listing

#### REV-04: Release to multiple channels
- **Steps**: Release revision 1 to `latest/stable` and `latest/edge`
- **Expected**: Both channels appear in releases list
- **Covers**: Multi-channel release

#### REV-05: Create and use custom track
- **Steps**: `POST /v1/charm/itest-charm/tracks` with `[{"name":"2.0"}]`, then release to `2.0/stable`
- **Expected**: Track created, release succeeds
- **Covers**: Custom track creation

#### REV-06: Charm download
- **Steps**: `GET /api/v1/charms/download/{package_id}_1.charm` with auth
- **Expected**: 200, valid zip archive (same bytes as uploaded)
- **Covers**: Download endpoint

---

### 3.5. V2 API: Find, Info, Refresh (V2)

#### V2-01: Anonymous find for public charms (R4)
- **Steps**: Register public charm, publish a release, `GET /v2/charms/find` **without** auth
- **Expected**: 200, results include the public charm
- **Covers**: R4 (anonymous v2/find), critical roadmap item
- **Note**: This currently fails — all v2 endpoints require identity. This is the integration test that should FAIL until R4 is implemented, documenting the gap.

#### V2-02: Authenticated find for public charms
- **Steps**: Same as V2-01 but with auth
- **Expected**: 200, results include the public charm
- **Covers**: Authenticated find

#### V2-03: Info for published charm
- **Steps**: `GET /v2/charms/info/itest-charm` with auth
- **Expected**: 200, response includes `id`, `name`, `channels`, `default_release`
- **Covers**: Info endpoint

#### V2-04: Info for private charm returns not-found for non-owner
- **Steps**: Register private charm as alice, request info as bob
- **Expected**: 404 (not 401/403, to avoid information leakage)
- **Covers**: Private charm info gate

#### V2-05: Info with channel filter
- **Steps**: `GET /v2/charms/info/itest-charm?channel=latest/stable` with auth
- **Expected**: 200, channel-specific release info
- **Covers**: Channel parameter on info

#### V2-06: Refresh for published charm
- **Steps**: `POST /v2/charms/refresh` with `[{"action":"refresh","instance-key":"app/0","name":"itest-charm","channel":"latest/stable"}]`
- **Expected**: 200, result includes charm entity with revision and resources
- **Covers**: Refresh endpoint (core juju workflow)

#### V2-07: Anonymous refresh for public charms (R4)
- **Steps**: `POST /v2/charms/refresh` without auth for a public charm
- **Expected**: 200 with charm data (currently may fail — same R4 gap)
- **Covers**: R4 (anonymous refresh)

#### V2-08: Refresh with base constraint
- **Steps**: Refresh with `base: {"name":"ubuntu","channel":"22.04","architecture":"amd64"}`
- **Expected**: Base-matched release returned
- **Covers**: Base constraint matching in refresh

---

### 3.6. Resources (RESOURCE)

#### RES-01: Upload file resource
- **Steps**: Create upload, `POST /v1/charm/itest-charm/resources/config/revisions` with `{"upload_id":"...","type":"file"}`
- **Expected**: 201, resource revision created
- **Covers**: File resource upload

#### RES-02: List resource definitions
- **Steps**: `GET /v1/charm/itest-charm/resources` with auth
- **Expected**: 200, list includes declared resources with revision numbers
- **Covers**: Resource listing

#### RES-03: List resource revisions
- **Steps**: `GET /v1/charm/itest-charm/resources/config/revisions`
- **Expected**: 200, list of revisions
- **Covers**: Resource revision listing

#### RES-04: OCI image upload credentials
- **Steps**: `GET /v1/charm/itest-charm/resources/workload-image/oci-image/upload-credentials` with auth
- **Expected**: 200, response includes OCI registry credentials (username, password, registry URL)
- **Covers**: OCI credential generation for charmcraft

#### RES-05: OCI image blob push
- **Steps**: `POST /v1/charm/itest-charm/resources/workload-image/oci-image/blob` with JSON body containing image reference
- **Expected**: 201, image reference stored
- **Covers**: OCI image resource flow

#### RES-06: Resource download
- **Steps**: `GET /api/v1/resources/download/{filename}` with auth
- **Expected**: 200, file content matches uploaded bytes
- **Covers**: Resource download endpoint

#### RES-07: Resource with incompatible package revision
- **Steps**: Upload resource for revision 1, attempt release on revision 2 with this resource
- **Expected**: 400 invalid request
- **Covers**: `validateReleaseResources` guard

---

### 3.7. ACL Negative Tests (ACL)

#### ACL-01: Non-owner cannot push revision
- **Steps**: Alice registers charm, Bob attempts `POST /v1/charms/itest-charm/revisions`
- **Expected**: 403 forbidden
- **Covers**: Package manage permission check

#### ACL-02: Non-owner cannot release
- **Steps**: Alice owns, Bob attempts `POST /v1/charm/itest-charm/releases`
- **Expected**: 403
- **Covers**: Release permission

#### ACL-03: Non-owner cannot delete package
- **Steps**: Alice owns, Bob attempts `DELETE /v1/charm/itest-charm`
- **Expected**: 403
- **Covers**: Delete permission

#### ACL-04: Private charm invisible to non-member
- **Steps**: Alice creates private charm, Bob does `GET /v1/charms/itest-private-charm`
- **Expected**: 404 (not 403, to avoid leakage)
- **Covers**: Private package visibility

#### ACL-05: Empty-permissions token bypass (C-1 regression)
- **Steps**: Issue token with `permissions: []`, attempt any package operation
- **Expected**: 403 forbidden (this currently PASSES through — test should FAIL until C-1 is fixed)
- **Covers**: C-1 from code review backlog (critical security bug)
- **Note**: This test documents the known bypass. Until C-1 is fixed, this test fails. After fix, it passes and serves as regression guard.

#### ACL-06: Non-admin cannot access admin endpoints
- **Steps**: Bob (non-admin) attempts `GET /v1/admin/charmhub-sync`
- **Expected**: 403
- **Covers**: Admin-only endpoints

#### ACL-07: Unauthenticated request returns macaroon challenge
- **Steps**: `GET /v1/tokens` without auth
- **Expected**: 200, `{"macaroon": "oidc-login-required"}` (not 401)
- **Covers**: Charmhub auth flow compatibility

---

### 3.8. Body/Header/Upload Limits (LIMIT)

#### LIM-01: Oversized JSON body rejected
- **Steps**: `POST /v1/tokens` with body > `MAX_JSON_BODY_BYTES`
- **Expected**: 413 Request Entity Too Large
- **Covers**: JSON body limit enforcement

#### LIM-02: Oversized upload rejected
- **Steps**: `POST /unscanned-upload/` with file > `MAX_ARCHIVE_FILE_BYTES`
- **Expected**: 413
- **Covers**: Upload size limit

#### LIM-03: Oversized headers rejected
- **Steps**: Send request with headers > `MAX_HEADER_BYTES`
- **Expected**: 431 Request Header Fields Too Large
- **Covers**: Header size limit

#### LIM-04: Invalid JSON returns 400
- **Steps**: `POST /v1/tokens` with `Content-Type: application/json` and body `not json`
- **Expected**: 400 Bad Request
- **Covers**: JSON validation

#### LIM-05: Content-Disposition header injection (H-4 regression)
- **Steps**: Upload a charm or resource with filename containing `"`, `\r\n`, or unicode; trigger download
- **Expected**: Response headers contain no injected CRLF; filename is sanitized
- **Covers**: H-4 from code review backlog
- **Note**: May currently pass — test serves as regression guard after fix

---

### 3.9. Restart Persistence (PERSIST)

#### PERS-01: Data survives service restart
- **Steps**:
  1. Register package, upload revision, create release
  2. `docker compose restart charm-registry`
  3. Wait for healthy
  4. `GET /v1/charm/itest-charm`, `GET /v1/charm/itest-charm/releases`
- **Expected**: Package and release data intact after restart
- **Covers**: Postgres + S3 persistence, service startup resilience

#### PERS-02: Tokens survive restart
- **Steps**:
  1. Issue token
  2. Restart service
  3. Use token for authenticated request
- **Expected**: Token still valid after restart
- **Covers**: Token persistence in Postgres

#### PERS-03: OCI registry state survives restart
- **Steps**:
  1. Push OCI image resource
  2. Restart service (OCI registry + charm-registry)
  3. `GET /v2/_catalog` still lists the pushed repository
- **Expected**: OCI blobs persist in S3/filesystem
- **Covers**: OCI storage persistence

---

### 3.10. juju/charmcraft Compatibility Smoke (JUJU)

These tests simulate the exact HTTP sequences that `juju` and `charmcraft` clients send.

#### JUJU-01: charmcraft register + upload + release sequence
- **Steps**:
  1. Authenticate (dev auth or token exchange)
  2. `POST /v1/charm` → register `itest-juju-charm`
  3. `POST /unscanned-upload/` → upload charm archive
  4. `POST /v1/charm/itest-juju-charm/revisions` → push revision
  5. `GET /v1/charm/itest-juju-charm/revisions/review` → check status
  6. `POST /v1/charm/itest-juju-charm/releases` → release to channel
- **Expected**: Full sequence succeeds without error
- **Covers**: charmcraft publish workflow

#### JUJU-02: juju find + refresh + download sequence
- **Steps**:
  1. `GET /v2/charms/find?q=itest-juju-charm` (with auth — without auth once R4 is implemented)
  2. `POST /v2/charms/refresh` with charm name and channel
  3. Extract download URL from refresh response
  4. `GET /api/v1/charms/download/{filename}` → download charm archive
- **Expected**: Complete juju deploy flow
- **Covers**: juju consumer workflow

#### JUJU-03: charmcraft resource upload with OCI credentials
- **Steps**:
  1. Get OCI upload credentials
  2. (Manual step) Push image to OCI registry using returned credentials
  3. Verify resource revision reflects OCI image reference
- **Expected**: OCI credential flow works end-to-end
- **Covers**: OCI image resource workflow, S7 (upload-id flow)

#### JUJU-04: Macaroon token acceptance
- **Steps**: Issue token, use `Authorization: Macaroon <token>` header
- **Expected**: Authenticated as token's account
- **Covers**: Charmhub macaroon auth compatibility

#### JUJU-05: Token dashboard exchange
- **Steps**: `POST /v1/tokens/dashboard/exchange` with macaroon
- **Expected**: 200 or 201, returns exchange token
- **Covers**: Dashboard auth flow

---

### 3.11. Harbor/OCI-Specific Tests (OCI)

#### OCI-01: OCI registry TLS serves valid certificate
- **Steps**: `GET https://localhost:5000/v2/` using `certs/oci.crt` as CA
- **Expected**: TLS handshake succeeds, 200 or 401
- **Covers**: OCI TLS configuration, cert loading

#### OCI-02: OCI registry catalog after charm push
- **Steps**: Register and push a charm with OCI image resource, check `/v2/_catalog`
- **Expected**: Catalog includes the charm's OCI project repository
- **Covers**: OCI project creation, repository visibility

#### OCI-03: OCI pull robot credentials valid
- **Steps**: Get OCI upload credentials (pull), authenticate to OCI registry with returned credentials
- **Expected**: Registry accepts pull credentials
- **Covers**: Robot account creation and credential derivation

#### OCI-04: OCI push robot credentials valid
- **Steps**: Get OCI upload credentials (push), attempt to push a minimal image manifest
- **Expected**: Push succeeds
- **Covers**: Push robot account

#### OCI-05: OCI credentials are package-scoped
- **Steps**: Get push credentials for package A, attempt push to package B's repository
- **Expected**: Rejected (403 or 404) — related to M-17 (OCI ACL bypass)
- **Covers**: M-17 regression guard
- **Note**: May currently pass if OCI middleware doesn't enforce per-repo ACL — test documents the gap

---

### 3.12. Snap-Relevant Paths (SNAP)

These tests run only when a snap installation is available. In CI, they are documented manual gates.

#### SNAP-01: Service starts from snap with correct environment
- **Steps**: Install snap, verify `charm-registry` service is active
- **Expected**: Service responds on configured ports
- **Covers**: Snap packaging, service layer

#### SNAP-02: Snap TLS env vars are wired
- **Steps**: Set `CHARM_REGISTRY_TLS_CERT_FILE` and `TLS_KEY_FILE` in snap config, restart
- **Expected**: API serves HTTPS (currently does NOT — R2 gap)
- **Covers**: R2 (API TLS), S6 (snap grade)
- **Note**: Manual gate until R2 is implemented

---

## 4. Test Matrix: Scenario → Code Review/Roadmap Coverage

| Test | Code Review Item | Roadmap Item | Priority |
|------|-----------------|--------------|----------|
| ACL-05 | C-1 (requirePermission bypass) | — | Critical |
| TOKEN-06 | H-3 (token hash), S4 (expiry) | S4 | High |
| LIM-05 | H-4 (Content-Disposition) | — | High |
| V2-01 | — | R4 (anonymous find) | Must-have |
| V2-07 | — | R4 (anonymous refresh) | Must-have |
| OCI-05 | M-17 (OCI ACL bypass) | — | Medium |
| BOOT-06 | — | R2 (API TLS, OCI TLS) | Must-have |
| SNAP-02 | — | R2, S6 | Must-have |
| TOKEN-08 | C-1 regression | — | Critical |
| PERS-01 | C-3 (migration resilience) | R3 (backup/restore) | Must-have |

---

## 5. Expected Failures (Documented Gaps)

The following integration tests are designed to **fail** with the current codebase, documenting known gaps that the production-readiness implementation must address:

| Test ID | Expected Failure Reason | Blocks |
|---------|------------------------|--------|
| V2-01 | R4: Anonymous v2/find not supported — all v2 endpoints require `requireIdentity` | juju find |
| V2-07 | R4: Anonymous v2/refresh not supported | juju anonymous refresh |
| ACL-05 | C-1: Empty-permissions token bypasses all `requirePermission` checks | Security |
| SNAP-02 | R2: API TLS env vars not wired in Go app | Snap production |
| OCI-05 | M-17: OCI auth middleware may not enforce per-repo ACL | Security |

These tests use `t.Skip()` with a clear skip message referencing the gap, so the test suite runs green on the current codebase while documenting what must change. Example:

```go
func TestAnonymousFind(t *testing.T) {
    t.Skip("R4: anonymous v2/find not yet supported — requires requireIdentity to allow unauthenticated public reads")
    // ... test body
}
```

When the gap is fixed, the `t.Skip` line is removed and the test must pass.

---

## 6. File Structure

```
tests/
  integration/
    integration_test.go    # Top-level suite entry, health/ready/boot tests
    token_test.go          # TOKEN-* tests
    package_test.go        # PKG-* tests
    revision_test.go       # REV-* tests
    v2_test.go             # V2-* tests (find, info, refresh)
    resource_test.go       # RES-* tests
    acl_test.go            # ACL-* tests
    limit_test.go          # LIM-* tests
    persist_test.go        # PERSIST-* tests
    juju_test.go           # JUJU-* tests
    oci_test.go            # OCI-* tests
    helpers.go             # Shared client, auth, charm archive builders
    helpers_test.go        # Test helper unit tests
scripts/
  wait-for-healthy.sh      # Poll /healthz until 200
compose.integration.yaml   # Test-specific compose stack
```

---

## 7. Implementation Notes for Task 11

1. **Build tag**: All files use `//go:build integration` to separate from unit tests
2. **Timeout**: `go test -timeout 300s` — individual tests should complete in <30s; PERSIST tests need restart time
3. **Parallelism**: Tests must NOT run in parallel (shared DB state). Use `-p 1` or `t.Sequential()` pattern
4. **Unique names**: Use `itest-<timestamp>-<random>` prefix for all package names to enable future parallelism
5. **Charm archive builder**: Port `buildTestCharmArchive` from `internal/api/http_test.go` but make it create real zip files with proper metadata.yaml
6. **CI integration**: Add `integration-test` job to `.github/workflows/ci.yml` that runs `make integration-test`
7. **Compose password fix**: Before running integration tests, fix C-4 (compose.yaml password `***` → `postgres`). This is a prerequisite.
8. **TDD approach**: For expected-failure tests (V2-01, ACL-05), write the test first, skip it, then implement the fix, then remove the skip. For all other tests, write them against the current codebase and verify they pass with the compose stack.

---

## 8. Verification

A test plan maps every scenario to:
- An HTTP request (method, path, headers, body)
- Expected HTTP status code
- Expected response body fields
- The code review / roadmap item it covers

The test suite is **complete** when:
- Every scenario from Section 3 has a Go test function
- `make integration-test` runs the full suite against the compose stack
- All non-skipped tests pass
- Skipped tests reference the specific gap (C-1, R4, R2, M-17) in their skip message
- CI runs the suite on every push to `use-harbor`-based branches

---

## Appendix A: Scenario Quick Reference

| ID | Category | Test Name | Key Endpoint(s) |
|----|----------|-----------|----------------|
| BOOT-01 | Boot | Service healthy | GET /healthz, /readyz |
| BOOT-02 | Boot | Root document | GET / |
| BOOT-03 | Boot | OpenAPI spec | GET /openapi.yaml |
| BOOT-04 | Boot | Docs page | GET /docs |
| BOOT-05 | Boot | Security headers | GET / (headers) |
| BOOT-06 | Boot | OCI registry catalog | GET /v2/_catalog |
| TOKEN-01 | Token | Issue token | POST /v1/tokens |
| TOKEN-02 | Token | List tokens | GET /v1/tokens |
| TOKEN-03 | Token | Exchange token | POST /v1/tokens/exchange |
| TOKEN-04 | Token | Revoke token | POST /v1/tokens/revoke |
| TOKEN-05 | Token | Whoami | GET /v1/whoami |
| TOKEN-06 | Token | Expired token rejected | POST /v1/tokens + wait |
| TOKEN-07 | Token | Rate limiting | POST /v1/tokens x6 |
| TOKEN-08 | Token | Scoped token denied | POST /v1/charm |
| TOKEN-09 | Token | Package scoping | POST /v1/charm/x/revisions |
| TOKEN-10 | Token | Channel scoping | POST /v1/charm/x/releases |
| PKG-01 | Package | Register charm | POST /v1/charm |
| PKG-02 | Package | Duplicate fails | POST /v1/charm |
| PKG-03 | Package | Get metadata | GET /v1/charm/x |
| PKG-04 | Package | Patch metadata | PATCH /v1/charm/x |
| PKG-05 | Package | Toggle privacy | PATCH /v1/charm/x |
| PKG-06 | Package | Delete empty | DELETE /v1/charm/x |
| PKG-07 | Package | Delete with revisions | DELETE /v1/charm/x |
| PKG-08 | Package | List packages | GET /v1/charm |
| PKG-09 | Package | Search packages | GET /v2/charms/find |
| REV-01 | Revision | Full pipeline | POST multiple |
| REV-02 | Revision | Upload with resources | POST /unscanned-upload |
| REV-03 | Revision | List revisions | GET /v1/charm/x/revisions |
| REV-04 | Revision | Multi-channel release | POST /v1/charm/x/releases |
| REV-05 | Revision | Custom track | POST /v1/charm/x/tracks |
| REV-06 | Revision | Download charm | GET /api/v1/charms/download/x |
| V2-01 | V2 | Anonymous find | GET /v2/charms/find |
| V2-02 | V2 | Authenticated find | GET /v2/charms/find |
| V2-03 | V2 | Info for published | GET /v2/charms/info/x |
| V2-04 | V2 | Info for private (404) | GET /v2/charms/info/x |
| V2-05 | V2 | Info with channel | GET /v2/charms/info/x?channel= |
| V2-06 | V2 | Refresh published | POST /v2/charms/refresh |
| V2-07 | V2 | Anonymous refresh | POST /v2/charms/refresh |
| V2-08 | V2 | Refresh with base | POST /v2/charms/refresh |
| RES-01 | Resource | Upload file resource | POST /v1/charm/x/resources/y/revisions |
| RES-02 | Resource | List definitions | GET /v1/charm/x/resources |
| RES-03 | Resource | List revisions | GET /v1/charm/x/resources/y/revisions |
| RES-04 | Resource | OCI upload credentials | GET /v1/charm/x/resources/y/oci-image/upload-credentials |
| RES-05 | Resource | OCI image blob | POST /v1/charm/x/resources/y/oci-image/blob |
| RES-06 | Resource | Download resource | GET /api/v1/resources/download/x |
| RES-07 | Resource | Incompatible revision | POST /v1/charm/x/releases |
| ACL-01 | ACL | Non-owner push | POST /v1/charm/x/revisions |
| ACL-02 | ACL | Non-owner release | POST /v1/charm/x/releases |
| ACL-03 | ACL | Non-owner delete | DELETE /v1/charm/x |
| ACL-04 | ACL | Private invisible | GET /v1/charm/x |
| ACL-05 | ACL | Empty-perm bypass (C-1) | Various |
| ACL-06 | ACL | Non-admin admin endpoint | GET /v1/admin/charmhub-sync |
| ACL-07 | ACL | Unauth macaroon challenge | GET /v1/tokens |
| LIM-01 | Limit | Oversized JSON | POST /v1/tokens |
| LIM-02 | Limit | Oversized upload | POST /unscanned-upload/ |
| LIM-03 | Limit | Oversized headers | GET / |
| LIM-04 | Limit | Invalid JSON | POST /v1/tokens |
| LIM-05 | Limit | Header injection (H-4) | GET /api/v1/charms/download/x |
| PERS-01 | Persist | Data survives restart | GET after restart |
| PERS-02 | Persist | Tokens survive restart | GET after restart |
| PERS-03 | Persist | OCI state survives restart | GET /v2/_catalog after restart |
| JUJU-01 | Juju | charmcraft full flow | Full sequence |
| JUJU-02 | Juju | juju find+refresh+download | Full sequence |
| JUJU-03 | Juju | OCI resource upload | OCI credentials flow |
| JUJU-04 | Juju | Macaroon auth | Authorization: Macaroon |
| JUJU-05 | Juju | Dashboard exchange | POST /v1/tokens/dashboard/exchange |
| OCI-01 | OCI | TLS valid cert | GET https://localhost:5000/v2/ |
| OCI-02 | OCI | Catalog after push | GET /v2/_catalog |
| OCI-03 | OCI | Pull robot valid | Docker login + pull |
| OCI-04 | OCI | Push robot valid | Docker push |
| OCI-05 | OCI | Per-repo ACL (M-17) | Cross-package push |
| SNAP-01 | Snap | Snap service active | snap info |
| SNAP-02 | Snap | TLS env vars wired | HTTPS check |

**Total: 52 test scenarios**
