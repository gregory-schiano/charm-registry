# Aligned Feature Roadmap — charm-registry `use-harbor` Branch

> **Branch:** `production-readiness-use-harbor` (commit 5119530)
> **Synthesized from:** GPT 5.5 (t_66b88a5e, 39 gaps), Claude Opus 4.7 (t_6c0f9236, 45 gaps), GLM 5.1 (t_c1f55f1d, 17 gaps)
> **Date:** 2026-06-07

---

## Methodology

Each gap from the three model analyses was normalized by domain and severity, then cross-referenced. Agreement levels:

- **3/3 agreed**: All three models identified the gap (or two explicitly + one implicitly by domain overlap)
- **2/3 agreed**: Two models identified; the third covered the domain but didn't flag this specific gap
- **Single-model, compelling**: Only one model flagged it, but the finding is verifiable from source
- **Rejected/needs reproduction**: Flagged by one model but not corroborated; may be speculative

Severity mapping across models:
| GPT 5.5 | Opus 4.7 | GLM 5.1 | Unified tier |
|---------|----------|---------|-------------|
| P0 | Critical | Critical | **Must-have** |
| P1 | High | High | **Should-have** |
| P2 | Medium/Low | Medium/Low | **Nice-to-have** |

---

## Must-Have Before Production

These gaps block any production deployment. They represent data-loss risk, security exposure, or broken core functionality.

### R1. Resolve Harbor vs. Embedded OCI Decision (3/3 agreed)

**The `use-harbor` branch contains zero Harbor-specific code.** The OCI layer is entirely an embedded CNCF Distribution v2 registry running in-process. No Harbor API client, no Harbor project lifecycle, no Harbor robot accounts, no Harbor replication/webhooks/scanning.

| Model | ID | Severity |
|-------|----|----------|
| GPT 5.5 | G1 | P0 |
| Opus 4.7 | §1.1 | Critical |
| GLM 5.1 | G1, G2, G3 | Critical |

**Decision required before any other Harbor-related work:**
- **Option A — Keep embedded Distribution v2**: Rename the branch, document that Harbor is out of scope, and harden the embedded registry (GC, health checks, cert rotation).
- **Option B — Integrate with external Harbor**: Rewrite the OCI client layer to delegate to Harbor's REST API (projects, robot accounts, replication, scanning, RBAC). This is a major effort (~entire `internal/oci/` rewrite).
- **Impact on roadmap**: Every other Harbor/OCI gap (GC, scanning, RBAC, replication, webhooks) depends on this decision. If Option A, many "Harbor-specific" gaps become inapplicable and are replaced by embedded-registry equivalents.

### R2. API Server TLS Termination (3/3 agreed)

The main API server (`cmd/charm-registry/main.go:89`) always uses `ListenAndServe()` — plain HTTP. The `Config` struct has `OCITLSCertFile`/`OCITLSKeyFile` but no API TLS fields. The snap wrapper sets `CHARM_REGISTRY_TLS_CERT_FILE`/`TLS_KEY_FILE` env vars but the Go app never reads them.

| Model | ID | Severity |
|-------|----|----------|
| GPT 5.5 | G9 | P1 → elevated to Must-have |
| Opus 4.7 | §2.1, §2.2 | Critical |
| GLM 5.1 | G9, G16 | Critical |

**Implementation path:** Add `TLSCertFile`/`TLSKeyFile` to Config, add `ListenAndServeTLS` path in `main.go`, wire snap wrapper env vars to the new config fields.

**Note:** GPT 5.5 rated this P1 (assuming reverse proxy), but Opus and GLM both rate it Critical because snap and compose deployments lack a reverse proxy. For production correctness, the Go app must support TLS directly.

### R3. Backup and Restore (3/3 agreed)

Zero backup infrastructure exists. No `pg_dump` integration, no S3 bucket versioning, no restore procedure, no migration rollback.

| Model | ID | Severity |
|-------|----|----------|
| GPT 5.5 | G44, G45, G46 | P0 |
| Opus 4.7 | §7.1-7.4 | Critical/High |

