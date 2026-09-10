-- name: UpdateItemContent :exec
-- Persist a body supplied by an enricher rather than by the feed payload.
-- HN is the case this exists for: news.ycombinator.com/rss ships a
-- description holding nothing but a "Comments" link, so the post text of an
-- Ask HN / Show HN / text-only story has to be filled in from the HN API.
-- Kept as its own query so callers can write content without touching scores.
UPDATE items SET content = @content, updated_at = DATETIME('now') WHERE id = @id;
