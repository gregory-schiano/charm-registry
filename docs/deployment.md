# Deployment

Charm Registry ships two production deployment targets: **Snap** (for Ubuntu hosts) and **Rock** (OCI image for Kubernetes). For local development and quick experiments, you can also run the Go binary directly.

## Running from source

For local development without packaging:

```bash
make build
.bin/charm-registry
```

Set environment variables or copy `.env.example` to `.env` for configuration. The binary serves the API on `:8080` and the embedded OCI registry on `:5000` by default.

Standalone mode (SQLite + filesystem, no external dependencies):

```bash
export CHARM_REGISTRY_DATABASE_BACKEND=sqlite
export CHARM_REGISTRY_STORAGE_BACKEND=filesystem
export CHARM_REGISTRY_OCI_STORAGE_BACKEND=filesystem
export CHARM_REGISTRY_DATA_DIR=/var/lib/charm-registry
.bin/charm-registry
```

## Snap

The snap packages the registry binary, admin CLI, and a wrapper script that handles snap-specific configuration and certificate management.

### Install

```bash
snap install charm-registry
```

The service does not auto-start on install (`install-mode: disable`). Start it explicitly:

```bash
snap start charm-registry
```

### Defaults

The snap defaults to standalone mode:

- **Database:** SQLite under `$SNAP_COMMON/data/registry.sqlite`
- **Blob storage:** Filesystem under `$SNAP_COMMON/data/blobs/`
- **OCI storage:** Filesystem under `$SNAP_COMMON/data/oci-registry/`
- **OCI TLS:** Self-signed certificate at `$SNAP_COMMON/certs/oci.crt`
- **API TLS:** Disabled by default. Enable with `snap set charm-registry tls.enabled=true`

### Configuration

Snap configuration uses dotted keys that the wrapper maps to environment variables:

```bash
# Networking
snap set charm-registry public-api-url=https://registry.example.com:8080
snap set charm-registry public-storage-url=https://registry.example.com:8080
snap set charm-registry public-registry-url=https://registry.example.com:5000

# API TLS
snap set charm-registry tls.enabled=true
snap set charm-registry tls.cert-file=/path/to/registry.crt
snap set charm-registry tls.key-file=/path/to/registry.key

# OIDC
snap set charm-registry oidc.issuer-url=https://sso.example.com/realms/main
snap set charm-registry oidc.client-id=charm-registry

# Admin identities
snap set charm-registry admin.usernames=admin
snap set charm-registry admin.emails=admin@example.com

# OCI secret key (required)
snap set charm-registry oci.secret-key=$(openssl rand -hex 32)

# Token and IP rate limits
snap set charm-registry rate-limit.ip-limit=120
snap set charm-registry rate-limit.ip-window=1m
snap set charm-registry rate-limit.token-limit=5
snap set charm-registry rate-limit.token-window=1m

# Switch to Postgres
snap set charm-registry database.backend=postgres
snap set charm-registry database.url='postgres://user:<password>@host:5432/charm_registry?sslmode=require'

# Switch to S3 storage
snap set charm-registry storage.backend=s3
snap set charm-registry storage.s3.endpoint=https://s3.example.com
snap set charm-registry storage.s3.bucket=charm-registry-blobs
snap set charm-registry storage.s3.access-key-id=AKIA...
snap set charm-registry storage.s3.secret-access-key=...
```

After changing snap configuration, restart the service:

```bash
snap restart charm-registry
```

### Certificate management

The snap's configure hook automatically generates self-signed TLS certificates:

- **API certificate** (`$SNAP_COMMON/certs/registry.crt`) — generated when `tls.enabled=true`, covering the public API/storage URL hostname
- **OCI certificate** (`$SNAP_COMMON/certs/oci.crt`) — generated on install, covering the public and internal OCI registry URLs

Certificates are regenerated when the hostname changes. For production, replace the self-signed certificates with CA-issued certificates:

```bash
snap set charm-registry tls.cert-file=/etc/ssl/certs/registry.crt
snap set charm-registry tls.key-file=/etc/ssl/private/registry.key
snap set charm-registry oci.tls.cert-file=/etc/ssl/certs/oci.crt
snap set charm-registry oci.tls.key-file=/etc/ssl/private/oci.key
```

The snap maps `tls.cert-file` and `tls.key-file` to `CHARM_REGISTRY_API_TLS_CERT_FILE` and `CHARM_REGISTRY_API_TLS_KEY_FILE`; the main API server reads those variables and serves HTTPS when both are set.

