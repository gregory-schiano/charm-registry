-- name: CreateTrack :execrows
INSERT INTO tracks (package_id, name, version_pattern, automatic_phasing_percentage, created_at)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT DO NOTHING;

-- name: ListTracks :many
SELECT name, version_pattern, automatic_phasing_percentage, created_at
FROM tracks
WHERE package_id = $1
ORDER BY created_at ASC;

-- name: ListTracksForPackages :many
SELECT package_id, name, version_pattern, automatic_phasing_percentage, created_at
FROM tracks
WHERE package_id = ANY($1::text[])
ORDER BY created_at ASC;
