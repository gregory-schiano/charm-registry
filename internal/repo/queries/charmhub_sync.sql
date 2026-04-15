-- name: CreateCharmhubSyncRule :exec
INSERT INTO charmhub_sync_rules (
    package_name, track, created_by_account_id, created_at, updated_at,
    last_sync_status, last_sync_started_at, last_sync_finished_at, last_sync_error
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9);

-- name: DeleteCharmhubSyncRule :execrows
DELETE FROM charmhub_sync_rules
WHERE package_name = $1
  AND track = $2;

-- name: ListCharmhubSyncRules :many
SELECT package_name, track, created_by_account_id, created_at, updated_at,
       last_sync_status, last_sync_started_at, last_sync_finished_at, last_sync_error
FROM charmhub_sync_rules
ORDER BY package_name ASC, track ASC;

-- name: ListCharmhubSyncRulesByPackageName :many
SELECT package_name, track, created_by_account_id, created_at, updated_at,
       last_sync_status, last_sync_started_at, last_sync_finished_at, last_sync_error
FROM charmhub_sync_rules
WHERE package_name = $1
ORDER BY track ASC;

-- name: UpdateCharmhubSyncRule :execrows
UPDATE charmhub_sync_rules
SET updated_at = $3,
    last_sync_status = $4,
    last_sync_started_at = $5,
    last_sync_finished_at = $6,
    last_sync_error = $7
WHERE package_name = $1
  AND track = $2;
