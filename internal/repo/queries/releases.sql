-- name: ReplaceRelease :exec
INSERT INTO releases (
    id, package_id, channel, revision,
    base, resources, when_created, expiration_date, progressive
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
ON CONFLICT (package_id, channel) DO UPDATE SET
    revision        = EXCLUDED.revision,
    base            = EXCLUDED.base,
    resources       = EXCLUDED.resources,
    when_created    = EXCLUDED.when_created,
    expiration_date = EXCLUDED.expiration_date,
    progressive     = EXCLUDED.progressive;

-- name: DeleteRelease :execrows
DELETE FROM releases
WHERE package_id = $1
  AND channel = $2;

-- name: ListReleases :many
SELECT id, package_id, channel, revision,
       base, resources, when_created, expiration_date, progressive
FROM releases
WHERE package_id = $1
ORDER BY channel ASC;

-- name: ResolveRelease :one
SELECT id, package_id, channel, revision,
       base, resources, when_created, expiration_date, progressive
FROM releases
WHERE package_id = $1
  AND channel    = $2;

-- name: ResolveDefaultRelease :one
SELECT
    r.id, r.package_id, r.channel, r.revision,
    r.base, r.resources, r.when_created, r.expiration_date, r.progressive
FROM packages p
JOIN releases r ON r.package_id = p.id
WHERE p.id = $1
  AND (
      (p.default_track IS NOT NULL AND r.channel = p.default_track || '/stable')
   OR (p.default_track IS NULL     AND r.channel = 'latest/stable')
  )
ORDER BY r.when_created DESC
LIMIT 1;

-- name: ResolveLatestRelease :one
SELECT id, package_id, channel, revision,
       base, resources, when_created, expiration_date, progressive
FROM releases
WHERE package_id = $1
ORDER BY when_created DESC
LIMIT 1;
