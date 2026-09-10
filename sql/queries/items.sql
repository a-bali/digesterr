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
ORDER BY published_at DESC, created_at DESC, id DESC
LIMIT sqlc.arg(limit) OFFSET sqlc.arg(offset);

-- name: ListItemsByFeedSortedDateAsc :many
SELECT i.id, i.title, i.status, i.published_at, i.content,
       i.extract_content, i.extract_status,
       i.points, i.comments_count, i.comment_url
FROM items i
WHERE feed_id = sqlc.arg(feed_id)
  AND (status = sqlc.narg(status) OR sqlc.narg(status) IS NULL)
  AND (points >= sqlc.arg(min_score) OR sqlc.arg(min_score) = 0)
ORDER BY published_at ASC, created_at ASC, id ASC
LIMIT sqlc.arg(limit) OFFSET sqlc.arg(offset);

-- name: ListItemsByFeedSortedScoreDesc :many
SELECT i.id, i.title, i.status, i.published_at, i.content,
       i.extract_content, i.extract_status,
       i.points, i.comments_count, i.comment_url
FROM items i
WHERE feed_id = sqlc.arg(feed_id)
  AND (status = sqlc.narg(status) OR sqlc.narg(status) IS NULL)
  AND (points >= sqlc.arg(min_score) OR sqlc.arg(min_score) = 0)
ORDER BY points DESC, published_at DESC, created_at DESC, id DESC
LIMIT sqlc.arg(limit) OFFSET sqlc.arg(offset);

-- name: ListItemsByFeedSortedScoreAsc :many
SELECT i.id, i.title, i.status, i.published_at, i.content,
       i.extract_content, i.extract_status,
       i.points, i.comments_count, i.comment_url
FROM items i
WHERE feed_id = sqlc.arg(feed_id)
  AND (status = sqlc.narg(status) OR sqlc.narg(status) IS NULL)
  AND (points >= sqlc.arg(min_score) OR sqlc.arg(min_score) = 0)
ORDER BY points ASC, published_at DESC, created_at DESC, id DESC
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

-- ---------------------------------------------------------------------------
-- Relative mark-read: mark everything at/above or below a boundary item.
--
-- The boundary row and its ordering keys are read inside SQL rather than bound
-- from Go. SQLite stores published_at/created_at as TEXT in DATETIME('now')
-- layout ("2026-09-10 10:31:06") whereas a Go time.Time binds as RFC3339
-- ("2026-09-10T10:31:06Z"), so a comparison against a bound value never matched
-- and the boundary item itself was never marked.
--
-- "above"/"below" are relative to the ordering the middle pane is showing, so
-- there is one query per (scope, sort). Both the ordering and the pane's score
-- filter live in SQL so rows that are not loaded yet, or are filtered out, are
-- still resolved correctly. The predicates mirror the list ORDER BY exactly,
-- including its id tiebreak:
--
--   DateDesc  (published_at DESC, created_at DESC, id DESC)
--   DateAsc   (published_at ASC,  created_at ASC,  id ASC)
--   ScoreDesc (points DESC, published_at DESC, created_at DESC, id DESC)
--   ScoreAsc  (points ASC,  published_at DESC, created_at DESC, id DESC)
--
-- Row-value comparison expresses the lexicographic "at or before" / "strictly
-- after" test in one expression, apart from ScoreAsc, whose keys mix directions
-- and so has its leading ASC key split off first. Because the id tiebreak is
-- part of both the predicate and the ordering, the two agree on every row:
-- above/below partition the pane exactly, and the boundary lands in "above".
-- COALESCE(published_at, '') matches SQLite's NULLS FIRST (ASC) / LAST (DESC)
-- placement, which keeps undated items reachable from either direction.
--
-- @min_score mirrors the pane's score filter and is applied to the rows being
-- updated, so rows the user cannot see are never marked.
-- ---------------------------------------------------------------------------

-- name: MarkFeedReadRelativeDateDesc :exec
-- Mark every item above/below the boundary item as read, within a single feed.
--
UPDATE items SET status = 'read', updated_at = DATETIME('now')
WHERE items.feed_id = sqlc.arg(feed_id)
  AND items.status = 'unread'
  AND (CAST(sqlc.arg(min_score) AS INTEGER) = 0 OR items.points >= CAST(sqlc.arg(min_score) AS INTEGER))
  AND (CASE WHEN CAST(sqlc.arg(above) AS INTEGER) THEN
         ((COALESCE(items.published_at, ''), items.created_at, items.id) >= (SELECT COALESCE(bi.published_at, ''), bi.created_at, bi.id FROM items bi WHERE bi.id = sqlc.arg(boundary_id)))
       ELSE
         ((COALESCE(items.published_at, ''), items.created_at, items.id) < (SELECT COALESCE(bi.published_at, ''), bi.created_at, bi.id FROM items bi WHERE bi.id = sqlc.arg(boundary_id)))
       END) = 1;