The OCI listener supports TLS directly via `CHARM_REGISTRY_OCI_TLS_CERT_FILE` / `CHARM_REGISTRY_OCI_TLS_KEY_FILE`.

### Snap grade

The snap is currently `grade: devel`, which blocks publishing to the stable channel. When the service is production-ready, the grade should be changed to `stable`.

## Rock (OCI image)

A Rockcraft-built OCI image for Kubernetes and other container orchestration platforms. The rock is built with `rockcraft pack`.

### Build

```bash
rockcraft pack
```

This produces a `.rock` file (OCI image archive) that can be pushed to any OCI registry with `skopeo`:

```bash
skopeo copy oci-archive:charm-registry_*.rock docker://ghcr.io/<org>/charm-registry:<tag>
```

### Deploy on Kubernetes

The rock runs the same `charm-registry` binary. Configure it with environment variables via ConfigMap or Secret:

```yaml
envFrom:
  - configMapRef:
      name: charm-registry-config
  - secretRef:
      name: charm-registry-secrets
ports:
  - containerPort: 8080
    name: api
  - containerPort: 5000
    name: oci
```

Key considerations:

- The rock does not include a shell — use `envFrom` in your pod spec
- Mount TLS certificates as volumes
- Set `CHARM_REGISTRY_OCI_SECRET_KEY` from a Kubernetes Secret
- The rock exposes ports 8080 (API) and 5000 (OCI registry)

### Platforms

The rock builds for `amd64`. Additional architectures (`arm64`, `ppc64el`, `s390x`) can be enabled in `rockcraft.yaml`.

## Charm (Juju)

The charm deploys charm-registry as a Kubernetes workload through Juju, with relations to PostgreSQL, S3-compatible storage, and two ingresses.

### Relations

Both ingress relations are **mandatory** — the charm stays blocked until they are related:

- `ingress` fronts the API and artifact-download endpoints. Its URL becomes `CHARM_REGISTRY_PUBLIC_API_URL` and `CHARM_REGISTRY_PUBLIC_STORAGE_URL`.
- `oci-ingress` fronts the embedded OCI registry. Its URL becomes `CHARM_REGISTRY_PUBLIC_REGISTRY_URL`.

There are no public-URL config options: the relations are the single source of truth, and URL changes on the ingress side propagate to the workload automatically.

### Deploy with Juju

```bash
juju deploy postgresql-k8s --trust
juju deploy traefik-k8s ingress-api --trust
juju deploy traefik-k8s ingress-oci --trust
juju deploy ./charm-registry_*.charm charm-registry

juju integrate charm-registry:postgresql postgresql-k8s:database
juju integrate charm-registry:ingress ingress-api:ingress
juju integrate charm-registry:oci-ingress ingress-oci:ingress
```

Two separate traefik applications are needed because each ingress relation publishes a route keyed on the same model/app name — a single traefik cannot serve both.

### TLS

The workload serves plain HTTP on both listeners; TLS terminates at the ingress. Give the traefik applications real certificates (for example via their `certificates` relation). The OCI ingress in particular must serve HTTPS — containerd and Docker refuse plain-HTTP registries by default.

See the charm's `charmcraft.yaml` for the full relation and configuration interface.

## Production hardening

Before any internet-facing deployment:

1. **Disable insecure dev auth:** Set `CHARM_REGISTRY_ENABLE_INSECURE_DEV_AUTH=false` (the default)
2. **Configure OIDC:** Set `CHARM_REGISTRY_OIDC_ISSUER_URL` and `CHARM_REGISTRY_OIDC_CLIENT_ID`
3. **Set admin identities:** Configure at least one of `CHARM_REGISTRY_ADMIN_SUBJECTS`, `CHARM_REGISTRY_ADMIN_EMAILS`, or `CHARM_REGISTRY_ADMIN_USERNAMES`
4. **Set OCI secret key:** Generate a strong random key for `CHARM_REGISTRY_OCI_SECRET_KEY`
5. **Enable database TLS:** Use `sslmode=require` or `verify-full` in `CHARM_REGISTRY_DATABASE_URL`
6. **Put TLS in front of the API:** Use a reverse proxy or the snap's TLS support
7. **Restrict network access:** Bind to specific interfaces or use firewall rules
8. **Protect `/metrics`:** The Prometheus endpoint is unauthenticated by design. Keep it on an internal listener/network or configure the reverse proxy/ingress to allow it only from trusted scrape sources.
