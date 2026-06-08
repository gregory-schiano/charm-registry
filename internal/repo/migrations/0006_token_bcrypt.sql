ALTER TABLE store_tokens ADD COLUMN IF NOT EXISTS token_prefix TEXT DEFAULT '';
ALTER TABLE store_tokens ADD COLUMN IF NOT EXISTS token_hash_scheme TEXT NOT NULL DEFAULT 'sha256';

-- Index for prefix-based lookup of bcrypt-hashed tokens.
CREATE INDEX IF NOT EXISTS idx_store_tokens_prefix ON store_tokens(token_prefix) WHERE token_prefix != '';
