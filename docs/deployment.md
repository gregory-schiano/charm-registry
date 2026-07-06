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

The Snap Store package is currently named `spellbook`. This is temporary while the Charm Registry name is being reserved; the installed service remains `charm-registry`, and the packaged CLI remains `charm-registryctl`.

### Install

```bash
sudo snap install spellbook
```

The service does not auto-start on install (`install-mode: disable`). Set the required OCI credential encryption key and configure authentication before starting it. For local-only standalone testing, enable development auth:

```bash
sudo snap set spellbook oci.secret-key="$(openssl rand -hex 32)"
sudo snap set spellbook insecure-dev-auth=true
sudo snap set spellbook admin.usernames=admin
sudo snap start spellbook
```

For production, configure OIDC and admin identities instead of `insecure-dev-auth`.

### Defaults

The snap defaults to standalone mode:

- **Database:** SQLite under `$SNAP_COMMON/data/registry.sqlite`
- **Blob storage:** Filesystem under `$SNAP_COMMON/data/blobs/`
- **OCI storage:** Filesystem under `$SNAP_COMMON/data/oci-registry/`
- **OCI TLS:** Self-signed certificate at `$SNAP_COMMON/certs/oci.crt`
- **API TLS:** Disabled by default. Enable with `sudo snap set spellbook tls.enabled=true`

### Service management

Manage the snap by package name. `snap services spellbook` shows the daemon as `spellbook.charm-registry`.

```bash
sudo snap start spellbook
sudo snap stop spellbook
sudo snap restart spellbook
snap services spellbook
```

View service logs and inspect current snap configuration:

```bash
sudo snap logs spellbook.charm-registry
snap get spellbook
snap get spellbook oci.secret-key
```

### Standalone configuration

Standalone mode is the default and uses SQLite plus filesystem storage under `/var/snap/spellbook/common/data/`. Configure the public URLs that Juju, Charmcraft, and OCI clients should use:

```bash
sudo snap set spellbook public-api-url=https://registry.example.com:8080
sudo snap set spellbook public-storage-url=https://registry.example.com:8080
sudo snap set spellbook public-registry-url=https://registry.example.com:5000
```

For local-only experiments, development bearer tokens can be enabled and an admin username bootstrapped before the service starts:

```bash
sudo snap set spellbook insecure-dev-auth=true
sudo snap set spellbook admin.usernames=admin
```

Never enable insecure development auth on a network-reachable deployment.

### Production configuration

Snap configuration uses dotted keys that the wrapper maps to environment variables:

```bash
# Networking
sudo snap set spellbook public-api-url=https://registry.example.com:8080
sudo snap set spellbook public-storage-url=https://registry.example.com:8080
sudo snap set spellbook public-registry-url=https://registry.example.com:5000

# API TLS
sudo snap set spellbook tls.enabled=true
sudo snap set spellbook tls.cert-file=/path/to/registry.crt
sudo snap set spellbook tls.key-file=/path/to/registry.key

# OIDC
sudo snap set spellbook oidc.issuer-url=https://sso.example.com/realms/main
sudo snap set spellbook oidc.client-id=charm-registry

# Admin identities
sudo snap set spellbook admin.usernames=admin
sudo snap set spellbook admin.emails=admin@example.com

# OCI secret key (required)
sudo snap set spellbook oci.secret-key="$(openssl rand -hex 32)"

# Token and IP rate limits
sudo snap set spellbook rate-limit.ip-limit=120
sudo snap set spellbook rate-limit.ip-window=1m
sudo snap set spellbook rate-limit.token-limit=5
sudo snap set spellbook rate-limit.token-window=1m

# Charm archive and upload byte limits
sudo snap set spellbook limits.max-archive-file-bytes=32MB
sudo snap set spellbook limits.max-upload-bytes=128MB
sudo snap set spellbook charmhub.max-artifact-bytes=128MB

# Switch to Postgres
sudo snap set spellbook database.backend=postgres
sudo snap set spellbook database.url='postgres://user:<password>@host:5432/charm_registry?sslmode=require'

# Switch to S3 storage
sudo snap set spellbook storage.backend=s3
sudo snap set spellbook storage.s3.endpoint=https://s3.example.com
sudo snap set spellbook storage.s3.bucket=charm-registry-blobs
sudo snap set spellbook storage.s3.access-key-id=AKIA...
sudo snap set spellbook storage.s3.secret-access-key=...
```

After changing snap configuration, restart the service:

```bash
sudo snap restart spellbook
```

Invoke the packaged admin CLI as `spellbook.charm-registryctl`:

```bash
spellbook.charm-registryctl --url https://registry.example.com:8080 --token '<admin-token>' sync list
spellbook.charm-registryctl sync add postgresql-k8s --track 14
spellbook.charm-registryctl sync run postgresql-k8s
```

Project links:

- Repository: <https://github.com/gregory-schiano/charm-registry>
- Issue tracker: <https://github.com/gregory-schiano/charm-registry/issues>

### Certificate management

The snap's configure hook automatically generates self-signed TLS certificates:

- **API certificate** (`$SNAP_COMMON/certs/registry.crt`) — generated when `tls.enabled=true`, covering the public API/storage URL hostname
- **OCI certificate** (`$SNAP_COMMON/certs/oci.crt`) — generated on install, covering the public and internal OCI registry URLs

Certificates are regenerated when the hostname changes. For production, replace the self-signed certificates with CA-issued certificates:

```bash
sudo snap set spellbook tls.cert-file=/etc/ssl/certs/registry.crt
sudo snap set spellbook tls.key-file=/etc/ssl/private/registry.key
sudo snap set spellbook oci.tls.cert-file=/etc/ssl/certs/oci.crt
sudo snap set spellbook oci.tls.key-file=/etc/ssl/private/oci.key
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

### OCI registry routing (serve at the host root)

`traefik-k8s` per-app ingress routes each application under a path prefix derived from the model and app name (for example `/<model>-charm-registry`). OCI clients — containerd, Docker, `skopeo` — expect a registry at the **host root** (`https://<host>/v2/...`) and cannot be told to use a path prefix. Configure the OCI-facing traefik to route the registry hostname at `/` instead of the default `/<model>-app` prefix (for example with a host-based route and `strip-prefix` disabled, or an external ingress/`IngressRoute` that maps the OCI hostname to the charm's `oci-ingress` at the root). `CHARM_REGISTRY_PUBLIC_REGISTRY_URL` published to clients must resolve to that root; if it still carries the model-app prefix, image pulls and pushes fail.

### Per-IP rate limiting behind ingress

Because the workload only ever sees the ingress as its transport peer, per-IP rate limiting treats the entire fleet as one client unless you tell it which proxies to trust. Set the `trusted-proxies` config option to the ingress/Traefik source range (pod or service CIDR) so the workload honours `X-Forwarded-For`; leave it empty and the whole deployment shares a single rate-limit bucket.

```bash
juju config charm-registry trusted-proxies="10.1.0.0/16"
```

### OCI credential-encryption key

The key that encrypts stored OCI registry credentials defaults to the framework-managed application secret key. This key is required to decrypt existing credentials, so back it up and keep it stable — rotating it (for example via the built-in `rotate-secret-key` action) makes stored OCI credentials unrecoverable. For an explicit, independently managed key, supply a Juju user secret:

```bash
juju add-secret oci-key value=$(openssl rand -hex 32)
juju grant-secret oci-key charm-registry
juju config charm-registry oci-secret-key=<secret-uri>

# Back up the effective key (leader-only action):
juju run charm-registry/leader get-oci-secret-key
```

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
