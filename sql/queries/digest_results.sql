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
--
-- The status filter is applied HERE, not in the browser. Filtering only the
-- rows the client happened to have loaded could only hide already-fetched
-- rows, so an older match sitting on page three was unreachable. Paginating a
-- filtered query is what makes the control mean what it says.
--
-- An unread result is stored as 'new' (generator.go writes it, and the
-- unread-count queries count status='new'). The handler translates the API's
-- 'unread' in, and the browser translates 'new' back to 'unread' on the way
-- out. narg keeps "no filter" NULL.
--
-- The id tiebreak keeps paging stable when several results share created_at.
--
-- NOTE: kept ASCII-only. An em dash inside a comment makes sqlc 1.31.1 fail to
-- parse the whole queries file (it emits `extraneous input` for unrelated
-- statements and generates nothing), so prose here uses plain hyphens.
SELECT dr.id, dr.user_id, dr.config_id, dr.status, dr.created_at, dr.updated_at,
       CAST(COALESCE(json_extract(dr.content, '$.title'), '') AS TEXT) AS title,
       COALESCE(json_array_length(json_extract(dr.content, '$.highlights')), 0)
       + COALESCE(json_array_length(json_extract(dr.content, '$.items')), 0) AS item_count
FROM digest_results dr
WHERE dr.config_id = sqlc.arg(config_id)
  AND dr.user_id = sqlc.arg(user_id)
  AND (dr.status = sqlc.narg(status) OR sqlc.narg(status) IS NULL)
ORDER BY dr.created_at DESC, dr.id DESC
LIMIT sqlc.arg(limit) OFFSET sqlc.arg(offset);

-- name: ListDigestResultsByConfigOldestFirstPaginated :many
-- Sort is a SEPARATE statement, not a bound sort direction. SQLite cannot take
-- a direction as a parameter, and sqlc does not collect parameters that appear
-- only inside ORDER BY expressions: it emits the literal @name and binds
-- nothing, so the request fails at runtime with "missing named argument".
-- Verified against the driver, so the sort variants stay explicit.
SELECT dr.id, dr.user_id, dr.config_id, dr.status, dr.created_at, dr.updated_at,
       CAST(COALESCE(json_extract(dr.content, '$.title'), '') AS TEXT) AS title,
       COALESCE(json_array_length(json_extract(dr.content, '$.highlights')), 0)
       + COALESCE(json_array_length(json_extract(dr.content, '$.items')), 0) AS item_count
FROM digest_results dr
WHERE dr.config_id = sqlc.arg(config_id)
  AND dr.user_id = sqlc.arg(user_id)
  AND (dr.status = sqlc.narg(status) OR sqlc.narg(status) IS NULL)
ORDER BY dr.created_at ASC, dr.id ASC
LIMIT sqlc.arg(limit) OFFSET sqlc.arg(offset);

-- name: CountDigestResultsByConfig :one
SELECT CAST(COUNT(*) AS INTEGER) AS count
FROM digest_results
WHERE config_id = sqlc.arg(config_id)
  AND user_id = sqlc.arg(user_id)
  AND (status = sqlc.narg(status) OR sqlc.narg(status) IS NULL);

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
--
-- status is applied server-side for the same reason as the per-config query:
-- the All-Digests list has the identical filter control, and filtering only the
-- loaded page left older matches unreachable.
SELECT dr.id, dr.user_id, dr.config_id, dr.status, dr.created_at, dr.updated_at,
       dc.name AS config_name,
       CAST(COALESCE(json_extract(dr.content, '$.title'), '') AS TEXT) AS title,
       COALESCE(json_array_length(json_extract(dr.content, '$.highlights')), 0)
       + COALESCE(json_array_length(json_extract(dr.content, '$.items')), 0) AS item_count
