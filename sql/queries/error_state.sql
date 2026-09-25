-- name: UpdateFeedError :exec
UPDATE feeds SET last_error=@error, last_error_at=datetime('now'), last_error_severity='error', updated_at=datetime('now') WHERE id=@id;

-- name: UpdateFeedWarning :exec
UPDATE feeds SET last_error=@error, last_error_at=datetime('now'), last_error_severity='warning', updated_at=datetime('now') WHERE id=@id AND (last_error='' OR last_error_severity='warning');

-- name: ClearFeedError :exec
-- Idempotent on purpose: the WHERE clause makes this a true no-op when there is
-- nothing to clear, so a caller on the success path can invoke it
-- unconditionally without first reading the row. Gating the call in Go instead
-- required the caller to know the row's current error state, which it cannot do
-- reliably: the feed struct it holds was read before the failure was recorded,
-- so the clear silently did nothing and the mark outlived the condition.
UPDATE feeds SET last_error='', last_error_at=NULL, last_error_severity='', updated_at=datetime('now')
WHERE id=@id AND (last_error != '' OR last_error_severity != '');

-- name: UpdateDigestConfigError :exec
UPDATE digest_configs SET last_error=@error, last_error_at=datetime('now'), updated_at=datetime('now') WHERE id=@id;

-- name: ClearDigestConfigError :exec
UPDATE digest_configs SET last_error='', last_error_at=NULL, updated_at=datetime('now') WHERE id=@id;
