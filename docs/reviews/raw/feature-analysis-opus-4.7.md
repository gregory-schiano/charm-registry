# Charm Registry Production-Readiness Gap Analysis

> Branch: `use-harbor` | Worktree: `/home/gschiano/.hermes/.worktrees/t_6c0f9236`
> API Version: 0.2.0 (OpenAPI 3.1.0)
> Stack: Go 1.25 / chi v5 / Postgres+SQLite / S3+filesystem / Docker distribution v3

---

## 1. Harbor/OCI Registry Integration

### What Exists
- **Embedded OCI registry**: `internal/oci/client.go` embeds Docker `distribution/v3` as an in-process registry with S3 or filesystem storage, custom auth middleware, and per-package robot account credentials (HMAC-derived, AES-GCM encrypted at rest).
  - `internal/oci/client.go:71-115` — New() creates the distribution app with storage driver, TLS transport, and auth middleware.
  - `internal/oci/client.go:400-427` — authMiddleware enforces Basic auth against robot accounts per OCI project.
  - `internal/oci/client.go:229-291` — MirrorImage() pulls a source image and pushes to the embedded registry.
  - `internal/oci/client.go:315-343` — DeleteImage() support for OCI resource cleanup.
  - `internal/oci/client.go:345-354` — DeletePackage() best-effort repository cleanup.
- **OCI service layer**: `internal/service/oci.go` — `ensureOCIProvisioned()`, `requireOCIPackageReady()` helpers.
- **OCI registry interface**: `internal/service/oci_registry.go` — typed interface for the OCI client.
- **OCI resource handling**: `internal/service/resources.go:125-141` — OCI-image resource type detection and digest extraction; `internal/service/resources.go:248-290` — OCI upload credential issuance; `internal/service/resources.go:387-418` — renderOCIImageBlob for Docker-style auth blob.
- **Charmhub OCI sync**: `internal/sync/reconcile.go:663-696` — `ensureCharmhubResourceRevision()` mirrors OCI images from upstream Charmhub.
- **Config**: `internal/config/config.go:49-65` — Full OCI config: listen address, internal URL, S3 backend, TLS cert/key, secret key, project/robot prefixes.

### What's Missing
1. **No Harbor integration at all** — The branch is named `use-harbor` but there is zero Harbor-specific code. No Harbor API client, no project/robot-account delegation to an external Harbor instance, no Harbor webhook support. The "OCI registry" is entirely the embedded Docker distribution/v3.
   - **Severity: Critical** — If Harbor was the intended production OCI backend, this is completely unimplemented.
2. **No OCI garbage collection** — The embedded distribution registry has no GC cron or manual trigger. Orphaned layers accumulate indefinitely.
   - **Severity: High** — Disk/bucket cost grows unboundedly over time.
3. **No OCI registry health check** — `/healthz` and `/readyz` only check Postgres and the main API. No liveness probe for the embedded OCI registry.
   - **Severity: Medium** — K8s/snap deployments can't detect OCI registry failure.
4. **No OCI image vulnerability scanning** — No integration with Trivy, Grype, or Harbor's built-in scanner.
   - **Severity: Medium** — Production registries need image scanning for compliance.
5. **No OCI catalog/list API** — No administrative endpoint to list all OCI repositories or tags in the registry.
   - **Severity: Low** — Operational inconvenience only.

---

## 2. TLS Configuration

### What Exists
- **OCI registry TLS**: `cmd/charm-registry/main.go:67-71` — The embedded OCI server supports `ListenAndServeTLS` when `OCITLSCertFile`/`OCITLSKeyFile` are set.
- **OCI internal client TLS**: `internal/oci/client.go:117-141` — `internalTransport()` builds a custom `http.Transport` that trusts the OCI TLS cert for internal calls.
- **Config validation**: `internal/config/config.go:383-396` — `validateOCIConfig` ensures cert and key are set together.
- **Snap self-signed certs**: `snap/hooks/configure:35-74` — `generate_cert()` creates self-signed x509 certs for both the API server and the OCI registry with configurable SANs.
- **Snap wrapper TLS**: `snap/local/charm-registry-wrapper:49-54` — Sets `CHARM_REGISTRY_TLS_CERT_FILE`/`KEY_FILE` when `tls.enabled=true`.
- **Deploy scripts**: `deploy/oci/generate-certs.sh` — Generates CA, server, and client certs for development; `deploy/k8s/install-oci-cert.sh` — Installs OCI registry cert as a K8s secret.