-- name: MarkFeedReadRelativeDateAsc :exec
-- Mark every item above/below the boundary item as read, within a single feed.
--
UPDATE items SET status = 'read', updated_at = DATETIME('now')
WHERE items.feed_id = sqlc.arg(feed_id)
  AND items.status = 'unread'
  AND (CAST(sqlc.arg(min_score) AS INTEGER) = 0 OR items.points >= CAST(sqlc.arg(min_score) AS INTEGER))
  AND (CASE WHEN CAST(sqlc.arg(above) AS INTEGER) THEN
         ((COALESCE(items.published_at, ''), items.created_at, items.id) <= (SELECT COALESCE(bi.published_at, ''), bi.created_at, bi.id FROM items bi WHERE bi.id = sqlc.arg(boundary_id)))
       ELSE
         ((COALESCE(items.published_at, ''), items.created_at, items.id) > (SELECT COALESCE(bi.published_at, ''), bi.created_at, bi.id FROM items bi WHERE bi.id = sqlc.arg(boundary_id)))
       END) = 1;

-- name: MarkFeedReadRelativeScoreDesc :exec
-- Mark every item above/below the boundary item as read, within a single feed.
--
UPDATE items SET status = 'read', updated_at = DATETIME('now')
WHERE items.feed_id = sqlc.arg(feed_id)
  AND items.status = 'unread'
  AND (CAST(sqlc.arg(min_score) AS INTEGER) = 0 OR items.points >= CAST(sqlc.arg(min_score) AS INTEGER))
  AND (CASE WHEN CAST(sqlc.arg(above) AS INTEGER) THEN
         ((items.points, COALESCE(items.published_at, ''), items.created_at, items.id) >= (SELECT bi.points, COALESCE(bi.published_at, ''), bi.created_at, bi.id FROM items bi WHERE bi.id = sqlc.arg(boundary_id)))
       ELSE
         ((items.points, COALESCE(items.published_at, ''), items.created_at, items.id) < (SELECT bi.points, COALESCE(bi.published_at, ''), bi.created_at, bi.id FROM items bi WHERE bi.id = sqlc.arg(boundary_id)))
       END) = 1;

-- name: MarkFeedReadRelativeScoreAsc :exec
-- Mark every item above/below the boundary item as read, within a single feed.
--
UPDATE items SET status = 'read', updated_at = DATETIME('now')
WHERE items.feed_id = sqlc.arg(feed_id)
  AND items.status = 'unread'
  AND (CAST(sqlc.arg(min_score) AS INTEGER) = 0 OR items.points >= CAST(sqlc.arg(min_score) AS INTEGER))
  AND (CASE WHEN CAST(sqlc.arg(above) AS INTEGER) THEN
         (items.points < (SELECT bi.points FROM items bi WHERE bi.id = sqlc.arg(boundary_id))
           OR (items.points = (SELECT bi.points FROM items bi WHERE bi.id = sqlc.arg(boundary_id)) AND (COALESCE(items.published_at, ''), items.created_at, items.id) >= (SELECT COALESCE(bi.published_at, ''), bi.created_at, bi.id FROM items bi WHERE bi.id = sqlc.arg(boundary_id))))
       ELSE
         (items.points > (SELECT bi.points FROM items bi WHERE bi.id = sqlc.arg(boundary_id))
           OR (items.points = (SELECT bi.points FROM items bi WHERE bi.id = sqlc.arg(boundary_id)) AND (COALESCE(items.published_at, ''), items.created_at, items.id) < (SELECT COALESCE(bi.published_at, ''), bi.created_at, bi.id FROM items bi WHERE bi.id = sqlc.arg(boundary_id))))
       END) = 1;

-- name: MarkCategoryReadRelativeDateDesc :exec
-- Mark every item above/below the boundary item as read, across one feed category.
--
UPDATE items SET status = 'read', updated_at = DATETIME('now')
WHERE items.feed_id IN (SELECT f.id FROM feeds f WHERE f.user_id = sqlc.arg(user_id) AND f.category = sqlc.arg(category))
  AND items.status = 'unread'
  AND (CAST(sqlc.arg(min_score) AS INTEGER) = 0 OR items.points >= CAST(sqlc.arg(min_score) AS INTEGER))
  AND (CASE WHEN CAST(sqlc.arg(above) AS INTEGER) THEN
         ((COALESCE(items.published_at, ''), items.created_at, items.id) >= (SELECT COALESCE(bi.published_at, ''), bi.created_at, bi.id FROM items bi WHERE bi.id = sqlc.arg(boundary_id)))
       ELSE
         ((COALESCE(items.published_at, ''), items.created_at, items.id) < (SELECT COALESCE(bi.published_at, ''), bi.created_at, bi.id FROM items bi WHERE bi.id = sqlc.arg(boundary_id)))
       END) = 1;

