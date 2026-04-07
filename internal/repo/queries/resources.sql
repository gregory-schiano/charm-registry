-- name: UpsertResourceDefinition :one
INSERT INTO resource_definitions (
    id, package_id, name, type, description, filename, optional, created_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
ON CONFLICT (package_id, name) DO UPDATE SET
    type        = EXCLUDED.type,
    description = EXCLUDED.description,
    filename    = EXCLUDED.filename,
    optional    = EXCLUDED.optional
RETURNING id, package_id, name, type, description, filename, optional, created_at;

-- name: GetResourceDefinition :one
SELECT id, package_id, name, type, description, filename, optional, created_at
FROM resource_definitions
WHERE package_id = $1
  AND name       = $2;

-- name: ListResourceDefinitions :many
SELECT id, package_id, name, type, description, filename, optional, created_at
FROM resource_definitions
WHERE package_id = $1
ORDER BY name ASC;

-- name: CreateResourceRevision :exec
INSERT INTO resource_revisions (
    id, resource_id, revision, name, type, description,
    filename, created_at, size, sha256, sha384, sha512, sha3_384,
    object_key, bases, architectures, oci_image_digest, oci_image_blob
) VALUES (
    $1, $2, $3, $4, $5, $6,
    $7, $8, $9, $10, $11, $12, $13,
    $14, $15, $16, $17, $18
);

-- name: UpdateResourceRevision :execrows
UPDATE resource_revisions
SET bases           = $4,
    architectures   = $5,
    oci_image_digest = $6,
    oci_image_blob  = $7
WHERE resource_id = $1
  AND revision    = $2
  AND id          = $3;

-- name: ListResourceRevisions :many
SELECT id, resource_id, revision, name, type, description,
       filename, created_at, size, sha256, sha384, sha512, sha3_384,
       object_key, bases, architectures, oci_image_digest, oci_image_blob
FROM resource_revisions
WHERE resource_id = $1
ORDER BY revision DESC;

-- name: GetResourceRevision :one
SELECT id, resource_id, revision, name, type, description,
       filename, created_at, size, sha256, sha384, sha512, sha3_384,
       object_key, bases, architectures, oci_image_digest, oci_image_blob
FROM resource_revisions
WHERE resource_id = $1
  AND revision    = $2;
