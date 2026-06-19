# Public Charmhub Consumer Endpoints Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Allow stock Juju to browse, resolve, and download public charms without credentials while retaining optional authenticated access to authorized private charms.

**Architecture:** Add an API middleware that distinguishes absent credentials from invalid supplied credentials, then route only the Juju consumer endpoints through it. Remove redundant mandatory-auth guards from the corresponding service reads and rely on the existing package visibility checks. Pin the behavior with HTTP/service regression tests and document the stable public compatibility contract.

**Tech Stack:** Go 1.26, chi router, testify, Markdown, embedded OpenAPI YAML

---

### Task 1: Optional identity middleware

**Files:**
- Modify: `internal/api/http.go`
- Test: `internal/api/http_test.go`

- [ ] **Step 1: Write failing middleware regression tests**

Add table-driven tests proving:

```go
func TestOptionalIdentity(t *testing.T) {
    // No Authorization header reaches the handler with an anonymous identity.
    // A valid dev bearer reaches the handler with an authenticated identity.
    // An invalid supplied bearer receives HTTP 401 and does not reach the handler.
}
```

Use a small test router and an `API` created with the existing test
authenticator/service helpers. Assert both `Identity.Authenticated` and whether
the wrapped handler was called.

- [ ] **Step 2: Run the focused test and verify RED**

Run:

```bash
go test ./internal/api -run '^TestOptionalIdentity$' -count=1
```

Expected: compilation failure because `optionalIdentity` does not exist.

- [ ] **Step 3: Implement minimal optional identity resolution**

Add:

```go
func (a *API) optionalIdentity(next func(http.ResponseWriter, *http.Request, core.Identity)) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        if strings.TrimSpace(r.Header.Get("Authorization")) == "" {
            next(w, r, core.Identity{})
            return
        }
        identity, err := a.resolveIdentity(r)
        if err != nil {
            writeError(w, r, err)
            return
        }
        next(w, r, identity)
    }
}
```

Do not treat invalid supplied credentials as anonymous.

- [ ] **Step 4: Run the focused test and verify GREEN**

Run the same test command and expect PASS.

### Task 2: Public Juju consumer routing and service reads

**Files:**
- Modify: `internal/api/http.go`
- Modify: `internal/service/packages.go`
- Modify: `internal/service/releases.go`
- Modify: `internal/service/revisions.go`
- Modify: `internal/service/resources.go`
- Test: `internal/api/http_test.go`
- Test: `internal/service/service_test.go`

- [ ] **Step 1: Write failing public-consumer HTTP tests**

Create a released public package with a file resource using existing test
helpers, then call without credentials:

```text
GET  /v2/charms/find?q=<name>
GET  /v2/charms/info/<name>
POST /v2/charms/refresh
GET  /v2/charms/resources/<name>/<resource>/revisions
GET  /api/v1/charms/download/<package-id>_1.charm
GET  /api/v1/resources/download/charm_<package-id>.<resource>_1
```

Assert `200` and expected payload/content for each route.

- [ ] **Step 2: Write failing privacy and credential tests**

Add tests that prove:

- anonymous search omits a private package;
- anonymous private info, resource revisions, charm download, and resource
  download do not succeed;
- anonymous refresh returns a per-action error for a private package;
- the owner can use valid credentials on all optional routes;
- an invalid bearer on an optional route returns `401`.

- [ ] **Step 3: Run focused HTTP tests and verify RED**

Run:

```bash
go test ./internal/api -run 'TestAnonymousJuju|TestOptionalConsumer|TestPrivateConsumer' -count=1
```

Expected: anonymous requests return `401`.

- [ ] **Step 4: Write service-level failing tests**

Add focused tests using `core.Identity{}` that expect:

- `SearchPackages` returns public matches and omits private matches;
- `GetPackageInfo` succeeds for a released public package;
- `ResolveRefresh` succeeds for a released public package;
- `ListResourceRevisions`, `DownloadCharmStream`, and
  `DownloadResourceStream` succeed for public artifacts;
- private access remains rejected or represented as the existing refresh
  per-action error.

- [ ] **Step 5: Run focused service tests and verify RED**

Run:

```bash
go test ./internal/service -run 'TestAnonymous.*(Search|Info|Refresh|Resource|Download)' -count=1
```

Expected: failures from the existing top-level `requireAuth` calls.

- [ ] **Step 6: Apply optional middleware to only the consumer routes**

In `internal/api/http.go`, change these routes from `requireIdentity` to
`optionalIdentity`:

```go
r.Get("/v2/charms/find", api.optionalIdentity(api.handleFind))
r.Get("/v2/charms/info/{name}", api.optionalIdentity(api.handleInfo))
r.Post("/v2/charms/refresh", api.optionalIdentity(api.handleRefresh))
r.Get("/v2/charms/resources/{name}/{resource}/revisions", api.optionalIdentity(api.handleListResourceRevisions))
r.Get("/api/v1/charms/download/{filename}", api.optionalIdentity(api.handleCharmDownload))
r.Get("/api/v1/resources/download/{filename}", api.optionalIdentity(api.handleResourceDownload))
```

