-- name: CreateUser :one
INSERT INTO users (email, password_hash, api_key)
VALUES (?, ?, ?)
RETURNING *;

-- name: GetUserByID :one
SELECT * FROM users WHERE id = ?;

-- name: GetUserByAPIKey :one
SELECT * FROM users WHERE api_key = ?;

-- name: GetUserByEmail :one
SELECT * FROM users WHERE email = ?;

-- name: UpdateUserPassword :one
UPDATE users
SET password_hash = ?, updated_at = DATETIME('now')
WHERE id = ?
RETURNING *;