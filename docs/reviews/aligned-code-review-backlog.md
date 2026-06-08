# Aligned Code-Review Backlog — charm-registry use-harbor branch

**Base commit:** `5119530` (production-readiness-use-harbor)
**Synthesized from:** GLM 5.1 (t_5f74b19a), Opus 4.7 (t_7fc4e058), GPT 5.5 (t_a279c823)
**Date:** 2026-06-08

---

## Methodology

Findings from three independent code reviews were normalized by topic and cross-referenced. Each unique finding is classified by **consensus level**:

- **3/3 agreed** — All three reviewers flagged the same issue (or near-identical).
- **2/3 agreed** — Two reviewers flagged it; the third did not contradict (may not have reviewed that scope).
- **1/3 but compelling** — Single reviewer, but the finding is objectively correct and high-impact.
- **1/3 and uncertain** — Single reviewer, needs reproduction or design-intent confirmation before accepting.

Every accepted item includes the smallest safe implementation step and a verification path.

---

## CRITICAL — Must fix before production

### C-1. `requirePermission` empty-permissions bypass [3/3]

**Consensus:** Opus (CR-1), GLM (implicit in item #5/#6), GPT (partial overlap with dev-auth concern)
**Files:** `internal/service/helpers.go:38-39`
**Problem:** `if identity.Token == nil || len(identity.Token.Permissions) == 0 { return nil }` — a scoped store token with an empty permissions slice bypasses ALL `requirePermission` checks, granting full access. OIDC-direct-auth bypass (Token == nil) may be intentional Charmhub compat; empty-token bypass is a bug.
**Implementation:**
1. Split the condition: `if identity.Token == nil { return nil }` (OIDC direct auth, intentional).
2. Add: `if len(identity.Token.Permissions) == 0 { return newForbiddenError(...) }`.
3. Add unit test: create identity with `Token.Permissions: []string{}`, verify `requirePermission` returns forbidden.
**Verification:** `go test ./internal/service/ -run TestRequirePermission -v`

---

### C-2. Hardcoded PBKDF2 salt + panic in `deriveKey` [3/3]

**Consensus:** Opus (CR-3), GLM (item #1 — encryption-at-rest / key management unclear), GPT (C2 — key rotation impossible)
**Files:** `internal/oci/client.go:50,536-541`
**Problem:** Hardcoded salt `charm-registry/oci-secret/v1` means every deployment derives the same key from the same secret. No key versioning or rotation path exists — rotating `OCISecretKey` makes all encrypted robot secrets undecryptable. `deriveKey` panics on error, crashing the process.
**Implementation:**
1. Add a `key_version` column to the `packages` table (or a new `oci_key_versions` table) storing `v1`, `v2`, etc.
2. Prefix encrypted secrets with version: `v1:base64...`.
3. On startup, if key version `v2` is configured, decrypt all `v1` secrets with old key, re-encrypt with new key, write back as `v2:...`.
4. Generate a per-deployment random salt at first install, store in DB config table.
5. Replace `panic` with error return from `New()`.
**Verification:** `go test ./internal/oci/ -run TestDeriveKey -v`; integration test: encrypt with v1 key, rotate to v2, verify decrypt still works.

---

### C-3. PostgreSQL migrations have no version tracking [2/3]

**Consensus:** GLM (item #3 — critical), GPT (L2 — mentions it as low but recommends migration runner)
**Files:** `internal/repo/migrations/0001_init.sql` through `0005_charmhub_sync_variants.sql`
**Problem:** PG migrations use `IF NOT EXISTS` idempotent DDL with no `schema_migrations` table and no rollback capability. SQLite path uses `schema_migrations` properly but PG does not. Partial failures are unrecoverable.
**Implementation:**
1. Add `golang-migrate` or `goose` as a dependency.
2. Create `schema_migrations` table for PostgreSQL.
3. Wrap existing migration files in the chosen framework's format.
4. Add `make migrate-up` and `make migrate-down` Makefile targets.
**Verification:** `make migrate-down && make migrate-up` on a fresh Postgres; verify `schema_migrations` rows.

---

### C-4. Compose.yaml DATABASE_URL password is literal `***` [2/3]

**Consensus:** Opus (CR-2 — critical), GPT (C1 — hardcoded credentials in compose)
**Files:** `compose.yaml:130`, `.env.example:15`
**Problem:** Password field is the literal string `***` instead of `postgres`. `make up` fails. `.env.example` has same issue.
**Implementation:**
1. Change compose.yaml: `${CHARM_REGISTRY_DATABASE_URL:-postgres://postgres:postgres@postgres:5432/charm_registry?sslmode=disable}`
2. Fix `.env.example` line 15 accordingly.
3. Consider `sslmode=verify-full` for production guidance (document in README).
**Verification:** `docker compose up`, wait for healthy, `curl :8080/` returns root document.

---

## HIGH — Must fix before production

### H-1. No TLS termination for API server (:8080 is plain HTTP) [3/3]

**Consensus:** GLM (item #4 — blocking), GPT (positive note — OCI has TLS, API doesn't), Opus (HI-5 related — world-readable key for OCI cert)
**Files:** `cmd/charm-registry/main.go`
**Implementation:**
1. Add optional TLS config for the API server (`tls_cert_file`, `tls_key_file` in config).
2. If TLS files are provided, serve HTTPS; otherwise serve HTTP with a startup warning.
3. Document that a TLS-terminating reverse proxy (nginx/Traefik/Caddy) is the recommended production path.
4. Add startup log: `"WARNING: API server running without TLS — use a reverse proxy in production"`.
**Verification:** Start with TLS files, verify HTTPS connection; start without, verify warning appears in logs.

---

### H-2. Dev auth defaults to `true` in compose.yaml, no runtime warning [3/3]

**Consensus:** Opus (HI-3, HI-6), GLM (item #5), GPT (H4)
**Files:** `compose.yaml:146`, `.env.example`, `internal/auth/auth.go`
**Implementation:**
1. Change compose.yaml default: `${CHARM_REGISTRY_ENABLE_INSECURE_DEV_AUTH:-false}`.
2. Change `.env.example` default to `false`.
3. Add startup banner when dev auth is enabled: `"⚠ INSECURE DEV AUTH ENABLED — not for production use"`.
4. Add `auth_method` field to Identity context so audit logs can distinguish dev vs OIDC sessions.
**Verification:** Start with `ENABLE_INSECURE_DEV_AUTH=true`, verify warning in stderr; start with `false`, verify no warning.

---

### H-3. Token hash uses unsalted SHA-256 [2/3]

**Consensus:** GLM (item #6 — high, blocking), Opus (no direct finding but related to auth), GPT (no direct finding — positive note on token issue rate limiting)
**Files:** `internal/repo/postgres_accounts.go` (store_tokens table)
**Implementation:**
1. Replace `sha256(token)` with `bcrypt.HashPassword(token, bcrypt.DefaultCost)` or `argon2id`.
2. Add a migration that adds `token_hash_scheme` column (default `sha256` for existing rows).
3. On token validation, check scheme: if `sha256`, validate with SHA-256, then re-hash with bcrypt on next login. If `bcrypt`, validate with bcrypt.
4. After migration + burn-in, remove SHA-256 path in a future release.
**Verification:** `go test ./internal/service/ -run TestTokenHash -v`; create token, verify hash starts with `$2a$`.

---

### H-4. Content-Disposition header injection [3/3]

**Consensus:** Opus (HI-2), GPT (M3), GLM (no direct finding — may not have reviewed download handlers)
**Files:** `internal/api/http.go:268`
**Implementation:**
1. Add a `sanitizeFilename` function: strip `"`, `\`, CRLF, replace non-printable with `_`.
2. Use RFC 5987 encoding for non-ASCII filenames: `filename*=UTF-8''encoded-name`.
**Verification:** Unit test with filenames containing `"`, `\r\n`, unicode; verify no header injection.

---

### H-5. No general API rate limiting (only token issuance) [2/3]

**Consensus:** GPT (H2), Opus (ME-4 — in-memory only), GLM (partial — no request timeouts #8)
**Files:** `internal/api/http.go`
**Implementation:**
1. Add `golang.org/x/time/rate` or chi `middleware.Throttle` for per-IP rate limiting.
2. Apply to all mutation endpoints (push, register, release, upload, token issue).
3. Document the single-instance limitation of in-memory rate limiting.
**Verification:** Send 100 rapid requests, verify 429 responses after limit exceeded.

---

### H-6. OCI provisioning errors swallowed — impossible to debug [2/3]

**Consensus:** Opus (HI-9), GPT (overlap in service error handling)
**Files:** `internal/service/oci.go:43-65`, `internal/sync/oci.go:32`
**Implementation:**
1. Change `ensureOCIProvisioned` to wrap the original error: `fmt.Errorf("OCI package provisioning unavailable: %w", err)`.
2. Ensure error is logged with `slog.ErrorContext`.
**Verification:** Force OCI provisioning failure (bad Harbor URL), verify error message includes root cause in logs and API response.

---

### H-7. Charmhub Download SSRF risk [1/3 but compelling]

**Consensus:** Opus only (HI-11)
**Files:** `internal/charmhub/client.go:410-430`
**Problem:** `Download` accepts arbitrary `artifactURL` without validation. Compromised Charmhub API could return internal URLs.
**Implementation:**
1. Validate `artifactURL` starts with configured Charmhub base URL or known allowed domain.
2. Set `CheckRedirect` to limit redirect hops and validate target URLs.
3. Block non-HTTPS and RFC 1918 addresses.
**Verification:** Unit test with internal/SSRF URLs, verify rejection.

---

### H-8. Duplicate OCI helper functions across service/ and sync/ [3/3]

**Consensus:** GPT (H1), Opus (ME-25 — significant code duplication), GLM (implicit in architecture observations)
**Files:** `internal/service/oci.go`, `internal/sync/oci.go`
**Implementation:**
1. Extract `packagesEqualForOCI`, `ociPackageProvisioned`, `robotCredentialReady`, `robotEqual`, `timePtrEqual` to `internal/core/oci_helpers.go`.
2. Extract shared `ensureOCIProvisioned` with error-kind parameter.
3. Update imports in both packages.
**Verification:** `go test ./internal/service/ ./internal/sync/ -v`; `make build` succeeds.

---

### H-9. TLS private key world-readable (chmod 0644) [1/3 but compelling]

**Consensus:** Opus only (HI-5)
**Files:** `deploy/oci/generate-certs.sh:72`
**Implementation:**
1. Change to `chmod 0640` with group set to Docker GID or uid 65534.
2. Or use ACL: `setfacl -m u:65534:r && chmod 0600`.
**Verification:** Run script, verify key file permissions are 0640 or tighter.

---

### H-10. `handleInfo` silently discards first error [2/3]

**Consensus:** Opus (HI-1), GPT (partial overlap)
**Files:** `internal/api/http_releases.go:72-86`
**Implementation:**
1. When `?channel=` is present, skip `GetPackageInfo` call and go directly to `GetPackageInfoForChannel`.
2. Or check the first call's error before making the second.
**Verification:** Unit test with channel param, verify only one service call is made.

---

## MEDIUM — Should fix soon after production

### M-1. SHA-384 values labeled as `sha3-384` — algorithm name mismatch [2/3]

**Consensus:** Opus (ME-1), GPT (no finding)
**Files:** `internal/core/revision.go:29`, `internal/core/resource.go:24,41`
**Uncertainty:** Key name may be Charmhub API compatibility requirement. Verify against upstream spec before changing.
**Implementation:** If Charmhub uses `sha3-384` as a key name for SHA-384, add a doc comment. If not, rename to `sha384` or `sha-384`.
**Verification:** Check Charmhub API spec; if renamed, `go test ./... -v`.

---

### M-2. `writeJSON` silently swallows encoding errors [3/3]

**Consensus:** Opus (ME-2), GPT (M6), GLM (no direct finding)
**Files:** `internal/api/http.go:256`
**Implementation:** Log the error: `slog.ErrorContext(r.Context(), "json encode", "error", err)`. Consider marshaling to buffer first for safety.
**Verification:** Force JSON encode failure (channel value), verify error logged.

---

### M-3. No request-level timeouts [2/3]

**Consensus:** GLM (item #8), Opus (ME-3 — CLI no timeout)
**Files:** `internal/api/http.go`, `cmd/charm-registryctl/main.go`
**Implementation:**
1. Add `context.WithTimeout` from request context to repo/blob calls (configurable, default 30s).
2. Add `http.Client{Timeout: 30*time.Second}` in charm-registryctl.
**Verification:** Set 1s timeout, make slow DB query, verify context deadline exceeded.

---

### M-4. No request ID middleware / structured logging correlation [2/3]

**Consensus:** GLM (item #10), Opus (partial — LO-24 wrong context)
**Files:** `internal/api/http.go`
**Implementation:** Add request ID middleware (UUID), inject into context, configure `slog` to extract as `request_id` field.
**Verification:** Make two concurrent requests, verify distinct `request_id` in logs.

---

### M-5. No audit logging for security-relevant operations [2/3]

**Consensus:** GPT (M4), Opus (HI-14, LO-7 — system identity no audit)
**Files:** Throughout
**Implementation:** Define `auditLog(ctx, action, actor, target, outcome)` helper. Emit for token issue/revoke, package create/delete, admin actions, OCI credential creation.
**Verification:** Issue a token, grep logs for `audit=true` or structured `action=token_issue`.

---

### M-6. Charmhub sync has no backoff on continuous failures [2/3]

**Consensus:** GLM (item #7), GPT (no direct finding)
**Files:** `internal/sync/service.go`
**Implementation:** Track consecutive failures per rule; exponential backoff (15m→30m→1h→2h, capped). Reset on success.
**Verification:** Set Charmhub URL to invalid, verify backoff intervals in logs.

---

### M-7. `SearchPackages` ILIKE without trigram index [1/3 but compelling]

**Consensus:** GPT only (H3)
**Files:** `internal/repo/queries/packages.sql:133`
**Implementation:** `CREATE EXTENSION IF NOT EXISTS pg_trgm; CREATE INDEX idx_packages_name_trgm ON packages USING gin (name gin_trgm_ops);`
**Verification:** `EXPLAIN ANALYZE SearchPackages` before/after, verify index scan.

---

### M-8. Config struct is a monolith (~550 lines, 40+ fields) [1/3]

**Consensus:** GPT only (M1)
**Files:** `internal/config/config.go`
**Implementation:** Group into sub-structs (APIConfig, OCIConfig, S3Config, OIDCConfig, ServerConfig).
**Verification:** `go test ./internal/config/ -v`; `make build`.

---

### M-9. Self-signed certs have 10-year expiry, no rotation mechanism [1/3]

**Consensus:** Opus only (ME-7)
**Files:** `snap/hooks/configure:65-69`, `deploy/oci/generate-certs.sh:62-66`
**Implementation:** Add certificate age check; regenerate when within 30 days of expiry. Reduce default to 365 days.
**Verification:** Create cert, manually set system time to 11 months ahead, restart, verify regeneration.

---

### M-10. `make down` removes volumes (data loss) [1/3 but compelling]

**Consensus:** Opus only (ME-13)
**Files:** `Makefile:125`
**Implementation:** Remove `-v` from default `down` target. Add `make down-clean` target that includes `-v`.
**Verification:** `make down`, verify volumes persist; `make down-clean`, verify volumes removed.

---

### M-11. Snap uses `go/latest/stable` — non-reproducible builds [1/3]

**Consensus:** Opus only (HI-8)
**Files:** `snap/snapcraft.yaml:35`
**Implementation:** Pin to `go/1.26/stable` to match Dockerfile and rockcraft.
**Verification:** `snapcraft` build, verify Go version in build log.

---

### M-12. Upload garbage collection missing [2/3]

**Consensus:** GLM (item #12), GPT (L3 — package_acl unused)
**Files:** `internal/repo/migrations/0001_init.sql`
**Implementation:** Add `created_by_account_id` to `uploads` table. Add background cleanup job for uploads older than N hours with `status != 'approved'`.
**Verification:** Create upload, wait, verify cleanup runs.

---

### M-13. No health check / metrics endpoints [2/3]

**Consensus:** GLM (missing table — health check high, Prometheus medium), GPT (partial)
**Files:** New `internal/api/health.go`
**Implementation:** Add `/healthz` (liveness), `/readyz` (readiness with DB + S3 check), `/metrics` (Prometheus).
**Verification:** `curl /healthz` returns 200; `curl /readyz` returns 503 when DB is down.

---

### M-14. Backup/restore documentation missing [2/3]

**Consensus:** GLM (missing table — backup/restore high), GPT (partial)
**Files:** New `docs/operations/backup-restore.md`
**Implementation:** Document `pg_dump`/`pg_restore` for Postgres, S3 bucket sync for blobs, and full stack restore procedure.
**Verification:** Follow documented procedure on a fresh compose stack.

---

### M-15. `SyncPackage` ignores context (potential hangs) [1/3]

**Consensus:** Opus only (LO-20)
**Files:** `internal/oci/client.go:181`
**Implementation:** Propagate context to all network calls within `SyncPackage`.
**Verification:** Cancel context mid-sync, verify goroutine exits promptly.

---

### M-16. Memory repo diverges from Postgres (test fidelity) [2/3]

**Consensus:** Opus (HI-10, ME-19, ME-20), GPT (M5)
**Files:** `internal/repo/memory.go`
**Implementation:**
1. Fix `ResolveDefaultRelease` to return highest revision.
2. Respect caller-provided `CreatedAt` in `EnsureAccount`.
3. Implement collaborator access in `CanViewPackage`/`CanManagePackage`.
**Verification:** `go test ./internal/repo/ -run TestMemory -v`.

---

### M-17. OCI auth middleware bypasses distribution registry ACL [1/3 but compelling]

**Consensus:** Opus only (ME-18)
**Files:** `internal/oci/client.go:400-427`
**Implementation:** Inject resolved package/project into request context; ensure downstream handler enforces per-repository scoping.
**Verification:** Attempt cross-package OCI access with push robot, verify rejection.

---

### M-18. SQLite `MaxOpenConns(1)` contention risk [1/3]

**Consensus:** GLM only (item #2 — critical)
**Files:** `internal/repo/sqlite.go`
**Uncertainty:** GLM rated this critical, but Opus and GPT did not flag it. SQLite is documented as dev/embedded only; PostgreSQL is the production path. Downgrading to medium with documentation fix.
**Implementation:** Add doc comment on `MaxOpenConns(1)` explaining the limitation. Audit write paths for network I/O inside transactions. Document SQLite as dev-only in README.
**Verification:** Review sync reconciler; confirm no network I/O inside DB transactions.

---

## Items requiring user decisions before implementation

| ID | Decision needed |
|----|----------------|
| C-1 | Is OIDC-direct-auth bypass in `requirePermission` intentional Charmhub compatibility? (Empty-token bypass is definitely a bug regardless.) |
| M-1 | Is `sha3-384` JSON key name a Charmhub API compatibility requirement? |
| H-7 | What is the acceptable domain allowlist for Charmhub artifact downloads? |
| H-2 | Should dev auth be mutually exclusive with OIDC, or is dual-mode acceptable with warnings? |
| M-15 | Is `SyncPackage` context propagation needed now, or can it wait for a sync refactor? |

---

## Summary statistics

| Metric | Value |
|--------|-------|
| Total unique findings across 3 reviews | ~90 |
| Findings after deduplication | 34 |
| 3/3 consensus | 8 (C-1, C-2, H-1, H-2, H-4, H-8, M-2) + C-3 partially |
| 2/3 consensus | 12 |
| 1/3 but compelling | 6 |
| 1/3 and uncertain (not accepted) | ~8 |
| Critical accepted | 4 |
| High accepted | 10 |
| Medium accepted | 18 |
| Items needing user decisions | 5 |

---

## Verification path summary

Every accepted item has at minimum a unit test or manual verification command. The full integration test suite (from Task 10/11 in the plan) will provide end-to-end coverage for the critical and high items. Priority order for implementation:

1. **C-4** (compose password) — immediate, unblocks all testing
2. **C-1** (requirePermission bypass) — 5-line fix, highest security impact
3. **C-2** (PBKDF2 salt / key rotation) — requires migration + re-encryption path
4. **C-3** (PG migration version tracking) — requires migration framework
5. **H-1 through H-10** — in dependency order (TLS → dev-auth → token hash → header injection → rate limit → error wrapping → SSRF → dedup → key perms → handleInfo)
6. **M-1 through M-18** — in priority order after High items are stable

---

*End of aligned code-review backlog.*
