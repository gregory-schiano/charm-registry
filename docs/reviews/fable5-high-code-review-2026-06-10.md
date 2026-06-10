# Code Review — branch `prod-ready-use-harbor/complete` vs `main`

- **Date:** 2026-06-10
- **Reviewer:** Claude (Fable 5), `/code-review` high effort
- **Scope:** `git diff main...HEAD` — 209 files, ~51.8k insertions, 86 commits.
  Vendored charm libraries (`charm/lib/**`), `docs/**`, `go.sum`, and generated
  coverage files were excluded. Test code was reviewed at lower priority.
- **Method:** 7 independent finder angles (line-by-line diff scan,
  removed-behavior audit, cross-file tracing, reuse, simplification,
  efficiency, altitude) producing ~30 candidates, deduplicated, then each
  candidate independently verified (CONFIRMED / PLAUSIBLE / REFUTED,
  recall-biased). 21 findings survived: 13 confirmed, 8 plausible.

---

## Correctness findings

### 1. Snap wrapper exports wrong TLS env var names — API TLS silently never enabled
**CONFIRMED · severity: high (security)** — `snap/local/charm-registry-wrapper:50`

The wrapper exports `CHARM_REGISTRY_TLS_CERT_FILE` / `CHARM_REGISTRY_TLS_KEY_FILE`,
but `internal/config/config.go` reads `CHARM_REGISTRY_API_TLS_CERT_FILE` /
`CHARM_REGISTRY_API_TLS_KEY_FILE`. Config validation (`config.go:469`) accepts
both TLS paths being empty together, so nothing flags the mismatch.

**Failure scenario:** Operator runs `snap set charm-registry tls.enabled=true`
with cert paths; the registry serves plain HTTP while the operator believes
TLS is on.

### 2. `ipRateLimiter` memory grows without bound — `Cleanup()` is never called
**CONFIRMED · severity: high (resource exhaustion)** — `internal/api/http.go:471`

`ipRateLimiter.Cleanup()` (commented "Call periodically") has zero callers.
`Allow()` prunes old timestamps per IP (lines 457–462) but never deletes empty
map entries. By contrast, `tokenIssueLimiter` prunes inline (lines 159–163)
and deletes empty keys (line 181).

**Failure scenario:** Sustained traffic or a spoofed-IP flood creates one
permanent entry per unique client IP in the `entries` map → unbounded memory
growth / OOM over time. Related to finding #14 (the limiter duplication is
where the bug crept in).

### 3. Dev-token parsing change alters extracted username for tokens with extra colons
**CONFIRMED · severity: medium (auth, dev mode only)** — `internal/auth/auth.go:254`

Parsing switched from `strings.Split(raw, ":")` (username = `parts[2]`,
truncated at colons) to `strings.SplitN(strings.TrimPrefix(raw, "dev:"), ":", 2)`
(username = everything after the second colon).

**Failure scenario:** Token `dev:subj:user:extra` — old code extracted
username `user`; new code extracts `user:extra`. Any dev-mode identity or test
fixture relying on the old truncation now resolves to a different account.

### 4. Rate-limit key from `X-Forwarded-For` is not whitespace-trimmed
**PLAUSIBLE · severity: medium (security)** — `internal/api/http.go:491`

`strings.SplitN(forwarded, ",", 2)[0]` keeps leading whitespace, so
`' 1.2.3.4'` and `'1.2.3.4'` get distinct rate-limit buckets, each with a full
allowance. Note chi's `RealIP` middleware is in the stack, but the rate-limit
middleware independently re-extracts the header.

**Failure scenario:** Attacker varies leading whitespace in `X-Forwarded-For`
to bypass per-IP rate limiting (requires the header to be trusted, i.e. no
sanitizing proxy in front).

### 5. Null-base releases silently overwrite each other on the same channel
**PLAUSIBLE · severity: medium (data loss vs. by-design ambiguity)** — `internal/repo/postgres_releases.go:10`

With `base = null`, the generated column `base::text` yields the string
`"null"` for every such release, so the unique index
`(package_id, channel, base_key)` collapses them, and `ReplaceRelease`'s
`ON CONFLICT … DO UPDATE` silently replaces the prior row. `memory.go`'s
`releaseVariantKey` (line 835–837) confirms the same channel-wide collapse for
nil base across backends.

**Failure scenario:** Two architecture-specific releases to the same channel
without an explicit base: the second silently replaces the first instead of
coexisting or erroring. If "null base = channel-wide release" is intended, it
deserves a test and a documentation note, because the data loss is silent.

### 6. Full charm/resource archives buffered in memory during sync
**CONFIRMED · severity: medium (resource exhaustion)** — `internal/charmhub/client.go:460`

`Download` returns `[]byte` via `io.ReadAll` of the response body.
`blob.Store.Put` accepts `io.Reader`, so streaming (with a `TeeReader` for
hash computation) was available.

**Failure scenario:** Syncing a charm with a multi-hundred-MB archive — or
several concurrently — allocates the full payloads on the heap, risking OOM.

