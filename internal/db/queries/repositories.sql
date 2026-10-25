-- Repository queries. Catalog pagination uses keyset paging on name so the
-- OCI `?n=&last=` contract maps 1:1 (see plan task 10).

-- name: CreateRepository :one
INSERT INTO repositories (name)
VALUES (sqlc.arg('name'))
ON CONFLICT (name) DO UPDATE SET name = excluded.name
RETURNING id, name, created_at, published;

-- name: GetRepositoryByName :one
SELECT id, name, created_at, published
FROM repositories
WHERE name = sqlc.arg('name');

-- name: ListRepositories :many
SELECT id, name, created_at, published
FROM repositories
WHERE name > sqlc.arg('after')
ORDER BY name
LIMIT sqlc.arg('limit');

-- name: ListRepositoriesFromStart :many
SELECT id, name, created_at, published
FROM repositories
ORDER BY name
LIMIT sqlc.arg('limit');

-- name: SetRepositoryPublished :execrows
UPDATE repositories
SET published = sqlc.arg('published')
WHERE name = sqlc.arg('name');

-- name: CountRepositories :one
SELECT count(*) AS n
FROM repositories;

-- name: DeleteRepository :exec
DELETE FROM repositories
WHERE id = sqlc.arg('id');