**Minimum deliverable:**
- Documented `pg_dump` + S3 bucket snapshot/restore procedure
- Forward-compatible migration strategy (or down-migration support)
- Tested restore runbook

### R4. Anonymous v2/charms/find and v2/charms/refresh (2/3 agreed, 3rd implicit)

Real Charmhub allows unauthenticated `find` and `refresh` for public charms. The current code requires identity on all v2 endpoints (`http.go:109-113`), breaking `juju find` and anonymous `juju refresh`.

| Model | ID | Severity |
|-------|----|----------|
| GPT 5.5 | G35 | P0 (no public package discovery) |
| Opus 4.7 | §4.1 | Critical |
| GLM 5.1 | — | (not in scope of Harbor+TLS analysis) |

**Implementation path:** Add public/anonymous read path for v2 find and refresh when the package is not private. Preserve auth requirement for private packages.

### R5. Prometheus Metrics Endpoint (3/3 agreed)

No `/metrics` endpoint. No request counters, latency histograms, error rates, or business metrics. Production services cannot be monitored.

| Model | ID | Severity |
|-------|----|----------|
| GPT 5.5 | G39 | P0 |
| Opus 4.7 | §6.1 | Critical |

**Implementation path:** Add `promhttp.Handler()` at `/metrics` with basic request rate, latency, and error counters. Prometheus client library is already a transitive dependency.

### R6. OCI Garbage Collection (3/3 agreed)

Deleted packages leave orphan blobs in S3/filesystem. `DeletePackage` only removes repository paths, not unreferenced layers.

| Model | ID | Severity |
|-------|----|----------|
| GPT 5.5 | G2 | P0 |
| Opus 4.7 | §1.2 | High → elevated to Must-have |

**Implementation path:** Add a GC cron or manual trigger that walks the storage driver's blob store and removes layers not referenced by any manifest. If Harbor is adopted (Option B), Harbor has built-in GC.

### R7. Admin Account Management (3/3 agreed)

Admin status is determined by OIDC claims matching hardcoded env var lists (`AdminSubjects`, `AdminEmails`, `AdminUsernames`). No API to list accounts, grant/revoke admin, or manage user lifecycle.

| Model | ID | Severity |
|-------|----|----------|
| GPT 5.5 | G51 | P1 → elevated |
| Opus 4.7 | §8.1 | Critical |

**Implementation path:** Add `/v1/admin/accounts` endpoints (list, grant/revoke admin). Add a bootstrapping mechanism for the first admin.

---

## Should-Have Soon After Production

These gaps are important for operational quality and security but don't block initial deployment if mitigated.

### S1. RBAC/Role Model (3/3 agreed)

Only `isAdmin` boolean. No roles like "viewer", "publisher", "editor". All non-admin users have the same privilege level unless they hold a scoped token.

| Model | ID | Severity |
|-------|----|----------|
| GPT 5.5 | G28 | P1 |
| Opus 4.7 | §5.1 | High |

### S2. Group/Team-Based ACL (3/3 agreed)

`account_groups` and `account_group_members` tables exist in the schema but are completely unimplemented. No code writes to or reads from them. No API to create/manage groups.

| Model | ID | Severity |
|-------|----|----------|
| GPT 5.5 | G34, G35, G36, G37, G38 | P0/P1 |
| Opus 4.7 | §5.2 | High |

**Note:** GPT 5.5 rates group ACL P0, but this is likely S1 (RBAC) dependency — groups without roles are incomplete. Should-have after RBAC is in place.

### S3. Audit Log (3/3 agreed)

No record of admin actions (who triggered sync, who revoked a token, who changed package metadata). Only structured log entries.

| Model | ID | Severity |
|-------|----|----------|
| GPT 5.5 | G30 | P1 |
| Opus 4.7 | §5.4, §8.5 | High |

### S4. Token Expiry Enforcement Verification (2/3 agreed)

`valid_until` exists on tokens but enforcement across all code paths needs verification. If tokens never expire, compromised tokens grant permanent access.

| Model | ID | Severity |
|-------|----|----------|
| GPT 5.5 | G26 | P0 → elevated concern |
| Opus 4.7 | §5.3 | Medium |