### What's Missing
1. **No TLS termination on the main API server** — `cmd/charm-registry/main.go:89` always calls `server.ListenAndServe()` (plain HTTP). The snap wrapper sets `CHARM_REGISTRY_TLS_CERT_FILE`/`KEY_FILE` env vars, but the `Config` struct and `main.go` never read or use them. The API server has no `ListenAndServeTLS` path.
   - **Severity: Critical** — The main API serves over plain HTTP in production. TLS termination must happen in an external reverse proxy (nginx/traefik) which is not documented or configured.
2. **No TLS config fields for the main API** — `internal/config/config.go:22-81` — The `Config` struct has `OCITLSCertFile`/`OCITLSKeyFile` but no `TLSCertFile`/`TLSKeyFile` for the main listener. The snap wrapper exports `CHARM_REGISTRY_TLS_CERT_FILE`/`CHARM_REGISTRY_TLS_KEY_FILE` but nothing reads them.
   - **Severity: Critical** — Directly blocks production TLS for the API.
3. **No automatic ACME/Let's Encrypt** — No auto-cert provisioning for public deployments.
   - **Severity: Medium** — Most production deployments would use a reverse proxy for this, but the gap should be documented.
4. **No TLS version/cipher suite configuration** — No env vars or config for minimum TLS version or cipher suites.
   - **Severity: Low** — Go's defaults are reasonable, but some compliance regimes require explicit configuration.
5. **compose.yaml has no TLS** — `compose.yaml` exposes only HTTP ports. No TLS-terminated proxy service.
   - **Severity: Medium** — Local dev is fine, but compose-based production deployments have no TLS guidance.

---

## 3. Snap Packaging

### What Exists
- **snap/snapcraft.yaml** — Complete snap definition: `core24` base, `strict` confinement, `devel` grade, two apps (`charm-registry` daemon, `charm-registryctl` CLI).
  - `snap/snapcraft.yaml:13-23` — Apps with `network`/`network-bind` plugs, daemon config, stop-timeout.
  - `snap/snapcraft.yaml:28-38` — Go plugin build with `CGO_ENABLED=0`, `GOFLAGS=-trimpath`.
- **snap/hooks/configure** — Auto-generates self-signed certs when `tls.enabled=true`, with SAN-aware regeneration.
- **snap/hooks/install** — Delegates to configure hook.
- **snap/local/charm-registry-wrapper** — Comprehensive shell wrapper that reads snap config keys via `snapctl get` and exports all `CHARM_REGISTRY_*` env vars. Defaults to SQLite + filesystem storage under `$SNAP_COMMON/data/`.
- **charm-registryctl CLI**: `cmd/charm-registryctl/main.go` — 284-line CLI tool supporting `sync list|add|remove|run` commands for Charmhub sync management. Read-only flag parsing, JSON API client, tabwriter output.

### What's Missing
1. **`charm-registryctl` limited scope** — `snap/snapcraft.yaml:23-25` correctly references `bin/charm-registryctl` and `cmd/charm-registryctl/main.go` exists (284 lines). However, the CLI only supports `sync list|add|remove|run` — no token, account, or package management. The snap build succeeds but the admin CLI covers minimal functionality.
   - **Severity: High** — Snap build works, but the admin CLI covers only sync management; all other admin operations require curl/httpie.
2. **`grade: devel`** — `snap/snapcraft.yaml:10` — Production snaps must use `grade: stable`.
   - **Severity: High** — Prevents publishing to stable channel.
