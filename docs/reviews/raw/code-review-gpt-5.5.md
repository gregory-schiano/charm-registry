# Code Review: charm-registry `use-harbor` branch

**Commit:** `5119530 docs: add use-harbor production readiness plan`
**Reviewer:** GPT-5.5 (automated, read-only)
**Date:** 2026-06-07

---

## Summary

The `charm-registry` is a Go-based charm/OCI package registry service that integrates with Harbor (or a Harbor-compatible OCI distribution registry) for OCI image storage. The codebase is well-structured, follows Go conventions, and has reasonable test coverage. The Harbor integration (OCI client, robot credential management, encryption at rest, auth middleware) is solid. Below are findings grouped by severity.

---

## CRITICAL

### C1. Hardcoded database credentials in compose.yaml

**File:** `compose.yaml:130`
```
CHARM_REGISTRY_DATABASE_URL: "postgres://postgres:postgres@postgres:5432/charm_registry?sslmode=disable"
```
The database URL embeds the password literally instead of using an environment variable substitution like `${CHARM_REGISTRY_DATABASE_URL}`. This is inconsistent with all other credentials in the same file which use `${VAR:-default}` syntax. While this is a dev compose file, the `sslmode=disable` also means no TLS to the database even in production-like setups.

**Recommendation:** Use `${CHARM_REGISTRY_DATABASE_URL:-postgres://postgres:postgres@...}` like all other env vars in compose.yaml.

### C2. Robot secrets encrypted with a derived key from a single env var — key rotation is impossible without data loss

**Files:** `internal/oci/client.go:536-542`, `internal/oci/client.go:544-583`

The `deriveKey` function uses PBKDF2 with a **hardcoded salt** (`keyDerivationSalt` constant) and a fixed iteration count to derive the AES-256-GCM encryption key from `CHARM_REGISTRY_OCI_SECRET_KEY`. If that key is ever rotated, **all previously encrypted robot secrets become undecryptable** — there is no key versioning, no key-wrapping, and no re-encryption path.

Additionally, the PBKDF2 uses SHA-256 as the PRF. While SHA-256 PBKDF2 is acceptable, the hardcoded salt means the derived key is deterministic for a given secret — violating NIST SP 800-132 guidance that salts must be random and unique per derivation.

**Recommendation:**
- Add a key version identifier stored alongside the encrypted secret (e.g., `v1:base64...`).
- Provide a key-rotation command in `charm-registryctl` that re-encrypts all secrets with a new key.
- Generate a random salt at install time and persist it alongside the key, or derive the key differently per package.

---

## HIGH

### H1. Duplicate OCI helper functions across two packages

**Files:** `internal/service/oci.go` and `internal/sync/oci.go`

The following four functions are **byte-for-byte identical** between the two packages:
- `packagesEqualForOCI`
- `ociPackageProvisioned`
- `robotCredentialReady`
- `robotEqual`
- `timePtrEqual`

Both packages also have `ensureOCIProvisioned` with nearly identical logic (the service version returns a `newError` with `ErrorKindConflict`; the sync version returns a `newError` from the `registrysync` package).

This is a maintenance trap: any bug fix or behavior change in one copy must be replicated in the other.

**Recommendation:** Extract these into `internal/core/oci_helpers.go` or a shared `internal/oci/helpers.go` package.

### H2. No general API rate limiting — only token issuance is rate-limited

**File:** `internal/api/http.go:120-161`

The `tokenIssueLimiter` only gates token issuance (5 per minute per key). All other API endpoints — including push revision, push resource, register package, release, and OCI blob upload — have **no rate limiting at all**. This makes the API vulnerable to:
- Brute-force token guessing
- Resource exhaustion via repeated uploads
- Denial-of-service through expensive operations (e.g., repeated charm parsing)

**Recommendation:** Add a general-purpose per-IP or per-account rate limiter middleware (chi has `throttle` or `ratelimit` middleware, or use `golang.org/x/time/rate`).

### H3. `SearchPackages` ILIKE query without trigram index

**File:** `internal/repo/queries/packages.sql:133`
```sql
WHERE p.name ILIKE $1::text ESCAPE '\'
```

This cannot use a standard B-tree index. For even moderate row counts, this degrades to a sequential scan. The `SearchPackages` query is used by the `find` and `list` endpoints which are likely hot paths.

