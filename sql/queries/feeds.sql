-- name: CreateFeed :one
INSERT INTO feeds (user_id, category, type, name, url, config, min_score, status, extract_content, readability)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
RETURNING *;

-- name: CreateArchiveFeed :one
-- Archive for a deleted feed's kept items (saved or digest-linked). A normal
-- row the poller ignores: status='paused' keeps it out of ListActiveFeeds and
-- is_archive=1 keeps it out of every user-facing feed listing. The name
-- preserves the original feed's provenance ("Saved -- <name>").
INSERT INTO feeds (user_id, category, type, name, url, config, min_score, status, extract_content, readability, is_archive)
VALUES (?, ?, 'rss', ?, '', '{}', 0, 'paused', 0, 0, 1)
RETURNING *;

-- name: GetFeed :one
SELECT * FROM feeds WHERE id = ? AND user_id = ?;

-- name: GetFeedByID :one
SELECT * FROM feeds WHERE id = ?;

-- name: ListFeeds :many
-- Archive feeds (is_archive=1, holding orphaned saved items of deleted feeds)
-- are hidden from the sidebar: they are not pollable sources.
SELECT * FROM feeds WHERE user_id = ? AND is_archive = 0 ORDER BY name;

-- name: ListFeedsByCategory :many
SELECT * FROM feeds WHERE category = ? AND user_id = ? AND is_archive = 0 ORDER BY name;

-- name: ListCategories :many
SELECT DISTINCT category FROM feeds WHERE user_id = ? AND category != '' AND is_archive = 0 ORDER BY category;

-- name: UpdateFeed :one
-- extract_content and readability are mutually exclusive (both write
-- items.extract_content); the handler rejects setting both.
UPDATE feeds
SET name = ?, url = ?, config = ?, category = ?, status = ?, extract_content = ?, min_score = ?, readability = ?, updated_at = DATETIME('now')
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
SELECT * FROM feeds WHERE status IN ('active', 'error') AND is_archive = 0 ORDER BY id;

-- name: ListDueFeeds :many
-- Poll-due subset of ListActiveFeeds: a feed whose last fetch failed backs off
-- (next_poll_at in the future) instead of retrying every cycle. Archive feeds
-- are excluded explicitly rather than by status, so the invariant holds even
-- if an archive row's status is ever touched by another code path.
SELECT * FROM feeds
WHERE status IN ('active', 'error') AND is_archive = 0
  AND (next_poll_at IS NULL OR next_poll_at <= DATETIME('now'))
ORDER BY id;

-- name: RecordFeedSuccess :exec
-- A successful poll clears the backoff: the next cycle polls on schedule.
UPDATE feeds
SET fail_count = 0, next_poll_at = NULL, last_fetched_at = DATETIME('now'), updated_at = DATETIME('now')
WHERE id = ?;

-- name: RecordFeedFailure :exec
-- Exponential backoff on consecutive failures: 5m, 10m, 20m, ... capped at
-- 2h. fail_count drives the delay; last_error bookkeeping is untouched (the
-- caller records it via UpdateFeedError as before).
UPDATE feeds
SET fail_count = fail_count + 1,
    next_poll_at = DATETIME('now', '+' || CAST(MIN(5 * (1 << MIN(fail_count, 5)), 120) AS TEXT) || ' minutes'),
    updated_at = DATETIME('now')
WHERE id = ?;
