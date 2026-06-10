# Code Review: `use-harbor` Branch (Production Readiness)

**Reviewer:** kanban worker (automated review)
**Branch:** `origin/use-harbor` (commit `5119530`)
**Diff scope:** 150 files changed, ~37,949 insertions, ~5,942 deletions
**Date:** 2026-06-07

---

## Executive Summary

The `use-harbor` branch is a **massive feature addition** that transforms the charm-registry from a simpler prototype into a production-capable system. Key additions include: embedded OCI registry, dual-database support (PostgreSQL + SQLite), charmhub sync service, macaroon-based auth compatibility, full resource lifecycle, and snap/charm/rock packaging. The architecture is solid and well-layered. However, several production-readiness concerns remain — primarily around security, error handling, observability, and migration safety.

**Verdict: Not ready for production as-is.** Blocking issues must be addressed first; non-blocking items should be tracked.

---

## Architecture Overview

```
cmd/charm-registry/main.go     → dual HTTP servers (API :8080, OCI :5000)
  └─ internal/app/app.go       → DI wiring
      ├─ internal/auth/         → OIDC + dev auth + macaroons
      ├─ internal/api/          → chi router, middleware, handlers
      ├─ internal/service/      → business logic
      ├─ internal/repo/         → PostgreSQL (pgxpool+sqlc) + SQLite
      ├─ internal/blob/         → S3 (minio) + filesystem storage
      ├─ internal/oci/          → embedded OCI distribution registry
      ├─ internal/charm/        → charm archive parsing
      ├─ internal/charmhub/     → upstream Charmhub API client
      └─ internal/sync/        → background charmhub sync reconciler
```

---

## BLOCKING Issues (must fix before production)

### 1. [CRITICAL] Robot credentials stored as encrypted but decryption key management is unclear

**File:** `internal/core/package.go` — `RobotCredential.EncryptedSecret`
**Files:** `internal/service/oci.go`, `internal/sync/oci.go`

`RobotCredential` stores `EncryptedSecret` in the database. The code checks `robotCredentialReady()` by verifying the field is non-empty, but:
- No code in the reviewed files shows where the secret is **decrypted** for actual OCI push/pull operations.
- If the encryption key is the same application key or a static config value, this is security theater — a database compromise gives both the ciphertext and the key.
- If per-package keys are used, there's no key rotation mechanism visible.

**Recommendation:** Document the encryption-at-rest scheme. If using a single app key, acknowledge this is primarily obfuscation, not real encryption-at-rest. Consider HashiCorp Vault or cloud KMS for production key management.

### 2. [CRITICAL] SQLite `MaxOpenConns(1)` with `busy_timeout(5000)` is fragile under concurrent write load

**File:** `internal/repo/sqlite.go`

SQLite with `MaxOpenConns(1)` serializes all database access through a single connection. Combined with `busy_timeout(5000ms)`:
- Any long-running write transaction blocks ALL reads and writes for up to 5 seconds.
- The sync reconciler performs multiple writes in a single sync cycle — if it holds a transaction open while downloading artifacts from Charmhub (network I/O inside a transaction), it will block the entire API.
- The WAL mode helps concurrent reads but does NOT help when `MaxOpenConns=1` (only one connection exists).

**Recommendation:**
- Audit all write paths to ensure no network I/O occurs inside transactions.
- Consider `MaxOpenConns(1)` for writes + a read pool for reads, or document that SQLite is for single-user/dev only and PostgreSQL is required for production.
- Add connection-pool metrics to detect contention.

### 3. [CRITICAL] PostgreSQL migrations use `CREATE TABLE IF NOT EXISTS` + `ALTER TABLE ADD COLUMN IF NOT EXISTS` — no rollback, no version tracking

**Files:** `internal/repo/migrations/0001_init.sql` through `0005_charmhub_sync_variants.sql`

The PostgreSQL migration strategy relies on idempotent DDL (`IF NOT EXISTS`) applied in sequence, with **no migration version tracking table** and **no rollback capability**:
- If migration 3 fails partway through, there's no way to know which statements succeeded.
- If a migration is re-run after a partial failure, `IF NOT EXISTS` silently skips existing objects — but dependent objects created later may fail.
- The SQLite path uses `schema_migrations` properly; the PostgreSQL path does not.
- Migration 5 drops a unique constraint (`releases_package_id_channel_key`) and creates a new one with `base_key` — if the drop fails, the create will fail on a duplicate constraint name.