3. **No snap data migration** — No post-refresh hook or version-aware migration for SQLite schema changes between snap revisions.
   - **Severity: High** — Snap refresh could break the database if schema changes.
4. **No snap store metadata** — No `icon.svg`, no screenshot, no `snap/gui/` directory for the Snap Store listing.
   - **Severity: Low** — Aesthetic only.
5. **Hardcoded `go/latest/stable` build snap** — `snap/snapcraft.yaml:35` — Should pin a specific major version for reproducibility.
   - **Severity: Medium** — Build reproducibility risk.
6. **No Prometheus/Grafana snap interfaces** — If observability is later added, the snap will need `content` plug/slot for metrics sharing.
   - **Severity: Low** — Future concern.

---

## 4. Juju/Charmcraft Compatibility

### What Exists
- **charm/charmcraft.yaml** — Full Juju charm definition: `ubuntu@24.04` base, `go-framework` extension, relations for PostgreSQL (`postgresql_client`), S3 (`s3` x2), OpenID (`oauth`), and OCI ingress (`ingress`).
  - `charm/charmcraft.yaml:31-50` — Requires/relations block.
  - `charm/charmcraft.yaml:89-141` — Config options matching the snap env vars.
- **charm/src/charm.py** — Pebble service layer that renders environment from Juju relations, manages TLS, and configures the service.
- **V1 Charmhub-compatible API endpoints**:
  - `internal/api/http.go:88-108` — Full v1 API surface: register, list, get, patch, delete packages; revisions, resources, releases, tracks; unscanned upload; OCI upload credentials.
  - `internal/api/http.go:109-113` — V2 endpoints: find, info, refresh (matching `juju find`, `juju info`, `juju refresh`).
  - `internal/api/http_libraries.go` — Libraries bulk endpoint returns empty list (charmcraft compatibility shim).
- **Charmhub sync**: `internal/sync/` — Full sync service that mirrors charms and resources from upstream Charmhub.
  - `internal/charmhub/client.go` — Charmhub API client supporting info, channel, refresh, and download.
  - `internal/sync/reconcile.go` — Reconciliation logic with idempotent revision/resource import, OCI image mirroring, and pruning.
- **Macaroon token support**: `internal/auth/macaroon.go` — Macaroon serialization/deserialization for charmcraft compatibility.

### What's Missing
1. **No v2/charms/find auth bypass** — Real Charmhub allows unauthenticated find/info for public charms. The current code requires identity on all v2 endpoints (`http.go:109-113`), which means anonymous `juju find` will fail with 401.
   - **Severity: Critical** — Breaks the primary Juju consumer workflow.
2. **No `/v2/charms/find` named charm filter** — `handleFind` searches all packages. Real Charmhub `find` supports type=charm/bundle filter and category filtering. Only type-agnostic search exists.
   - **Severity: Medium** — `juju find` works but may return unexpected results.
3. **No bundle support** — No `type=bundle` package handling. `registerPackage` defaults to `charm` type. No bundle-specific metadata or manifest parsing.
   - **Severity: Medium** — Bundle publishers cannot use this registry.
4. **No charm libraries support** — `handleLibrariesBulk` always returns empty. No library CRUD, no library versioning.
   - **Severity: Medium** — `charmcraft publish-lib` is not supported. Only the compatibility shim exists.
5. **No `/v1/charm/{name}/collaborators` endpoint** — Real Charmhub has collaborator management. The service layer has `CanViewPackage`/`CanManagePackage` via the repo but no API endpoint to add/remove collaborators.
   - **Severity: Medium** — Package ownership is sole-owner; no team management.
6. **No upload-id flow for charm revisions** — The v1 push-revision endpoint takes JSON body, not a multipart upload with `upload-id`. This differs from Charmhub's upload flow where charmcraft first creates an upload, then posts binary, then reviews.
   - **Severity: High** — `charmcraft upload` may not work without adaptation; the unscanned-upload endpoint exists but is a different path.
