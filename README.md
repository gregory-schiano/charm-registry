# Charm Registry

A private charm registry that works with stock `juju` and stock `charmcraft`. No patched clients required.

The service stores charm metadata in Postgres or SQLite, stores charm and resource artifacts in S3-compatible storage or on the filesystem, and runs an embedded OCI Distribution registry in-process for image push/pull and sync workflows.

## What's implemented

**Juju-facing consumer APIs:**

- `GET /v2/charms/find` — find public and authorized private charms
- `GET /v2/charms/info/{name}` — charm metadata
- `POST /v2/charms/refresh` — refresh/channel resolution
- Artifact download endpoints under `/api/v1/...`

**Charmcraft-facing publisher APIs:**

- `/v1/tokens*` — issue, list, exchange, revoke store tokens
- `/v1/whoami` — identity verification
- `/v1/charm...` — registration, metadata, revisions, resources, releases, tracks
- `POST /unscanned-upload/` — charm archive upload

**Authentication:**

- OIDC-backed identity resolution (any provider that speaks OIDC Discovery)
- Opaque store-token issuance with configurable TTL and scoped permissions
- Macaroon token support for charmcraft compatibility
- Insecure dev bearer tokens for local development (off by default)

**Storage:**

- S3-compatible or filesystem blob storage for charm/resource artifacts
- Embedded OCI Distribution v2 registry for image resources
- Per-package OCI push/pull credentials encrypted at rest

**Operations:**

- Registry-managed Charmhub track synchronization with a background worker
- Admin CLI `charm-registryctl` for managing synchronized tracks
- Health (`/healthz`), readiness (`/readyz`), and Prometheus (`/metrics`) endpoints
- OpenAPI spec at `/openapi.yaml`

## Architecture

```
cmd/charm-registry/       — process entrypoint
cmd/charm-registryctl/    — admin CLI
cmd/functional-test/      — shared functional test harness
internal/api/             — HTTP router, response shaping, OpenAPI stub
internal/app/             — application wiring
internal/service/         — registry business logic
internal/repo/            — Postgres, SQLite, and in-memory repositories
internal/blob/            — S3-compatible and filesystem blob stores
internal/auth/            — OIDC, macaroon, and store-token authentication
internal/charm/           — charm archive parsing
internal/charmhub/        — upstream Charmhub client (sync worker)
internal/oci/             — embedded OCI Distribution registry backend
internal/config/          — environment-driven configuration
```

## Local development

Build and run from source:

```bash
make build
.bin/charm-registry
```

Or in standalone mode (SQLite + filesystem, no Postgres or S3 needed):

```bash
export CHARM_REGISTRY_DATABASE_BACKEND=sqlite
export CHARM_REGISTRY_STORAGE_BACKEND=filesystem
export CHARM_REGISTRY_OCI_STORAGE_BACKEND=filesystem
export CHARM_REGISTRY_DATA_DIR=/var/lib/charm-registry
.bin/charm-registry
```

| Service | URL |
|---------|-----|
| API | http://localhost:8080 |
| Embedded OCI registry | https://localhost:5000 |

### TLS for the embedded OCI registry

The local OCI registry uses HTTPS because `charmcraft` assumes OCI registries use TLS. Generate self-signed certs:

```bash
make generate-cert
```

Install the certificate on any machine that runs `charmcraft`, `skopeo`, or another OCI client:

```bash
make install-cert
```

For Canonical `k8s` snap nodes where containerd needs to trust the certificate:

```bash
make install-k8s-cert
```

### Authentication

For local-only development, you can opt into insecure bearer tokens:

```text
Authorization: Bearer ***
```

This mode is **only** for development. Never enable it on a network-reachable deployment. Production deployments must configure OIDC.

The embedded OCI registry does not allow anonymous image pulls or pushes outside the `/v2/` ping. Log in with the package-scoped credentials returned by the registry's OCI endpoints:

```bash
skopeo login localhost:5000 --username '<package-push-username>' --password '<package-push-secret>'
```

Push credentials can push and pull; pull credentials can only pull.

See [Configuration](docs/configuration.md) for all options.

## Charmhub synchronization

The registry can mirror public Charmhub charms on a track-by-track basis. Each sync rule is `(charm name, track)`, and the worker mirrors every matching base/architecture variant for the latest release on:

