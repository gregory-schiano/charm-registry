# Deployment

Charm Registry ships three deployment targets: Docker Compose, Snap, and Rock (OCI image).

## Docker Compose

The compose stack includes Postgres, MinIO, and the registry service. It is designed for local development and small private deployments.

### Quick start

```bash
# Create the shared network
docker network create charm-registry-shared

# Start the stack
make up
```

This builds the registry image, generates TLS certificates for the OCI listener, and starts all services.

### Customization

Copy `.env.example` to `.env` and adjust values:

```bash
cp .env.example .env
```

Key settings to change for non-local use:

```bash
CHARM_REGISTRY_PUBLIC_API_URL=http://your-host:8080
CHARM_REGISTRY_PUBLIC_STORAGE_URL=http://your-host:8080
CHARM_REGISTRY_PUBLIC_REGISTRY_URL=https://your-host:5000
```

### Production hardening

Before exposing the compose stack beyond localhost:

1. **Disable insecure dev auth:** Set `CHARM_REGISTRY_ENABLE_INSECURE_DEV_AUTH=false`
2. **Configure OIDC:** Set `CHARM_REGISTRY_OIDC_ISSUER_URL` and `CHARM_REGISTRY_OIDC_CLIENT_ID`
3. **Set admin identities:** Configure at least one of `CHARM_REGISTRY_ADMIN_SUBJECTS`, `CHARM_REGISTRY_ADMIN_EMAILS`, or `CHARM_REGISTRY_ADMIN_USERNAMES`
4. **Change default secrets:** Replace `CHARM_REGISTRY_OCI_SECRET_KEY`, MinIO root credentials, and S3 access keys
5. **Enable database TLS:** Change `sslmode=disable` to `sslmode=require` or `verify-full` in `CHARM_REGISTRY_DATABASE_URL`
6. **Put TLS in front of the API:** The Go app serves plain HTTP on `:8080`. Use a reverse proxy (nginx, Caddy, Traefik) with TLS termination.
7. **Restrict network access:** The compose stack exposes ports on all interfaces by default. Bind to `127.0.0.1` or use firewall rules.

### Stopping

```bash
make down
```

This stops and removes containers, networks, and volumes.

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

# Switch to Postgres
snap set charm-registry database.backend=postgres
snap set charm-registry database.url=postgres://user:pass@host:5432/charm_registry?sslmode=require

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

### Known limitation: API TLS in the Go app

The snap wrapper exports `CHARM_REGISTRY_TLS_CERT_FILE` and `CHARM_REGISTRY_TLS_KEY_FILE` when TLS is enabled, but the Go application does not yet read these variables. The main API server always uses `ListenAndServe()` (plain HTTP). This means:

- With `tls.enabled=true`, the snap generates certificates but the API server ignores them
- You still need a reverse proxy in front of the snap for API TLS termination
- The OCI listener does support TLS directly via `CHARM_REGISTRY_OCI_TLS_CERT_FILE` / `CHARM_REGISTRY_OCI_TLS_KEY_FILE`

This gap is tracked in the production-readiness roadmap.

### Snap grade

The snap is currently `grade: devel`, which blocks publishing to the stable channel. When the service is production-ready, the grade should be changed to `stable`.

## Rock (OCI image)

A Rockcraft-based OCI image is available for Kubernetes and other container orchestration platforms.

### Build

```bash
rockcraft pack
```

This produces a Rock (OCI image) that can be pushed to any OCI registry.

### Deploy

The Rock runs the same `charm-registry` binary as the Docker and snap deployments. Configure it with the same environment variables.

For Kubernetes, create a ConfigMap or Secret with the environment variables and mount them into the container. Key considerations:

- The Rock does not include a shell — use `envFrom` in your pod spec
- Mount TLS certificates as volumes
- Set `CHARM_REGISTRY_OCI_SECRET_KEY` from a Kubernetes Secret
- The Rock exposes ports 8080 (API) and 5000 (OCI registry)

Example pod spec fragment:

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

### Platforms

The Rock currently builds for `amd64` only. `arm64`, `ppc64el`, and `s390x` are commented out in `rockcraft.yaml`.
