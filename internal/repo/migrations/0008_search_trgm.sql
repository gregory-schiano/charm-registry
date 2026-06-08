-- 0008: Add pg_trgm extension and trigram index for fast package search.
-- SearchPackages uses ILIKE which is slow on large tables without trigram
-- support. This migration is a no-op on SQLite (pg_trgm is PG-only).
CREATE EXTENSION IF NOT EXISTS pg_trgm;
CREATE INDEX IF NOT EXISTS idx_packages_name_trgm
    ON packages USING gin (name gin_trgm_ops);