**Action:** Audit every bearer token validation path to confirm `valid_until` is checked. Add integration tests for expired token rejection.

### S5. Distributed Tracing (2/3 agreed)

No OpenTelemetry span creation. OTel libraries exist as transitive dependencies but the app creates zero spans. Cannot trace requests across API → service → DB → OCI.

| Model | ID | Severity |
|-------|----|----------|
| GPT 5.5 | G40 | P0 → Should-have (basic metrics first) |
| Opus 4.7 | §6.2 | High |

### S6. Snap Grade and Data Migration (2/3 agreed)

`snap/snapcraft.yaml` uses `grade: devel`, blocking stable channel publishing. No post-refresh hook or version-aware migration for SQLite schema changes.

| Model | ID | Severity |
|-------|----|----------|
| GPT 5.5 | G13, G14 | P1/P2 |
| Opus 4.7 | §3.2, §3.3 | High |

### S7. charmcraft Upload-ID Flow (2/3 agreed)

The v1 push-revision endpoint takes JSON body, not a multipart upload with `upload-id`. This differs from Charmhub's flow where charmcraft first creates an upload, then posts binary, then reviews.

| Model | ID | Severity |
|-------|----|----------|
| GPT 5.5 | — | (implicit in G19) |
| Opus 4.7 | §4.6 | High |

### S8. TLS Hardening Stack (2/3 agreed, GLM deep-dive)

Multiple TLS issues beyond the core API termination gap:
- Self-signed 10-year certs with no rotation (GLM G10)
- Private key at 0644 in `generate-certs.sh` (GLM G13)
- No certificate hot-reload (GLM G15, GPT G3)
- `name.Insecure` used for HTTP OCI internal URLs (GLM G17)
- S3DisableTLS shared between blob and OCI storage (GLM G8)
- No TLS for database connections (GLM G14)
- Compose insecure defaults: dev auth on, placeholder secrets, sslmode=disable (GLM G12)

| Model | IDs |
|-------|-----|
| GPT 5.5 | G3 (cert rotation P0), G10-G12 (P2) |
| GLM 5.1 | G10-G17 (9 High, 5 Medium) |

### S9. Robot Credential Rotation (2/3 agreed)

OCI robot credentials are deterministic HMAC-derived from a static `OCISecretKey`. Changing the key breaks all existing credentials with no migration path.

| Model | ID | Severity |
|-------|----|----------|
| GPT 5.5 | G4 | P0 → Should-have (mitigated by keeping key secret) |
| GLM 5.1 | G2 | Critical (Harbor context) |

### S10. Readiness Check Depth (2/3 agreed)

`CheckReady` only verifies DB connectivity, not S3/bucket or OCI storage driver health. K8s/snap deployments can't detect OCI or S3 failure.

| Model | ID | Severity |
|-------|----|----------|
| GPT 5.5 | G41 | P1 |
| Opus 4.7 | §1.3 | Medium |

### S11. Admin CLI Expansion (2/3 agreed)

`charm-registryctl` only supports `sync list|add|remove|run`. Token management, account admin, package operations, and bulk import/export require `curl`/`httpie`.

| Model | ID | Severity |
|-------|----|----------|
| GPT 5.5 | G55 | P1 |
| Opus 4.7 | §3.1, §8.8 | High |

---

## Nice-to-Have / Explicitly Out of Scope

These improve quality of life but are not production blockers.

### N1. Charm Library Hosting (2/3 agreed)

`/v1/charm/libraries/bulk` returns empty list. `handleLibrariesBulk` is a stub. Library CRUD and versioning not implemented.

| Model | ID | Severity |
|-------|----|----------|
| GPT 5.5 | G17 | P0 → Nice-to-have (private registry, library hosting is secondary) |
| Opus 4.7 | §4.4 | Medium |

**Rationale for de-prioritization:** Library hosting is a Charmhub feature for public charm ecosystem sharing. A private registry's primary workflow is charm publish/consume. Libraries can be vendored or shared via git.

