# Backup and Restore

This document covers backup and restore procedures for the charm-registry
deployment stack (PostgreSQL, Harbor/S3 blob storage, and application state).

## Overview

| Component | What to back up | Tool | Frequency |
|---|---|---|---|
| PostgreSQL | All databases | `pg_dump` / `pg_restore` | Daily (cron) |
| Blob storage | S3 bucket objects | `aws s3 sync` / rclone | Daily (cron) |
| TLS certificates | `deploy/oci/certs/` directory | File copy | On renewal |
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

## Blob Storage (S3 / Harbor)

### Backup

```bash
# Full bucket sync to local backup directory
aws s3 sync s3://$S3_BUCKET/ s3-backup-$(date +%Y%m%d)/ \
  --endpoint-url $S3_ENDPOINT_URL

# With rclone (for non-AWS S3-compatible stores like MinIO/Harbor)
rclone sync harbor-s3:$S3_BUCKET/ s3-backup-$(date +%Y%mDD)/
```

### Restore

```bash
# Sync backup back to bucket
aws s3 sync s3-backup-YYYYMMDD/ s3://$S3_BUCKET/ \
  --endpoint-url $S3_ENDPOINT_URL
```

## Full Stack Restore Procedure

1. **Stop the application** to prevent writes during restore:

   ```bash
   # Snap deployment:
   sudo snap stop charm-registry
   # Binary deployment: stop the systemd service or send SIGTERM
   ```

2. **Restore PostgreSQL** (see above).

3. **Restore blob storage** (see above).

4. **Verify TLS certificates** are present and valid:

   ```bash
   ls -la deploy/oci/certs/
   openssl x509 -checkend 86400 -noout -in deploy/oci/certs/server.crt
   ```

5. **Restart the application**:

   ```bash
   # Snap deployment:
   sudo snap start charm-registry
   # Binary deployment: start the systemd service
   ```

6. **Smoke-test** the restored instance:

   ```bash
   curl -sf http://localhost:8080/v1/health | jq .
   ```

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
