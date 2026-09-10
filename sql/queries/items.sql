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

-- name: ListItemsByFeedStreamPage :many
-- Reader-stream page: newest-first or oldest-first (when oldest_first is 1),
-- offset-based. COALESCE mirrors itemTimestamp's published_at fallback.
SELECT * FROM items WHERE feed_id = ?
ORDER BY COALESCE(published_at, fetched_at, created_at) DESC, created_at DESC
LIMIT @lim OFFSET @off;

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

-- name: DeleteExpiredItems :execrows
DELETE FROM items
WHERE status != 'saved'
  AND items.created_at < ?
  AND NOT EXISTS (SELECT 1 FROM digest_result_items dri WHERE dri.item_id = items.id);

-- name: GetItemWithFeedOwner :one
SELECT i.* FROM items i
JOIN feeds f ON f.id = i.feed_id
WHERE i.id = ? AND f.user_id = ?;

-- name: GetUnreadCounts :many
-- Single GROUP BY join instead of a correlated subquery per feed.
SELECT f.id AS feed_id, CAST(COALESCE(COUNT(i.id), 0) AS INTEGER) AS unread_count
FROM feeds f
LEFT JOIN items i ON i.feed_id = f.id AND i.status = 'unread'
WHERE f.user_id = ?
GROUP BY f.id;

-- name: NewestUnreadItemPerFeed :many
SELECT f.id AS feed_id,
       CAST(COALESCE(MAX(strftime('%s', COALESCE(i.published_at, i.fetched_at))), 0) AS INTEGER) AS newest_sec
FROM feeds f
LEFT JOIN items i ON i.feed_id = f.id AND i.status = 'unread'
WHERE f.user_id = ?
GROUP BY f.id;

-- name: CountStarredItems :one
SELECT CAST(COUNT(*) AS INTEGER) AS count FROM items i
JOIN feeds f ON f.id = i.feed_id
WHERE f.user_id = ? AND i.status = 'saved';


-- name: ListItemsByFeedPaginated :many
SELECT * FROM items WHERE feed_id = ? ORDER BY published_at DESC, created_at DESC LIMIT ? OFFSET ?;

-- name: ListItemsByFeedAndStatusPaginated :many
SELECT * FROM items WHERE feed_id = ? AND status = ? ORDER BY published_at DESC, created_at DESC LIMIT ? OFFSET ?;

-- name: ListItemsByFeedSortedDateDesc :many
-- Preview query for the middle pane: excludes hash, url, author, and
-- internal metadata not needed for list display.
SELECT i.id, i.title, i.status, i.published_at, i.content,
       i.extract_content, i.extract_status,
       i.points, i.comments_count, i.comment_url
FROM items i
WHERE feed_id = sqlc.arg(feed_id)
  AND (status = sqlc.narg(status) OR sqlc.narg(status) IS NULL)
  AND (points >= sqlc.arg(min_score) OR sqlc.arg(min_score) = 0)
ORDER BY published_at DESC, created_at DESC
LIMIT sqlc.arg(limit) OFFSET sqlc.arg(offset);

-- name: ListItemsByFeedSortedDateAsc :many
SELECT i.id, i.title, i.status, i.published_at, i.content,
       i.extract_content, i.extract_status,
       i.points, i.comments_count, i.comment_url
FROM items i
WHERE feed_id = sqlc.arg(feed_id)
  AND (status = sqlc.narg(status) OR sqlc.narg(status) IS NULL)
  AND (points >= sqlc.arg(min_score) OR sqlc.arg(min_score) = 0)
ORDER BY published_at ASC, created_at ASC
LIMIT sqlc.arg(limit) OFFSET sqlc.arg(offset);

-- name: ListItemsByFeedSortedScoreDesc :many
SELECT i.id, i.title, i.status, i.published_at, i.content,
       i.extract_content, i.extract_status,
       i.points, i.comments_count, i.comment_url
FROM items i
WHERE feed_id = sqlc.arg(feed_id)
  AND (status = sqlc.narg(status) OR sqlc.narg(status) IS NULL)
  AND (points >= sqlc.arg(min_score) OR sqlc.arg(min_score) = 0)
ORDER BY points DESC, published_at DESC, created_at DESC
LIMIT sqlc.arg(limit) OFFSET sqlc.arg(offset);

-- name: ListItemsByFeedSortedScoreAsc :many
SELECT i.id, i.title, i.status, i.published_at, i.content,
       i.extract_content, i.extract_status,
       i.points, i.comments_count, i.comment_url
