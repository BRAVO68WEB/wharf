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
