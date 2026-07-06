-- 0009: Store the SHA-512 digest of uploads so resource publishing can reuse
-- it instead of re-reading and re-hashing the whole blob.
ALTER TABLE uploads ADD COLUMN IF NOT EXISTS sha512 TEXT NOT NULL DEFAULT '';
