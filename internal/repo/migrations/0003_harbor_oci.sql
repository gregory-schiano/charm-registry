ALTER TABLE packages
    ADD COLUMN IF NOT EXISTS harbor_project TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS harbor_push_robot_id BIGINT NULL,
    ADD COLUMN IF NOT EXISTS harbor_push_robot_name TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS harbor_push_robot_secret TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS harbor_pull_robot_id BIGINT NULL,
    ADD COLUMN IF NOT EXISTS harbor_pull_robot_name TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS harbor_pull_robot_secret TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS harbor_synced_at TIMESTAMPTZ NULL;