- `track/stable`
- `track/candidate`
- `track/beta`
- `track/edge`

By default, a rule syncs all upstream bases and architectures for that track. You can narrow the rule with repeatable base and architecture filters:

- Base filters use `name@channel`, for example `ubuntu@24.04`.
- Architecture filters use Juju/Charmhub architecture names, for example `amd64` or `arm64`.
- Filters are inclusive allowlists. A release variant must match both the base filter and the architecture filter.

Important behavior:

- Synchronization is admin-only.
- Synchronized packages are registry-owned and marked with `authority=charmhub`.
- While a package is synchronized, normal publisher mutations are blocked.
- Removing a synchronized track prunes local charm/resource artifacts that are no longer referenced.
- Removing the last synchronized track deletes the mirrored package and its OCI artifacts locally.
- Trying to manually register a synchronized package name returns a conflict.
- Trying to synchronize a package name that already exists as a normal package also returns a conflict.

The worker runs on a configurable schedule (default: 15 minutes). Adding or removing a sync rule also triggers an immediate asynchronous reconciliation.

## Admin CLI

Build the CLI:

```bash
make build
```

The binary will be at `.bin/charm-registryctl`.

The CLI talks to the registry over HTTP and requires an admin bearer token. Set the connection details once:

```bash
export CHARM_REGISTRY_URL=http://localhost:8080
export CHARM_REGISTRY_TOKEN='***'
```

If you are using insecure dev auth locally, configure an admin identity on the running server through one of:

- `CHARM_REGISTRY_ADMIN_SUBJECTS`
- `CHARM_REGISTRY_ADMIN_EMAILS`
- `CHARM_REGISTRY_ADMIN_USERNAMES`

For example:

```bash
export CHARM_REGISTRY_ADMIN_USERNAMES=admin
```

That admin bootstrap setting must be present in the running `charm-registry` server process, not just in the shell where you invoke the CLI.

Then use the CLI:

```bash
.bin/charm-registryctl sync list
.bin/charm-registryctl sync add postgresql-k8s --track 14
.bin/charm-registryctl sync add postgresql-k8s --track 14 --base ubuntu@24.04 --arch amd64
.bin/charm-registryctl sync remove postgresql-k8s --track 14
.bin/charm-registryctl sync run postgresql-k8s
```

You can also pass connection details explicitly:

```bash
.bin/charm-registryctl --url http://localhost:8080 --token 'dev:admin:admin' sync list
```

Commands:

- `sync list` — show configured rules and last known sync status
- `sync add <name> --track <track> [--base <name@channel> ...] [--arch <arch> ...]` — create a sync rule and enqueue an immediate sync
- `sync remove <name> --track <track>` — remove the rule and enqueue cleanup/reconciliation
- `sync run <name>` — trigger an immediate reconciliation for all synchronized tracks of that package

## Deployment

The registry ships two production deployment targets:

1. **Snap** — for Ubuntu hosts. Includes built-in TLS certificate generation and snap configuration. See [docs/deployment.md](docs/deployment.md) for details.
2. **Rock (OCI image)** — for Kubernetes and container orchestration. Built with `rockcraft pack`, published with `skopeo`. See [docs/deployment.md](docs/deployment.md) for details.

The charm deploys charm-registry as a Kubernetes workload through Juju. See [docs/deployment.md](docs/deployment.md) for the full deployment reference including configuration, TLS, and production hardening.

## Configuration

All configuration is through environment variables. See [.env.example](.env.example) for the full list with defaults.

Key settings:

