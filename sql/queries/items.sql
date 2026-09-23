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
-- @min_score_filter = 1 applies the feed's own min_score threshold, matching
-- the web UI's default view for that feed.
SELECT items.* FROM items
JOIN feeds f ON f.id = items.feed_id
WHERE items.feed_id = @feed_id
  AND (CAST(@min_score_filter AS INTEGER) = 0 OR f.min_score = 0 OR items.points >= f.min_score)
ORDER BY COALESCE(items.published_at, items.fetched_at, items.created_at) DESC, items.created_at DESC
LIMIT @lim OFFSET @off;

-- name: ListItemsByFeedAndStatus :many
SELECT * FROM items WHERE feed_id = ? AND status = ? ORDER BY published_at DESC, created_at DESC;

-- name: UpdateItemStatus :one
UPDATE items
SET status = ?, updated_at = DATETIME('now')
WHERE id = ?
RETURNING *;

-- name: SetItemSaved :one
-- Saved is orthogonal to read state: this touches ONLY is_saved, so a read
-- toggle can never destroy saved-ness and vice versa. `saved = 1` also marks an
-- unread item read (matching the pre-flag behaviour, where a saved item was not
-- in the unread counts); unsaving clears the flag and leaves the read state
-- alone, so an item that was already read stays read.
--
-- mark_read carries the same value as saved. It is a second named parameter
-- rather than a repeat of `saved` because a bare repeat inside the CASE would
-- work in SQLite but leaves sqlc generating a positional Column2, and the
-- duplication documents the rule: only the SAVE direction forces read.
UPDATE items
SET is_saved = sqlc.arg(saved),
    status = CASE WHEN sqlc.arg(mark_read) = 1 AND status = 'unread' THEN 'read' ELSE status END,
    updated_at = DATETIME('now')
WHERE id = sqlc.arg(id)
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

