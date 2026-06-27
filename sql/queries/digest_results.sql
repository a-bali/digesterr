-- name: CreateDigestResult :one
INSERT INTO digest_results (user_id, config_id, config_snapshot, content, status)
VALUES (?, ?, ?, ?, ?)
RETURNING *;

-- name: GetDigestResult :one
SELECT * FROM digest_results WHERE id = ? AND user_id = ?;

-- name: ListDigestResultsByConfig :many
SELECT * FROM digest_results WHERE config_id = ? AND user_id = ? ORDER BY created_at DESC;

-- name: ListDigestResultsByConfigPaginated :many
SELECT * FROM digest_results WHERE config_id = ? AND user_id = ? ORDER BY created_at DESC LIMIT ? OFFSET ?;

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

-- name: ListDigestResultItemsWithData :many
SELECT dri.id, dri.result_id, dri.item_id, dri.is_highlight, dri.created_at AS result_item_created_at,
       i.feed_id, i.hash, i.title, i.url, i.content, i.author, i.comment_url, i.points, i.comments_count,
       i.published_at, i.status, i.fetched_at, i.created_at, i.updated_at, i.last_enriched_at,
       i.extract_content, i.extract_status, i.extract_retries, i.extract_date
FROM digest_result_items dri
JOIN items i ON i.id = dri.item_id
WHERE dri.result_id = ?
ORDER BY dri.is_highlight DESC, i.points DESC;

-- name: GetLatestDigestResult :one
SELECT * FROM digest_results WHERE config_id = ? AND user_id = ? ORDER BY created_at DESC LIMIT 1;

-- name: ListUnreadDigestItems :many
SELECT i.* FROM items i
JOIN feeds f ON f.id = i.feed_id
WHERE f.user_id = ? AND i.status = 'unread'
ORDER BY i.points DESC
LIMIT ?;
-- name: GetDigestUnreadCounts :many
SELECT dc.id AS config_id, CAST(COALESCE(SUM(CASE WHEN dr.status = 'new' THEN 1 ELSE 0 END), 0) AS INTEGER) AS unread_count
FROM digest_configs dc
LEFT JOIN digest_results dr ON dr.config_id = dc.id
WHERE dc.user_id = ?
GROUP BY dc.id;

-- name: ListUnreadDigestItemsByFeed :many
SELECT * FROM items
WHERE feed_id = ? AND status = 'unread'
ORDER BY points DESC
LIMIT ?;
