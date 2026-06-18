-- name: CreateDigestConfig :one
INSERT INTO digest_configs (user_id, name, schedule, config)
VALUES (?, ?, ?, ?)
RETURNING *;

-- name: GetDigestConfig :one
SELECT * FROM digest_configs WHERE id = ? AND user_id = ?;

-- name: ListDigestConfigs :many
SELECT * FROM digest_configs WHERE user_id = ? ORDER BY created_at;

-- name: ListAllDigestConfigs :many
SELECT * FROM digest_configs ORDER BY id;

-- name: UpdateDigestConfig :one
UPDATE digest_configs
SET name = ?, schedule = ?, config = ?, updated_at = DATETIME('now')
WHERE id = ? AND user_id = ?
RETURNING *;

-- name: DeleteDigestConfig :one
DELETE FROM digest_configs WHERE id = ? AND user_id = ? RETURNING id;