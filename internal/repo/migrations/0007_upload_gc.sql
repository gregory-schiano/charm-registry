-- 0007: Add created_by_account_id to uploads for garbage-collection accountability.
ALTER TABLE uploads ADD COLUMN IF NOT EXISTS created_by_account_id TEXT;

-- Create an index to support efficient cleanup queries on stale uploads.
CREATE INDEX IF NOT EXISTS idx_uploads_status_created_at
    ON uploads (status, created_at);