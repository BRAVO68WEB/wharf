-- Audit log queries. Every mutating dashboard/registry action lands here.
-- Keyset pagination walks id descending (newest first).

-- name: InsertAuditEntry :exec
INSERT INTO audit_log (action, user_id, target, created_at)
VALUES (sqlc.arg('action'), sqlc.arg('user_id'), sqlc.arg('target'), sqlc.arg('created_at'));

-- name: ListAuditEntries :many
SELECT id, action, user_id, target, created_at
FROM audit_log
WHERE id < sqlc.arg('before')
ORDER BY id DESC
LIMIT sqlc.arg('limit');

-- name: ListAuditEntriesFromStart :many
SELECT id, action, user_id, target, created_at
FROM audit_log
ORDER BY id DESC
LIMIT sqlc.arg('limit');

-- name: ListAuditEntriesByTarget :many
SELECT id, action, user_id, target, created_at
FROM audit_log
WHERE target = sqlc.arg('target')
  AND id < sqlc.arg('before')
ORDER BY id DESC
LIMIT sqlc.arg('limit');

-- name: CountAuditEntries :one
SELECT count(*) AS n
FROM audit_log;
