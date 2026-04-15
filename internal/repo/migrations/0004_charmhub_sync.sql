CREATE TABLE IF NOT EXISTS charmhub_sync_rules (
    package_name TEXT NOT NULL,
    track TEXT NOT NULL,
    created_by_account_id TEXT NOT NULL REFERENCES accounts(id),
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    last_sync_status TEXT NOT NULL,
    last_sync_started_at TIMESTAMPTZ NULL,
    last_sync_finished_at TIMESTAMPTZ NULL,
    last_sync_error TEXT NULL,
    PRIMARY KEY (package_name, track)
);