7. **No automatic charm review/approval** — `ReviewUpload` exists but seems to always approve. No lint/check step.
   - **Severity: Low** — Private registry may not need review.
8. **No `/v2/charms/refresh` anonymous support** — Juju models that haven't logged in need anonymous refresh. Currently requires identity.
   - **Severity: High** — Breaks Juju model refresh for non-logged-in users.
9. **OCI ingress relation requires `limit: 1`** but marked `optional: false` — `charm/charmcraft.yaml:84-87` — This means the charm cannot deploy without an ingress relation, which may not be desired in all deployments.
   - **Severity: Medium** — Over-constrained relation.

---

## 5. Auth & ACLs

### What Exists
- **OIDC authentication**: `internal/auth/auth.go:47-54` — OIDC provider discovery and ID token verification with configurable claims (`sub`, `preferred_username`, `name`, `email`).
- **Store token (macaroon) authentication**: `internal/auth/auth.go:89-101` — Store tokens are HMAC-SHA256 hashed, looked up in Postgres, and validated for revocation/expiry.
  - `internal/auth/macaroon.go` — Macaroon serialization/deserialization.
- **Insecure dev auth**: `internal/auth/auth.go:170-187` — `dev:subject:username` token format for local development.
- **Token CRUD**: `internal/service/tokens.go` — Full token lifecycle: issue, revoke, list, whoami, exchange, offline exchange, dashboard exchange.
- **Token rate limiting**: `internal/api/http.go:120-182` — Per-account rate limiter (5 tokens/minute).
- **Permission system**: `internal/service/helpers.go:18-134` — Hierarchical permission checks: `requireAuth`, `requirePermission`, `requirePackageView`, `requirePackageManage`, `enforceChannelRestriction`.
  - Admin bypass: `identity.Account.IsAdmin` grants all permissions.
  - Token-scoped permissions: `permPackageManage`, `permPackageView`, etc.
  - Token-scoped packages: `tokenAllowsPackage()`.
  - Token-scoped channels: `enforceChannelRestriction()`.
- **Security headers middleware**: `internal/api/http.go:203-212` — CSP, Referrer-Policy, X-Content-Type-Options, X-Frame-Options.

### What's Missing
1. **No RBAC/role model** — Only `isAdmin` boolean. No roles like "viewer", "publisher", "admin". All non-admin users have the same privilege level unless they hold a scoped token.
   - **Severity: High** — Cannot implement least-privilege access control for team-based deployments.
2. **No group/team management** — No groups, teams, or organizations. Package ownership is per-account only. No API to transfer ownership.
   - **Severity: High** — Cannot support team-based charm publishing workflows.
3. **No token auto-expiry** — Tokens have `ValidUntil` but the issue API appears to set a very long expiry (or none). No refresh token flow.
   - **Severity: Medium** — Long-lived tokens are a security risk; no rotation mechanism.
4. **No token audit log** — No log of which tokens were used when. Only issuance and revocation are tracked.
   - **Severity: Medium** — Cannot investigate compromised tokens.
5. **No OIDC refresh flow** — Only ID tokens are verified. No refresh token handling. OIDC sessions expire when the ID token expires.
   - **Severity: Medium** — Users must re-authenticate frequently.
6. **No CORS configuration** — No CORS middleware. Dashboard/web clients from different origins will be blocked.
   - **Severity: Medium** — Breaks web dashboard integration.
7. **No CSRF protection** — No CSRF tokens. State-changing endpoints accept simple POST without origin validation.
   - **Severity: Low** — Only relevant for browser-based clients; API is primarily used by CLI tools.
8. **No API key rotation** — No mechanism to rotate the OCI secret key or admin credentials without downtime.
   - **Severity: Medium** — Compromised credentials require service restart.

---

## 6. Observability

