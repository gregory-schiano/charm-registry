-- name: DeleteStaleUploads :execrows
DELETE FROM uploads
WHERE status != 'approved'
  AND created_at < NOW() - $1::interval;