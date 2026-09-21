-- name: CreateDigestResult :one
INSERT INTO digest_results (user_id, config_id, config_snapshot, content, status)
VALUES (?, ?, ?, ?, ?)
RETURNING *;

-- name: GetDigestResult :one
SELECT * FROM digest_results WHERE id = ? AND user_id = ?;

-- name: ListDigestResultsByConfig :many
SELECT * FROM digest_results WHERE config_id = ? AND user_id = ? ORDER BY created_at DESC;

-- name: ListDigestResultsByConfigPaginated :many
-- Preview query for the middle pane: excludes heavy content/config_snapshot,
-- pre-computes title and item_count from the stored JSON.
SELECT dr.id, dr.user_id, dr.config_id, dr.status, dr.created_at, dr.updated_at,
       CAST(COALESCE(json_extract(dr.content, '$.title'), '') AS TEXT) AS title,
       COALESCE(json_array_length(json_extract(dr.content, '$.highlights')), 0)
       + COALESCE(json_array_length(json_extract(dr.content, '$.items')), 0) AS item_count
FROM digest_results dr
WHERE config_id = ? AND user_id = ? ORDER BY created_at DESC LIMIT ? OFFSET ?;

-- name: ListDigestResultsByConfigOldestFirstPaginated :many
SELECT dr.id, dr.user_id, dr.config_id, dr.status, dr.created_at, dr.updated_at,
       CAST(COALESCE(json_extract(dr.content, '$.title'), '') AS TEXT) AS title,
       COALESCE(json_array_length(json_extract(dr.content, '$.highlights')), 0)
       + COALESCE(json_array_length(json_extract(dr.content, '$.items')), 0) AS item_count
FROM digest_results dr
WHERE config_id = ? AND user_id = ? ORDER BY created_at ASC LIMIT ? OFFSET ?;

-- name: ListDigestResultsByUserStatusPaginated :many
-- Reader-stream page for a user-wide digest status.
SELECT * FROM digest_results WHERE user_id = @user_id AND status = @status
ORDER BY created_at DESC
LIMIT @lim OFFSET @off;

-- name: UpdateDigestResultStatus :one
UPDATE digest_results
SET status = ?, updated_at = DATETIME('now')
WHERE id = ? AND user_id = ?
RETURNING *;

-- name: DeleteDigestResult :one
DELETE FROM digest_results WHERE id = ? AND user_id = ? RETURNING id;

-- name: DeleteExpiredDigestResults :execrows
DELETE FROM digest_results WHERE created_at < ?;

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

-- name: ListAllDigestResultsByUser :many
-- Preview query for the middle pane: excludes heavy content/config_snapshot,
-- pre-computes title and item_count from the stored JSON.
SELECT dr.id, dr.user_id, dr.config_id, dr.status, dr.created_at, dr.updated_at,
       dc.name AS config_name,
       CAST(COALESCE(json_extract(dr.content, '$.title'), '') AS TEXT) AS title,
       COALESCE(json_array_length(json_extract(dr.content, '$.highlights')), 0)
       + COALESCE(json_array_length(json_extract(dr.content, '$.items')), 0) AS item_count
FROM digest_results dr
JOIN digest_configs dc ON dc.id = dr.config_id
WHERE dr.user_id = ?
ORDER BY dr.created_at DESC
LIMIT ? OFFSET ?;

-- name: CountAllDigestResultsByUser :one
SELECT CAST(COUNT(*) AS INTEGER) AS count
FROM digest_results dr
JOIN digest_configs dc ON dc.id = dr.config_id
WHERE dr.user_id = ?;

-- name: ListDigestResultsByUserStatus :many
SELECT * FROM digest_results WHERE user_id = ? AND status = ? ORDER BY created_at DESC;

-- name: MarkAllUserDigestResultsRead :exec
UPDATE digest_results SET status = 'read', updated_at = DATETIME('now') WHERE user_id = ? AND status = 'new';

-- name: MarkDigestConfigRead :exec
UPDATE digest_results SET status = 'read', updated_at = DATETIME('now') WHERE config_id = ? AND user_id = ? AND status = 'new';

