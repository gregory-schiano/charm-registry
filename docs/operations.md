# Operations

Operational procedures for Charm Registry.

## Database prerequisites (PostgreSQL)

When using a PostgreSQL backend, certain migrations require extensions or
privileges beyond what the application's database role typically holds.

Migrations run automatically on startup, serialized by a blocking Postgres
advisory lock held on a dedicated connection. When several units start
concurrently (for example a multi-unit Juju deployment), the first one runs
the migrations and the others wait for the lock instead of failing.

### pg_trgm extension (migration 0008)

The trigram index on `packages.name` requires the `pg_trgm` extension.
`CREATE EXTENSION` needs one of:

- A superuser connection (common on managed services like RDS, Cloud SQL).
- A role with `pg_database_owner` membership (PostgreSQL ≥ 13, trusted
  extension path — `pg_trgm` is trusted by default).

If neither applies, the migration fails with:

```
ERROR: permission denied to create extension "pg_trgm"
```

**Pre-provisioning (recommended):** run as a superuser before deploying
the version that includes migration 0008:

```sql
CREATE EXTENSION IF NOT EXISTS pg_trgm;
```

The subsequent `CREATE INDEX` only requires table-owner privileges and will
be a safe no-op (via `IF NOT EXISTS`) if the extension is already present.

### Index locking

Migration 0008 creates a GIN index on the `packages` table with a
non-concurrent `CREATE INDEX`. This acquires a `SHARE` lock that blocks
writes for the duration of the build. The migration runner wraps each
migration in a transaction, which is incompatible with `CREATE INDEX
CONCURRENTLY`.

For small-to-medium tables (< 100 k rows) the build is fast and the lock
duration is negligible. For very large tables:

1. Build the index manually with `CONCURRENTLY` during low traffic:

   ```sql
   CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_packages_name_trgm
       ON packages USING gin (name gin_trgm_ops);
   ```

2. Deploy as normal — the migration's `IF NOT EXISTS` clause makes the
   index creation a safe no-op.

## Health checks

| Endpoint | Purpose | What it checks |
|----------|---------|-----------------|
| `GET /healthz` | Liveness | Process is running and handling requests |
| `GET /readyz` | Readiness | Database connectivity (Postgres ping or SQLite open) |

The readiness check does not verify S3 or OCI storage driver health. If S3 is down, `/readyz` may return 200 while artifact uploads and OCI operations fail. This is a known limitation.

## Monitoring

The registry exposes Prometheus metrics at `GET /metrics` on the main API listener. Treat this endpoint as an internal-only operational endpoint: it is intentionally unauthenticated for Prometheus compatibility, so production deployments must keep it off the public internet by binding `CHARM_REGISTRY_LISTEN` to a private interface, firewalling the listener, or allowing `/metrics` only from trusted scrape networks at the ingress/reverse-proxy layer.

The endpoint currently includes the standard Go/process Prometheus collectors and custom process/build metrics such as `charm_registry_build_info` and `charm_registry_go_goroutines`.

The registry also produces structured log output via `slog` (text handler). Every request is logged with:

- Request ID
- Method and path
- Status code
- Response bytes
- Duration in milliseconds
- Remote address

Log level is currently not configurable at runtime. The registry logs at the default level.

### What is not monitored

- **No distributed tracing.** The application does not emit OpenTelemetry spans.
- **No error aggregation.** Errors go to structured logs only, not to Sentry or similar.

For production monitoring, scrape `/metrics` only from trusted networks and scrape logs through an observability stack such as Promtail + Loki.

## Backup and restore

There is no built-in backup infrastructure. Backups must be performed manually — see [Backup and Restore](backup-restore.md) for the full procedures (Postgres, SQLite, S3, filesystem) and the disaster-recovery checklist.

One rule worth repeating here: the registry does not support down-migrations. If a database migration goes wrong, the only way back is the last backup taken before the upgrade, so always take one first.

## Token management

### Listing tokens

```bash
curl -H "Authorization: Bearer $TOKEN" http://localhost:8080/v1/tokens
```

### Revoking a token

```bash
curl -X POST -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"token": "<token-id>"}' \
  http://localhost:8080/v1/tokens/revoke
```