FROM digest_results dr
JOIN digest_configs dc ON dc.id = dr.config_id
WHERE dr.user_id = sqlc.arg(user_id)
  AND (dr.status = sqlc.narg(status) OR sqlc.narg(status) IS NULL)
ORDER BY dr.created_at DESC, dr.id DESC
LIMIT sqlc.arg(limit) OFFSET sqlc.arg(offset);

-- name: ListAllDigestResultsByUserOldestFirst :many
-- Oldest-first twin of ListAllDigestResultsByUser; see its comment for why the
-- sort direction is a separate statement rather than a bound parameter.
SELECT dr.id, dr.user_id, dr.config_id, dr.status, dr.created_at, dr.updated_at,
       dc.name AS config_name,
       CAST(COALESCE(json_extract(dr.content, '$.title'), '') AS TEXT) AS title,
       COALESCE(json_array_length(json_extract(dr.content, '$.highlights')), 0)
       + COALESCE(json_array_length(json_extract(dr.content, '$.items')), 0) AS item_count
FROM digest_results dr
JOIN digest_configs dc ON dc.id = dr.config_id
WHERE dr.user_id = sqlc.arg(user_id)
  AND (dr.status = sqlc.narg(status) OR sqlc.narg(status) IS NULL)
ORDER BY dr.created_at ASC, dr.id ASC
LIMIT sqlc.arg(limit) OFFSET sqlc.arg(offset);

-- name: CountAllDigestResultsByUser :one
SELECT CAST(COUNT(*) AS INTEGER) AS count
FROM digest_results dr
JOIN digest_configs dc ON dc.id = dr.config_id
WHERE dr.user_id = sqlc.arg(user_id)
  AND (dr.status = sqlc.narg(status) OR sqlc.narg(status) IS NULL);

-- name: ListDigestResultsByUserStatus :many
SELECT * FROM digest_results WHERE user_id = ? AND status = ? ORDER BY created_at DESC;

-- name: MarkAllUserDigestResultsRead :many
-- RETURNING id, not :exec: the caller must cascade into the linked ITEMS of the
-- results this statement actually transitioned. Deriving that set from a state
-- test instead (status = 'read') also matches every result that was already read
-- before the request, so an unrelated mark-read swept those results' items too.
UPDATE digest_results SET status = 'read', updated_at = DATETIME('now')
WHERE user_id = ? AND status = 'new'
RETURNING id;

-- name: MarkDigestConfigRead :many
-- RETURNING id for the same reason as MarkAllUserDigestResultsRead.
UPDATE digest_results SET status = 'read', updated_at = DATETIME('now')
WHERE config_id = ? AND user_id = ? AND status = 'new'
RETURNING id;

-- name: MarkDigestConfigReadAbove :many
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
--
-- RETURNING id so the cascade can be driven by the rows this statement
-- transitioned rather than by a status test over the whole config.
UPDATE digest_results SET status = 'read', updated_at = DATETIME('now')
WHERE digest_results.config_id = @config_id
  AND digest_results.user_id = @user_id
  AND digest_results.status = 'new'
  AND (
    digest_results.created_at > (SELECT br.created_at FROM digest_results br WHERE br.id = @boundary_id)
    OR (digest_results.created_at = (SELECT br.created_at FROM digest_results br WHERE br.id = @boundary_id)
        AND digest_results.id > @boundary_id)
  )
RETURNING id;

-- name: MarkDigestConfigReadBelow :many
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
  )
RETURNING id;

-- name: MarkAllDigestReadAbove :many
-- Mark all digest results at or above the boundary result as read.
-- See MarkDigestConfigReadAbove for the boundary rationale.
UPDATE digest_results SET status = 'read', updated_at = DATETIME('now')
WHERE digest_results.user_id = @user_id
  AND digest_results.status = 'new'
  AND (
    digest_results.created_at > (SELECT br.created_at FROM digest_results br WHERE br.id = @boundary_id)
    OR (digest_results.created_at = (SELECT br.created_at FROM digest_results br WHERE br.id = @boundary_id)
        AND digest_results.id > @boundary_id)
  )
