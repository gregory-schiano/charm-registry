ALTER TABLE charmhub_sync_rules
    ADD COLUMN IF NOT EXISTS bases JSONB NOT NULL DEFAULT '[]'::jsonb,
    ADD COLUMN IF NOT EXISTS architectures JSONB NOT NULL DEFAULT '[]'::jsonb;

ALTER TABLE releases
    ADD COLUMN IF NOT EXISTS base_key TEXT GENERATED ALWAYS AS (base::text) STORED;

ALTER TABLE releases
    DROP CONSTRAINT IF EXISTS releases_package_id_channel_key;

CREATE UNIQUE INDEX IF NOT EXISTS releases_package_id_channel_base_key_idx
    ON releases (package_id, channel, base_key);
