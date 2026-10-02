-- Session queries. Sessions back the dashboard cookie auth (plan task 14).

-- name: CreateSession :exec
INSERT INTO sessions (token, user_id, expires_at)
VALUES (sqlc.arg('token'), sqlc.arg('user_id'), sqlc.arg('expires_at'));

-- name: GetSession :one
SELECT token, user_id, expires_at
FROM sessions
WHERE token = sqlc.arg('token');

-- name: DeleteSession :exec
DELETE FROM sessions
WHERE token = sqlc.arg('token');

-- name: DeleteSessionsForUser :exec
DELETE FROM sessions
WHERE user_id = sqlc.arg('user_id');

-- name: DeleteExpiredSessions :exec
DELETE FROM sessions
WHERE expires_at < sqlc.arg('now');