FROM items i
WHERE feed_id = sqlc.arg(feed_id)
  AND (status = sqlc.narg(status) OR sqlc.narg(status) IS NULL)
  AND (points >= sqlc.arg(min_score) OR sqlc.arg(min_score) = 0)
ORDER BY points ASC, published_at DESC, created_at DESC
LIMIT sqlc.arg(limit) OFFSET sqlc.arg(offset);

-- name: CountItemsByFeed :one
SELECT CAST(COUNT(*) AS INTEGER) AS count FROM items
WHERE feed_id = sqlc.arg(feed_id)
  AND (status = sqlc.narg(status) OR sqlc.narg(status) IS NULL)
  AND (points >= sqlc.arg(min_score) OR sqlc.arg(min_score) = 0);

-- name: ListItemsNeedingExtraction :many
SELECT * FROM items WHERE feed_id = ? AND url != '' AND extract_status IN ('pending', 'failed') AND extract_retries < 3 ORDER BY extract_retries ASC, published_at DESC;

-- name: UpdateItemExtraction :one
UPDATE items SET extract_content = ?, extract_status = ?, extract_retries = ?, extract_date = DATETIME('now'), updated_at = DATETIME('now') WHERE id = ? RETURNING *;

-- name: MarkFeedRead :exec
UPDATE items SET status = 'read', updated_at = DATETIME('now') WHERE feed_id = ? AND status = 'unread';

-- name: MarkAllUserItemsRead :exec
UPDATE items SET status = 'read', updated_at = DATETIME('now') WHERE feed_id IN (SELECT id FROM feeds WHERE user_id = ?) AND status = 'unread';

-- name: MarkCategoryItemsRead :exec
UPDATE items SET status = 'read', updated_at = DATETIME('now')
WHERE feed_id IN (SELECT id FROM feeds WHERE user_id = @user_id AND category = @category)
  AND status = 'unread';

-- name: MarkFeedReadAbove :exec
-- Mark items at or above the boundary item as read, within a single feed.
--
-- The boundary row is identified by id and its ordering key is read inside SQL
-- rather than bound as a timestamp from Go. SQLite stores these columns as TEXT
-- and a Go time.Time binds in a different text layout
-- ("2026-09-10 10:31:06+00:00") than the stored value ("2026-09-10T10:31:06Z"),
-- so comparing against a bound time matched nothing and the boundary item was
-- never included.
--
-- Ordering matches the item list: COALESCE(published_at, '') DESC, id DESC. The
-- '' stand-in keeps undated items (published_at IS NULL) reachable instead of
-- dropping them out of both directions, and id replaces created_at as the
-- tiebreak so the comparison never crosses timestamp layouts.
UPDATE items SET status = 'read', updated_at = DATETIME('now')
WHERE items.feed_id = @feed_id
  AND items.status = 'unread'
  AND (
    COALESCE(items.published_at, '') > (SELECT COALESCE(bi.published_at, '') FROM items bi WHERE bi.id = @boundary_id)
    OR (COALESCE(items.published_at, '') = (SELECT COALESCE(bi.published_at, '') FROM items bi WHERE bi.id = @boundary_id)
        AND items.id >= @boundary_id)
  );

-- name: MarkFeedReadBelow :exec
-- Mark items strictly below the boundary item as read, within a single feed.
-- See MarkFeedReadAbove for the boundary/ordering rationale.
UPDATE items SET status = 'read', updated_at = DATETIME('now')
WHERE items.feed_id = @feed_id
  AND items.status = 'unread'
  AND (
    COALESCE(items.published_at, '') < (SELECT COALESCE(bi.published_at, '') FROM items bi WHERE bi.id = @boundary_id)
    OR (COALESCE(items.published_at, '') = (SELECT COALESCE(bi.published_at, '') FROM items bi WHERE bi.id = @boundary_id)
        AND items.id < @boundary_id)
  );

-- name: MarkCategoryReadAbove :exec
-- Mark items at or above the boundary item as read, across a feed category.
-- See MarkFeedReadAbove for the boundary/ordering rationale.
UPDATE items SET status = 'read', updated_at = DATETIME('now')
WHERE items.feed_id IN (SELECT f.id FROM feeds f WHERE f.user_id = @user_id AND f.category = @category)
  AND items.status = 'unread'
  AND (
    COALESCE(items.published_at, '') > (SELECT COALESCE(bi.published_at, '') FROM items bi WHERE bi.id = @boundary_id)
    OR (COALESCE(items.published_at, '') = (SELECT COALESCE(bi.published_at, '') FROM items bi WHERE bi.id = @boundary_id)
        AND items.id >= @boundary_id)
  );

