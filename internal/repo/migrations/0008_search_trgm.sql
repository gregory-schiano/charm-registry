-- 0008: Add pg_trgm extension and trigram index for fast package search.
-- SearchPackages uses ILIKE which is slow on large tables without trigram
-- support. This migration is a no-op on SQLite (pg_trgm is PG-only).
--
-- PRIVILEGE REQUIREMENT:
--   CREATE EXTENSION requires the database user to be a superuser or a
--   member of the pg_database_owner role (PostgreSQL ≥13 with trusted
--   extensions, pg_trgm is trusted by default on most distributions).
--   If the application's database role lacks this privilege the migration
--   will fail with:
--
--       ERROR: permission denied to create extension "pg_trgm"
--
--   To pre-provision, run as a superuser BEFORE deploying this version:
--
--       CREATE EXTENSION IF NOT EXISTS pg_trgm;
--
--   The CREATE INDEX statement below only requires table owner privileges.
--
-- LOCKING NOTE:
--   CREATE INDEX (without CONCURRENTLY) acquires a SHARE lock on the
--   "packages" table, blocking writes for the duration of the index build.
--   The migration runner wraps this in a transaction, so CONCURRENTLY is
--   not possible here (CONCURRENTLY cannot run inside a transaction block).
--   For small-to-medium package tables (<100k rows) the index builds
--   quickly and the lock duration is negligible.  For very large tables,
--   schedule this during a maintenance window or create the index manually
--   with CONCURRENTLY before deploying:
--
--       CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_packages_name_trgm
--           ON packages USING gin (name gin_trgm_ops);
--
--   The IF NOT EXISTS clause in the migration makes it a safe no-op if
--   the index was already created out-of-band.

CREATE EXTENSION IF NOT EXISTS pg_trgm;
CREATE INDEX IF NOT EXISTS idx_packages_name_trgm
    ON packages USING gin (name gin_trgm_ops);