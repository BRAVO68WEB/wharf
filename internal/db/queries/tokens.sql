-- Access tokens. token_hash is the hex SHA-256 of a high-entropy token.
-- The plaintext is returned once at creation and never stored.

-- name: CreateAccessToken :one
INSERT INTO access_tokens (user_id, name, token_hash)
VALUES (sqlc.arg('user_id'), sqlc.arg('name'), sqlc.arg('token_hash'))
RETURNING id, user_id, name, created_at;

-- name: GetActiveAccessToken :one
SELECT id, user_id, name, created_at
FROM access_tokens
WHERE user_id = sqlc.arg('user_id')
  AND token_hash = sqlc.arg('token_hash')
  AND revoked_at IS NULL;

-- name: ListAccessTokensByUser :many
SELECT id, name, created_at, revoked_at
FROM access_tokens
WHERE user_id = sqlc.arg('user_id')
ORDER BY id DESC;

-- name: RevokeAccessTokensByUser :exec
UPDATE access_tokens
SET revoked_at = unixepoch()
WHERE user_id = sqlc.arg('user_id')
  AND revoked_at IS NULL;

-- name: RevokeAccessToken :execrows
UPDATE access_tokens
SET revoked_at = unixepoch()
WHERE id = sqlc.arg('id')
  AND user_id = sqlc.arg('user_id')
  AND revoked_at IS NULL;

-- name: CountAdmins :one
SELECT count(*) AS n
FROM users
WHERE is_admin = 1;
