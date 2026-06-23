# Configuration

All configuration is through environment variables. The application validates required settings at startup and refuses to start if critical configuration is missing.

## Quick reference

### Core

| Variable | Default | Description |
|----------|---------|-------------|
| `CHARM_REGISTRY_LISTEN` | `:8080` | API server listen address |
| `CHARM_REGISTRY_PUBLIC_API_URL` | `http://localhost:8080` | URL handed to clients for API endpoints |
| `CHARM_REGISTRY_PUBLIC_STORAGE_URL` | `http://localhost:8080` | URL handed to clients for artifact download |
| `CHARM_REGISTRY_PUBLIC_REGISTRY_URL` | `https://localhost:5000` | URL handed to clients for OCI registry |
| `CHARM_REGISTRY_API_TLS_CERT_FILE` | — | TLS certificate for API listener (set together with key) |
| `CHARM_REGISTRY_API_TLS_KEY_FILE` | — | TLS key for API listener (set together with cert) |

### Database

| Variable | Default | Description |
|----------|---------|-------------|
| `CHARM_REGISTRY_DATABASE_BACKEND` | `auto` | `auto`, `postgres`, or `sqlite` |
| `CHARM_REGISTRY_DATABASE_URL` | — | Postgres connection string. Required when backend resolves to `postgres`. |
| `CHARM_REGISTRY_DATA_DIR` | `data` | Base directory for local storage |
| `CHARM_REGISTRY_SQLITE_PATH` | `<data_dir>/registry.sqlite` | SQLite database file path |

In `auto` mode, the registry uses Postgres when `CHARM_REGISTRY_DATABASE_URL` is set, otherwise SQLite.

The `CHARM_REGISTRY_DATABASE_URL` variable also accepts the legacy alias `POSTGRESQL_DB_CONNECT_STRING`.

### Blob storage

| Variable | Default | Description |
|----------|---------|-------------|
| `CHARM_REGISTRY_STORAGE_BACKEND` | `auto` | `auto`, `s3`, or `filesystem` |
| `CHARM_REGISTRY_BLOB_DIR` | `<data_dir>/blobs` | Filesystem blob directory |
| `CHARM_REGISTRY_S3_BUCKET` | `charm-registry` | S3 bucket for charm/resource artifacts |
| `CHARM_REGISTRY_S3_REGION` | `us-east-1` | S3 region |
| `CHARM_REGISTRY_S3_ENDPOINT` | — | S3 endpoint URL (leave empty for AWS) |
| `CHARM_REGISTRY_S3_ACCESS_KEY_ID` | — | S3 access key |
| `CHARM_REGISTRY_S3_SECRET_ACCESS_KEY` | — | S3 secret key |
| `CHARM_REGISTRY_S3_USE_PATH_STYLE` | `true` | Use path-style S3 URLs (required for MinIO) |
| `CHARM_REGISTRY_S3_DISABLE_TLS` | `false` | Disable TLS for S3 connections (development only) |

In `auto` mode, the registry uses S3 when `CHARM_REGISTRY_S3_ENDPOINT` or credentials are set, otherwise filesystem.

### OCI registry

| Variable | Default | Description |
|----------|---------|-------------|
| `CHARM_REGISTRY_OCI_LISTEN` | `:5000` | Embedded OCI registry listen address |
| `CHARM_REGISTRY_OCI_INTERNAL_URL` | `https://127.0.0.1:5000` | URL the service uses to push into its own OCI registry |
| `CHARM_REGISTRY_OCI_STORAGE_BACKEND` | `auto` | `auto`, `s3`, or `filesystem` for OCI blobs |
| `CHARM_REGISTRY_OCI_STORAGE_DIR` | `<data_dir>/oci-registry` | Filesystem OCI storage directory |
| `CHARM_REGISTRY_OCI_S3_BUCKET` | `charm-registry-oci` | S3 bucket for OCI blobs |
| `CHARM_REGISTRY_OCI_S3_PREFIX` | `oci` | S3 key prefix for OCI blobs |
| `CHARM_REGISTRY_OCI_S3_REGION` | (same as S3 region) | S3 region for OCI blobs |
| `CHARM_REGISTRY_OCI_S3_ENDPOINT` | (same as S3 endpoint) | S3 endpoint for OCI blobs |
| `CHARM_REGISTRY_OCI_S3_ACCESS_KEY` | (same as S3 access key) | S3 access key for OCI blobs |
| `CHARM_REGISTRY_OCI_S3_SECRET_KEY` | (same as S3 secret key) | S3 secret key for OCI blobs |
| `CHARM_REGISTRY_OCI_S3_USE_PATH_STYLE` | (same as `S3_USE_PATH_STYLE`) | Use path-style S3 URLs for OCI blobs |
| `CHARM_REGISTRY_OCI_SECRET_KEY` | — | **Required.** Encryption key for OCI credentials at rest; treat as immutable after credentials are created. Changing it makes existing encrypted OCI robot secrets undecryptable until regenerated. |
| `CHARM_REGISTRY_OCI_PROJECT_PREFIX` | `charm` | OCI project name prefix |
| `CHARM_REGISTRY_OCI_PULL_ROBOT_PREFIX` | `pull` | OCI pull robot account prefix |
| `CHARM_REGISTRY_OCI_PUSH_ROBOT_PREFIX` | `push` | OCI push robot account prefix |
| `CHARM_REGISTRY_OCI_TLS_CERT_FILE` | — | TLS certificate for OCI listener (set together with key) |
| `CHARM_REGISTRY_OCI_TLS_KEY_FILE` | — | TLS key for OCI listener (set together with cert) |
| `CHARM_REGISTRY_OCI_MAX_MANIFEST_BYTES` | `16777216` (16 MiB) | Max OCI manifest size |

