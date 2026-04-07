-- name: CreatePackage :exec
INSERT INTO packages (
    id, name, type, private, status, owner_account_id,
    authority, contact, default_track,
    description, summary, title, website,
    links, media, track_guardrails,
    created_at, updated_at
) VALUES (
    $1, $2, $3, $4, $5, $6,
    $7, $8, $9,
    $10, $11, $12, $13,
    $14, $15, $16,
    $17, $18
);

-- name: UpdatePackage :execrows
UPDATE packages SET
    private          = $2,
    status           = $3,
    authority        = $4,
    contact          = $5,
    default_track    = $6,
    description      = $7,
    summary          = $8,
    title            = $9,
    website          = $10,
    links            = $11,
    media            = $12,
    track_guardrails = $13,
    updated_at       = $14
WHERE id = $1;

-- name: DeletePackage :execrows
DELETE FROM packages WHERE id = $1;

-- name: GetPackageByName :one
SELECT
    p.id, p.name, p.type, p.private, p.status, p.owner_account_id,
    p.authority, p.contact, p.default_track, p.description, p.summary,
    p.title, p.website, p.links, p.media, p.track_guardrails,
    p.created_at, p.updated_at,
    a.id          AS pub_id,
    a.username    AS pub_username,
    a.display_name AS pub_display_name,
    a.email       AS pub_email,
    a.validation  AS pub_validation
FROM packages p
JOIN accounts a ON a.id = p.owner_account_id
WHERE p.name = $1;

-- name: GetPackageByID :one
SELECT
    p.id, p.name, p.type, p.private, p.status, p.owner_account_id,
    p.authority, p.contact, p.default_track, p.description, p.summary,
    p.title, p.website, p.links, p.media, p.track_guardrails,
    p.created_at, p.updated_at,
    a.id          AS pub_id,
    a.username    AS pub_username,
    a.display_name AS pub_display_name,
    a.email       AS pub_email,
    a.validation  AS pub_validation
FROM packages p
JOIN accounts a ON a.id = p.owner_account_id
WHERE p.id = $1;

-- name: ListPackagesForAccount :many
SELECT
    p.id, p.name, p.type, p.private, p.status, p.owner_account_id,
    p.authority, p.contact, p.default_track, p.description, p.summary,
    p.title, p.website, p.links, p.media, p.track_guardrails,
    p.created_at, p.updated_at,
    a.id          AS pub_id,
    a.username    AS pub_username,
    a.display_name AS pub_display_name,
    a.email       AS pub_email,
    a.validation  AS pub_validation
FROM packages p
JOIN accounts a ON a.id = p.owner_account_id
WHERE p.owner_account_id = $1;

-- name: SearchPackages :many
SELECT
    p.id, p.name, p.type, p.private, p.status, p.owner_account_id,
    p.authority, p.contact, p.default_track, p.description, p.summary,
    p.title, p.website, p.links, p.media, p.track_guardrails,
    p.created_at, p.updated_at,
    a.id          AS pub_id,
    a.username    AS pub_username,
    a.display_name AS pub_display_name,
    a.email       AS pub_email,
    a.validation  AS pub_validation
FROM packages p
JOIN accounts a ON a.id = p.owner_account_id
WHERE p.name ILIKE $1
ORDER BY p.name ASC;

-- name: GetPackageOwner :one
SELECT private, owner_account_id FROM packages WHERE id = $1;

-- name: CanViewPackage :one
-- Uses = ANY($3) so the allowed-roles list is a parameterized value, not an
-- interpolated SQL fragment. This is the safe replacement for the old
-- fmt.Sprintf(roleCondition) pattern.
SELECT EXISTS (
    SELECT 1 FROM package_acl acl
    LEFT JOIN account_group_members gm
      ON acl.principal_type = 'group' AND acl.principal_id = gm.group_id
    WHERE acl.package_id = $1
      AND acl.role = ANY($3::text[])
      AND ((acl.principal_type = 'account' AND acl.principal_id = $2)
           OR gm.account_id = $2)
) AS allowed;

-- name: CanManagePackage :one
SELECT EXISTS (
    SELECT 1 FROM package_acl acl
    LEFT JOIN account_group_members gm
      ON acl.principal_type = 'group' AND acl.principal_id = gm.group_id
    WHERE acl.package_id = $1
      AND acl.role = ANY($3::text[])
      AND ((acl.principal_type = 'account' AND acl.principal_id = $2)
           OR gm.account_id = $2)
) AS allowed;
