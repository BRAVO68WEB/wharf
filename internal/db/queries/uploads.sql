-- Upload session queries. Rows drive the chunked-upload protocol state
-- machine (plan task 8); expires_at lets a sweeper reap stale uploads.
-- "offset" is quoted because OFFSET is a SQL keyword.

-- name: CreateUpload :exec
INSERT INTO uploads (uuid, repo_id, digest, "offset", size, temp_path, created_at, expires_at)
VALUES (sqlc.arg('uuid'), sqlc.arg('repo_id'), sqlc.arg('digest'), sqlc.arg('offset'),
        sqlc.arg('size'), sqlc.arg('temp_path'), sqlc.arg('created_at'), sqlc.arg('expires_at'));

-- name: GetUpload :one
SELECT uuid, repo_id, digest, "offset", size, temp_path, created_at, expires_at
FROM uploads
WHERE uuid = sqlc.arg('uuid');

-- name: UpdateUploadProgress :exec
UPDATE uploads
SET digest   = sqlc.arg('digest'),
    "offset" = sqlc.arg('offset'),
    size     = sqlc.arg('size')
WHERE uuid = sqlc.arg('uuid');

-- name: DeleteUpload :exec
DELETE FROM uploads
WHERE uuid = sqlc.arg('uuid');

-- name: ListExpiredUploads :many
SELECT uuid, repo_id, digest, "offset", size, temp_path, created_at, expires_at
FROM uploads
WHERE expires_at < sqlc.arg('now')
ORDER BY expires_at;

-- name: ListActiveUploads :many
SELECT uuid, repo_id, digest, "offset", size, temp_path, created_at, expires_at
FROM uploads
WHERE expires_at >= sqlc.arg('now')
ORDER BY expires_at;