### N2. Bundle Support (2/3 agreed)

No `type=bundle` package handling. No bundle-specific metadata or manifest parsing.

| Model | ID | Severity |
|-------|----|----------|
| GPT 5.5 | G18 | P0 → Nice-to-have |
| Opus 4.7 | §4.3 | Medium |

**Rationale:** Bundles are deprecated in favor of Scopes in modern Juju. Low priority for a private registry.

### N3. Vulnerability Scanning Integration (2/3 agreed)

No integration with Trivy, Grype, or Harbor's built-in scanner.

| Model | ID | Severity |
|-------|----|----------|
| GPT 5.5 | G8 | P1 |
| Opus 4.7 | §1.4 | Medium |
| GLM 5.1 | G5 | High (Harbor context) |

**Rationale:** Important for compliance but can be run externally (Trivy CLI) against OCI images. If Harbor is adopted (Option B), this comes for free.

### N4. Harbor Replication Policies / Webhooks (1/3 agreed, Harbor-dependent)

Only GLM 5.1 flagged these explicitly since they're Harbor-specific features. Entirely moot if embedded OCI is kept.

| Model | ID | Severity |
|-------|----|----------|
| GLM 5.1 | G4, G6 | High |

### N5. mTLS (1/3 agreed)

No mutual TLS anywhere in the stack.

| Model | ID | Severity |
|-------|----|----------|
| GLM 5.1 | G11 | High |

**Rationale:** mTLS is valuable for zero-trust deployments but can be handled at the infrastructure level (service mesh, reverse proxy). Not a Go-app concern initially.

### N6. HSTS Header (1/3 agreed)

No `Strict-Transport-Security` header.

| Model | ID | Severity |
|-------|----|----------|
| GPT 5.5 | G12 | P2 |

### N7. Progressive Rollout / Phasing (1/3 agreed, single-model)

`releases` table has `progressive` and `expiration_date` columns but no API to configure progressive rollout.

| Model | ID | Severity |
|-------|----|----------|
| GPT 5.5 | G22 | P1 → Nice-to-have (single-model, not verified) |

### N8. ARM64 Support (1/3 agreed)

Only `amd64` platform enabled in snapcraft.yaml.

| Model | ID | Severity |
|-------|----|----------|
| GPT 5.5 | G16 | P2 |

### N9. pprof Endpoint (1/3 agreed)

No `/debug/pprof` for runtime profiling.

| Model | ID | Severity |
|-------|----|----------|
| Opus 4.7 | §6.5 | Low |

### N10. API Pagination (1/3 agreed)

List endpoints return all results; no `?page=&limit=` support.

| Model | ID | Severity |
|-------|----|----------|
| GPT 5.5 | G58 | P2 |

### N11. Log Level Configuration (2/3 agreed)

Log level is hardcoded. No env var to set verbosity at runtime.

| Model | ID | Severity |
|-------|----|----------|
| GPT 5.5 | G43 | P1 |
| Opus 4.7 | §6.3 | Medium |

### N12. CORS Configuration (1/3 agreed)

No CORS middleware. Dashboard/web clients from different origins will be blocked.

| Model | ID | Severity |
|-------|----|----------|
| Opus 4.7 | §5.6 | Medium |

### N13. API Rate Limiting (1/3 agreed)

Only token issue has rate limiting. No general API rate limiting.

| Model | ID | Severity |
|-------|----|----------|
| GPT 5.5 | G57 | P2 |

### N14. Structured Error Tracking / Sentry (2/3 agreed)

No error aggregation beyond slog.

| Model | ID | Severity |
|-------|----|----------|
| GPT 5.5 | G42 | P1 |
| Opus 4.7 | §6.4 | Medium |

### N15. Sync Error Recovery (1/3 agreed, single-model)

Charmhub sync fails silently on individual charm errors; no retry with backoff, no dead-letter queue.

| Model | ID | Severity |
|-------|----|----------|
| GPT 5.5 | G24 | P1 → Nice-to-have (single-model) |

---

## Model Disagreements