### 7. `Base` structure is never validated before persistence
**CONFIRMED · severity: medium (validation gap)** — `internal/core/constructors.go:57`, `internal/service/releases.go:21-79`

`core.NewRelease` validates ID/channel/revision/timestamp but not `Base`; the
service-layer `CreateRelease` validates channel, revision existence, and
resources but never the base. Downstream parsing
(`postgres_sqlc.go:192-198`) is permissive, so malformed bases round-trip as
partially-zero structs rather than erroring.

**Failure scenario:** A release with an incomplete base (missing name or
architecture) is accepted and persisted; refresh/info API clients receive base
objects with empty required fields.

### 8. Rate limits cannot be configured in snap deployments
**CONFIRMED · severity: medium (functional gap)** — `snap/local/charm-registry-wrapper:44`

`config.go:311-323` reads `CHARM_REGISTRY_IP_RATE_LIMIT`,
`CHARM_REGISTRY_TOKEN_RATE_LIMIT` (and related) from the environment, but the
wrapper has no `set_env_from_config` mapping for any of them, and the
configure hook neither validates nor advertises such options.

**Failure scenario:** Operator under load tries to raise limits via snap
config; the binary silently keeps compiled-in defaults with no supported way
to change them in a snap deployment.

### 9. SQLite conflict detection by error-string matching
**PLAUSIBLE · severity: medium** — `internal/repo/sqlite.go:1111`

`isSQLiteConstraint` uses `strings.Contains(err.Error(), "constraint failed")`
rather than the driver's typed error codes, and does not distinguish UNIQUE
from FOREIGN KEY violations.

**Failure scenario:** A message-format variation or a FK violation is
misclassified — a non-conflict error surfaces as 409, or a real conflict
becomes a 500. Behavior also diverges from the postgres backend's typed
error-code handling.

### 10. Sync constructs `core.Track` / `core.ResourceDefinition` without validation
**CONFIRMED · severity: low-medium (validation gap)** — `internal/sync/reconcile.go:473`, `internal/sync/reconcile.go:804`

`reconcile.go` uses direct struct literals for `core.Track` (line 473) and
`core.ResourceDefinition` (line 804); no `NewTrack`/`NewResourceDefinition`
constructors exist in `internal/core/constructors.go` (only `NewPackage`,
`NewRevision`, `NewRelease`, `NewStoreToken`). The `core.Package{}` literal at
line 43 is fine — it is validated later via `NewPackage`.

**Failure scenario:** Upstream Charmhub data with an empty track name or
malformed resource definition is persisted unvalidated, failing later at
DB-constraint time with a generic error — or storing inconsistent data the
API layer would have rejected.

---

## Efficiency findings

### 11. `ListTracks` queried 3× in a single package sync pass
**CONFIRMED** — `internal/sync/reconcile.go:448`, `:464`, `:729`

Within one sync pass for the same package, `ensureCharmhubTrack`,
`updatePackageFromUpstream`, and `persistSyncedPackage` each issue their own
`ListTracks` query. Fetch once and pass the result through.

### 12. Resources downloaded strictly sequentially during sync
**CONFIRMED** — `internal/sync/reconcile.go:614-631`

`ensureCharmhubResourceArtifacts` loops resources one at a time; each
iteration blocks on `charmhub.Download` plus DB writes. A charm with many
resources pays full serial latency; a bounded worker pool would cut wall time.
(May be a deliberate simplicity choice — worth a comment if so.)

### 13. N+1 release deletes when pruning stale variants
**PLAUSIBLE** — `internal/sync/reconcile.go:420-431`

`ListReleases` fetches all releases, then the loop issues one
`DeleteReleaseForBase` per stale release. N is bounded by base/architecture
variants per channel, so the cost is real but modest; a single bulk `DELETE`
with a `WHERE` clause would do.

---

## Reuse / duplication findings

### 14. Two near-identical sliding-window rate limiters
**CONFIRMED** — `internal/api/http.go:125` (`tokenIssueLimiter`) and `:423` (`ipRateLimiter`)

Both implement map-of-timestamps sliding windows with per-key pruning, ~60+
lines each, but with different cleanup strategies — and the divergence is
exactly where bug #2 lives (one prunes inline and deletes empty keys, the
other relies on a never-called `Cleanup()`). Consolidate into one
implementation.

### 15. `internal/sync` duplicates the service layer's error helpers
**PLAUSIBLE** — `internal/sync/service.go:603-621` vs `internal/service/errors.go:54-60`, `internal/service/helpers.go:304-315`

`newError`, `newErrorWithCause`, and `translateRepoError` are copied verbatim.
No import cycle prevents reuse (sync already imports service), but the service
versions are unexported. Export them (or move to a shared package) so repo
error mapping has a single source of truth.

### 16. Duplicate shell helpers between snap hook and wrapper
**CONFIRMED** — `snap/hooks/configure:22-32` and `snap/local/charm-registry-wrapper:21-32`