OCI S3 variables fall back to the main S3 variables when not set explicitly.

### Authentication

| Variable | Default | Description |
|----------|---------|-------------|
| `CHARM_REGISTRY_OIDC_ISSUER_URL` | — | OIDC issuer URL (must be set together with client ID) |
| `CHARM_REGISTRY_OIDC_CLIENT_ID` | — | OIDC client ID (must be set together with issuer URL) |
| `CHARM_REGISTRY_OIDC_USERNAME_CLAIM` | `preferred_username` | OIDC claim for username |
| `CHARM_REGISTRY_OIDC_DISPLAY_NAME_CLAIM` | `name` | OIDC claim for display name |
| `CHARM_REGISTRY_OIDC_EMAIL_CLAIM` | `email` | OIDC claim for email |
| `CHARM_REGISTRY_ADMIN_SUBJECTS` | — | Comma-separated OIDC subjects with admin access |
| `CHARM_REGISTRY_ADMIN_EMAILS` | — | Comma-separated emails with admin access |
| `CHARM_REGISTRY_ADMIN_USERNAMES` | — | Comma-separated usernames with admin access |
| `CHARM_REGISTRY_ENABLE_INSECURE_DEV_AUTH` | `false` | Accept insecure dev bearer tokens. **Never use in production.** |

The application requires either OIDC configuration or explicit opt-in to insecure dev auth. If neither is set, it refuses to start.

### Charmhub sync

| Variable | Default | Description |
|----------|---------|-------------|
| `CHARM_REGISTRY_CHARMHUB_URL` | `https://api.charmhub.io` | Upstream Charmhub API base URL |
| `CHARM_REGISTRY_CHARMHUB_SYNC_INTERVAL` | `15m` | Background sync scan interval |
| `CHARM_REGISTRY_CHARMHUB_MAX_RESPONSE_BYTES` | `4194304` (4 MiB) | Max Charmhub API response size |
| `CHARM_REGISTRY_CHARMHUB_MAX_ARTIFACT_BYTES` | (same as `MAX_UPLOAD_BYTES`) | Max Charmhub artifact download size |

### Server tuning

| Variable | Default | Description |
|----------|---------|-------------|
| `CHARM_REGISTRY_REQUEST_TIMEOUT` | `30s` | Per-request router timeout; raise this for large or slow upload/download operations |
| `CHARM_REGISTRY_SERVER_READ_HEADER_TIMEOUT` | `10s` | HTTP read header timeout |
| `CHARM_REGISTRY_SERVER_READ_TIMEOUT` | `30s` | HTTP read timeout |
| `CHARM_REGISTRY_SERVER_WRITE_TIMEOUT` | `30s` | HTTP write timeout |
| `CHARM_REGISTRY_SERVER_IDLE_TIMEOUT` | `2m` | HTTP idle timeout |
| `CHARM_REGISTRY_SERVER_SHUTDOWN_TIMEOUT` | `30s` | Graceful shutdown timeout |
| `CHARM_REGISTRY_SERVER_MAX_HEADER_BYTES` | `1048576` (1 MiB) | Max request header size |
| `CHARM_REGISTRY_MAX_JSON_BODY_BYTES` | `1048576` (1 MiB) | Max JSON request body size |
| `CHARM_REGISTRY_MAX_ARCHIVE_FILE_BYTES` | `10485760` (10 MiB) | Max per-entry decompressed charm archive size |
| `CHARM_REGISTRY_MAX_UPLOAD_BYTES` | `67108864` (64 MiB) | Max upload body size |