-- name: MarkCategoryReadRelativeDateAsc :exec
-- Mark every item above/below the boundary item as read, across one feed category.
--
UPDATE items SET status = 'read', updated_at = DATETIME('now')
WHERE items.feed_id IN (SELECT f.id FROM feeds f WHERE f.user_id = sqlc.arg(user_id) AND f.category = sqlc.arg(category))
  AND items.status = 'unread'
  AND (CAST(sqlc.arg(min_score) AS INTEGER) = 0 OR items.points >= CAST(sqlc.arg(min_score) AS INTEGER))
  AND (CASE WHEN CAST(sqlc.arg(above) AS INTEGER) THEN
         ((COALESCE(items.published_at, ''), items.created_at, items.id) <= (SELECT COALESCE(bi.published_at, ''), bi.created_at, bi.id FROM items bi WHERE bi.id = sqlc.arg(boundary_id)))
       ELSE
         ((COALESCE(items.published_at, ''), items.created_at, items.id) > (SELECT COALESCE(bi.published_at, ''), bi.created_at, bi.id FROM items bi WHERE bi.id = sqlc.arg(boundary_id)))
       END) = 1;

-- name: MarkCategoryReadRelativeScoreDesc :exec
-- Mark every item above/below the boundary item as read, across one feed category.
--
UPDATE items SET status = 'read', updated_at = DATETIME('now')
WHERE items.feed_id IN (SELECT f.id FROM feeds f WHERE f.user_id = sqlc.arg(user_id) AND f.category = sqlc.arg(category))
  AND items.status = 'unread'
  AND (CAST(sqlc.arg(min_score) AS INTEGER) = 0 OR items.points >= CAST(sqlc.arg(min_score) AS INTEGER))
  AND (CASE WHEN CAST(sqlc.arg(above) AS INTEGER) THEN
         ((items.points, COALESCE(items.published_at, ''), items.created_at, items.id) >= (SELECT bi.points, COALESCE(bi.published_at, ''), bi.created_at, bi.id FROM items bi WHERE bi.id = sqlc.arg(boundary_id)))
       ELSE
         ((items.points, COALESCE(items.published_at, ''), items.created_at, items.id) < (SELECT bi.points, COALESCE(bi.published_at, ''), bi.created_at, bi.id FROM items bi WHERE bi.id = sqlc.arg(boundary_id)))
       END) = 1;

-- name: MarkCategoryReadRelativeScoreAsc :exec
-- Mark every item above/below the boundary item as read, across one feed category.
--
UPDATE items SET status = 'read', updated_at = DATETIME('now')
WHERE items.feed_id IN (SELECT f.id FROM feeds f WHERE f.user_id = sqlc.arg(user_id) AND f.category = sqlc.arg(category))
  AND items.status = 'unread'
  AND (CAST(sqlc.arg(min_score) AS INTEGER) = 0 OR items.points >= CAST(sqlc.arg(min_score) AS INTEGER))
  AND (CASE WHEN CAST(sqlc.arg(above) AS INTEGER) THEN
         (items.points < (SELECT bi.points FROM items bi WHERE bi.id = sqlc.arg(boundary_id))
           OR (items.points = (SELECT bi.points FROM items bi WHERE bi.id = sqlc.arg(boundary_id)) AND (COALESCE(items.published_at, ''), items.created_at, items.id) >= (SELECT COALESCE(bi.published_at, ''), bi.created_at, bi.id FROM items bi WHERE bi.id = sqlc.arg(boundary_id))))
       ELSE
         (items.points > (SELECT bi.points FROM items bi WHERE bi.id = sqlc.arg(boundary_id))
           OR (items.points = (SELECT bi.points FROM items bi WHERE bi.id = sqlc.arg(boundary_id)) AND (COALESCE(items.published_at, ''), items.created_at, items.id) < (SELECT COALESCE(bi.published_at, ''), bi.created_at, bi.id FROM items bi WHERE bi.id = sqlc.arg(boundary_id))))
       END) = 1;