### What Exists
- **Structured logging**: `log/slog` used throughout the service layer (`internal/service/packages.go:66`, `internal/service/resources.go:148`, etc.) and API layer (`internal/api/http.go:219-228`).
- **Request logging middleware**: `internal/api/http.go:214-229` — Logs method, path, status, bytes, duration_ms, remote_addr with request ID.
- **Health endpoints**: `internal/api/http.go:66-67` — `/healthz` and `/readyz` endpoints (handlers not shown but referenced).
- **Security headers**: CSP, X-Frame-Options, etc.
- **OpenTelemetry in go.sum** — OTEL libraries are present as transitive dependencies (from the Docker distribution v3 library) but are NOT used directly by the charm-registry code.

### What's Missing
1. **No metrics endpoint** — No `/metrics` Prometheus endpoint. No request counter, latency histogram, error rate, or custom business metrics (packages, uploads, sync status).
   - **Severity: Critical** — Production services require metrics for alerting, capacity planning, and SLA monitoring.
2. **No distributed tracing** — No OpenTelemetry trace provider or span creation. Despite OTEL libraries being in go.sum as transitive deps, the application code creates zero spans.
   - **Severity: High** — Cannot debug latency issues across service boundaries (especially sync + OCI + S3 calls).
3. **No log level configuration** — `cmd/charm-registry/main.go:20` — Hardcoded `slog.NewTextHandler(os.Stdout, nil)` with default level. No env var to set log level.
   - **Severity: Medium** — Cannot increase/decrease verbosity at runtime.
4. **No structured error tracking** — No Sentry, no error aggregation. Internal errors are logged but not tracked.
   - **Severity: Medium** — Cannot monitor error trends.
5. **No pprof endpoint** — No `/debug/pprof` for runtime profiling.
   - **Severity: Low** — Useful for debugging but not critical.
6. **No sync progress metrics** — Charmhub sync logs individual operations but doesn't export sync duration, items synced, errors, or last-success timestamp as metrics.
   - **Severity: Medium** — Cannot monitor sync health without reading logs.

---

## 7. Backup/Restore

### What Exists
- Nothing. Zero backup or restore functionality exists in the codebase.

### What's Missing
1. **No database backup** — No `pg_dump` integration, no SQLite file copy, no WAL archiving, no point-in-time recovery.
   - **Severity: Critical** — Data loss on database failure. The only "backup" is manual ops intervention.
2. **No blob storage backup** — No S3 bucket versioning, no bucket replication, no snapshot workflow.
   - **Severity: Critical** — Charm artifacts and OCI images are lost on bucket failure.
3. **No restore procedure** — No documented or automated way to restore from backup.
   - **Severity: Critical** — Even if backups existed, there's no tested restore path.
4. **No export/import API** — No endpoint to dump all packages, revisions, resources, and releases to a portable format.
   - **Severity: High** — Cannot migrate between instances.
5. **No disaster recovery documentation** — No runbook for data recovery scenarios.
   - **Severity: High** — Operators have no guidance.
6. **No OCI registry backup** — The embedded distribution registry's storage (S3 prefix or filesystem directory) is not included in any backup strategy.
   - **Severity: High** — OCI images are lost on failure.

---

## 8. Admin UX/API

### What Exists
- **Admin endpoints**:
  - `GET /v1/admin/charmhub-sync` — List sync rules.
  - `POST /v1/admin/charmhub-sync` — Add sync rule.
  - `DELETE /v1/admin/charmhub-sync/{name}/{track}` — Remove sync rule.
  - `POST /v1/admin/charmhub-sync/{name}/run` — Trigger manual sync.
  - `GET /v1/tokens` — List tokens.
  - `POST /v1/tokens` — Issue token.
  - `POST /v1/tokens/revoke` — Revoke token.
  - `GET /v1/tokens/whoami` — Token identity.
  - `POST /v1/tokens/exchange` — Token exchange.
  - `POST /v1/tokens/offline/exchange` — Offline exchange.
  - `POST /v1/tokens/dashboard/exchange` — Dashboard exchange.
  - `GET /v1/whoami` — Current identity.