The embedded OCI registry server has its own timeouts. Read and write timeouts default to `0` (disabled) so that large image blob transfers are never truncated:

| Variable | Default | Description |
|----------|---------|-------------|
| `CHARM_REGISTRY_OCI_SERVER_READ_HEADER_TIMEOUT` | `10s` | OCI server read header timeout |
| `CHARM_REGISTRY_OCI_SERVER_READ_TIMEOUT` | `0` (disabled) | OCI server read timeout |
| `CHARM_REGISTRY_OCI_SERVER_WRITE_TIMEOUT` | `0` (disabled) | OCI server write timeout |
| `CHARM_REGISTRY_OCI_SERVER_IDLE_TIMEOUT` | `5m` | OCI server idle timeout |

### Rate limiting

| Variable | Default | Description |
|----------|---------|-------------|
| `CHARM_REGISTRY_IP_RATE_LIMIT` | `30` | Max requests per IP per window; `0` disables IP limiting |
| `CHARM_REGISTRY_IP_RATE_WINDOW` | `1m` | Sliding window for the per-IP limit |
| `CHARM_REGISTRY_TOKEN_RATE_LIMIT` | `5` | Max token issuances per identity per window; `0` disables |
| `CHARM_REGISTRY_TOKEN_RATE_WINDOW` | `1m` | Sliding window for the token issuance limit |

The per-IP limit applies to unauthenticated traffic and keys on the client address as seen behind trusted proxy headers. If charmcraft sessions hit `429` responses during busy uploads, raise the token limit.

### Legacy variable aliases

Many variables accept legacy aliases for backward compatibility:

| Primary variable | Legacy alias |
|------------------|-------------|
| `CHARM_REGISTRY_DATABASE_URL` | `POSTGRESQL_DB_CONNECT_STRING` |
| `CHARM_REGISTRY_S3_ENDPOINT` | `S3_ENDPOINT` |
| `CHARM_REGISTRY_S3_ACCESS_KEY_ID` | `S3_ACCESS_KEY` |
| `CHARM_REGISTRY_S3_SECRET_ACCESS_KEY` | `S3_SECRET_KEY` |
| `CHARM_REGISTRY_OCI_S3_ENDPOINT` | `APP_OCI_S3_ENDPOINT` |
| `CHARM_REGISTRY_OCI_S3_ACCESS_KEY` | `APP_OCI_S3_ACCESS_KEY` |
| `CHARM_REGISTRY_OCI_S3_SECRET_KEY` | `APP_OCI_S3_SECRET_KEY` |
| `CHARM_REGISTRY_OCI_S3_BUCKET` | `APP_OCI_S3_BUCKET` |
| `CHARM_REGISTRY_OCI_S3_PREFIX` | `APP_OCI_S3_PREFIX` |
| `CHARM_REGISTRY_OIDC_ISSUER_URL` | `API_BASE_URL` (via oauth helper) |
| `CHARM_REGISTRY_OIDC_CLIENT_ID` | `CLIENT_ID` (via oauth helper) |
| `CHARM_REGISTRY_ADMIN_SUBJECTS` | `APP_ADMIN_SUBJECTS` |
| `CHARM_REGISTRY_ADMIN_EMAILS` | `APP_ADMIN_EMAILS` |
| `CHARM_REGISTRY_ADMIN_USERNAMES` | `APP_ADMIN_USERNAMES` |
| `CHARM_REGISTRY_OCI_LISTEN` | `APP_OCI_LISTEN` |
| `CHARM_REGISTRY_OCI_INTERNAL_URL` | `APP_OCI_INTERNAL_URL` |
| `CHARM_REGISTRY_OCI_SECRET_KEY` | `APP_SECRET_KEY` |
| `CHARM_REGISTRY_OCI_PROJECT_PREFIX` | `APP_OCI_PROJECT_PREFIX` |
| `CHARM_REGISTRY_OCI_PULL_ROBOT_PREFIX` | `APP_OCI_PULL_ROBOT_PREFIX` |
| `CHARM_REGISTRY_OCI_PUSH_ROBOT_PREFIX` | `APP_OCI_PUSH_ROBOT_PREFIX` |
| `CHARM_REGISTRY_CHARMHUB_URL` | `APP_CHARMHUB_URL` |
| `CHARM_REGISTRY_CHARMHUB_SYNC_INTERVAL` | `APP_CHARMHUB_SYNC_INTERVAL` |
| `CHARM_REGISTRY_PUBLIC_API_URL` | `APP_PUBLIC_API_URL` |
| `CHARM_REGISTRY_PUBLIC_STORAGE_URL` | `APP_PUBLIC_STORAGE_URL` |
| `CHARM_REGISTRY_PUBLIC_REGISTRY_URL` | `APP_PUBLIC_REGISTRY_URL` |
| `CHARM_REGISTRY_ENABLE_INSECURE_DEV_AUTH` | `APP_ENABLE_INSECURE_DEV_AUTH` |
| `CHARM_REGISTRY_S3_USE_PATH_STYLE` | `APP_S3_USE_PATH_STYLE` |
| `CHARM_REGISTRY_OCI_S3_USE_PATH_STYLE` | `APP_OCI_S3_USE_PATH_STYLE` |