### Token expiry

Store tokens have a `valid_until` timestamp. Expired tokens are rejected at validation time. The default TTL depends on how the token was issued. When creating tokens, set a reasonable TTL and rotate them regularly.

### OCI credential encryption and key rotation

OCI push/pull robot credentials are encrypted at rest with AES-GCM. The encryption key is derived from `CHARM_REGISTRY_OCI_SECRET_KEY` and each stored ciphertext is currently tagged with the `v1` key format.

Treat `CHARM_REGISTRY_OCI_SECRET_KEY` as immutable for a live deployment. The service does not keep previous keys and does not currently implement a versioned re-encryption workflow. Changing the key makes all existing encrypted OCI robot secrets undecryptable, so package OCI authentication will fail until those credentials are regenerated.

If the key must be replaced:

1. Schedule a maintenance window; OCI push/pull operations that depend on existing robot credentials can fail during the transition.
2. Back up the database and OCI/blob storage before changing the key.
3. Change `CHARM_REGISTRY_OCI_SECRET_KEY` and restart the service.
4. Regenerate each package's OCI robot credentials by clearing/re-provisioning the package OCI robot fields or by recreating the affected packages through the supported import/upload flow.
5. Verify every package can return OCI credentials and complete a pull/push against the embedded registry.

A future safe rotation path should store a key version alongside each encrypted robot secret, keep the previous key available during rollout, and re-encrypt stored secrets from the old key version to the new one before retiring the old key.

## Charmhub sync operations

### Checking sync status

```bash
.bin/charm-registryctl sync list
```

### Adding a sync rule

```bash
.bin/charm-registryctl sync add postgresql-k8s --track 14
```

### Narrowing to specific bases/architectures

```bash
.bin/charm-registryctl sync add postgresql-k8s --track 14 --base ubuntu@24.04 --arch amd64
```

### Removing a sync rule

Removing a rule triggers cleanup: artifacts that are no longer referenced by any remaining sync rule are pruned.

```bash
.bin/charm-registryctl sync remove postgresql-k8s --track 14
```

### Forcing an immediate sync

```bash
.bin/charm-registryctl sync run postgresql-k8s
```

### Waiting for all sync rules to complete

Polls until every rule reports `ok`, automatically re-triggering rules that
failed with a transient error (timeouts, connection resets). Fails fast when a
rule hits a permanent error.

```bash
.bin/charm-registryctl sync wait --timeout 30m
```

### Unregistering a charm

This is destructive. It removes the package metadata plus registry-managed charm archives, resource blobs, and OCI project data.

```bash
.bin/charm-registryctl unregister postgresql-k8s --yes
```

### Troubleshooting sync

- Check logs for `Charmhub sync` entries
- Verify `CHARM_REGISTRY_CHARMHUB_URL` is reachable from the registry host
- Verify the admin token has the correct identity and admin privileges
- Individual charm sync failures do not stop the worker from processing other rules — check logs per-package

## Upgrading

1. Take a backup (see above)
2. Stop the registry service
3. Install the new version (binary replacement, rock image pull, or snap refresh)
4. Start the service — database migrations run automatically on startup
5. Check `/healthz` and `/readyz`
6. Verify charm operations: `juju find`, `juju refresh`, `charmcraft upload`

## Certificate management

### OCI registry certificates

Local development uses `make generate-cert` to create self-signed certificates. Install them with `make install-cert` (Debian/Ubuntu) or `make install-k8s-cert` (Canonical k8s snap).

For production, replace the self-signed certificate with a CA-issued certificate. Set `CHARM_REGISTRY_OCI_TLS_CERT_FILE` and `CHARM_REGISTRY_OCI_TLS_KEY_FILE` to the certificate and key paths.

Certificate hot-reload is not supported. To update certificates, restart the service.

### API server certificates

The API server serves HTTPS natively when `CHARM_REGISTRY_API_TLS_CERT_FILE` and `CHARM_REGISTRY_API_TLS_KEY_FILE` are both set (the snap maps `tls.enabled` / `tls.cert-file` / `tls.key-file` onto these). Without them it serves plain HTTP, in which case put a reverse proxy or ingress in front for TLS termination. See [Deployment](deployment.md) for details.
