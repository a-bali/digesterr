-- name: CreateFeed :one
INSERT INTO feeds (user_id, category_id, type, name, url, config, status)
VALUES (?, ?, ?, ?, ?, ?, ?)
RETURNING *;

-- name: GetFeed :one
SELECT * FROM feeds WHERE id = ? AND user_id = ?;

-- name: ListFeeds :many
SELECT * FROM feeds WHERE user_id = ? ORDER BY name;

-- name: ListFeedsByCategory :many
SELECT * FROM feeds WHERE category_id = ? AND user_id = ? ORDER BY name;

-- name: UpdateFeed :one
UPDATE feeds
SET name = ?, url = ?, config = ?, category_id = ?, status = ?, updated_at = DATETIME('now')
WHERE id = ? AND user_id = ?
RETURNING *;

-- name: UpdateFeedFetchTime :one
UPDATE feeds
SET last_fetched_at = DATETIME('now'), updated_at = DATETIME('now')
WHERE id = ?
RETURNING *;

-- name: UpdateFeedStatus :one
UPDATE feeds
SET status = ?, updated_at = DATETIME('now')
WHERE id = ?
RETURNING *;

-- name: DeleteFeed :one
DELETE FROM feeds WHERE id = ? AND user_id = ? RETURNING id;