-- name: MarkDigestConfigReadAbove :exec
-- Mark digest results at or above the boundary result as read, within a config.
--
-- The boundary row is identified by id and its ordering key is read inside SQL
-- rather than bound as a timestamp from Go: SQLite stores created_at as TEXT in
-- DATETIME('now') layout ("2026-09-10 10:31:06") while a Go time.Time binds as
-- RFC3339 ("2026-09-10T10:31:06Z"), so a bound comparison never matched and the
-- boundary result itself was never marked. The digest list orders by
-- created_at alone, which leaves rows sharing a timestamp in an arbitrary
-- order; id completes the comparison so the split between above and below is
-- deterministic.
--
-- Both directions EXCLUDE the boundary result, matching the item endpoints:
-- "above" is strictly before it, "below" strictly after it.
UPDATE digest_results SET status = 'read', updated_at = DATETIME('now')
WHERE digest_results.config_id = @config_id
  AND digest_results.user_id = @user_id
  AND digest_results.status = 'new'
  AND (
    digest_results.created_at > (SELECT br.created_at FROM digest_results br WHERE br.id = @boundary_id)
    OR (digest_results.created_at = (SELECT br.created_at FROM digest_results br WHERE br.id = @boundary_id)
        AND digest_results.id > @boundary_id)
  );

-- name: MarkDigestConfigReadBelow :exec
-- Mark digest results strictly below the boundary result as read, within a
-- config. See MarkDigestConfigReadAbove for the boundary rationale.
UPDATE digest_results SET status = 'read', updated_at = DATETIME('now')
WHERE digest_results.config_id = @config_id
  AND digest_results.user_id = @user_id
  AND digest_results.status = 'new'
  AND (
    digest_results.created_at < (SELECT br.created_at FROM digest_results br WHERE br.id = @boundary_id)
    OR (digest_results.created_at = (SELECT br.created_at FROM digest_results br WHERE br.id = @boundary_id)
        AND digest_results.id < @boundary_id)
  );

-- name: MarkAllDigestReadAbove :exec
-- Mark all digest results at or above the boundary result as read.
-- See MarkDigestConfigReadAbove for the boundary rationale.
UPDATE digest_results SET status = 'read', updated_at = DATETIME('now')
WHERE digest_results.user_id = @user_id
  AND digest_results.status = 'new'
  AND (
    digest_results.created_at > (SELECT br.created_at FROM digest_results br WHERE br.id = @boundary_id)
    OR (digest_results.created_at = (SELECT br.created_at FROM digest_results br WHERE br.id = @boundary_id)
        AND digest_results.id > @boundary_id)
  );

-- name: MarkAllDigestReadBelow :exec
-- Mark all digest results strictly below the boundary result as read.
-- See MarkDigestConfigReadAbove for the boundary rationale.
UPDATE digest_results SET status = 'read', updated_at = DATETIME('now')
WHERE digest_results.user_id = @user_id
  AND digest_results.status = 'new'
  AND (
    digest_results.created_at < (SELECT br.created_at FROM digest_results br WHERE br.id = @boundary_id)
    OR (digest_results.created_at = (SELECT br.created_at FROM digest_results br WHERE br.id = @boundary_id)
        AND digest_results.id < @boundary_id)
  );

-- name: MarkDigestResultItemsRead :exec
-- When a digest result is marked read, its linked items transition from
-- digested to read (spec: item lifecycle). Items in other states are untouched.
UPDATE items SET status = 'read', updated_at = DATETIME('now')
WHERE status = 'digested'
  AND id IN (SELECT item_id FROM digest_result_items WHERE result_id = ?);

-- name: MarkDigestConfigResultItemsRead :execrows
-- Cascade for mark-config-read / mark-all-read: flips digested items of every
-- result that just transitioned new -> read for the given config (or all
-- configs when configID is NULL).
UPDATE items SET status = 'read', updated_at = DATETIME('now')
WHERE status = 'digested'
  AND id IN (
    SELECT dri.item_id FROM digest_result_items dri
    JOIN digest_results dr ON dr.id = dri.result_id
    WHERE dr.user_id = ?1
      AND dr.status = 'read'
      AND (?2 IS NULL OR dr.config_id = ?2)
  );

-- name: ListExpiredDigestResultIDs :many
-- IDs of digest results older than the cutoff. Expiry flips each result's
-- linked items back to read (MarkDigestResultItemsRead) before deleting the
-- result, so expiry never strands items in 'digested'.
SELECT id FROM digest_results WHERE created_at < ? ORDER BY id;

-- end of file
