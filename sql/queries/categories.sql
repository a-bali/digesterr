-- name: CreateCategory :one
INSERT INTO categories (user_id, name)
VALUES (?, ?)
RETURNING *;

-- name: GetCategory :one
SELECT * FROM categories WHERE id = ? AND user_id = ?;

-- name: ListCategories :many
SELECT * FROM categories WHERE user_id = ? ORDER BY name;

-- name: UpdateCategory :one
UPDATE categories
SET name = ?, updated_at = DATETIME('now')
WHERE id = ? AND user_id = ?
RETURNING *;

-- name: DeleteCategory :one
DELETE FROM categories WHERE id = ? AND user_id = ? RETURNING id;