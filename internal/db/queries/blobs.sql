-- Blob queries. blob_refs is the per-repo "this repo uses this blob" edge
-- (cross-repo mounts add edges without re-uploading); blob_metadata is the
-- global registry-wide blob record GC sweeps against.

-- name: AddBlobRef :exec
INSERT INTO blob_refs (digest, repo_id)
VALUES (sqlc.arg('digest'), sqlc.arg('repo_id'))
ON CONFLICT (digest, repo_id) DO NOTHING;

-- name: GetBlobRef :one
SELECT digest, repo_id
FROM blob_refs
WHERE digest = sqlc.arg('digest')
  AND repo_id = sqlc.arg('repo_id');

-- name: ListBlobRefsByRepo :many
SELECT digest, repo_id
FROM blob_refs
WHERE repo_id = sqlc.arg('repo_id')
ORDER BY digest;

-- name: ListReposForBlob :many
SELECT repo_id
FROM blob_refs
WHERE digest = sqlc.arg('digest')
ORDER BY repo_id;

-- name: CountBlobRefs :one
SELECT count(*) AS n
FROM blob_refs;

-- name: DeleteBlobRef :exec
DELETE FROM blob_refs
WHERE digest = sqlc.arg('digest')
  AND repo_id = sqlc.arg('repo_id');

-- name: UpsertBlobMetadata :exec
INSERT INTO blob_metadata (digest, size, media_type)
VALUES (sqlc.arg('digest'), sqlc.arg('size'), sqlc.arg('media_type'))
ON CONFLICT (digest) DO UPDATE SET
    size       = excluded.size,
    media_type = excluded.media_type;

-- name: GetBlobMetadata :one
SELECT digest, size, media_type, created_at
FROM blob_metadata
WHERE digest = sqlc.arg('digest');

-- name: ListBlobMetadata :many
SELECT digest, size, media_type, created_at
FROM blob_metadata
ORDER BY digest
LIMIT sqlc.arg('limit');

-- name: ListBlobMetadataAfter :many
SELECT digest, size, media_type, created_at
FROM blob_metadata
WHERE digest > sqlc.arg('after')
ORDER BY digest
LIMIT sqlc.arg('limit');

-- name: CountBlobMetadata :one
SELECT count(*) AS n
FROM blob_metadata;

-- name: DeleteBlobMetadata :exec
DELETE FROM blob_metadata
WHERE digest = sqlc.arg('digest');