RETURNING id;

-- name: MarkAllDigestReadBelow :many
-- Mark all digest results strictly below the boundary result as read.
-- See MarkDigestConfigReadAbove for the boundary rationale.
UPDATE digest_results SET status = 'read', updated_at = DATETIME('now')
WHERE digest_results.user_id = @user_id
  AND digest_results.status = 'new'
  AND (
    digest_results.created_at < (SELECT br.created_at FROM digest_results br WHERE br.id = @boundary_id)
    OR (digest_results.created_at = (SELECT br.created_at FROM digest_results br WHERE br.id = @boundary_id)
        AND digest_results.id < @boundary_id)
  )
RETURNING id;

-- name: MarkDigestResultItemsRead :exec
-- When a digest result is marked read, its linked items transition from
-- digested to read (spec: item lifecycle). Items in other states are untouched.
UPDATE items SET status = 'read', updated_at = DATETIME('now')
WHERE status = 'digested'
  AND id IN (SELECT item_id FROM digest_result_items WHERE result_id = ?);

-- name: ListExpiredDigestResultIDs :many
-- IDs of digest results older than the cutoff. Expiry flips each result's
-- linked items back to read (MarkDigestResultItemsRead) before deleting the
-- result, so expiry never strands items in 'digested'.
SELECT id FROM digest_results WHERE created_at < ? ORDER BY id;

-- end of file

-- name: UpdateDigestResultStatusByIDs :execrows
-- Batch twin of UpdateDigestResultStatus for Reader edit-tag syncs: one
-- statement for the whole ID set, ownership by user_id in the predicate.
UPDATE digest_results SET status = sqlc.arg(status), updated_at = DATETIME('now')
WHERE id IN (SELECT value FROM json_each(sqlc.arg(ids)))
  AND user_id = sqlc.arg(user_id) AND status != sqlc.arg(status);

-- name: TransitionDigestResultStatusByIDs :many
-- Batch status update that RETURNS the ids it actually changed, so a caller
-- can act on the TRANSITION rather than on the ids it submitted.
--
-- The submit-set twin above cannot express this: `status != @status` confines
-- the update to rows that really move, but the caller still only holds the ids
-- it asked for, so a downstream cascade driven by that set also fires for
-- unchanged rows. Same defect class as the reader cascade fixed in 39baa90 --
-- there the fix was to drive the cascade from the transitioned id set.
--
-- Ownership stays in the predicate, so a foreign id simply matches nothing and
-- is absent from the result.
UPDATE digest_results SET status = sqlc.arg(status), updated_at = DATETIME('now')
WHERE id IN (SELECT value FROM json_each(sqlc.arg(ids)))
  AND user_id = sqlc.arg(user_id) AND status != sqlc.arg(status)
RETURNING id;

-- name: MarkDigestResultItemsReadByResultIDs :execrows
-- Batch twin of MarkDigestResultItemsRead for the digest cascade: flips
-- linked items of every listed result in one statement.
--
-- Ownership is enforced HERE, not only on the caller's result update: this
-- statement takes the submitted result ids straight from the request, so without
-- the predicate an attacker could pass another account's result id and flip that
-- account's items from digested to read. Mirrors MarkDigestConfigResultItemsRead,
-- which scopes by dr.user_id. The join also means an id the caller does not own
-- simply matches nothing, so the cascade stays a no-op for foreign ids.
UPDATE items SET status = 'read', updated_at = DATETIME('now')
WHERE status = 'digested'
  AND id IN (
    SELECT dri.item_id FROM digest_result_items dri
    JOIN digest_results dr ON dr.id = dri.result_id
    WHERE dr.user_id = sqlc.arg(user_id)
      AND dri.result_id IN (SELECT value FROM json_each(sqlc.arg(ids)))
  );
