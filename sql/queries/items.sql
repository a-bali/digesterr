-- name: CreateItem :one
INSERT INTO items (feed_id, hash, title, url, content, comment_url, author, published_at, points, comments_count, status)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
RETURNING *;

-- name: CreateItemIfNotExists :one
INSERT OR IGNORE INTO items (feed_id, hash, title, url, content, comment_url, author, published_at, points, comments_count, status)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 'unread')
RETURNING id;

-- name: UpdateItemURL :exec
UPDATE items SET url = ?, updated_at = DATETIME('now') WHERE id = ?;

-- name: GetItem :one
SELECT * FROM items WHERE id = ?;

-- name: ListItemsByFeed :many
SELECT * FROM items WHERE feed_id = ? ORDER BY published_at DESC, created_at DESC;

-- name: ListItemsByFeedAndStatus :many
SELECT * FROM items WHERE feed_id = ? AND status = ? ORDER BY published_at DESC, created_at DESC;

-- name: UpdateItemStatus :one
UPDATE items
SET status = ?, updated_at = DATETIME('now')
WHERE id = ?
RETURNING *;

-- name: UpdateItemPointsAndComments :one
UPDATE items
SET points = ?, comments_count = ?, comment_url = ?, updated_at = DATETIME('now'), last_enriched_at = DATETIME('now')
WHERE id = ?
RETURNING *;

-- name: ListUnreadItemsForEnrichment :many
SELECT * FROM items WHERE feed_id = ? AND status = 'unread' AND fetched_at > ? AND (last_enriched_at IS NULL OR last_enriched_at < ?) ORDER BY last_enriched_at IS NULL DESC, last_enriched_at ASC, published_at DESC;

-- name: DeleteItem :one
DELETE FROM items WHERE id = ? RETURNING id;

-- name: GetItemWithFeedOwner :one
SELECT i.* FROM items i
JOIN feeds f ON f.id = i.feed_id
WHERE i.id = ? AND f.user_id = ?;

-- name: GetUnreadCounts :many
SELECT f.id AS feed_id, CAST(COALESCE((SELECT COUNT(*) FROM items WHERE feed_id = f.id AND status = 'unread'), 0) AS INTEGER) AS unread_count FROM feeds f WHERE f.user_id = ?;

-- name: ListItemsByFeedPaginated :many
SELECT * FROM items WHERE feed_id = ? ORDER BY published_at DESC, created_at DESC LIMIT ? OFFSET ?;

-- name: ListItemsByFeedAndStatusPaginated :many
SELECT * FROM items WHERE feed_id = ? AND status = ? ORDER BY published_at DESC, created_at DESC LIMIT ? OFFSET ?;

-- name: ListItemsNeedingExtraction :many
SELECT * FROM items WHERE feed_id = ? AND url != '' AND extract_status IN ('pending', 'failed') AND extract_retries < 3 ORDER BY extract_retries ASC, published_at DESC;

-- name: UpdateItemExtraction :one
UPDATE items SET extract_content = ?, extract_status = ?, extract_retries = ?, extract_date = DATETIME('now'), updated_at = DATETIME('now') WHERE id = ? RETURNING *;

-- name: MarkFeedRead :exec
UPDATE items SET status = 'read', updated_at = DATETIME('now') WHERE feed_id = ? AND status = 'unread';