**Recommendation:** Use a proper migration framework (golang-migrate, goose, or pgx's built-in migration runner) with forward/backward version tracking for PostgreSQL, matching the SQLite approach.

### 4. [HIGH] No TLS termination for the API server

**File:** `cmd/charm-registry/main.go`

The OCI registry port supports TLS (auto-generated self-signed certs via `snap/hooks/configure`), but the **API server on :8080 is plain HTTP only**. In production, OIDC tokens, store tokens, and macaroons transit in cleartext unless an external reverse proxy terminates TLS.

**Recommendation:** Either add TLS support to the API server (like the OCI server has), or document clearly that a TLS-terminating reverse proxy (nginx, Traefik, Caddy) is **required** in production. Add a startup warning log when running without TLS.

### 5. [HIGH] `dev_auth` mode has no safe default — `CHARM_REGISTRY_ENABLE_INSECURE_DEV_AUTH` defaults to empty (disabled), but the code path is still reachable

**File:** `internal/auth/auth.go`

Dev auth bypasses OIDC entirely, accepting any `Authorization: Bearer <token>` as a valid session. While disabled by default:
- There's no runtime warning that dev auth is active when enabled.
- The `Identity` from dev auth gets `Authenticated: true` and the full account claims — there's no audit trail distinguishing dev-auth sessions from real OIDC sessions.

**Recommendation:** Add a loud startup banner/warning when dev auth is enabled. Add an `auth_method` field to the Identity/context so audit logs can distinguish dev vs OIDC sessions.

### 6. [HIGH] Token hash uses SHA-256 without a salt

**File:** `internal/repo/postgres_accounts.go` (store_tokens table: `token_hash TEXT NOT NULL UNIQUE`)

Store tokens are hashed with SHA-256 before storage, but there's **no per-token salt**. If two users generate the same raw token (unlikely but possible with deterministic test tokens), the hash collides. More importantly, unsalted SHA-256 is vulnerable to rainbow table attacks if the database is compromised.

**Recommendation:** Use bcrypt, scrypt, or argon2id for token hashing — these are purpose-built for secret storage. At minimum, use HMAC-SHA256 with a server-side secret key.

---

## NON-BLOCKING Issues (should fix, won't block launch)

### 7. [MEDIUM] Charmhub sync `Manager` goroutine has no backoff on continuous failures

**File:** `internal/sync/service.go`

The background goroutine wakes on a ticker (default 15m) or a wake channel. If all sync rules fail (e.g., Charmhub is down), the manager will retry every 15 minutes with **no exponential backoff**. This can generate significant noise in logs and waste resources.

**Recommendation:** Track consecutive failure count per rule; apply exponential backoff (15m → 30m → 1h → 2h, capped). Reset on success.

### 8. [MEDIUM] No request-level timeouts on the API handlers

**File:** `internal/api/http.go`

The chi router uses `http.TimeoutHandler` as middleware, but the timeout value is not visible in the reviewed code. Database queries and blob store operations have no per-request deadline enforcement.

**Recommendation:** Propagate `context.WithTimeout` from the request context through to repo and blob store calls. Add configurable request timeout (default 30s).

### 9. [MEDIUM] `io.Copy(io.Discard, r.Body)` in `handleLibrariesBulk` ignores errors

**File:** `internal/api/http_libraries.go`

```go
_, _ = io.Copy(io.Discard, r.Body)
```

The error is explicitly discarded. While this is a no-op endpoint, a malicious client could send an extremely large body that consumes server memory/disk before the copy drains it.

**Recommendation:** Add `http.MaxBytesReader` before draining, or set `http.Server.ReadTimeout` / `MaxHeaderBytes` appropriately.

### 10. [MEDIUM] No structured logging correlation / request IDs

**Files:** Throughout — `slog.DebugContext`, `slog.InfoContext`

Structured logging with `slog` is used consistently (good!), but there's **no request ID middleware**. In a concurrent production environment, it will be very difficult to correlate log lines from the same request.

**Recommendation:** Add a request ID middleware that generates a UUID and injects it into the context. Configure `slog` to extract it as a `request_id` field.

### 11. [MEDIUM] Charm archive parsing loads entire file into memory

**File:** `internal/charm/archive.go`

`ParseArchive(payload []byte)` takes the entire archive as a byte slice. For large charms (the 10 MiB limit is reasonable), this is fine, but `ParseArchiveWithMaxFileSize` only limits per-entry decompressed size — the compressed archive itself is fully in memory.

**Recommendation:** Document the maximum total archive size. Consider streaming parsing for very large archives, or add a total-archive-size check.

### 12. [MEDIUM] `uploads` table has no foreign key to packages or accounts

**Files:** `internal/repo/migrations/0001_init.sql`

The `uploads` table stores `object_key`, `size`, `sha256`, `sha384` but has **no reference to a package or account**. Orphaned uploads (from failed or abandoned upload flows) will accumulate indefinitely with no cleanup mechanism.

**Recommendation:** Add a `created_by_account_id` column to `uploads`. Add a background cleanup job for uploads older than N hours with `status != 'approved'`.

### 13. [LOW] `internal/repo/memory.go` — in-memory backend for testing

The in-memory repo implementation is comprehensive (~336 lines) but:
- It uses `sync.Mutex` (coarse lock) — acceptable for tests but not for production use.
- No documentation that it's test-only.

**Recommendation:** Add a doc comment explicitly stating this is for testing only. Consider `//go:build !prod` or similar if it shouldn't ship in production binaries.

### 14. [LOW] `compose.yaml` uses `postgres:16-alpine` but `pgxpool` may need `pgx/v5` driver compatibility

**File:** `compose.yaml`

The compose stack uses `postgres:16-alpine`. Ensure the `pgx` driver version in `go.mod` supports PostgreSQL 16 features (it does, but this should be documented).

### 15. [LOW] Release unique constraint changed from `(package_id, channel)` to `(package_id, channel, base_key)`

**File:** `internal/repo/migrations/0005_charmhub_sync_variants.sql`

The migration drops the old unique constraint and creates a new one with `base_key` (a generated column from the `base` JSONB). This is a **breaking schema change** — existing data with the same `(package_id, channel)` but different `base` values would fail the old constraint but pass the new one. The migration uses `DROP CONSTRAINT IF EXISTS` + `CREATE UNIQUE INDEX IF NOT EXISTS`, which is safe, but:

**Recommendation:** Add a data migration that resolves any existing duplicate `(package_id, channel)` rows before the constraint change.

### 16. [LOW] OCI client (`internal/oci/client.go`) is ~583 lines with no sub-structure

The OCI client handles project creation, robot account management, artifact push/pull, and catalog operations in a single file. This will become unwieldy as the OCI integration grows.

**Recommendation:** Split into `oci/project.go`, `oci/robot.go`, `oci/artifact.go`, `oci/catalog.go`.

---

## Positive Observations

1. **Clean layered architecture** — API → Service → Repo with clear interfaces. Dependency injection via `app.go` is straightforward and testable.
2. **Good test coverage** — CI enforces 70% minimum coverage. Fuzz tests exist for archive parsing, macaroons, and HTTP handlers.
3. **Security tooling in CI** — `govulncheck`, `gosec`, `golangci-lint` all run in CI.
4. **Charmhub client has sensible defaults** — 4 MiB API response limit, 64 MiB artifact limit, 30s HTTP timeout.
5. **Archive parsing has decompressed size limits** — 10 MiB per entry by default, with `io.LimitReader` overflow detection.
6. **Macaroon compatibility layer** — Clever approach to make `charmcraft login` work without a full bakery implementation.
7. **Dual-backend repo** — PostgreSQL for production, SQLite for lightweight/embedded deployments. The interface abstraction makes this clean.
8. **Structured logging throughout** — Consistent use of `slog` with context propagation.
9. **sqlc-generated queries** — Type-safe SQL with compile-time verification.
10. **Dockerfile uses distroless** — Minimal attack surface for the container image.

---

## Missing for Production Readiness

| Area | Status | Priority |
|---|---|---|
| TLS for API server | Missing | Blocking |
| Token hashing (salted/bcrypt) | Missing | Blocking |
| PG migration version tracking | Missing | Blocking |
| Request ID correlation | Missing | High |
| Rate limiting (beyond token limiter) | Partial — tokens only | Medium |
| Health check endpoint | Not found | High |
| Graceful degradation for OCI unavailability | Partial — `ensureOCIProvisioned` returns error | Medium |
| Database connection pool metrics | Missing | Medium |
| Prometheus metrics endpoint | Not found | Medium |
| Audit logging (who did what) | Partial — slog only | Medium |
| Upload garbage collection | Missing | Medium |
| Backup/restore documentation | Not found | High |
| Secret rotation (OCI robot credentials, auth signing key) | Not found | High |

---

## Summary of Recommendations

**Must fix before production:**
1. Document/fix encryption-at-rest scheme for robot credentials
2. Audit SQLite write paths for network I/O inside transactions; document SQLite as dev-only or fix contention
3. Add migration version tracking for PostgreSQL
4. Add or mandate TLS termination for the API server
5. Add startup warning for dev auth; add auth_method to audit context
6. Salt token hashes (use bcrypt/argon2id)

**Should fix soon:**
7. Add exponential backoff for sync failures
8. Add per-request timeouts with context propagation
9. Add `MaxBytesReader` for request body limits
10. Add request ID middleware
11. Add upload garbage collection
12. Add health check and metrics endpoints
13. Document backup/restore procedures
14. Add secret rotation documentation

---

*End of review.*
