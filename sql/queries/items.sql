-- name: CreateItem :one
INSERT INTO items (feed_id, guid, title, url, content, comment_url, author, published_at, points, comments_count, status)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
RETURNING *;

-- name: CreateItemIfNotExists :one
INSERT OR IGNORE INTO items (feed_id, guid, title, url, content, comment_url, author, published_at, points, comments_count, status)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 'unread')
RETURNING id;

-- name: UpdateItemURL :exec
UPDATE items SET url = ?, updated_at = DATETIME('now') WHERE id = ?;

-- name: GetItem :one
SELECT * FROM items WHERE id = ?;

-- name: ListItemsByFeed :many
SELECT * FROM items WHERE feed_id = ? ORDER BY created_at DESC;

-- name: ListItemsByFeedAndStatus :many
SELECT * FROM items WHERE feed_id = ? AND status = ? ORDER BY created_at DESC;

-- name: UpdateItemStatus :one
UPDATE items
SET status = ?, updated_at = DATETIME('now')
WHERE id = ?
RETURNING *;

-- name: UpdateItemPointsAndComments :one
UPDATE items
SET points = ?, comments_count = ?, updated_at = DATETIME('now'), last_enriched_at = DATETIME('now')
WHERE id = ?
RETURNING *;

-- name: ListUnreadItemsForEnrichment :many
SELECT * FROM items WHERE feed_id = ? AND status = 'unread' AND fetched_at > ? AND (last_enriched_at IS NULL OR last_enriched_at < ?) ORDER BY created_at DESC;

-- name: DeleteItem :one
DELETE FROM items WHERE id = ? RETURNING id;

-- name: GetItemWithFeedOwner :one
SELECT i.* FROM items i
JOIN feeds f ON f.id = i.feed_id
WHERE i.id = ? AND f.user_id = ?;