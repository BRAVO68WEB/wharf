-- Tag queries. Tags are the mutable name -> manifest mapping; retagging is
-- an upsert here (plan task 20).

-- name: SetTag :exec
INSERT INTO tags (repo_id, name, manifest_digest, updated_at)
VALUES (sqlc.arg('repo_id'), sqlc.arg('name'), sqlc.arg('manifest_digest'), sqlc.arg('updated_at'))
ON CONFLICT (repo_id, name) DO UPDATE SET
    manifest_digest = excluded.manifest_digest,
    updated_at      = excluded.updated_at;

-- name: GetTag :one
SELECT repo_id, name, manifest_digest, updated_at
FROM tags
WHERE repo_id = sqlc.arg('repo_id')
  AND name = sqlc.arg('name');

-- name: ListTagsByRepo :many
SELECT repo_id, name, manifest_digest, updated_at
FROM tags
WHERE repo_id = sqlc.arg('repo_id')
  AND name > sqlc.arg('after')
ORDER BY name
LIMIT sqlc.arg('limit');

-- name: ListTagsByRepoFromStart :many
SELECT repo_id, name, manifest_digest, updated_at
FROM tags
WHERE repo_id = sqlc.arg('repo_id')
ORDER BY name
LIMIT sqlc.arg('limit');

-- name: CountTagsByRepo :one
SELECT count(*) AS n
FROM tags
WHERE repo_id = sqlc.arg('repo_id');

-- name: DeleteTag :exec
DELETE FROM tags
WHERE repo_id = sqlc.arg('repo_id')
  AND name = sqlc.arg('name');

-- name: DeleteTagsByManifest :exec
DELETE FROM tags
WHERE repo_id = sqlc.arg('repo_id')
  AND manifest_digest = sqlc.arg('manifest_digest');