## Snap configuration

When running as a snap, the wrapper script (`snap/local/charm-registry-wrapper`) reads snap configuration keys via `snapctl get` and maps them to environment variables. For example:

```bash
sudo snap set spellbook public-api-url=https://registry.example.com:8080
sudo snap set spellbook tls.enabled=true
sudo snap set spellbook oidc.issuer-url=https://sso.example.com/realms/main
sudo snap set spellbook oidc.client-id=charm-registry
sudo snap set spellbook oci.secret-key="$(openssl rand -hex 32)"
```

Snap keys use dotted paths that correspond to the environment variable names. Supported snap options include:

| Snap option | Environment variable | Value |
|---|---|---|
| `listen` | `CHARM_REGISTRY_LISTEN` | API listen address, for example `:8080` |
| `oci.listen` | `CHARM_REGISTRY_OCI_LISTEN` | OCI listen address, for example `:5000` |
| `public-api-url` | `CHARM_REGISTRY_PUBLIC_API_URL` | Public API base URL |
| `public-storage-url` | `CHARM_REGISTRY_PUBLIC_STORAGE_URL` | Public storage base URL |
| `public-registry-url` | `CHARM_REGISTRY_PUBLIC_REGISTRY_URL` | Public OCI registry base URL |
| `tls.enabled` | Enables `CHARM_REGISTRY_API_TLS_CERT_FILE` / `CHARM_REGISTRY_API_TLS_KEY_FILE` | Boolean (`true` / `false`) |
| `tls.cert-file` | `CHARM_REGISTRY_API_TLS_CERT_FILE` | API TLS certificate path |
| `tls.key-file` | `CHARM_REGISTRY_API_TLS_KEY_FILE` | API TLS private key path |
| `oci.internal-url` | `CHARM_REGISTRY_OCI_INTERNAL_URL` | Internal OCI URL used by the API |
| `database.backend` | `CHARM_REGISTRY_DATABASE_BACKEND` | `sqlite` or `postgres` |
| `database.url` | `CHARM_REGISTRY_DATABASE_URL` | Postgres connection URL |
| `storage.backend` | `CHARM_REGISTRY_STORAGE_BACKEND` | `filesystem` or `s3` |
| `storage.s3.endpoint` | `CHARM_REGISTRY_S3_ENDPOINT` | S3 endpoint URL |
| `storage.s3.bucket` | `CHARM_REGISTRY_S3_BUCKET` | S3 bucket name |
| `storage.s3.region` | `CHARM_REGISTRY_S3_REGION` | S3 region |
| `storage.s3.access-key-id` | `CHARM_REGISTRY_S3_ACCESS_KEY_ID` | S3 access key ID |
| `storage.s3.secret-access-key` | `CHARM_REGISTRY_S3_SECRET_ACCESS_KEY` | S3 secret access key |
| `storage.s3.use-path-style` | `CHARM_REGISTRY_S3_USE_PATH_STYLE` | Boolean |
| `storage.s3.disable-tls` | `CHARM_REGISTRY_S3_DISABLE_TLS` | Boolean |
| `oci.storage.backend` | `CHARM_REGISTRY_OCI_STORAGE_BACKEND` | `filesystem` or `s3` |
| `oci.storage.s3.endpoint` | `CHARM_REGISTRY_OCI_S3_ENDPOINT` | OCI S3 endpoint URL |
| `oci.storage.s3.bucket` | `CHARM_REGISTRY_OCI_S3_BUCKET` | OCI S3 bucket name |
| `oci.storage.s3.prefix` | `CHARM_REGISTRY_OCI_S3_PREFIX` | OCI S3 key prefix |
| `oci.storage.s3.region` | `CHARM_REGISTRY_OCI_S3_REGION` | OCI S3 region |
| `oci.storage.s3.access-key` | `CHARM_REGISTRY_OCI_S3_ACCESS_KEY` | OCI S3 access key |
| `oci.storage.s3.secret-key` | `CHARM_REGISTRY_OCI_S3_SECRET_KEY` | OCI S3 secret key |
| `oci.storage.s3.use-path-style` | `CHARM_REGISTRY_OCI_S3_USE_PATH_STYLE` | Boolean |
| `oci.secret-key` | `CHARM_REGISTRY_OCI_SECRET_KEY` | Secret used to encrypt OCI credentials |
| `oci.tls.cert-file` | `CHARM_REGISTRY_OCI_TLS_CERT_FILE` | OCI TLS certificate path |
| `oci.tls.key-file` | `CHARM_REGISTRY_OCI_TLS_KEY_FILE` | OCI TLS private key path |
| `oidc.issuer-url` | `CHARM_REGISTRY_OIDC_ISSUER_URL` | OIDC issuer URL |
| `oidc.client-id` | `CHARM_REGISTRY_OIDC_CLIENT_ID` | OIDC client ID |
| `oidc.username-claim` | `CHARM_REGISTRY_OIDC_USERNAME_CLAIM` | OIDC username claim name |
| `oidc.display-name-claim` | `CHARM_REGISTRY_OIDC_DISPLAY_NAME_CLAIM` | OIDC display-name claim name |
| `oidc.email-claim` | `CHARM_REGISTRY_OIDC_EMAIL_CLAIM` | OIDC email claim name |
| `admin.subjects` | `CHARM_REGISTRY_ADMIN_SUBJECTS` | Comma-separated admin OIDC subjects |
| `admin.emails` | `CHARM_REGISTRY_ADMIN_EMAILS` | Comma-separated admin email addresses |
| `admin.usernames` | `CHARM_REGISTRY_ADMIN_USERNAMES` | Comma-separated admin usernames |
| `insecure-dev-auth` | `CHARM_REGISTRY_ENABLE_INSECURE_DEV_AUTH` | Boolean; development only |
| `charmhub.url` | `CHARM_REGISTRY_CHARMHUB_URL` | Charmhub API URL |
| `charmhub.sync-interval` | `CHARM_REGISTRY_CHARMHUB_SYNC_INTERVAL` | Sync interval duration |
| `charmhub.max-artifact-bytes` | `CHARM_REGISTRY_CHARMHUB_MAX_ARTIFACT_BYTES` | Positive integer byte limit for synchronized Charmhub artifact downloads |
| `limits.max-archive-file-bytes` | `CHARM_REGISTRY_MAX_ARCHIVE_FILE_BYTES` | Positive integer byte limit for each decompressed charm archive entry |
| `limits.max-upload-bytes` | `CHARM_REGISTRY_MAX_UPLOAD_BYTES` | Positive integer byte limit for upload request bodies |
| `rate-limit.ip-limit` | `CHARM_REGISTRY_IP_RATE_LIMIT` | Non-negative integer; `0` disables IP limiting |
| `rate-limit.ip-window` | `CHARM_REGISTRY_IP_RATE_WINDOW` | Positive duration such as `30s`, `1m`, or `2h` |
| `rate-limit.token-limit` | `CHARM_REGISTRY_TOKEN_RATE_LIMIT` | Non-negative integer; `0` disables token issuance limiting |
| `rate-limit.token-window` | `CHARM_REGISTRY_TOKEN_RATE_WINDOW` | Positive duration such as `30s`, `1m`, or `2h` |

The configure hook rejects invalid rate-limit values before snapd stores them: limits must be non-negative integers, and windows must be positive durations.

The wrapper keeps the full mapping in `snap/local/config-env.map` and packages
that file as `$SNAP/etc/charm-registry/config-env.map`. Add or review snap
configuration keys there instead of appending ad-hoc `snapctl get` calls to the
wrapper; this keeps mappings centralized and makes key/env typos (including TLS
certificate variables) visible in one table.
