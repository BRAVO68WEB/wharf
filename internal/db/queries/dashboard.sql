-- Dashboard read models (task 15). Read-only cross-table queries for the HTMX
-- UI; writes stay in the per-table query files.
--
-- The filter takes a LIKE pattern built by the caller (internal/dashboard),
-- which escapes `%`/`_`/`\` in the operator's search text so the box matches
-- substrings literally. An empty search becomes the pattern '%'.

-- name: ListRepositoriesWithStats :many
SELECT r.id,
       r.name,
       r.created_at,
       (SELECT count(*) FROM tags t WHERE t.repo_id = r.id)      AS tag_count,
       (SELECT count(*) FROM manifests m WHERE m.repo_id = r.id) AS manifest_count
FROM repositories r
WHERE r.name LIKE sqlc.arg('pattern') ESCAPE '\'
ORDER BY r.name
LIMIT sqlc.arg('limit') OFFSET sqlc.arg('offset');

-- name: CountRepositoriesMatching :one
SELECT count(*) AS n
FROM repositories r
WHERE r.name LIKE sqlc.arg('pattern') ESCAPE '\';

-- Catalog rows for the embedded dashboard. updated_at is the newest tag
-- touch, or the repository's created_at when it has no tags. size_bytes is
-- the sum of blob_metadata sizes referenced by the repository (shared blobs
-- count once per repository). The mounted row is the first of this same
-- order; the handler skips it in the index with the offset, because a second
-- bound parameter after ESCAPE does not survive sqlc's parser.

-- name: GetMountedRepository :one
SELECT r.id,
       r.name,
       r.created_at,
       COALESCE((SELECT MAX(t.updated_at) FROM tags t WHERE t.repo_id = r.id), r.created_at) AS updated_at,
       (SELECT count(*) FROM tags t WHERE t.repo_id = r.id) AS tag_count,
       (SELECT count(*) FROM manifests m WHERE m.repo_id = r.id) AS manifest_count,
       CAST(COALESCE((
           SELECT SUM(bm.size)
           FROM blob_refs br
           JOIN blob_metadata bm ON bm.digest = br.digest
           WHERE br.repo_id = r.id
       ), 0) AS INTEGER) AS size_bytes,
       r.published
FROM repositories r
ORDER BY updated_at DESC, r.name ASC
LIMIT 1;

-- name: ListCatalogRepositories :many
SELECT r.id,
       r.name,
       r.created_at,
       COALESCE((SELECT MAX(t.updated_at) FROM tags t WHERE t.repo_id = r.id), r.created_at) AS updated_at,
       (SELECT count(*) FROM tags t WHERE t.repo_id = r.id) AS tag_count,
       (SELECT count(*) FROM manifests m WHERE m.repo_id = r.id) AS manifest_count,
       CAST(COALESCE((
           SELECT SUM(bm.size)
           FROM blob_refs br
           JOIN blob_metadata bm ON bm.digest = br.digest
           WHERE br.repo_id = r.id
       ), 0) AS INTEGER) AS size_bytes,
       r.published
FROM repositories r
WHERE r.name LIKE sqlc.arg('pattern') ESCAPE '\'
ORDER BY updated_at DESC, r.name ASC
LIMIT sqlc.arg('limit') OFFSET sqlc.arg('offset');

-- Public catalog. published is a literal 1, not a bound flag, so a caller
-- cannot ask this query for private rows. The LIKE argument stays the only
-- parameter before LIMIT, clear of the ESCAPE quote.

-- name: CountPublishedRepositories :one
SELECT count(*) AS n
FROM repositories r
WHERE r.name LIKE sqlc.arg('pattern') ESCAPE '\'
  AND r.published = 1;

-- name: ListPublishedRepositories :many
SELECT r.id,
       r.name,
       r.created_at,
       COALESCE((SELECT MAX(t.updated_at) FROM tags t WHERE t.repo_id = r.id), r.created_at) AS updated_at,
       (SELECT count(*) FROM tags t WHERE t.repo_id = r.id) AS tag_count,
       (SELECT count(*) FROM manifests m WHERE m.repo_id = r.id) AS manifest_count,
       CAST(COALESCE((
           SELECT SUM(bm.size)
           FROM blob_refs br
           JOIN blob_metadata bm ON bm.digest = br.digest
           WHERE br.repo_id = r.id
       ), 0) AS INTEGER) AS size_bytes,
       r.published
FROM repositories r
WHERE r.name LIKE sqlc.arg('pattern') ESCAPE '\'
  AND r.published = 1
ORDER BY updated_at DESC, r.name ASC
LIMIT sqlc.arg('limit') OFFSET sqlc.arg('offset');