**Recommendation:** Create a GIN index with `pg_trgm`:
```sql
CREATE EXTENSION IF NOT EXISTS pg_trgm;
CREATE INDEX idx_packages_name_trgm ON packages USING gin (name gin_trgm_ops);
```
Then use `%` pattern matching with `ILIKE`, or switch to full-text search.

### H4. `compose.yaml` enables insecure dev auth by default

**File:** `compose.yaml:146`
```
CHARM_REGISTRY_ENABLE_INSECURE_DEV_AUTH: "${CHARM_REGISTRY_ENABLE_INSECURE_DEV_AUTH:-true}"
```

The default is `true`, meaning anyone running `docker compose up` gets a registry that accepts any identity without OIDC verification. While documented as dev-only, the default makes it easy to accidentally deploy with insecure auth enabled.

**Recommendation:** Default to `false`. Make the dev experience opt-in via an explicit override or a separate `compose.dev.yaml` override file.

---

## MEDIUM

### M1. Config struct is very large (~550 lines) — no separation of concerns

**File:** `internal/config/config.go`

The `Config` struct contains 40+ fields spanning API, OCI, S3, OIDC, server timeouts, Charmhub, admin subjects, etc. — all in one flat struct. This makes it hard to reason about which parts of the config are relevant to which subsystem, and it makes testing harder (every test needs the full config even if it only cares about one section).

**Recommendation:** Group related fields into sub-structs:
```go
type Config struct {
    API    APIConfig
    OCI    OCIConfig
    S3     S3Config
    OIDC   OIDCConfig
    Server ServerConfig
    // ...
}
```

### M2. Releases table UNIQUE constraint on (package_id, channel) is too narrow

**File:** `internal/repo/migrations/0001_init.sql:155`
```sql
UNIQUE (package_id, channel)
```

A package can only have one release per channel (e.g., `latest/stable`). The sync code works around this by encoding the base variant into the channel name (e.g., `latest/stable/ubuntu@22.04:amd64`). This is fragile — if the channel naming convention ever changes, the constraint will cause unexpected conflicts or silent overwrites via `ReplaceRelease`.

**Recommendation:** Either:
- Add `base` to the unique constraint: `UNIQUE (package_id, channel, base)`, or
- Document the convention clearly with a CHECK constraint or comment.

### M3. Content-Disposition header vulnerable to filename injection

**File:** `internal/api/http.go:268`
```go
w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
```

If `filename` contains a `"` character or newline, this breaks the header. While filenames are currently derived from internal IDs (low risk), this is a classic header injection vector.

**Recommendation:** Sanitize the filename or use RFC 5987 encoding:
```go
safe := strings.Map(func(r rune) rune {
    if unicode.IsPrint(r) && r != '"' && r != '\\' { return r }
    return '_'
}, filename)
```

### M4. No structured logging for audit trail

The codebase uses `slog` throughout, which is good, but there are no structured audit log entries for security-relevant operations like:
- Token issuance / revocation
- Package registration / deletion
- Admin role assignment
- OCI robot credential creation

The `slog.InfoContext` calls log general operations but don't follow a consistent audit schema.

**Recommendation:** Define an audit log helper that emits a consistent structured format with fields like `action`, `actor`, `target`, `outcome`, `timestamp`. This is important for compliance and incident response.

### M5. Memory backend doesn't store OCI fields — tests may not catch OCI regressions

**File:** `internal/repo/memory.go`

The `Memory` backend stores packages in a flat map but doesn't handle OCI-specific fields (`OCIProject`, `OCIPushRobot`, `OCIPullRobot`, `OCISyncedAt`) in `CreatePackage`/`UpdatePackage`. Looking at the code, the in-memory `UpdatePackage` simply replaces the entire struct, so OCI fields would be preserved on update. But `CreatePackage` stores the full struct, so it does work. However, the `testutil/oci_registry.go` stub sets `EncryptedSecret: "push"` (plaintext), which means test code path doesn't exercise the actual encrypt/decrypt round-trip.

**Recommendation:** Ensure test utilities use real encryption/decryption where possible, or add explicit OCI-field tests to the memory backend.

### M6. `writeJSON` silently swallows encoding errors

**File:** `internal/api/http.go:256`
```go
_ = json.NewEncoder(w).Encode(payload)
```