| Gap | GPT 5.5 | Opus 4.7 | GLM 5.1 | Resolution |
|-----|---------|----------|---------|------------|
| API TLS severity | P1 (assumes reverse proxy) | Critical | Critical | **Must-have** — snap/compose have no reverse proxy |
| Token expiry enforcement | P0 | Medium | — | **Should-have** — audit needed, but mitigated by short token TTL defaults |
| OCI cert rotation | P0 | — | High | **Should-have** — real risk but mitigated by cert-manager/infra in K8s |
| Group ACL | P0 | High | — | **Should-have** — depends on RBAC (S1) first |
| Charm libraries | P0 | Medium | — | **Nice-to-have** — de-prioritized for private registry |
| Bundle support | P0 | Medium | — | **Nice-to-have** — bundles declining in relevance |
| Robot credential rotation | P0 | — | Critical | **Should-have** — mitigated by key secrecy; full rotation needs Harbor decision |
| S3DisableTLS coupling | — | — | Medium | **Should-have** — verified in source, real security concern |
| Private key 0644 | — | — | Medium | **Must-have subset** — quick fix, must be 0600 in all modes |
| name.Insecure usage | — | — | Medium | **Should-have** — forces HTTPS internal URL in production |

---

## Harbor / TLS / Snap Impact Matrix

This matrix shows how each roadmap item impacts the three critical deployment paths: Harbor (if adopted), TLS, and Snap.

| Roadmap Item | Harbor Impact | TLS Impact | Snap Impact |
|---|---|---|---|
| R1. Harbor decision | **Critical** — determines entire OCI architecture | Harbor handles TLS termination for OCI | Snap deployment may need external Harbor or embedded |
| R2. API TLS | If Harbor reverse-proxies, less critical | **Critical** — API must support TLS | **Critical** — snap sets TLS env vars Go doesn't read |
| R3. Backup/restore | Harbor has its own backup story | — | Snap needs pre-backup/post-restore hooks |
| R4. Anonymous v2/find | Harbor RBAC can gate anonymous access | — | — |
| R5. Prometheus metrics | Harbor exposes own metrics | — | Snap needs metrics interface |
| R6. OCI GC | Harbor has built-in GC | — | Snap needs GC cron job |
| S1. RBAC | Harbor has project-level RBAC | — | — |
| S2. Group ACL | Harbor group mapping can sync | — | — |
| S8. TLS hardening | Harbor can terminate mTLS | **Critical** — cert rotation, key perms, DB TLS, compose defaults | Snap cert generation scripts need overhaul |
| S9. Credential rotation | Harbor robot accounts solve this | — | — |

---

## Compatibility Validation Checklist — Stock `juju` and `charmcraft`

These are the concrete compatibility gates that must pass for stock Juju and charmcraft CLI tools to work against this registry.

### juju find

- [ ] `GET /v2/charms/find` returns results **without authentication** for public/non-private charms
- [ ] Response format matches Charmhub v2 find response schema (name, channel, etc.)
- [ ] Named charm filter works (type=charm, possibly type=bundle — N2)
- [ ] Category/keyword filtering works
- [ ] Pagination works for large result sets (N10)

### juju info

- [ ] `GET /v2/charms/info/{name}` returns charm metadata **without authentication** for public charms
- [ ] Response includes channels, revision, bases, and resource info matching Charmhub format
- [ ] Private charms return 404 (not 401, to avoid information leakage)

### juju refresh

- [ ] `POST /v2/charms/refresh` works with **anonymous** requests for public charms
- [ ] `POST /v2/charms/refresh` works with **macaroon/bakery** tokens for private charms
- [ ] Macaroon token format is pymacaroons-compatible (existing implementation)
- [ ] Refresh returns correct resource info including OCI image references
- [ ] Docker auth blob for OCI resources is generated correctly (existing code)
- [ ] Base constraint matching works ( Juju model bases vs charm bases )

### charmcraft login / register

- [ ] `POST /v1/tokens/exchange` or `/v1/tokens/dashboard/exchange` works with charmcraft's OIDC flow
- [ ] `POST /v1/charm` (register) works with charmcraft's auth headers
- [ ] Token scoping (package, channel, permissions) is enforced