Leave every mutation, management, token, upload, OCI, and admin route on
`requireIdentity`.

- [ ] **Step 7: Remove redundant service-level authentication gates**

Remove only the leading `s.requireAuth(identity)` calls from:

```go
SearchPackages
GetPackage
ResolveRefresh
ListResourceRevisions
DownloadCharmStream
DownloadResourceStream
```

Do not remove `requirePackageView`; it is the public/private authorization
boundary.

- [ ] **Step 8: Run focused service and HTTP tests and verify GREEN**

Run both focused commands from Steps 3 and 5 and expect PASS.

### Task 3: Protect the mandatory-auth boundary

**Files:**
- Test: `internal/api/http_test.go`

- [ ] **Step 1: Add a table-driven protected-route regression**

Without credentials, assert `401` for representative routes in every protected
category:

```text
POST /v1/charm
POST /unscanned-upload/
POST /v1/charm/{name}/revisions
GET  /v1/charm/{name}/resources/{resource}/oci-image/upload-credentials
GET  /v1/tokens
GET  /v1/admin/charmhub-sync
```

Keep the existing anonymous `POST /v1/charm/libraries/bulk` assertion.

- [ ] **Step 2: Run the protected-route test**

Run:

```bash
go test ./internal/api -run 'TestProtectedRoutesRemainAuthenticated|TestLibrariesBulkNoAuth' -count=1
```

Expected: PASS without production changes.

### Task 4: Public single-library compatibility route

**Files:**
- Modify: `internal/api/http.go`
- Modify: `internal/api/http_libraries.go`
- Test: `internal/api/http_test.go`

- [ ] **Step 1: Write a failing route test**

Call:

```text
GET /v1/charm/libraries/example/00000000000000000000000000000000
```

without credentials and assert `404`, not `401`. This documents Charmcraft's
anonymous single-library lookup while preserving the registry's explicit lack
of library hosting.

- [ ] **Step 2: Run the test and verify RED**

Run:

```bash
go test ./internal/api -run '^TestSingleLibraryLookupNoAuthReturnsNotFound$' -count=1
```

Expected: current router returns its generic not-found response; if the status
already passes, strengthen the assertion to the library-specific error code
before implementation.

- [ ] **Step 3: Add the explicit public route and handler**

Register:

```go
r.Get("/v1/charm/libraries/{charm}/{libraryID}", api.handleLibraryNotFound)
```

Implement `handleLibraryNotFound` to return a normal API `404` with code
`not-found` and message `library not found`. No database or library model is
introduced.

- [ ] **Step 4: Run the focused test and verify GREEN**

Run the command from Step 2 and expect PASS.

### Task 5: Document the compatibility contract

**Files:**
- Modify: `docs/api-compatibility.md`
- Modify: `internal/api/spec.go`

- [ ] **Step 1: Update endpoint authentication documentation**

In `docs/api-compatibility.md`, mark the six Juju consumer/download routes as
public with optional credentials. Replace the obsolete roadmap note with:

- anonymous callers see public packages only;
- valid credentials additionally expose authorized private packages;
- invalid supplied credentials receive `401`;
- these routes are a stable Juju compatibility contract;
- Charmcraft library lookup and bulk lookup are public, while library hosting
  is unsupported.

- [ ] **Step 2: Update embedded OpenAPI descriptions**

Describe `/v2/charms/find`, `/v2/charms/info/{name}`, and
`/v2/charms/refresh` as public consumer operations with optional authentication
for private packages. Add the resource revision and artifact download paths if
missing.

- [ ] **Step 3: Validate documentation and formatting**

Run:

```bash
git diff --check
```

Expected: exit code 0.

### Task 6: Full verification

**Files:**
- No new files

- [ ] **Step 1: Format changed Go files**

Run:

```bash
gofmt -w internal/api/http.go internal/api/http_libraries.go internal/api/http_test.go \
  internal/service/packages.go internal/service/releases.go \
  internal/service/revisions.go internal/service/resources.go \
  internal/service/service_test.go
```

- [ ] **Step 2: Run focused package tests**

Run:

```bash
go test ./internal/api ./internal/service -count=1
```

Expected: PASS.

- [ ] **Step 3: Run the repository test suite**

Run:

```bash
make test
```

Expected: PASS. If the workspace cannot download or execute Go 1.26.4, report
that environmental blocker and run the strongest available static/focused
checks instead.

- [ ] **Step 4: Run static validation**

Run:

```bash
make vet
git diff --check
```

Expected: PASS, subject to the same Go toolchain availability constraint.

- [ ] **Step 5: Review scope**

Confirm the diff contains only the approved API middleware/routing, read-path
authorization changes, regressions, and compatibility documentation. Do not
include the user's pre-existing workflow, Makefile, Rockcraft, or `.codex`
changes in this feature summary.

> Note: commits cannot be created in the current workspace because `.git` is
> mounted read-only. Preserve logical task boundaries in the diff and report
> this limitation at handoff.
