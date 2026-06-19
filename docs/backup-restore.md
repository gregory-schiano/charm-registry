# Backup and Restore

This document covers backup and restore procedures for the charm-registry
deployment stack: the database (PostgreSQL or SQLite), blob storage
(S3-compatible or filesystem), TLS certificates, and application configuration.

## Overview

| Component | What to back up | Tool | Frequency |
|---|---|---|---|
| PostgreSQL | All databases | `pg_dump` / `pg_restore` | Daily (cron) |
| SQLite | `registry.sqlite` | `sqlite3 .backup` | Daily (cron) |
| Blob storage (S3) | Bucket objects | `aws s3 sync` / rclone | Daily (cron) |
| Blob storage (filesystem) | `$CHARM_REGISTRY_DATA_DIR` | `tar` | Daily (cron) |
| TLS certificates | `certs/` (local) or `$SNAP_COMMON/certs/` (snap) | File copy | On renewal |
| Application config | `.env` / snap config | File copy | On change |

## PostgreSQL

### Backup

```bash
# Full custom-format backup (recommended — supports parallel restore)
pg_dump -Fc -h $PGHOST -U $PGUSER -d $PGDATABASE -f registry_$(date +%Y%m%d).dump

# Plain SQL backup (human-readable, but no parallel restore)
pg_dump -h $PGHOST -U $PGUSER -d $PGDATABASE -f registry_$(date +%Y%m%d).sql
```

### Restore

Custom-format restore:

```bash
pg_restore -h $PGHOST -U $PGUSER -d $PGDATABASE \
  --clean --if-exists registry_YYYYMMDD.dump
```

Plain SQL restore:

```bash
psql -h $PGHOST -U $PGUSER -d $PGDATABASE < registry_YYYYMMDD.sql
```

> **Note:** `--clean --if-exists` drops existing objects before restoring.
> Omit these flags if you want to restore into a fresh database.

## SQLite

Back up with SQLite's online backup command (safe while the service is running):

```bash
sqlite3 "$CHARM_REGISTRY_DATA_DIR/registry.sqlite" \
  ".backup '$CHARM_REGISTRY_DATA_DIR/registry.sqlite.bak'"
```

Or stop the service, copy the file, and restart. To restore, stop the service
and copy the backup file back into place.

## Blob Storage (S3-compatible)

### Backup

```bash
# Full bucket sync to local backup directory
aws s3 sync s3://$S3_BUCKET/ s3-backup-$(date +%Y%m%d)/ \
  --endpoint-url $S3_ENDPOINT_URL

# With rclone (for non-AWS S3-compatible stores like MinIO)
rclone sync s3:$S3_BUCKET/ s3-backup-$(date +%Y%m%d)/
```

Remember the embedded OCI registry has its own bucket (or key prefix) — back up
both the charm-blob bucket and the OCI storage bucket.

### Restore

```bash
# Sync backup back to bucket
aws s3 sync s3-backup-YYYYMMDD/ s3://$S3_BUCKET/ \
  --endpoint-url $S3_ENDPOINT_URL
```

## Blob Storage (filesystem)

For filesystem-backed deployments, the data directory holds the SQLite
database, charm blobs, and OCI storage together:

```bash
tar czf charm-registry-data-$(date +%Y%m%d).tar.gz -C "$CHARM_REGISTRY_DATA_DIR" .
```

For the snap, the data directory is `/var/snap/spellbook/common/data/`.

## Full Stack Restore Procedure

1. **Stop the application** to prevent writes during restore:

   ```bash
   # Snap deployment:
   sudo snap stop spellbook
   # Binary deployment: stop the systemd service or send SIGTERM
   ```

2. **Restore PostgreSQL** (see above).

3. **Restore blob storage** (see above).

4. **Verify TLS certificates** are present and valid (paths depend on the
   deployment: `certs/` for a local checkout, `$SNAP_COMMON/certs/` for the snap):

   ```bash
   openssl x509 -checkend 86400 -noout -in /var/snap/spellbook/common/certs/oci.crt
   ```

5. **Restart the application**:

   ```bash
   # Snap deployment:
   sudo snap start spellbook
   # Binary deployment: start the systemd service
   ```

6. **Smoke-test** the restored instance:

   ```bash
   curl -sf http://localhost:8080/healthz
   curl -sf http://localhost:8080/readyz
   ```

   Then test a package download and a `juju refresh` against the restored
   service.

## Automated Backup (Cron)

Example crontab for daily 03:00 UTC backups with 30-day retention:

```cron
0 3 * * * pg_dump -Fc -h $PGHOST -U $PGUSER -d $PGDATABASE -f /var/backups/registry/registry_$(date +\%Y\%m\%d).dump
0 4 * * * aws s3 sync s3://$S3_BUCKET/ /var/backups/registry/s3-$(date +\%Y\%m\%d)/ --endpoint-url $S3_ENDPOINT_URL
0 5 * * * find /var/backups/registry/ -mtime +30 -delete
```

## Disaster Recovery Checklist

- [ ] PostgreSQL backup verified (`pg_restore --list` shows tables)
- [ ] S3 backup verified (object count matches source)
- [ ] TLS certificates backed up separately
- [ ] `.env` / snap configuration backed up
- [ ] Restore procedure tested on a fresh deployment at least once per quarter
