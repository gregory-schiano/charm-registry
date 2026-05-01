# Private Charm Registry

This repository contains a Go-based private charm registry that supports stock `juju` and stock `charmcraft` for the supported charm and resource workflows, without patching either client. The service is API-first, stores metadata in Postgres or SQLite, stores charm/resource artifacts in S3-compatible or filesystem storage, and embeds an OCI Distribution registry for image push/pull and sync workflows.

## What is implemented

- Juju-facing consumer APIs:
  - `GET /v2/charms/find`
  - `GET /v2/charms/info/{name}`
  - `POST /v2/charms/refresh`
  - artifact download endpoints under `/api/v1/...`
- Charmcraft-facing publisher APIs:
  - `/v1/tokens*`
  - `/v1/whoami`
  - `/v1/charm...` registration, metadata, revisions, resources, releases, and tracks
  - `POST /unscanned-upload/`
- OIDC-backed identity resolution plus opaque store-token issuance
- Private-by-default packages with owner-only management and admin override
- S3-backed or filesystem-backed charm/resource blobs and embedded OCI registry credential/blob helpers
- Registry-managed Charmhub track synchronization with a background worker
- Admin CLI `charm-registryctl` for managing synchronized tracks

## Architecture

- `cmd/charm-registry`: process entrypoint
- `internal/api`: HTTP router, response shaping, OpenAPI stub
- `internal/service`: registry business logic for charmcraft and juju compatibility
- `internal/repo`: Postgres, SQLite, and in-memory repositories
- `internal/blob`: S3-compatible and filesystem blob stores
- `internal/auth`: OIDC and store-token authentication
- `internal/charm`: charm archive parsing
- `internal/charmhub`: upstream Charmhub client used by the sync worker
- `internal/oci`: embedded OCI Distribution registry backend

## Local development

Bring up the full dev stack:

```bash
make up
```

The compose stack includes:

- Postgres
- MinIO for S3-compatible storage
- The charm registry service
- An embedded OCI registry listener in the charm registry process

