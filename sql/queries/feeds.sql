-- name: CreateFeed :one
INSERT INTO feeds (user_id, category, type, name, url, config, status, extract_content)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)
RETURNING *;

-- name: GetFeed :one
SELECT * FROM feeds WHERE id = ? AND user_id = ?;

-- name: GetFeedByID :one
SELECT * FROM feeds WHERE id = ?;

-- name: ListFeeds :many
SELECT * FROM feeds WHERE user_id = ? ORDER BY name;

-- name: ListFeedsByCategory :many
SELECT * FROM feeds WHERE category = ? AND user_id = ? ORDER BY name;

-- name: ListCategories :many
SELECT DISTINCT category FROM feeds WHERE user_id = ? AND category != '' ORDER BY category;

-- name: UpdateFeed :one
UPDATE feeds
SET name = ?, url = ?, config = ?, category = ?, status = ?, extract_content = ?, updated_at = DATETIME('now')
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

-- name: ListAllFeeds :many
SELECT * FROM feeds ORDER BY id;

-- name: ListActiveFeeds :many
SELECT * FROM feeds WHERE status IN ('active', 'error') ORDER BY id;
