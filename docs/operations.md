# Operations

Operational procedures for Charm Registry.

## Database prerequisites (PostgreSQL)

When using a PostgreSQL backend, certain migrations require extensions or
privileges beyond what the application's database role typically holds.

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

The registry produces structured log output via `slog` (text handler). Every request is logged with:

- Request ID
- Method and path
- Status code
- Response bytes
- Duration in milliseconds
- Remote address

Log level is currently not configurable at runtime. The registry logs at the default level.

### What is not monitored

- **No `/metrics` endpoint.** There is no Prometheus metrics endpoint. This is a known gap.
- **No distributed tracing.** The application does not emit OpenTelemetry spans.
- **No error aggregation.** Errors go to structured logs only, not to Sentry or similar.

For production monitoring, scrape the logs or put an observability stack in front of the registry (e.g., Promtail + Loki for log aggregation, or an HTTP metrics exporter as a sidecar).

## Backup and restore

There is no built-in backup infrastructure. Backups must be performed manually.

### Postgres backup

```bash
pg_dump -Fc -f charm-registry-$(date +%Y%m%d).dump charm_registry
```

### Postgres restore

```bash
pg_restore -d charm_registry charm-registry-YYYYMMDD.dump
```

### S3 backup

For S3-compatible storage, use your provider's bucket versioning, replication, or snapshot features. With MinIO:

```bash
mc mirror local/charm-registry-blobs /backup/charm-registry-blobs/
mc mirror local/charm-registry-oci /backup/charm-registry-oci/
```

### SQLite backup

```bash
sqlite3 "$CHARM_REGISTRY_DATA_DIR/registry.sqlite" ".backup '$CHARM_REGISTRY_DATA_DIR/registry.sqlite.bak'"
```

Or stop the service, copy the file, and restart.

### Filesystem backup

```bash
tar czf charm-registry-data-$(date +%Y%m%d).tar.gz -C "$CHARM_REGISTRY_DATA_DIR" .
```

### Restoration procedure

1. Stop the registry service
2. Restore the database from backup
3. Restore blob/OCI storage from backup
4. Verify file permissions and ownership
5. Start the registry service
6. Check `/healthz` and `/readyz`
7. Test a package download and a `juju refresh` against the restored service

### Migration rollback

If a database migration goes wrong, restore from the last backup taken before the migration. The registry does not support down-migrations. Always take a backup before upgrading.

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

### OCI credential rotation

OCI push/pull credentials are deterministic HMAC-derived from `CHARM_REGISTRY_OCI_SECRET_KEY`. There is no per-credential rotation mechanism. Changing `CHARM_REGISTRY_OCI_SECRET_KEY` invalidates all existing OCI credentials with no migration path — every package's OCI robot accounts must be re-provisioned.

To rotate:

1. Pick a maintenance window (OCI operations will fail during transition)
2. Change `CHARM_REGISTRY_OCI_SECRET_KEY`
3. Restart the service
4. Re-provision OCI projects by triggering a re-sync or re-upload for each package

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

The API server does not natively support TLS. Use a reverse proxy or the snap wrapper's TLS support for API TLS termination. See [Deployment](deployment.md) for details.
