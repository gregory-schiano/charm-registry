# Private Charm Registry

This repository contains a Go-based private charm registry that supports stock `juju` and stock `charmcraft` for the supported charm and resource workflows, without patching either client. The service is API-first, stores metadata in Postgres, stores artifacts in S3-compatible object storage, and now delegates OCI image storage and access control to Harbor.

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
- S3-backed charm/resource blobs and Harbor-backed OCI credential/blob helpers

## Architecture

- `cmd/charm-registry`: process entrypoint
- `internal/api`: HTTP router, response shaping, OpenAPI stub
- `internal/service`: registry business logic for charmcraft and juju compatibility
- `internal/repo`: Postgres and in-memory repositories
- `internal/blob`: S3-compatible blob store
- `internal/auth`: OIDC and store-token authentication
- `internal/charm`: charm archive parsing

## Local development

Bring up the full dev stack:

```bash
make up
```

The compose stack includes:

- Postgres
- MinIO for S3-compatible storage
- Harbor as the OCI registry and authz layer
- The charm registry service

The API is exposed at [http://localhost:8080](http://localhost:8080), MinIO at [http://localhost:9001](http://localhost:9001), and Harbor at [https://localhost:9443](https://localhost:9443).

If Juju or another client runs outside the Docker host, set `CHARM_REGISTRY_PUBLIC_API_URL` and `CHARM_REGISTRY_PUBLIC_STORAGE_URL` to a host/IP that is reachable from that client. Leaving them at `localhost` will cause the registry to hand out download URLs that only work on the registry host itself.

On first run, `make up` generates a local CA and a TLS certificate for Harbor and writes them to `./certs/`. The Harbor leaf certificate SANs are derived from `CHARM_REGISTRY_PUBLIC_REGISTRY_URL` and `CHARM_REGISTRY_HARBOR_URL`, so if you point those at a reachable host/IP before bootstrapping, the generated cert will cover that address. If those values change later, rerun `make harbor-prepare` or `make up` to reissue the Harbor leaf certificate. Install the CA once so that skopeo and other container tools trust Harbor:

```bash
make install-cert   # requires sudo; supports Ubuntu/Debian and Fedora/RHEL
```

For local-only auth you can opt into insecure development bearer tokens:

```text
Authorization: Bearer dev:alice:alice
```

The local Harbor registry does not allow anonymous pulls. For direct testing, log in with a Harbor robot credential returned by the Charm Registry OCI endpoints, or use the Harbor admin account for operator-only checks:

```bash
docker login localhost:9443 \
  --username "${CHARM_REGISTRY_HARBOR_ADMIN_USERNAME:-admin}" \
  --password "${CHARM_REGISTRY_HARBOR_ADMIN_PASSWORD:-Harbor12345}"
```

Production deployments should leave `CHARM_REGISTRY_ENABLE_INSECURE_DEV_AUTH=false`, configure OIDC with `CHARM_REGISTRY_OIDC_ISSUER_URL` and `CHARM_REGISTRY_OIDC_CLIENT_ID`, and point the application at a Harbor control-plane account plus a dedicated Harbor secret-encryption key.

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

See [.env.example](/src/Canonical/charm-registry/.env.example) for the supported environment variables.

Important auth settings:

- `CHARM_REGISTRY_ENABLE_INSECURE_DEV_AUTH=true` enables development-only bearer tokens and anonymous token minting for local workflows.
- `CHARM_REGISTRY_OIDC_ISSUER_URL` and `CHARM_REGISTRY_OIDC_CLIENT_ID` enable the production authentication path.
- `CHARM_REGISTRY_ADMIN_SUBJECTS`, `CHARM_REGISTRY_ADMIN_EMAILS`, and `CHARM_REGISTRY_ADMIN_USERNAMES` bootstrap admin identities with access to every charm.
- `CHARM_REGISTRY_HARBOR_URL`, `CHARM_REGISTRY_HARBOR_API_URL`, `CHARM_REGISTRY_HARBOR_ADMIN_USERNAME`, and `CHARM_REGISTRY_HARBOR_ADMIN_PASSWORD` configure the Harbor control plane used for project and robot provisioning. In the local compose stack, `CHARM_REGISTRY_HARBOR_API_URL` should target the internal shared-network alias `https://harbor-proxy:8443/api/v2.0`, not the public `localhost` URL.
- `HARBOR_HTTP_PORT` and `HARBOR_HTTPS_PORT` control the local Harbor listener ports used by `make harbor-prepare` and `make harbor-up`.
- `CHARM_REGISTRY_HARBOR_SECRET_KEY` encrypts Harbor robot secrets at rest in the Charm Registry database.

## Current limitations

- The registry does not include a browse UI, bundle-specific extras, analytics, or collaborator management UX.
- Embedded charm libraries are intentionally stubbed and returned as unsupported store-side content.
- Harbor project and robot provisioning is automated, but Harbor itself is still an external system with its own lifecycle and operational footprint.
- Group ACL data model exists, but the effective access model is intentionally minimal: owner-managed charms plus configured admins.
- Stock `juju` can target an alternate Charmhub URL, but private package auth support is still the main compatibility risk to validate end-to-end in your environment. If Juju does not forward auth for consumer requests, private deployments may need network-level access controls in front of the registry.

## Quality gates

The repository now carries a Juju-inspired Go hygiene baseline:

- `.golangci.yml` with curated linters instead of enabling everything blindly
- the `tool` block in `go.mod` to pin lint and security tooling in-module
- `make lint`, `make vuln`, and `make gosec` for repeatable local checks
- explicit HTTP timeouts, body-size limits, and basic security headers

I intentionally did not raise the language floor aggressively just to satisfy the scanners. Instead, the module now keeps a conservative `go` directive while pinning a patched preferred toolchain, which improves security posture without forcing the same compatibility jump on every downstream integration.
