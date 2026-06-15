-- name: CreateDigestResult :one
INSERT INTO digest_results (user_id, config_id, config_snapshot, content, status)
VALUES (?, ?, ?, ?, ?)
RETURNING *;

-- name: GetDigestResult :one
SELECT * FROM digest_results WHERE id = ? AND user_id = ?;

-- name: ListDigestResultsByConfig :many
SELECT * FROM digest_results WHERE config_id = ? AND user_id = ? ORDER BY created_at DESC;

-- name: UpdateDigestResultStatus :one
UPDATE digest_results
SET status = ?, updated_at = DATETIME('now')
WHERE id = ? AND user_id = ?
RETURNING *;

-- name: DeleteDigestResult :one
DELETE FROM digest_results WHERE id = ? AND user_id = ? RETURNING id;

-- name: CreateDigestResultItem :one
INSERT INTO digest_result_items (result_id, item_id, is_highlight)
VALUES (?, ?, ?)
RETURNING *;

-- name: ListDigestResultItems :many
SELECT * FROM digest_result_items WHERE result_id = ?;