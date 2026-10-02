-- Manifest queries. Manifests are scoped per repository; the same digest may
-- exist in multiple repositories (each with its own blob_refs rows).

-- name: UpsertManifest :exec
INSERT INTO manifests (repo_id, digest, media_type, payload, size)
VALUES (sqlc.arg('repo_id'), sqlc.arg('digest'), sqlc.arg('media_type'), sqlc.arg('payload'), sqlc.arg('size'))
ON CONFLICT (repo_id, digest) DO UPDATE SET
    media_type = excluded.media_type,
    payload    = excluded.payload,
    size       = excluded.size;

-- name: GetManifest :one
SELECT id, repo_id, digest, media_type, payload, size, created_at
FROM manifests
WHERE repo_id = sqlc.arg('repo_id')
  AND digest = sqlc.arg('digest');

-- name: GetManifestByTag :one
SELECT m.id, m.repo_id, m.digest, m.media_type, m.payload, m.size, m.created_at
FROM manifests m
JOIN tags t ON t.repo_id = m.repo_id AND t.manifest_digest = m.digest
WHERE m.repo_id = sqlc.arg('repo_id')
  AND t.name = sqlc.arg('name');

-- name: ListManifestsByRepo :many
SELECT id, repo_id, digest, media_type, payload, size, created_at
FROM manifests
WHERE repo_id = sqlc.arg('repo_id')
ORDER BY id;

-- name: ListManifestDigests :many
SELECT digest
FROM manifests
ORDER BY digest;

-- name: CountManifestsByRepo :one
SELECT count(*) AS n
FROM manifests
WHERE repo_id = sqlc.arg('repo_id');

-- name: DeleteManifest :exec
DELETE FROM manifests
WHERE repo_id = sqlc.arg('repo_id')
  AND digest = sqlc.arg('digest');

-- name: ListTagsPointingToManifest :many
SELECT repo_id, name, manifest_digest, updated_at
FROM tags
WHERE repo_id = sqlc.arg('repo_id')
  AND manifest_digest = sqlc.arg('manifest_digest');