-- name: MarkAllFeedsReadRelativeDateDesc :exec
-- Mark every item above/below the boundary item as read, across every feed the user owns (the "Feeds" section).
--
UPDATE items SET status = 'read', updated_at = DATETIME('now')
WHERE items.feed_id IN (SELECT f.id FROM feeds f WHERE f.user_id = sqlc.arg(user_id))
  AND items.status = 'unread'
  AND (CAST(sqlc.arg(min_score) AS INTEGER) = 0 OR items.points >= CAST(sqlc.arg(min_score) AS INTEGER))
  AND (CASE WHEN CAST(sqlc.arg(above) AS INTEGER) THEN
         ((COALESCE(items.published_at, ''), items.created_at, items.id) >= (SELECT COALESCE(bi.published_at, ''), bi.created_at, bi.id FROM items bi WHERE bi.id = sqlc.arg(boundary_id)))
       ELSE
         ((COALESCE(items.published_at, ''), items.created_at, items.id) < (SELECT COALESCE(bi.published_at, ''), bi.created_at, bi.id FROM items bi WHERE bi.id = sqlc.arg(boundary_id)))
       END) = 1;

-- name: MarkAllFeedsReadRelativeDateAsc :exec
-- Mark every item above/below the boundary item as read, across every feed the user owns (the "Feeds" section).
--
UPDATE items SET status = 'read', updated_at = DATETIME('now')
WHERE items.feed_id IN (SELECT f.id FROM feeds f WHERE f.user_id = sqlc.arg(user_id))
  AND items.status = 'unread'
  AND (CAST(sqlc.arg(min_score) AS INTEGER) = 0 OR items.points >= CAST(sqlc.arg(min_score) AS INTEGER))
  AND (CASE WHEN CAST(sqlc.arg(above) AS INTEGER) THEN
         ((COALESCE(items.published_at, ''), items.created_at, items.id) <= (SELECT COALESCE(bi.published_at, ''), bi.created_at, bi.id FROM items bi WHERE bi.id = sqlc.arg(boundary_id)))
       ELSE
         ((COALESCE(items.published_at, ''), items.created_at, items.id) > (SELECT COALESCE(bi.published_at, ''), bi.created_at, bi.id FROM items bi WHERE bi.id = sqlc.arg(boundary_id)))
       END) = 1;

-- name: MarkAllFeedsReadRelativeScoreDesc :exec
-- Mark every item above/below the boundary item as read, across every feed the user owns (the "Feeds" section).
--
UPDATE items SET status = 'read', updated_at = DATETIME('now')
WHERE items.feed_id IN (SELECT f.id FROM feeds f WHERE f.user_id = sqlc.arg(user_id))
  AND items.status = 'unread'
  AND (CAST(sqlc.arg(min_score) AS INTEGER) = 0 OR items.points >= CAST(sqlc.arg(min_score) AS INTEGER))
  AND (CASE WHEN CAST(sqlc.arg(above) AS INTEGER) THEN
         ((items.points, COALESCE(items.published_at, ''), items.created_at, items.id) >= (SELECT bi.points, COALESCE(bi.published_at, ''), bi.created_at, bi.id FROM items bi WHERE bi.id = sqlc.arg(boundary_id)))
       ELSE
         ((items.points, COALESCE(items.published_at, ''), items.created_at, items.id) < (SELECT bi.points, COALESCE(bi.published_at, ''), bi.created_at, bi.id FROM items bi WHERE bi.id = sqlc.arg(boundary_id)))
       END) = 1;

-- name: MarkAllFeedsReadRelativeScoreAsc :exec
-- Mark every item above/below the boundary item as read, across every feed the user owns (the "Feeds" section).
--
UPDATE items SET status = 'read', updated_at = DATETIME('now')
WHERE items.feed_id IN (SELECT f.id FROM feeds f WHERE f.user_id = sqlc.arg(user_id))
  AND items.status = 'unread'
  AND (CAST(sqlc.arg(min_score) AS INTEGER) = 0 OR items.points >= CAST(sqlc.arg(min_score) AS INTEGER))
  AND (CASE WHEN CAST(sqlc.arg(above) AS INTEGER) THEN
         (items.points < (SELECT bi.points FROM items bi WHERE bi.id = sqlc.arg(boundary_id))
           OR (items.points = (SELECT bi.points FROM items bi WHERE bi.id = sqlc.arg(boundary_id)) AND (COALESCE(items.published_at, ''), items.created_at, items.id) >= (SELECT COALESCE(bi.published_at, ''), bi.created_at, bi.id FROM items bi WHERE bi.id = sqlc.arg(boundary_id))))
       ELSE
         (items.points > (SELECT bi.points FROM items bi WHERE bi.id = sqlc.arg(boundary_id))
           OR (items.points = (SELECT bi.points FROM items bi WHERE bi.id = sqlc.arg(boundary_id)) AND (COALESCE(items.published_at, ''), items.created_at, items.id) < (SELECT COALESCE(bi.published_at, ''), bi.created_at, bi.id FROM items bi WHERE bi.id = sqlc.arg(boundary_id))))
       END) = 1;

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
