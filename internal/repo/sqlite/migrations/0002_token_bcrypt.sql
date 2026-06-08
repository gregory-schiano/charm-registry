ALTER TABLE store_tokens ADD COLUMN token_prefix TEXT DEFAULT '';
ALTER TABLE store_tokens ADD COLUMN token_hash_scheme TEXT NOT NULL DEFAULT 'sha256';

CREATE INDEX idx_store_tokens_prefix ON store_tokens(token_prefix) WHERE token_prefix != '';