### charmcraft upload / publish

- [ ] `POST /v1/charm/{name}/revisions` accepts charm archives in charmcraft's expected format
- [ ] **Upload-ID flow** (S7): charmcraft may use a two-step upload-id + binary post; current single-step endpoint may need adaptation
- [ ] `GET /v1/charm/{name}/revisions/review` returns revision status
- [ ] `POST /v1/charm/{name}/releases` creates releases with channel/track/base
- [ ] Resource upload via OCI image credentials works end-to-end
- [ ] Charm icon/media upload (GPT G20) — verify if charmcraft sends these

### charmcraft publish-lib

- [ ] `/v1/charm/libraries/bulk` returns actual libraries, not empty list (N1)
- [ ] Library CRUD endpoints exist (create, update, list versions)
- [ ] **Status:** Currently a stub — requires full implementation (Nice-to-have)

### charmcraft upload-resource

- [ ] `POST /v1/charm/{name}/resources` accepts resource uploads
- [ ] OCI image resource type: Docker auth blob is returned for registry push
- [ ] File resource type: direct upload works
- [ ] Resource revision tracking is correct

### juju deploy (implied by refresh)

- [ ] `POST /v2/charms/refresh` is the primary endpoint; no separate `/v2/charms/deploy` exists
- [ ] Download URL (`GET /api/v1/charms/download/{filename}`) returns the charm archive
- [ ] Download requires auth for private charms but works with macaroon token

### Charm (Juju) deployment path

- [ ] `charm/charmcraft.yaml` relations are complete: postgresql, s3, oci-s3, openid, ingress
- [ ] Pebble service layer renders correct environment from Juju relations
- [ ] OCI ingress relation works (or is optional if ingress not required)
- [ ] Tracing relation (commented out in charmcraft.yaml) — Nice-to-have

---

## Implementation Order Recommendation

Based on dependency analysis and blast radius:

1. **R1 — Harbor decision** (blocks all Harbor-dependent work)
2. **R2 — API TLS** (security gate, enables all HTTPS-dependent testing)
3. **R3 — Backup/restore** (data safety gate)
4. **R4 — Anonymous v2/find** (breaks core Juju workflow)
5. **R5 — Prometheus metrics** (enables operational visibility)
6. **R6 — OCI GC** (prevents storage cost runaway)
7. **R7 — Admin account management** (enables operational management)
8. **S4 — Token expiry audit** (security hardening)
9. **S8 — TLS hardening** (cert rotation, key perms, compose defaults, DB TLS)
10. **S1 — RBAC** (foundation for S2)
11. **S2 — Group ACL** (depends on S1)
12. **S3 — Audit log** (compliance)
13. **S7 — Upload-ID flow** (charmcraft compatibility)

---

## Summary Statistics

| Tier | Count | Key Themes |
|------|-------|------------|
| **Must-have** | 7 | Harbor decision, API TLS, backup/restore, anonymous v2, Prometheus, OCI GC, admin mgmt |
| **Should-have** | 11 | RBAC, group ACL, audit log, token expiry, tracing, snap grade, upload-id, TLS hardening, credential rotation, readiness depth, admin CLI |
| **Nice-to-have** | 15 | Libraries, bundles, scanning, Harbor replication/webhooks, mTLS, HSTS, progressive rollout, ARM64, pprof, pagination, log level, CORS, rate limiting, Sentry, sync retry |
| **Total unique gaps** | 33 | Deduplicated from 101 total findings across 3 models |

### Agreement levels across must-have items

| Item | 3/3 | 2/3 | Single-model |
|------|-----|-----|-------------|
| R1 Harbor decision | ✓ | | |
| R2 API TLS | | ✓ (Opus+GLM Critical, GPT P1) | |
| R3 Backup/restore | ✓ | | |
| R4 Anonymous v2 | | ✓ (GPT+Opus) | |
| R5 Prometheus | ✓ | | |
| R6 OCI GC | ✓ | | |
| R7 Admin mgmt | | ✓ (Opus Critical, GPT P1) | |