| Variable | Purpose | Default |
|----------|---------|---------|
| `CHARM_REGISTRY_LISTEN` | API listen address | `:8080` |
| `CHARM_REGISTRY_DATABASE_BACKEND` | `auto`, `postgres`, or `sqlite` | `auto` |
| `CHARM_REGISTRY_STORAGE_BACKEND` | `auto`, `s3`, or `filesystem` | `auto` |
| `CHARM_REGISTRY_OCI_STORAGE_BACKEND` | `auto`, `s3`, or `filesystem` | `auto` |
| `CHARM_REGISTRY_ENABLE_INSECURE_DEV_AUTH` | Allow dev bearer tokens | `false` |
| `CHARM_REGISTRY_OIDC_ISSUER_URL` | OIDC issuer (required in production) | — |
| `CHARM_REGISTRY_OIDC_CLIENT_ID` | OIDC client ID (required in production) | — |
| `CHARM_REGISTRY_ADMIN_SUBJECTS` | Admin OIDC subjects (comma-separated) | — |
| `CHARM_REGISTRY_ADMIN_EMAILS` | Admin emails (comma-separated) | — |
| `CHARM_REGISTRY_ADMIN_USERNAMES` | Admin usernames (comma-separated) | — |
| `CHARM_REGISTRY_OCI_SECRET_KEY` | Encryption key for OCI credentials | — (required) |
| `CHARM_REGISTRY_OCI_LISTEN` | Embedded OCI registry listen address | `:5000` |
| `CHARM_REGISTRY_PUBLIC_REGISTRY_URL` | OCI URL handed to clients | `https://localhost:5000` |
| `CHARM_REGISTRY_OCI_TLS_CERT_FILE` | OCI TLS certificate path | — |
| `CHARM_REGISTRY_OCI_TLS_KEY_FILE` | OCI TLS key path | — |
| `CHARM_REGISTRY_CHARMHUB_SYNC_INTERVAL` | Background sync scan interval | `15m` |

In `auto` mode, the registry uses Postgres when `CHARM_REGISTRY_DATABASE_URL` is set, otherwise SQLite. For storage, it uses S3 when S3 endpoint or credentials are set, otherwise the filesystem.

See [docs/configuration.md](docs/configuration.md) for the complete reference.

## Useful commands

```bash
make help
make fmt          # format Go code
make vet          # run go vet
make lint         # run golangci-lint
make test         # run unit tests
make test-race    # run tests with the race detector
make coverage     # run tests with coverage report
make tidy         # tidy and verify Go modules
make vuln         # run govulncheck
make gosec        # run gosec static analysis
make audit        # run lint, tests, and security checks
make build        # build registry and admin CLI
make charm-pack   # pack the charm with charmcraft
make rock-pack    # pack the OCI rock with rockcraft
make snap-pack    # pack the snap with snapcraft
make artifact-build                  # build all artifacts (charm, rock, snap)
make charm-integration-test          # run charm integration tests (Jubilant)
make snap-integration-test           # run snap integration tests (spread)
```

`make build` produces both binaries in `.bin/`:

- `.bin/charm-registry`
- `.bin/charm-registryctl`

See [docs/testing.md](docs/testing.md) for the full testing guide.

## Current limitations

- **No browse UI.** There is no web dashboard. Package management is API/CLI only.
- **No bundle support.** Bundle-specific metadata and manifests are not handled.
- **No charm library hosting.** `/v1/charm/libraries/bulk` returns an empty list. Library CRUD is not implemented. For a private registry, vendoring libraries or sharing them via git is usually sufficient.
- **OCI garbage collection is best-effort.** The embedded OCI backend deletes manifests and repository metadata on best effort, but unreferenced S3 blobs still need a future garbage-collection pass.
- **Minimal access model.** Group ACL tables exist in the database schema but are not implemented. The effective access model is: owners manage their own charms, and configured admins can access everything.
- **Juju auth forwarding risk.** Stock `juju` can target an alternate Charmhub URL, but private package auth support is still the main compatibility risk to validate end-to-end in your environment. If Juju does not forward auth for consumer requests, private deployments may need network-level access controls in front of the registry.
- **No API TLS in the Go app.** The main API server always uses plain HTTP. TLS termination requires a reverse proxy or the snap wrapper's TLS support.
- **No backup infrastructure.** There are no built-in backup or restore commands. See [docs/operations.md](docs/operations.md) for manual backup procedures.
- **Internal-only Prometheus metrics.** `GET /metrics` is unauthenticated for Prometheus compatibility and must be kept on an internal network or protected by firewall/ingress rules.
- **Snap grade is `devel`.** The snap cannot be published to the stable channel until the grade is changed.

## Quality gates

The repository carries a Juju-inspired Go hygiene baseline:

- `.golangci.yml` with curated linters
- `go.mod` `tool` block pinning lint and security tooling
- `make lint`, `make vuln`, `make gosec` for repeatable local checks
- Explicit HTTP timeouts, body-size limits, and security headers
- Non-root execution in the rock image
