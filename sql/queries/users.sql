-- name: CreateUser :one
INSERT INTO users (email, password_hash)
VALUES (?, ?)
RETURNING *;

-- name: GetUserByID :one
SELECT * FROM users WHERE id = ?;

-- name: GetUserByEmail :one
SELECT * FROM users WHERE email = ?;

-- name: UpdateUserPassword :one
UPDATE users
SET password_hash = ?, updated_at = DATETIME('now')
WHERE id = ?
RETURNING *;