Both files define identical `config_bool()` (configure also carries
`url_host()` / `is_ip()`). The copies can drift, making the hook and the
runtime wrapper interpret the same snap option differently. Source a shared
helper file from both.

### 17. `rawJSON` + error-check boilerplate repeated 24× in the SQLite backend
**CONFIRMED** — `internal/repo/sqlite.go:156` (pattern start)

The `xJSON, err := rawJSON(v); if err != nil { return err }` pattern appears
24 times. A small helper (or restructuring marshalling into the row-mapping
layer) would collapse the repetition.

### 18. One hand-rolled error response bypasses `writeError`
**PLAUSIBLE** — `internal/api/http_releases.go:86`

`writeJSON(w, http.StatusNotFound, codeMessageResponse{...})` builds an error
payload by hand instead of funnelling through `writeError`
(`internal/api/http.go:314`). The only such site found, but it yields an
inconsistent error shape for that endpoint.

---

## Altitude findings

### 19. S3 access-denied detection falls back to error-string matching
**CONFIRMED** — `internal/blob/store.go:283`

A typed `errors.As`/`ErrorCode()` check exists (line 280), but the fallback
matches `"AccessDenied"` / `"StatusCode: 403"` in `err.Error()`. If the SDK's
message format shifts, permission failures are misclassified. Prefer relying
solely on the typed smithy `APIError` code.

### 20. TLS env-var export decisions hardcoded in the snap wrapper
**PLAUSIBLE** — `snap/local/charm-registry-wrapper:44-88`

Each config option needs a hand-written `set_env_from_config` line; new
options (e.g. mTLS client certs) require wrapper edits separate from
`internal/config`. A generic snap-option→env mapping is feasible but
complicated by the kebab-case→SNAKE_CASE naming translation. This indirection
is also what allowed bug #1 to slip through.

### 21. OCI-image resource type special-cased in two layers
**PLAUSIBLE** — `internal/sync/reconcile.go:663`, `internal/service/resources.go:125`

`if item.Type == "oci-image"` branches exist in both the sync and service
layers. With only one special type today the cost is modest — a per-type
handler abstraction would be premature now — but adding a second resource
type means hunting down scattered branches.

---

## Refuted candidates

Recorded for completeness; each was disproven during verification.

| Candidate | Refutation |
|---|---|
| Migration 0005 could leave a partial constraint state | Migration runs inside an explicit transaction under a non-blocking advisory lock; it applies atomically or not at all. Generic "migrations can fail" speculation, no code defect. |
| `s.accounts` nil panic in `ResolveIdentity` (`internal/service/tokens.go:29`) | `service.New` (`service.go:127`) always assigns the repo backend to `accounts`; all construction paths in `app.go` error-check repo creation first. |
| bcrypt token prefix lookup (`FindStoreTokenByPrefix`) breaks auth | Flow verified correct: exact prefix match in both backends, `VerifyTokenHash` handles bcrypt and SHA-256 schemes, with a sound fallback path. |
| `service.Error` `Status`→`Kind` refactor breaks callers at build time | `go build ./...` passes; mapping is complete in `serviceErrorStatus()`. |
| Repository interface split breaks call sites at build time | `go build ./...` passes. |
| `api.New()` signature change breaks callers at build time | `go build ./...` passes; `app.go` wires the sync service. |
| Admin config fields (`AdminSubjects`/`AdminEmails`/`AdminUsernames`) are dead | Consumed by `Config.IsAdminIdentity` (`config.go:511`), used in `tokens.go:34`. |
| OCI manifest downloaded twice (`checkManifestHead` + `MirrorImage`) | First call is a true `remote.Head` (metadata only, no body, `oci/client.go:300`); only the `remote.Get` downloads the manifest. |
| CI coverage filtering duplicates Makefile exclusion logic | CI runs `make coverage`; filtering lives only in the Makefile (`_COVER_PKGS`). No duplication. |
| Duplicate `markCharmhubSyncRules(... deleting ...)` calls (`reconcile.go:121/141`) | Distinct control flows (final package deletion vs. rule pruning) with different error handling; unifying would merge unrelated concerns. |
| Three `ensure*Artifacts` functions should share a template | The load→parse→persist shape is superficial; the variants differ substantively in every step. |
| Config env parsing is repetitive boilerplate | Already delegated to idiomatic helpers (`envBool`, `envDuration`, …); pure style, no observable cost. |

---

## Suggested fix order

1. **#1** (snap TLS env names) — one-line rename in the wrapper; also add a
   config validation that rejects `tls.enabled` semantics with empty paths.
2. **#2 + #14 together** — consolidate the two rate limiters into the
   `tokenIssueLimiter` pattern (inline prune + empty-key deletion), fixing the
   leak and the duplication at once. Add `TrimSpace` for **#4** in the same
   pass.
3. **#5** — decide and document the null-base release semantics; add a test
   either way.
4. **#3** — confirm intended dev-token format and pin it with a test.
5. **#6–#10**, then the efficiency/cleanup items as opportunity allows.
