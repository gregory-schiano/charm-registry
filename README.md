# Private Charm Registry MVP

This repository contains a Go-based private charm registry that implements the Charmhub API subset needed by stock `juju` and stock `charmcraft`, without patching either client. The service is API-first, stores metadata in Postgres, stores artifacts in S3-compatible object storage, and treats OCI image delivery as an external registry dependency.

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
- OIDC-compatible identity ingestion plus opaque store-token issuance
- Private-by-default packages with owner and group-based access checks
- S3-backed charm/resource blobs and OCI registry credential/blob helpers

## Architecture

- `cmd/charm-registry`: process entrypoint
- `internal/api`: HTTP router, response shaping, OpenAPI stub
- `internal/service`: Charmhub-compatible business logic
- `internal/repo`: Postgres and in-memory repositories
- `internal/blob`: S3-compatible blob store
- `internal/auth`: OIDC and store-token authentication
- `internal/charm`: charm archive parsing

## Local development

Bring up the full dev stack:

```bash
docker compose up --build
```

The compose stack includes:

- Postgres
- MinIO for S3-compatible storage
- Docker Distribution as the OCI registry
- The charm registry service

The API is exposed at [http://localhost:8080](http://localhost:8080), MinIO at [http://localhost:9001](http://localhost:9001), and the OCI registry at [http://localhost:5000](http://localhost:5000).

For local-only auth you can use insecure development bearer tokens:

```text
Authorization: Bearer dev:alice:alice
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

## Configuration

See [.env.example](/Users/gschiano/charm-registry/.env.example) for the supported environment variables. The only strictly required variable outside the compose stack is `CHARM_REGISTRY_DATABASE_URL`.

## Current limitations

- The MVP does not include a browse UI, charm libraries, bundles-specific extras, analytics, or collaborator management UX.
- Token attenuation and revocation are implemented in the registry, but the external OCI registry is still an off-the-shelf dependency.
- Group ACL data model exists, but there are no dedicated admin endpoints for group management yet.
- Stock `juju` can target an alternate Charmhub URL, but private package auth support is still the main compatibility risk to validate end-to-end in your environment. If Juju does not forward auth for consumer requests, private deployments may need network-level access controls in front of the registry.

## Quality gates

The repository now carries a Juju-inspired Go hygiene baseline:

- `.golangci.yml` with curated linters instead of enabling everything blindly
- `tools.go` to pin lint and security tooling in-module
- `make lint`, `make vuln`, and `make gosec` for repeatable local checks
- explicit HTTP timeouts, body-size limits, and basic security headers

I intentionally did not raise the language floor aggressively just to satisfy the scanners. Instead, the module now keeps a conservative `go` directive while pinning a patched preferred toolchain, which improves security posture without forcing the same compatibility jump on every downstream integration.