- **Package management**: Register, list, get, patch, delete, search packages.
- **Health endpoints**: `/healthz`, `/readyz`, root document `/`, `/openapi.yaml`, `/docs`.
- **Token issue rate limiting**: `internal/api/http.go:120-182` — In-memory rate limiter.

### What's Missing
1. **No admin account management** — No endpoint to list accounts, grant/revoke admin status, or manage user lifecycle. Admin status is determined by OIDC claims matching hardcoded env var lists (`AdminSubjects`, `AdminEmails`, `AdminUsernames`).
   - **Severity: Critical** — Admin management requires redeploying the service with new env vars.
2. **No package transfer** — No endpoint to transfer package ownership between accounts.
   - **Severity: High** — Cannot handle team member changes.
3. **No bulk operations** — No bulk package import/export, no bulk token creation, no bulk sync trigger.
   - **Severity: Medium** — Operational overhead for large deployments.
4. **No admin dashboard** — No web UI. Only API endpoints exist.
   - **Severity: Medium** — CLI-only administration is functional but less accessible.
5. **No audit log** — No record of admin actions (who triggered sync, who revoked a token, who changed package metadata). Only structured log entries.
   - **Severity: High** — Cannot investigate or audit admin actions.
6. **No configuration API** — No endpoint to view or modify running configuration. Must restart to change settings.
   - **Severity: Medium** — Common in production services.
7. **No version/deployment info endpoint** — The root document returns `service-name` and `version` but no build info, git commit, or deployment timestamp.
   - **Severity: Low** — Operational convenience.
8. **Limited `charm-registryctl` CLI** — `cmd/charm-registryctl/main.go` exists (284 lines) but only supports `sync list|add|remove|run`. No token management, account admin, package operations, or bulk import/export through the CLI.
   - **Severity: High** — Admin operations beyond sync management require `curl`/`httpie` with manual token management.

---

## Summary Table

| Domain | Critical | High | Medium | Low |
|--------|----------|------|--------|-----|
| 1. Harbor/OCI Registry | 1 | 1 | 2 | 1 |
| 2. TLS | 2 | 0 | 1 | 1 |
| 3. Snap Packaging | 0 | 3 | 1 | 1 |
| 4. Juju/Charmcraft | 1 | 2 | 3 | 1 |
| 5. Auth & ACLs | 0 | 2 | 4 | 1 |
| 6. Observability | 1 | 1 | 2 | 1 |
| 7. Backup/Restore | 3 | 2 | 0 | 0 |
| 8. Admin UX/API | 1 | 3 | 2 | 1 |
| **Total** | **9** | **14** | **15** | **7** |

### Top 5 Critical Gaps (Must-fix before production)

1. **No TLS on main API server** — `cmd/charm-registry/main.go:89` always uses plain HTTP. Config struct lacks `TLSCertFile`/`TLSKeyFile`.
2. **No Harbor integration** — Branch is named `use-harbor` but the OCI registry is entirely embedded Docker distribution. Zero Harbor client code.
3. **No anonymous v2/charms/find and refresh** — `internal/api/http.go:109-113` — All v2 endpoints require authentication, breaking `juju find` and anonymous `juju refresh`.
4. **No database/blob backup or restore** — Zero backup infrastructure. Data loss on any storage failure.
5. **No admin account management** — Admin list is env-var-only. No API to manage users or grant/revoke admin.

### Top 5 High Gaps (Should-fix soon)

1. **No OCI garbage collection** — Orphaned layers accumulate indefinitely.
2. **`snap/snapcraft.yaml` grade: devel** — Must be `stable` for production publishing; also charm-registryctl CLI only covers sync commands.
3. **No RBAC/role model** — Only `isAdmin` boolean. No team/group management.
4. **No upload-id flow for charmcraft** — `charmcraft upload` likely incompatible.
5. **No audit log** — Admin actions not recorded in queryable storage.