The API is exposed at [http://localhost:8080](http://localhost:8080), MinIO at [http://localhost:9001](http://localhost:9001), and the embedded OCI registry at [https://localhost:5000](https://localhost:5000).

If Juju or another client runs outside the Docker host, set `CHARM_REGISTRY_PUBLIC_API_URL`, `CHARM_REGISTRY_PUBLIC_STORAGE_URL`, and `CHARM_REGISTRY_PUBLIC_REGISTRY_URL` to a host/IP that is reachable from that client. Leaving them at `localhost` will cause the registry to hand out download or OCI image URLs that only work on the registry host itself. If the public registry URL uses a non-default port, set `CHARM_REGISTRY_OCI_HOST_PORT` to the same port before running `make up`.

The local embedded OCI registry defaults to HTTPS because `charmcraft` assumes OCI registries use TLS. `make up` generates `certs/oci.crt` and `certs/oci.key` for the host in `CHARM_REGISTRY_PUBLIC_REGISTRY_URL`; run `make install-cert` on any machine that runs `charmcraft`, `skopeo`, or another client that needs to trust the local registry certificate. If Juju/containerd pulls from a Canonical `k8s` snap node, run `make install-k8s-cert` on that node too.

For local-only auth you can opt into insecure development bearer tokens:

```text
Authorization: Bearer dev:alice:alice
```

The embedded OCI registry does not allow anonymous image pulls or pushes outside the `/v2/` ping. For direct testing, log in with the package-scoped credentials returned by the Charm Registry OCI endpoints. Push credentials can push and pull; pull credentials can only pull.

```bash
docker login localhost:5000 --username '<package-push-username>' --password '<package-push-secret>'
```

Production deployments should leave `CHARM_REGISTRY_ENABLE_INSECURE_DEV_AUTH=false`, configure OIDC with `CHARM_REGISTRY_OIDC_ISSUER_URL` and `CHARM_REGISTRY_OIDC_CLIENT_ID`, use TLS for both listeners, and set a dedicated `CHARM_REGISTRY_OCI_SECRET_KEY` for credential encryption.

For standalone local operation without Postgres or S3, omit `CHARM_REGISTRY_DATABASE_URL` and S3 endpoint/credential variables, or set the backends explicitly:

```bash
export CHARM_REGISTRY_DATABASE_BACKEND=sqlite
export CHARM_REGISTRY_STORAGE_BACKEND=filesystem
export CHARM_REGISTRY_OCI_STORAGE_BACKEND=filesystem
export CHARM_REGISTRY_DATA_DIR=/var/lib/charm-registry
```

## Useful commands

```bash
make help
make fmt
make vet
make lint
make test
make test-race
make tidy
make vuln
make gosec
make audit
make up
make down
```

`make build` now produces both binaries in `.bin/`:

- `.bin/charm-registry`
- `.bin/charm-registryctl`

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

The worker runs on a registry-owned schedule. Adding or removing a sync rule also triggers an immediate asynchronous reconciliation.

## Admin CLI

Build the CLI:

```bash
make build
```

The binary will be at:

```bash
.bin/charm-registryctl
```

The CLI talks to the registry over HTTP and requires an admin bearer token.

Set the connection details once:

```bash
export CHARM_REGISTRY_URL=http://localhost:8080
export CHARM_REGISTRY_TOKEN='dev:admin:admin'
```

If you are using insecure dev auth locally, make sure that identity is configured as an admin through one of:

- `CHARM_REGISTRY_ADMIN_SUBJECTS`
- `CHARM_REGISTRY_ADMIN_EMAILS`
- `CHARM_REGISTRY_ADMIN_USERNAMES`

For example:

```bash
export CHARM_REGISTRY_ADMIN_USERNAMES=admin
```

That admin bootstrap setting must be present in the running `charm-registry` server process, not just in the shell where you invoke the CLI. With Docker Compose, either put `CHARM_REGISTRY_ADMIN_USERNAMES=admin` in your real `.env`, or export it before `make up`, then restart the registry so the container picks it up.

Then use the CLI:

```bash
.bin/charm-registryctl sync list
.bin/charm-registryctl sync add postgresql-k8s --track 14
.bin/charm-registryctl sync add postgresql-k8s --track 14 --base ubuntu@24.04 --arch amd64
.bin/charm-registryctl sync remove postgresql-k8s --track 14
.bin/charm-registryctl sync run postgresql-k8s
```

You can also pass the connection details explicitly instead of using environment variables:

```bash
.bin/charm-registryctl --url http://localhost:8080 --token 'dev:admin:admin' sync list
```

What the commands do:

- `sync list`: show the configured rules and the last known sync status
- `sync add <name> --track <track> [--base <name@channel> ...] [--arch <arch> ...]`: create a sync rule and enqueue an immediate sync
- `sync remove <name> --track <track>`: remove the rule and enqueue cleanup/reconciliation
- `sync run <name>`: trigger an immediate reconciliation for all synchronized tracks of that package

## Configuration

See [.env.example](/src/Canonical/charm-registry/.env.example) for the supported environment variables.

Important settings:

- `CHARM_REGISTRY_DATABASE_BACKEND` selects `auto`, `postgres`, or `sqlite`. In `auto`, the registry uses Postgres when `CHARM_REGISTRY_DATABASE_URL` or `POSTGRESQL_DB_CONNECT_STRING` is set, otherwise SQLite.
- `CHARM_REGISTRY_STORAGE_BACKEND` selects `auto`, `s3`, or `filesystem` for charm/resource artifacts. In `auto`, S3 endpoint or credentials select S3, otherwise filesystem storage.
- `CHARM_REGISTRY_OCI_STORAGE_BACKEND` selects `auto`, `s3`, or `filesystem` for embedded OCI registry blobs.
- `CHARM_REGISTRY_DATA_DIR`, `CHARM_REGISTRY_SQLITE_PATH`, `CHARM_REGISTRY_BLOB_DIR`, and `CHARM_REGISTRY_OCI_STORAGE_DIR` control local fallback paths.
- `CHARM_REGISTRY_ENABLE_INSECURE_DEV_AUTH=true` enables development-only bearer tokens and anonymous token minting for local workflows.
- `CHARM_REGISTRY_OIDC_ISSUER_URL` and `CHARM_REGISTRY_OIDC_CLIENT_ID` enable the production authentication path.
- `CHARM_REGISTRY_ADMIN_SUBJECTS`, `CHARM_REGISTRY_ADMIN_EMAILS`, and `CHARM_REGISTRY_ADMIN_USERNAMES` bootstrap admin identities with access to every charm.
- `CHARM_REGISTRY_OCI_LISTEN` controls the embedded OCI listener. The default is `:5000`.
- `CHARM_REGISTRY_PUBLIC_REGISTRY_URL` is the OCI registry URL handed to Juju, charmcraft, and synced resource payloads. The local default is `https://localhost:5000`.
- `CHARM_REGISTRY_OCI_INTERNAL_URL` is the URL the registry service uses when it pushes mirrored upstream OCI images into its own embedded listener.
- `CHARM_REGISTRY_OCI_S3_BUCKET` and `CHARM_REGISTRY_OCI_S3_PREFIX` configure the embedded OCI Distribution S3 storage area when OCI storage resolves to S3.
- `CHARM_REGISTRY_OCI_SECRET_KEY` encrypts package-scoped OCI push/pull credentials at rest.
- `CHARM_REGISTRY_OCI_TLS_CERT_FILE` and `CHARM_REGISTRY_OCI_TLS_KEY_FILE` enable TLS on the embedded OCI listener when set together. The compose stack mounts `certs/oci.crt` and `certs/oci.key` at `/certs`.
- `CHARM_REGISTRY_CHARMHUB_URL` overrides the upstream Charmhub API base URL used by the sync worker. The default is `https://api.charmhub.io`.
- `CHARM_REGISTRY_CHARMHUB_SYNC_INTERVAL` controls how often the background sync worker scans all configured rules. The default is `15m`.
- `CHARM_REGISTRY_MAX_ARCHIVE_FILE_BYTES` controls the per-entry decompressed size limit when parsing charm archives. The default is `10485760` (10 MiB).

## Current limitations

- The registry does not include a browse UI, bundle-specific extras, analytics, or collaborator management UX.
- Embedded charm libraries are intentionally stubbed and returned as unsupported store-side content.
- The embedded OCI backend deletes manifests and repository metadata on best effort, but unreferenced S3 blobs still need a future garbage-collection story.
- Group ACL data model exists, but the effective access model is intentionally minimal: owner-managed charms plus configured admins.
- Stock `juju` can target an alternate Charmhub URL, but private package auth support is still the main compatibility risk to validate end-to-end in your environment. If Juju does not forward auth for consumer requests, private deployments may need network-level access controls in front of the registry.

## Quality gates

The repository now carries a Juju-inspired Go hygiene baseline:

- `.golangci.yml` with curated linters instead of enabling everything blindly
- the `tool` block in `go.mod` to pin lint and security tooling in-module
- `make lint`, `make vuln`, and `make gosec` for repeatable local checks
- explicit HTTP timeouts, body-size limits, and basic security headers

I intentionally did not raise the language floor aggressively just to satisfy the scanners. Instead, the module now keeps a conservative `go` directive while pinning a patched preferred toolchain, which improves security posture without forcing the same compatibility jump on every downstream integration.