If JSON encoding fails (e.g., due to a channel value that can't be marshalled), the error is silently ignored. The response may be partially written or have an incorrect Content-Length.

**Recommendation:** Log the error at minimum:
```go
if err := json.NewEncoder(w).Encode(payload); err != nil {
    slog.ErrorContext(r.Context(), "json encode", "error", err)
}
```

---

## LOW

### L1. `docker compose up` creates an external network — no `docker network create` documented

**File:** `compose.yaml:178-180`
```yaml
networks:
  shared:
    external: true
    name: charm-registry-shared
```

The compose file references an external network that must be created manually. There's no documentation or script to set this up. Running `docker compose up` without creating the network first will fail with a confusing error.

**Recommendation:** Add a Makefile target or a note in README/dev docs.

### L2. Migration 0003 uses `ADD COLUMN IF NOT EXISTS` — idempotent but masks errors

**File:** `internal/repo/migrations/0003_oci.sql`

Using `ADD COLUMN IF NOT EXISTS` means if a migration is run twice, it silently succeeds. While this is convenient, it can mask partial migration failures where a column was added but downstream schema changes weren't applied.

**Recommendation:** Acceptable for this project size, but consider using a proper migration runner (golang-migrate, goose) that tracks applied migrations.

### L3. `package_acl` table is defined but never populated by the service layer

**File:** `internal/repo/migrations/0001_init.sql:54-60`

The `package_acl` table and the `CanViewPackage`/`CanManagePackage` queries support group-based ACL, but the service layer (`internal/service/packages.go`) only checks `OwnerAccountID` and `IsAdmin`. The ACL table is never written to.

**Recommendation:** Either implement the ACL write path or remove the table/queries to avoid confusion. If planned for future, add a `// TODO:` comment.

### L4. Test utilities mock OCI registry with plaintext secrets

**File:** `internal/testutil/oci_registry.go:21-24`
```go
pkg.OCIPushRobot = &core.RobotCredential{ID: 1, Username: "robot$push-" + pkg.ID, EncryptedSecret: "push"}
pkg.OCIPullRobot = &core.RobotCredential{ID: 2, Username: "robot$pull-" + pkg.ID, EncryptedSecret: "pull"}
```

The `EncryptedSecret` field is set to the literal string `"push"`/`"pull"`, which would fail if ever passed to the real `decrypt()` function. This works because the test OCI registry stub doesn't call decrypt, but it means the test infrastructure is inconsistent with production.

**Recommendation:** Use `encrypt("push", testKey)` or at minimum add a comment that this is intentionally plaintext for the stub.

### L5. `Dockerfile` copies binary from `/build` but doesn't declare it as a stage

**File:** `Dockerfile`

The Dockerfile uses a two-step pattern (build in a builder image, copy the binary), but it's a single-stage build. Using multi-stage `FROM ... AS builder` would make the intent clearer and reduce final image size.

### L6. No `CHANGELOG.md` or version tagging visible in the codebase

There's no version constant in the codebase and no CHANGELOG. The `rootDocumentResponse` has a `Version` field but it's not clear what value it returns.

**Recommendation:** Add a `version` package or inject the version at build time via `-ldflags`.

---

## POSITIVE OBSERVATIONS

1. **Security headers are well-implemented** — CSP, Referrer-Policy, X-Content-Type-Options, X-Frame-Options are set via middleware on every response (`http.go:203-212`).

2. **AES-256-GCM encryption with random nonce** for robot secrets is correct — authenticated encryption prevents tampering and the random nonce prevents nonce-reuse attacks.

3. **Token issue rate limiting** is implemented correctly with a sliding window approach and proper cleanup.

4. **The service layer properly validates identity and authorization** before any mutation — all routes use `requireIdentity` middleware.

5. **OCI auth middleware enforces package-scoped access** — pull robots can only pull, push robots can push, and cross-package access is denied. Good test coverage for this in `client_test.go`.

6. **The in-memory repository** is a well-structured test double that mirrors the PostgreSQL contract, enabling unit testing without a database.

7. **`MaxBytesReader` on JSON bodies** and **`DisallowUnknownFields`** on JSON decoders prevent several classes of DoS and unexpected-input bugs.

8. **Container security** — `cap_drop: ALL`, `read_only: true`, `no-new-privileges`, and `tmpfs: /tmp` in compose.yaml are excellent security defaults.

---

## STATISTICS

| Metric | Value |
|--------|-------|
| Go source files reviewed | ~50 |
| Total lines read | ~15,000+ |
| Test files | 21 |
| Critical findings | 2 |
| High findings | 4 |
| Medium findings | 6 |
| Low findings | 6 |