-- name: DeleteUnkeptFeedItems :execrows
-- Removes a feed's disposable items on feed deletion: everything that is
-- neither saved nor referenced by a digest result (those are re-homed to the
-- feed's archive instead). Saved-ness is checked first because a saved item
-- may also be digest-linked -- either reason keeps it.
DELETE FROM items
WHERE feed_id = ?
  AND is_saved = 0
  AND NOT EXISTS (SELECT 1 FROM digest_result_items dri WHERE dri.item_id = items.id);

-- name: RehomeFeedItems :execrows
-- Moves a feed's kept items (saved or digest-linked) to its archive feed on
-- deletion, preserving the original read state. The (feed_id, hash) unique
-- index cannot collide: archive feeds start empty and receive rows once.
UPDATE items SET feed_id = sqlc.arg(new_feed_id), updated_at = DATETIME('now')
WHERE feed_id = sqlc.arg(old_feed_id)
  AND (is_saved = 1 OR EXISTS (SELECT 1 FROM digest_result_items dri WHERE dri.item_id = items.id));

-- name: DeleteExpiredItems :execrows
DELETE FROM items
WHERE is_saved = 0
  AND items.created_at < ?
  AND NOT EXISTS (SELECT 1 FROM digest_result_items dri WHERE dri.item_id = items.id);

-- name: GetItemWithFeedOwner :one
SELECT i.* FROM items i
JOIN feeds f ON f.id = i.feed_id
WHERE i.id = ? AND f.user_id = ?;

-- name: GetUnreadCounts :many
-- Single GROUP BY join instead of a correlated subquery per feed.
-- Items below their feed's min_score are invisible to the list views, so
-- they are not counted either: the badge must agree with the pane.
SELECT f.id AS feed_id, CAST(COALESCE(COUNT(i.id), 0) AS INTEGER) AS unread_count
FROM feeds f
LEFT JOIN items i ON i.feed_id = f.id AND i.status = 'unread'
  AND (f.min_score = 0 OR i.points >= f.min_score)
WHERE f.user_id = ?
GROUP BY f.id;

-- name: GetReaderUnreadCounts :many
-- Unfiltered twin of GetUnreadCounts for the Google Reader API, which has no
-- min_score parameter: third-party clients see every unread item.
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
WHERE f.user_id = ? AND i.is_saved = 1;

-- name: ListSavedItemsByUser :many
-- The Saved view, assembled server-side in one query instead of N per-feed
-- calls. Archive feeds (orphaned saved items of deleted feeds) join normally,
-- so surviving items keep their "Saved -- <name>" provenance.
--
-- @status filters by read state ('' = every saved item). It is applied here
-- rather than in the client because the list is paginated: filtering the loaded
-- page would silently narrow to whichever rows happen to be in memory.
--
-- LIMIT -1 means "no limit" in SQLite, so an unpaginated call (lim = -1) returns
-- the whole list and callers that pass no limit keep working. Paging is offset
-- based and the ORDER BY ends in i.id DESC for that reason: (published_at,
-- created_at) is not unique, so a page boundary inside a tie would otherwise
-- duplicate or skip rows between requests.
SELECT i.id, i.title, substr(i.content, 1, 8192) AS content, i.comment_url, i.points, i.comments_count,
       i.published_at, i.status, i.is_saved, substr(i.extract_content, 1, 8192) AS extract_content, i.extract_status,
       i.feed_id, f.name AS feed_name, f.type AS feed_type
FROM items i
JOIN feeds f ON f.id = i.feed_id
WHERE f.user_id = @user_id AND i.is_saved = 1
  AND (i.status = @status OR @status = '')
ORDER BY i.published_at DESC, i.created_at DESC, i.id DESC
LIMIT @lim OFFSET @off;

-- name: CountSavedItemsByUser :one
-- Filter-aware total for the paginated Saved view, matching ListSavedItemsByUser
-- exactly so the header's loaded/total pair describes one set.
SELECT CAST(COUNT(*) AS INTEGER) AS count FROM items i
JOIN feeds f ON f.id = i.feed_id
WHERE f.user_id = @user_id AND i.is_saved = 1
  AND (i.status = @status OR @status = '');


-- name: ListItemsByFeedPaginated :many
SELECT * FROM items WHERE feed_id = ? ORDER BY published_at DESC, created_at DESC LIMIT ? OFFSET ?;

-- name: ListItemsByFeedAndStatusPaginated :many
SELECT * FROM items WHERE feed_id = ? AND status = ? ORDER BY published_at DESC, created_at DESC LIMIT ? OFFSET ?;

-- name: ListItemsByFeedSortedDateDesc :many
-- Preview query for the middle pane: excludes hash, url, author, and
-- internal metadata not needed for list display.
--
-- The bodies are capped at 8192 characters, not 500: the row's excerpt is
-- flattened from this prefix server-side (feed.ExcerptForPreview), and a cut is
-- a CHARACTER count over RAW HTML, so a body opening with inline SVG or a long
-- srcset list can spend several KB of markup before its first word of prose.
-- At 500 chars those rows rendered an EMPTY excerpt that filled in only after
-- the item was opened. The cap still keeps whole articles out of the response.
SELECT i.id, i.title, i.status, i.is_saved, i.published_at,
       substr(i.content, 1, 8192) AS content,
       substr(i.extract_content, 1, 8192) AS extract_content, i.extract_status,
       i.points, i.comments_count, i.comment_url
FROM items i
WHERE feed_id = sqlc.arg(feed_id)
  AND (status = sqlc.narg(status) OR sqlc.narg(status) IS NULL)
  -- saved_only = 1 asks for the starred set, which is a flag rather than a
  -- status now. The handler translates a `status=saved` request into
  -- status=NULL + saved_only=1, so the two filters never fight.
  -- CAST is required: a bare sqlc.arg comparison infers interface{} and a
  -- nil value makes `? = 0` NULL, which silently drops EVERY row (including
  -- callers that never set the filter). CAST makes it a real integer.
  AND (CAST(sqlc.arg(saved_only) AS INTEGER) = 0 OR i.is_saved = 1)
  AND (points >= sqlc.arg(min_score) OR sqlc.arg(min_score) = 0)
ORDER BY published_at DESC, created_at DESC, id DESC
LIMIT sqlc.arg(limit) OFFSET sqlc.arg(offset);

-- name: ListItemsByFeedSortedDateAsc :many
SELECT i.id, i.title, i.status, i.is_saved, i.published_at,
       substr(i.content, 1, 8192) AS content,
       substr(i.extract_content, 1, 8192) AS extract_content, i.extract_status,
       i.points, i.comments_count, i.comment_url
FROM items i
WHERE feed_id = sqlc.arg(feed_id)
  AND (status = sqlc.narg(status) OR sqlc.narg(status) IS NULL)
  -- saved_only = 1 asks for the starred set, which is a flag rather than a
  -- status now. The handler translates a `status=saved` request into
  -- status=NULL + saved_only=1, so the two filters never fight.
  -- CAST is required: a bare sqlc.arg comparison infers interface{} and a
  -- nil value makes `? = 0` NULL, which silently drops EVERY row (including
  -- callers that never set the filter). CAST makes it a real integer.
  AND (CAST(sqlc.arg(saved_only) AS INTEGER) = 0 OR i.is_saved = 1)
  AND (points >= sqlc.arg(min_score) OR sqlc.arg(min_score) = 0)
ORDER BY published_at ASC, created_at ASC, id ASC
LIMIT sqlc.arg(limit) OFFSET sqlc.arg(offset);

-- name: ListItemsByFeedSortedScoreDesc :many
SELECT i.id, i.title, i.status, i.is_saved, i.published_at,
       substr(i.content, 1, 8192) AS content,
       substr(i.extract_content, 1, 8192) AS extract_content, i.extract_status,
       i.points, i.comments_count, i.comment_url
FROM items i
WHERE feed_id = sqlc.arg(feed_id)
  AND (status = sqlc.narg(status) OR sqlc.narg(status) IS NULL)
  -- saved_only = 1 asks for the starred set, which is a flag rather than a
  -- status now. The handler translates a `status=saved` request into
  -- status=NULL + saved_only=1, so the two filters never fight.
  -- CAST is required: a bare sqlc.arg comparison infers interface{} and a
  -- nil value makes `? = 0` NULL, which silently drops EVERY row (including
  -- callers that never set the filter). CAST makes it a real integer.
  AND (CAST(sqlc.arg(saved_only) AS INTEGER) = 0 OR i.is_saved = 1)
  AND (points >= sqlc.arg(min_score) OR sqlc.arg(min_score) = 0)
ORDER BY points DESC, published_at DESC, created_at DESC, id DESC
LIMIT sqlc.arg(limit) OFFSET sqlc.arg(offset);

-- name: ListItemsByFeedSortedScoreAsc :many
SELECT i.id, i.title, i.status, i.is_saved, i.published_at,
       substr(i.content, 1, 8192) AS content,
       substr(i.extract_content, 1, 8192) AS extract_content, i.extract_status,
       i.points, i.comments_count, i.comment_url
FROM items i
WHERE feed_id = sqlc.arg(feed_id)
  AND (status = sqlc.narg(status) OR sqlc.narg(status) IS NULL)
  -- saved_only = 1 asks for the starred set, which is a flag rather than a
  -- status now. The handler translates a `status=saved` request into
  -- status=NULL + saved_only=1, so the two filters never fight.
  -- CAST is required: a bare sqlc.arg comparison infers interface{} and a
  -- nil value makes `? = 0` NULL, which silently drops EVERY row (including
  -- callers that never set the filter). CAST makes it a real integer.
  AND (CAST(sqlc.arg(saved_only) AS INTEGER) = 0 OR i.is_saved = 1)
  AND (points >= sqlc.arg(min_score) OR sqlc.arg(min_score) = 0)
ORDER BY points ASC, published_at DESC, created_at DESC, id DESC
LIMIT sqlc.arg(limit) OFFSET sqlc.arg(offset);

-- name: CountItemsByFeed :one
SELECT CAST(COUNT(*) AS INTEGER) AS count FROM items
WHERE feed_id = sqlc.arg(feed_id)
  AND (status = sqlc.narg(status) OR sqlc.narg(status) IS NULL)
  AND (CAST(sqlc.arg(saved_only) AS INTEGER) = 0 OR is_saved = 1)
  AND (points >= sqlc.arg(min_score) OR sqlc.arg(min_score) = 0);

-- name: ListItemsNeedingExtraction :many
SELECT * FROM items WHERE feed_id = ? AND url != '' AND extract_status IN ('pending', 'failed') AND extract_retries < 3 ORDER BY extract_retries ASC, published_at DESC;

-- name: UpdateItemExtraction :one
UPDATE items SET extract_content = ?, extract_status = ?, extract_retries = ?, extract_date = DATETIME('now'), updated_at = DATETIME('now') WHERE id = ? RETURNING *;

-- name: ListItemsNeedingReadability :many
-- Items on a readability feed whose body has not been processed yet.
--
-- Unlike ListItemsNeedingExtraction this does NOT require url != '': the input is
-- the stored body, not a fetchable page, and newsletter items often have no URL.
--
-- Only a never-processed item ('') qualifies. The transform is deterministic over a
-- fixed body, so re-running it cannot change the outcome: 'done' and 'skipped' would
-- repeat identically, and 'failed' (a mangled result) and 'cleared' (a user's
-- deletion) must not be undone by the next poll.
SELECT * FROM items WHERE feed_id = ? AND content != '' AND extract_status = '' ORDER BY published_at DESC;

-- name: ClearItemExtraction :one
-- Drop an extracted body so the item falls back to its original content.
-- extract_status = 'cleared' is a deliberate marker, not an empty string:
-- "needs extraction" is expressed as NOT(done) in this schema (see
-- ListItemsNeedingExtraction and the digest generator's per-item check), so an
-- empty status would be indistinguishable from "never extracted" and the next
-- extraction pass would fetch the body straight back, undoing the user's delete.
UPDATE items SET extract_content = '', extract_status = 'cleared', extract_retries = 0, extract_date = NULL, updated_at = DATETIME('now') WHERE id = ? RETURNING *;

-- name: MarkFeedRead :exec
UPDATE items SET status = 'read', updated_at = DATETIME('now') WHERE feed_id = ? AND status = 'unread';

-- name: MarkAllUserItemsRead :exec
UPDATE items SET status = 'read', updated_at = DATETIME('now') WHERE feed_id IN (SELECT id FROM feeds WHERE user_id = ?) AND status = 'unread';

-- name: MarkCategoryItemsRead :exec
UPDATE items SET status = 'read', updated_at = DATETIME('now')
WHERE feed_id IN (SELECT id FROM feeds WHERE user_id = @user_id AND category = @category)
  AND status = 'unread';

-- ---------------------------------------------------------------------------
-- Relative mark-read: mark everything above or below a boundary item.
--
-- Both directions EXCLUDE the boundary item itself: "above" is everything
-- strictly before it in the list order, "below" everything strictly after it.
-- The two therefore do not overlap and the hovered row is left untouched by
-- whichever button is pressed.
--
-- The boundary row and its ordering keys are read inside SQL rather than bound
-- from Go. SQLite stores published_at/created_at as TEXT in DATETIME('now')
-- layout ("2026-09-10 10:31:06") whereas a Go time.Time binds as RFC3339
-- ("2026-09-10T10:31:06Z"), so a comparison against a bound value never matched.
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
-- Row-value comparison expresses the lexicographic "strictly before"/"strictly
-- after" test in one expression, apart from ScoreAsc, whose keys mix directions
-- and so has its leading ASC key split off first. Because the id tiebreak is
-- part of both the predicate and the ordering, the two agree on every row.
-- COALESCE(published_at, '') matches SQLite's NULLS FIRST (ASC) / LAST (DESC)
-- placement, which keeps undated items reachable from either direction.
--
-- @min_score mirrors the pane's score filter and is applied to the rows being
-- updated, so rows the user cannot see are never marked.
-- ---------------------------------------------------------------------------

-- name: MarkFeedReadRelativeDateDesc :exec
-- Mark every item strictly above/below the boundary item as read, within a single feed.
--
UPDATE items SET status = 'read', updated_at = DATETIME('now')
WHERE items.feed_id = sqlc.arg(feed_id)
  AND items.status = 'unread'
  AND (CAST(sqlc.arg(min_score) AS INTEGER) = 0 OR items.points >= CAST(sqlc.arg(min_score) AS INTEGER))
  AND (CASE WHEN CAST(sqlc.arg(above) AS INTEGER) THEN
         ((COALESCE(items.published_at, ''), items.created_at, items.id) > (SELECT COALESCE(bi.published_at, ''), bi.created_at, bi.id FROM items bi WHERE bi.id = sqlc.arg(boundary_id)))
       ELSE
         ((COALESCE(items.published_at, ''), items.created_at, items.id) < (SELECT COALESCE(bi.published_at, ''), bi.created_at, bi.id FROM items bi WHERE bi.id = sqlc.arg(boundary_id)))
       END) = 1;

-- name: MarkFeedReadRelativeDateAsc :exec
-- Mark every item strictly above/below the boundary item as read, within a single feed.
--
UPDATE items SET status = 'read', updated_at = DATETIME('now')
WHERE items.feed_id = sqlc.arg(feed_id)
  AND items.status = 'unread'
  AND (CAST(sqlc.arg(min_score) AS INTEGER) = 0 OR items.points >= CAST(sqlc.arg(min_score) AS INTEGER))
  AND (CASE WHEN CAST(sqlc.arg(above) AS INTEGER) THEN
         ((COALESCE(items.published_at, ''), items.created_at, items.id) < (SELECT COALESCE(bi.published_at, ''), bi.created_at, bi.id FROM items bi WHERE bi.id = sqlc.arg(boundary_id)))
       ELSE
         ((COALESCE(items.published_at, ''), items.created_at, items.id) > (SELECT COALESCE(bi.published_at, ''), bi.created_at, bi.id FROM items bi WHERE bi.id = sqlc.arg(boundary_id)))
       END) = 1;

-- name: MarkFeedReadRelativeScoreDesc :exec
-- Mark every item strictly above/below the boundary item as read, within a single feed.
--
UPDATE items SET status = 'read', updated_at = DATETIME('now')
WHERE items.feed_id = sqlc.arg(feed_id)
  AND items.status = 'unread'
  AND (CAST(sqlc.arg(min_score) AS INTEGER) = 0 OR items.points >= CAST(sqlc.arg(min_score) AS INTEGER))
  AND (CASE WHEN CAST(sqlc.arg(above) AS INTEGER) THEN
         ((items.points, COALESCE(items.published_at, ''), items.created_at, items.id) > (SELECT bi.points, COALESCE(bi.published_at, ''), bi.created_at, bi.id FROM items bi WHERE bi.id = sqlc.arg(boundary_id)))
       ELSE
         ((items.points, COALESCE(items.published_at, ''), items.created_at, items.id) < (SELECT bi.points, COALESCE(bi.published_at, ''), bi.created_at, bi.id FROM items bi WHERE bi.id = sqlc.arg(boundary_id)))
       END) = 1;

-- name: MarkFeedReadRelativeScoreAsc :exec
-- Mark every item strictly above/below the boundary item as read, within a single feed.
--
UPDATE items SET status = 'read', updated_at = DATETIME('now')
WHERE items.feed_id = sqlc.arg(feed_id)
  AND items.status = 'unread'
  AND (CAST(sqlc.arg(min_score) AS INTEGER) = 0 OR items.points >= CAST(sqlc.arg(min_score) AS INTEGER))
  AND (CASE WHEN CAST(sqlc.arg(above) AS INTEGER) THEN
         (items.points < (SELECT bi.points FROM items bi WHERE bi.id = sqlc.arg(boundary_id))
           OR (items.points = (SELECT bi.points FROM items bi WHERE bi.id = sqlc.arg(boundary_id)) AND (COALESCE(items.published_at, ''), items.created_at, items.id) > (SELECT COALESCE(bi.published_at, ''), bi.created_at, bi.id FROM items bi WHERE bi.id = sqlc.arg(boundary_id))))
       ELSE
         (items.points > (SELECT bi.points FROM items bi WHERE bi.id = sqlc.arg(boundary_id))
           OR (items.points = (SELECT bi.points FROM items bi WHERE bi.id = sqlc.arg(boundary_id)) AND (COALESCE(items.published_at, ''), items.created_at, items.id) < (SELECT COALESCE(bi.published_at, ''), bi.created_at, bi.id FROM items bi WHERE bi.id = sqlc.arg(boundary_id))))
       END) = 1;

-- name: MarkCategoryReadRelativeDateDesc :exec
-- Mark every item strictly above/below the boundary item as read, across one feed category.
--
UPDATE items SET status = 'read', updated_at = DATETIME('now')
WHERE items.feed_id IN (SELECT f.id FROM feeds f WHERE f.user_id = sqlc.arg(user_id) AND f.category = sqlc.arg(category))
  AND items.status = 'unread'
  AND (CAST(sqlc.arg(min_score) AS INTEGER) = 0 OR items.points >= CAST(sqlc.arg(min_score) AS INTEGER))
  AND (CASE WHEN CAST(sqlc.arg(above) AS INTEGER) THEN
         ((COALESCE(items.published_at, ''), items.created_at, items.id) > (SELECT COALESCE(bi.published_at, ''), bi.created_at, bi.id FROM items bi WHERE bi.id = sqlc.arg(boundary_id)))
       ELSE
         ((COALESCE(items.published_at, ''), items.created_at, items.id) < (SELECT COALESCE(bi.published_at, ''), bi.created_at, bi.id FROM items bi WHERE bi.id = sqlc.arg(boundary_id)))
       END) = 1;

-- name: MarkCategoryReadRelativeDateAsc :exec
-- Mark every item strictly above/below the boundary item as read, across one feed category.
--
UPDATE items SET status = 'read', updated_at = DATETIME('now')
WHERE items.feed_id IN (SELECT f.id FROM feeds f WHERE f.user_id = sqlc.arg(user_id) AND f.category = sqlc.arg(category))
  AND items.status = 'unread'
  AND (CAST(sqlc.arg(min_score) AS INTEGER) = 0 OR items.points >= CAST(sqlc.arg(min_score) AS INTEGER))
  AND (CASE WHEN CAST(sqlc.arg(above) AS INTEGER) THEN
         ((COALESCE(items.published_at, ''), items.created_at, items.id) < (SELECT COALESCE(bi.published_at, ''), bi.created_at, bi.id FROM items bi WHERE bi.id = sqlc.arg(boundary_id)))
       ELSE
         ((COALESCE(items.published_at, ''), items.created_at, items.id) > (SELECT COALESCE(bi.published_at, ''), bi.created_at, bi.id FROM items bi WHERE bi.id = sqlc.arg(boundary_id)))
       END) = 1;

-- name: MarkCategoryReadRelativeScoreDesc :exec
-- Mark every item strictly above/below the boundary item as read, across one feed category.
--
UPDATE items SET status = 'read', updated_at = DATETIME('now')
WHERE items.feed_id IN (SELECT f.id FROM feeds f WHERE f.user_id = sqlc.arg(user_id) AND f.category = sqlc.arg(category))
  AND items.status = 'unread'
  AND (CAST(sqlc.arg(min_score) AS INTEGER) = 0 OR items.points >= CAST(sqlc.arg(min_score) AS INTEGER))
  AND (CASE WHEN CAST(sqlc.arg(above) AS INTEGER) THEN
         ((items.points, COALESCE(items.published_at, ''), items.created_at, items.id) > (SELECT bi.points, COALESCE(bi.published_at, ''), bi.created_at, bi.id FROM items bi WHERE bi.id = sqlc.arg(boundary_id)))
       ELSE
         ((items.points, COALESCE(items.published_at, ''), items.created_at, items.id) < (SELECT bi.points, COALESCE(bi.published_at, ''), bi.created_at, bi.id FROM items bi WHERE bi.id = sqlc.arg(boundary_id)))
       END) = 1;

-- name: MarkCategoryReadRelativeScoreAsc :exec
-- Mark every item strictly above/below the boundary item as read, across one feed category.
--
UPDATE items SET status = 'read', updated_at = DATETIME('now')
WHERE items.feed_id IN (SELECT f.id FROM feeds f WHERE f.user_id = sqlc.arg(user_id) AND f.category = sqlc.arg(category))
  AND items.status = 'unread'
  AND (CAST(sqlc.arg(min_score) AS INTEGER) = 0 OR items.points >= CAST(sqlc.arg(min_score) AS INTEGER))
  AND (CASE WHEN CAST(sqlc.arg(above) AS INTEGER) THEN
         (items.points < (SELECT bi.points FROM items bi WHERE bi.id = sqlc.arg(boundary_id))
           OR (items.points = (SELECT bi.points FROM items bi WHERE bi.id = sqlc.arg(boundary_id)) AND (COALESCE(items.published_at, ''), items.created_at, items.id) > (SELECT COALESCE(bi.published_at, ''), bi.created_at, bi.id FROM items bi WHERE bi.id = sqlc.arg(boundary_id))))
       ELSE
         (items.points > (SELECT bi.points FROM items bi WHERE bi.id = sqlc.arg(boundary_id))
           OR (items.points = (SELECT bi.points FROM items bi WHERE bi.id = sqlc.arg(boundary_id)) AND (COALESCE(items.published_at, ''), items.created_at, items.id) < (SELECT COALESCE(bi.published_at, ''), bi.created_at, bi.id FROM items bi WHERE bi.id = sqlc.arg(boundary_id))))
       END) = 1;

-- name: MarkAllFeedsReadRelativeDateDesc :exec
-- Mark every item strictly above/below the boundary item as read, across every feed the user owns (the "Feeds" section).
--
UPDATE items SET status = 'read', updated_at = DATETIME('now')
WHERE items.feed_id IN (SELECT f.id FROM feeds f WHERE f.user_id = sqlc.arg(user_id))
  AND items.status = 'unread'
  AND (CAST(sqlc.arg(min_score) AS INTEGER) = 0 OR items.points >= CAST(sqlc.arg(min_score) AS INTEGER))
  AND (CASE WHEN CAST(sqlc.arg(above) AS INTEGER) THEN
         ((COALESCE(items.published_at, ''), items.created_at, items.id) > (SELECT COALESCE(bi.published_at, ''), bi.created_at, bi.id FROM items bi WHERE bi.id = sqlc.arg(boundary_id)))
       ELSE
         ((COALESCE(items.published_at, ''), items.created_at, items.id) < (SELECT COALESCE(bi.published_at, ''), bi.created_at, bi.id FROM items bi WHERE bi.id = sqlc.arg(boundary_id)))
       END) = 1;

-- name: MarkAllFeedsReadRelativeDateAsc :exec
-- Mark every item strictly above/below the boundary item as read, across every feed the user owns (the "Feeds" section).
--
UPDATE items SET status = 'read', updated_at = DATETIME('now')
WHERE items.feed_id IN (SELECT f.id FROM feeds f WHERE f.user_id = sqlc.arg(user_id))
  AND items.status = 'unread'
  AND (CAST(sqlc.arg(min_score) AS INTEGER) = 0 OR items.points >= CAST(sqlc.arg(min_score) AS INTEGER))
  AND (CASE WHEN CAST(sqlc.arg(above) AS INTEGER) THEN
         ((COALESCE(items.published_at, ''), items.created_at, items.id) < (SELECT COALESCE(bi.published_at, ''), bi.created_at, bi.id FROM items bi WHERE bi.id = sqlc.arg(boundary_id)))
       ELSE
         ((COALESCE(items.published_at, ''), items.created_at, items.id) > (SELECT COALESCE(bi.published_at, ''), bi.created_at, bi.id FROM items bi WHERE bi.id = sqlc.arg(boundary_id)))
       END) = 1;

-- name: MarkAllFeedsReadRelativeScoreDesc :exec
-- Mark every item strictly above/below the boundary item as read, across every feed the user owns (the "Feeds" section).
--
UPDATE items SET status = 'read', updated_at = DATETIME('now')
WHERE items.feed_id IN (SELECT f.id FROM feeds f WHERE f.user_id = sqlc.arg(user_id))
  AND items.status = 'unread'
  AND (CAST(sqlc.arg(min_score) AS INTEGER) = 0 OR items.points >= CAST(sqlc.arg(min_score) AS INTEGER))
  AND (CASE WHEN CAST(sqlc.arg(above) AS INTEGER) THEN
         ((items.points, COALESCE(items.published_at, ''), items.created_at, items.id) > (SELECT bi.points, COALESCE(bi.published_at, ''), bi.created_at, bi.id FROM items bi WHERE bi.id = sqlc.arg(boundary_id)))
       ELSE
         ((items.points, COALESCE(items.published_at, ''), items.created_at, items.id) < (SELECT bi.points, COALESCE(bi.published_at, ''), bi.created_at, bi.id FROM items bi WHERE bi.id = sqlc.arg(boundary_id)))
       END) = 1;

-- name: MarkAllFeedsReadRelativeScoreAsc :exec
-- Mark every item strictly above/below the boundary item as read, across every feed the user owns (the "Feeds" section).
--
UPDATE items SET status = 'read', updated_at = DATETIME('now')
WHERE items.feed_id IN (SELECT f.id FROM feeds f WHERE f.user_id = sqlc.arg(user_id))
  AND items.status = 'unread'
  AND (CAST(sqlc.arg(min_score) AS INTEGER) = 0 OR items.points >= CAST(sqlc.arg(min_score) AS INTEGER))
  AND (CASE WHEN CAST(sqlc.arg(above) AS INTEGER) THEN
         (items.points < (SELECT bi.points FROM items bi WHERE bi.id = sqlc.arg(boundary_id))
           OR (items.points = (SELECT bi.points FROM items bi WHERE bi.id = sqlc.arg(boundary_id)) AND (COALESCE(items.published_at, ''), items.created_at, items.id) > (SELECT COALESCE(bi.published_at, ''), bi.created_at, bi.id FROM items bi WHERE bi.id = sqlc.arg(boundary_id))))
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
-- Reader-stream page for a user-wide status filter. saved_only = 1 selects the
-- starred set instead of a status, which is how the reader API's starred stream
-- stays independent of read state.
--
-- @min_score_filter = 1 applies each feed's own min_score threshold, so a
-- third-party client sees the same set the web UI's default view shows. The
-- starred stream passes 0: a favourite the user chose to keep must not be
-- hidden by a threshold that later changes.
SELECT i.* FROM items i
JOIN feeds f ON f.id = i.feed_id
WHERE f.user_id = @user_id
  AND (i.status = @status OR @status = '')
  AND (CAST(@saved_only AS INTEGER) = 0 OR i.is_saved = 1)
  AND (CAST(@min_score_filter AS INTEGER) = 0 OR f.min_score = 0 OR i.points >= f.min_score)
ORDER BY COALESCE(i.published_at, i.fetched_at, i.created_at) DESC, i.created_at DESC
LIMIT @lim OFFSET @off;

-- name: ListItemsByUserReadPaginated :many
-- Reader-stream page for the 'read' stream (read + digested). Read items are
-- never threshold-filtered: the threshold governs what is still unread.
SELECT i.* FROM items i
JOIN feeds f ON f.id = i.feed_id
WHERE f.user_id = @user_id AND i.status IN ('read', 'digested')
ORDER BY COALESCE(i.published_at, i.fetched_at, i.created_at) DESC, i.created_at DESC
LIMIT @lim OFFSET @off;

-- name: ListItemsByCategoryPaginated :many
-- Reader-stream page for a label (feed category). @min_score_filter = 1 applies
-- each feed's own min_score threshold.
SELECT i.* FROM items i
JOIN feeds f ON f.id = i.feed_id
WHERE f.category = @category AND f.user_id = @user_id
  AND (CAST(@min_score_filter AS INTEGER) = 0 OR f.min_score = 0 OR i.points >= f.min_score)
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

-- name: UpdateItemStatusByIDs :execrows
-- Batch twin of UpdateItemStatus for Reader edit-tag syncs: one statement for
-- the whole ID set, with ownership enforced by the feed join instead of a
-- per-ID pre-check. Foreign or missing IDs simply match nothing (best-effort,
-- matching the handler's 200-OK contract).
UPDATE items SET status = sqlc.arg(status), updated_at = DATETIME('now')
WHERE items.id IN (SELECT value FROM json_each(sqlc.arg(ids)))
  AND EXISTS (SELECT 1 FROM feeds f WHERE f.id = items.feed_id AND f.user_id = sqlc.arg(user_id));

-- name: SetItemSavedByIDs :execrows
-- Batch twin of SetItemSaved: flips the saved flag for the whole ID set with
-- ownership enforced by join. mark_read carries the same value as saved,
-- mirroring the single-item rule (saving an unread item reads it).
UPDATE items
SET is_saved = CAST(sqlc.arg(saved) AS INTEGER),
    status = CASE WHEN CAST(sqlc.arg(saved) AS INTEGER) = 1 AND status = 'unread' THEN 'read' ELSE status END,
    updated_at = DATETIME('now')
WHERE items.id IN (SELECT value FROM json_each(sqlc.arg(ids)))
  AND EXISTS (SELECT 1 FROM feeds f WHERE f.id = items.feed_id AND f.user_id = sqlc.arg(user_id));
