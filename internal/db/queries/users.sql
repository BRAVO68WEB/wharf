-- User queries. password_hash is a bcrypt hash (plan task 11); plaintext
-- passwords never touch this table. is_admin is stored as 0/1 INTEGER and
-- mapped to Go bool by the sqlc override in sqlc.yaml.

-- name: CreateUser :one
INSERT INTO users (username, password_hash, is_admin)
VALUES (sqlc.arg('username'), sqlc.arg('password_hash'), sqlc.arg('is_admin'))
RETURNING id, username, password_hash, is_admin;

-- name: GetUserByID :one
SELECT id, username, password_hash, is_admin
FROM users
WHERE id = sqlc.arg('id');

-- name: GetUserByUsername :one
SELECT id, username, password_hash, is_admin
FROM users
WHERE username = sqlc.arg('username');

-- name: ListUsers :many
SELECT id, username, password_hash, is_admin
FROM users
ORDER BY username;

-- name: SetUserAdmin :exec
UPDATE users
SET is_admin = sqlc.arg('is_admin')
WHERE id = sqlc.arg('id');

-- name: DeleteUser :exec
DELETE FROM users
WHERE id = sqlc.arg('id');

-- name: CountUsers :one
SELECT count(*) AS n
FROM users;