-- name: MarkCategoryReadBelow :exec
-- Mark items strictly below the boundary item as read, across a feed category.
-- See MarkFeedReadAbove for the boundary/ordering rationale.
UPDATE items SET status = 'read', updated_at = DATETIME('now')
WHERE items.feed_id IN (SELECT f.id FROM feeds f WHERE f.user_id = @user_id AND f.category = @category)
  AND items.status = 'unread'
  AND (
    COALESCE(items.published_at, '') < (SELECT COALESCE(bi.published_at, '') FROM items bi WHERE bi.id = @boundary_id)
    OR (COALESCE(items.published_at, '') = (SELECT COALESCE(bi.published_at, '') FROM items bi WHERE bi.id = @boundary_id)
        AND items.id < @boundary_id)
  );

-- name: MarkAllFeedsReadAbove :exec
-- Mark items at or above the boundary item as read, across every feed the user
-- owns (the "Feeds" section view).
-- See MarkFeedReadAbove for the boundary/ordering rationale.
UPDATE items SET status = 'read', updated_at = DATETIME('now')
WHERE items.feed_id IN (SELECT f.id FROM feeds f WHERE f.user_id = @user_id)
  AND items.status = 'unread'
  AND (
    COALESCE(items.published_at, '') > (SELECT COALESCE(bi.published_at, '') FROM items bi WHERE bi.id = @boundary_id)
    OR (COALESCE(items.published_at, '') = (SELECT COALESCE(bi.published_at, '') FROM items bi WHERE bi.id = @boundary_id)
        AND items.id >= @boundary_id)
  );

-- name: MarkAllFeedsReadBelow :exec
-- Mark items strictly below the boundary item as read, across every feed the
-- user owns (the "Feeds" section view).
-- See MarkFeedReadAbove for the boundary/ordering rationale.
UPDATE items SET status = 'read', updated_at = DATETIME('now')
WHERE items.feed_id IN (SELECT f.id FROM feeds f WHERE f.user_id = @user_id)
  AND items.status = 'unread'
  AND (
    COALESCE(items.published_at, '') < (SELECT COALESCE(bi.published_at, '') FROM items bi WHERE bi.id = @boundary_id)
    OR (COALESCE(items.published_at, '') = (SELECT COALESCE(bi.published_at, '') FROM items bi WHERE bi.id = @boundary_id)
        AND items.id < @boundary_id)
  );

-- name: ListItemsByUserStatus :many
SELECT i.* FROM items i
JOIN feeds f ON f.id = i.feed_id
WHERE f.user_id = ? AND i.status = ?
ORDER BY i.published_at DESC, i.created_at DESC;

-- name: ListItemsByUserStatusPaginated :many
-- Reader-stream page for a user-wide status filter.
SELECT i.* FROM items i
JOIN feeds f ON f.id = i.feed_id
WHERE f.user_id = @user_id AND i.status = @status
ORDER BY COALESCE(i.published_at, i.fetched_at, i.created_at) DESC, i.created_at DESC
LIMIT @lim OFFSET @off;

-- name: ListItemsByUserReadPaginated :many
-- Reader-stream page for the 'read' stream (read + digested).
SELECT i.* FROM items i
JOIN feeds f ON f.id = i.feed_id
WHERE f.user_id = @user_id AND i.status IN ('read', 'digested')
ORDER BY COALESCE(i.published_at, i.fetched_at, i.created_at) DESC, i.created_at DESC
LIMIT @lim OFFSET @off;

-- name: ListItemsByCategoryPaginated :many
-- Reader-stream page for a label (feed category).
SELECT i.* FROM items i
JOIN feeds f ON f.id = i.feed_id
WHERE f.category = @category AND f.user_id = @user_id
ORDER BY COALESCE(i.published_at, i.fetched_at, i.created_at) DESC, i.created_at DESC
LIMIT @lim OFFSET @off;

-- name: ListItemsByUserRead :many
SELECT i.* FROM items i
JOIN feeds f ON f.id = i.feed_id
WHERE f.user_id = ? AND i.status IN ('read', 'digested')
ORDER BY i.published_at DESC, i.created_at DESC;

-- name: ListItemsByCategory :many
SELECT i.* FROM items i
JOIN feeds f ON f.id = i.feed_id
WHERE f.category = ? AND f.user_id = ?
ORDER BY i.published_at DESC, i.created_at DESC;

-- name: GetItemTitleByHash :one
SELECT title FROM items WHERE feed_id = ? AND hash = ?;

-- name: UpdateItemTitle :exec
UPDATE items SET title = ?, updated_at = DATETIME('now') WHERE feed_id = ? AND hash = ?;
