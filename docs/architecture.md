# Architecture

Charm Registry is a single Go binary that runs two HTTP servers: a Charmhub-compatible API server and an embedded OCI Distribution v2 registry.

## Process layout

```
┌─────────────────────────────────────────────────────┐
│                  charm-registry                      │
│                                                      │
│  ┌──────────────┐         ┌───────────────────────┐ │
│  │  API server   │         │  OCI registry server   │ │
│  │  :8080        │         │  :5000 (HTTPS)         │ │
│  │  chi router   │         │  CNCF Distribution v2  │ │
│  └──────┬───────┘         └───────────┬───────────┘ │
│         │                             │              │
│  ┌──────▼─────────────────────────────▼───────────┐ │
│  │                Service layer                    │ │
│  │  Packages / Revisions / Releases / Resources    │ │
│  │  Tokens / Auth / OCI provisioning / Sync        │ │
│  └──────┬─────────────────────────────┬───────────┘ │
│         │                             │              │
│  ┌──────▼──────┐  ┌──────────────┐   ┌▼────────────┐ │
│  │  Repository  │  │  Blob store   │   │  OCI backend│ │
│  │  Postgres /  │  │  S3 / FS     │   │  S3 / FS    │ │
│  │  SQLite      │  │              │   │             │ │
│  └─────────────┘  └──────────────┘   └─────────────┘ │
└─────────────────────────────────────────────────────┘
```

## Package layout

| Package | Purpose |
|---------|---------|
| `cmd/charm-registry` | Process entrypoint, signal handling, server lifecycle |
| `cmd/charm-registryctl` | Admin CLI (sync rules only) |
| `internal/api` | HTTP handlers, chi router, response shaping, OpenAPI spec |
| `internal/app` | Dependency wiring (repos, blob store, OCI, auth, service) |
| `internal/service` | Business logic: packages, revisions, releases, resources, tokens, OCI, sync |
| `internal/repo` | Postgres (sqlc-generated), SQLite, and in-memory repositories |
| `internal/repo/db` | sqlc-generated query code (excluded from lint/security scans) |
| `internal/blob` | S3-compatible and filesystem blob stores |
| `internal/auth` | OIDC, macaroon, and store-token authentication |
| `internal/charm` | Charm archive parsing and metadata extraction |
| `internal/charmhub` | Upstream Charmhub API client (used by the sync worker) |
| `internal/oci` | Embedded OCI Distribution v2 registry backend |
| `internal/config` | Environment-driven configuration with validation |

## Data flow

### Publishing a charm (charmcraft → registry)

1. charmcraft authenticates via OIDC, receives a store token
2. `POST /v1/charm` registers the package (or returns the existing one)
3. `POST /unscanned-upload/` uploads the charm archive
4. `POST /v1/charm/{name}/revisions` creates a revision from the upload
5. `POST /v1/charm/{name}/releases` releases the revision to a channel/track
6. The service extracts metadata from the archive, stores the blob in S3/filesystem, and persists metadata in Postgres/SQLite
7. If the charm has OCI image resources, the service provisions OCI project space and credentials in the embedded registry

### Consuming a charm (juju → registry)

1. `juju` calls `POST /v2/charms/refresh` with the charm name, channel, and current revision
2. The service resolves the latest revision for the requested channel/base/arch
3. The response includes download URLs for the charm archive and any resources
4. OCI image resources include pull credentials for the embedded OCI registry

### Charmhub synchronization (admin → registry → Charmhub)

1. Admin creates a sync rule via CLI or API: `sync add postgresql-k8s --track 14`
2. The service enqueues an immediate reconciliation
3. The sync worker fetches the latest release info from Charmhub
4. For each matching base/arch variant, the worker downloads the charm archive and resources
5. Artifacts are stored as registry-owned packages with `authority=charmhub`
6. OCI images from upstream are mirrored into the embedded registry
7. The worker re-scans at `CHARM_REGISTRY_CHARMHUB_SYNC_INTERVAL` (default: 15 minutes)

## Embedded OCI registry

The registry embeds a CNCF Distribution v2 registry in-process. This is **not** Harbor — it is the reference OCI Distribution implementation configured with S3 or filesystem storage.

Key behaviors:

- Each charm package gets an OCI project (e.g., `charm/<package-name>`)
- Per-package robot accounts are created for push and pull operations
- Credentials are encrypted at rest with `CHARM_REGISTRY_OCI_SECRET_KEY`
- The OCI listener runs HTTPS by default (charmcraft requires TLS for OCI registries)
- Internal registry pushes (e.g., during sync mirroring) use `CHARM_REGISTRY_OCI_INTERNAL_URL`

### OCI credential lifecycle

1. When a package is first registered or receives an OCI image resource, the service provisions an OCI project and two robot accounts (push, pull)
2. Push credentials allow push and pull; pull credentials allow pull only
3. `GET /v1/charm/{name}/resources/{resource}/oci-image/upload-credentials` returns push credentials for the package
4. Credentials are deterministic HMAC-derived from `CHARM_REGISTRY_OCI_SECRET_KEY` — changing this key invalidates all existing credentials with no migration path

## Authentication model

The registry uses a two-tier authentication model:

1. **OIDC identity** — Users authenticate through an OIDC provider. The registry extracts claims (subject, username, display name, email) to establish identity.
2. **Store tokens** — After OIDC authentication, users can issue store tokens with scoped permissions (package, channel, permission level). Store tokens are used for CLI and CI workflows.

Admin identities are bootstrapped from environment variables:

- `CHARM_REGISTRY_ADMIN_SUBJECTS` — OIDC subject claims
- `CHARM_REGISTRY_ADMIN_EMAILS` — Email claims
- `CHARM_REGISTRY_ADMIN_USERNAMES` — Username claims

Admins can access and manage every package. Non-admin users can only manage packages they own.

### Insecure dev auth

When `CHARM_REGISTRY_ENABLE_INSECURE_DEV_AUTH=true`, the registry accepts opaque development bearer tokens in the format `dev:<username>:<display>`. This is for local development only and must never be enabled in production.

The application validates at startup that either OIDC is configured or insecure dev auth is explicitly enabled.
