-- name: CreateUpload :exec
INSERT INTO uploads (
    id, filename, object_key, size, sha256, sha384,
    status, kind, created_at, approved_at, revision, errors
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12);

-- name: GetUpload :one
SELECT id, filename, object_key, size, sha256, sha384,
       status, kind, created_at, approved_at, revision, errors
FROM uploads
WHERE id = $1;

-- name: ApproveUpload :execrows
UPDATE uploads
SET approved_at = NOW(),
    revision    = $2,
    errors      = $3,
    status      = $4
WHERE id = $1;

-- name: CreateRevision :exec
INSERT INTO revisions (
    id, package_id, revision, version, status,
    created_at, created_by, size, sha256, sha384,
    object_key, metadata_yaml, config_yaml, actions_yaml,
    bundle_yaml, readme_md, bases, attributes, relations, subordinate
) VALUES (
    $1, $2, $3, $4, $5,
    $6, $7, $8, $9, $10,
    $11, $12, $13, $14,
    $15, $16, $17, $18, $19, $20
);

-- name: ListRevisions :many
SELECT id, package_id, revision, version, status,
       created_at, created_by, size, sha256, sha384,
       object_key, metadata_yaml, config_yaml, actions_yaml,
       bundle_yaml, readme_md, bases, attributes, relations, subordinate
FROM revisions
WHERE package_id = $1
ORDER BY revision DESC;

-- name: GetLatestRevision :one
SELECT id, package_id, revision, version, status,
       created_at, created_by, size, sha256, sha384,
       object_key, metadata_yaml, config_yaml, actions_yaml,
       bundle_yaml, readme_md, bases, attributes, relations, subordinate
FROM revisions
WHERE package_id = $1
ORDER BY revision DESC
LIMIT 1;

-- name: ListRevisionsByNumbers :many
SELECT id, package_id, revision, version, status,
       created_at, created_by, size, sha256, sha384,
       object_key, metadata_yaml, config_yaml, actions_yaml,
       bundle_yaml, readme_md, bases, attributes, relations, subordinate
FROM revisions
WHERE package_id = $1
  AND revision = ANY($2::int4[])
ORDER BY revision DESC;

-- name: GetRevisionByNumber :one
SELECT id, package_id, revision, version, status,
       created_at, created_by, size, sha256, sha384,
       object_key, metadata_yaml, config_yaml, actions_yaml,
       bundle_yaml, readme_md, bases, attributes, relations, subordinate
FROM revisions
WHERE package_id = $1
  AND revision   = $2;
