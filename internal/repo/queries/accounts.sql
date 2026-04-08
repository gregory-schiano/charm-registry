-- name: EnsureAccount :one
INSERT INTO accounts (id, subject, username, display_name, email, validation, is_admin, created_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
ON CONFLICT (subject) DO UPDATE SET
    username     = EXCLUDED.username,
    display_name = EXCLUDED.display_name,
    email        = EXCLUDED.email,
    validation   = EXCLUDED.validation,
    is_admin     = EXCLUDED.is_admin
RETURNING id, subject, username, display_name, email, validation, is_admin, created_at;

-- name: GetAccountByID :one
SELECT id, subject, username, display_name, email, validation, is_admin, created_at
FROM accounts
WHERE id = $1;

-- name: CreateStoreToken :exec
INSERT INTO store_tokens (
    session_id, token_hash, account_id, description,
    packages, channels, permissions,
    valid_since, valid_until, revoked_at, revoked_by
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11);

-- name: ListActiveStoreTokens :many
SELECT session_id, token_hash, account_id, description,
       packages, channels, permissions,
       valid_since, valid_until, revoked_at, revoked_by
FROM store_tokens
WHERE account_id = $1
  AND revoked_at IS NULL
  AND valid_until > NOW()
ORDER BY valid_since ASC;

-- name: ListAllStoreTokens :many
SELECT session_id, token_hash, account_id, description,
       packages, channels, permissions,
       valid_since, valid_until, revoked_at, revoked_by
FROM store_tokens
WHERE account_id = $1
ORDER BY valid_since ASC;

-- name: RevokeStoreToken :execrows
UPDATE store_tokens
SET revoked_at = NOW(),
    revoked_by = $3
WHERE account_id = $1
  AND session_id = $2;

-- name: FindStoreTokenByHash :one
SELECT
    t.session_id, t.token_hash, t.account_id, t.description,
    t.packages, t.channels, t.permissions,
    t.valid_since, t.valid_until, t.revoked_at, t.revoked_by,
    a.id          AS acc_id,
    a.subject     AS acc_subject,
    a.username    AS acc_username,
    a.display_name AS acc_display_name,
    a.email       AS acc_email,
    a.validation  AS acc_validation,
    a.is_admin    AS acc_is_admin,
    a.created_at  AS acc_created_at
FROM store_tokens t
JOIN accounts a ON a.id = t.account_id
WHERE t.token_hash = $1;
