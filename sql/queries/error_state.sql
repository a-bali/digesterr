-- name: UpdateFeedError :exec
UPDATE feeds SET last_error=@error, last_error_at=datetime('now'), last_error_severity='error', updated_at=datetime('now') WHERE id=@id;

-- name: UpdateFeedWarning :exec
UPDATE feeds SET last_error=@error, last_error_at=datetime('now'), last_error_severity='warning', updated_at=datetime('now') WHERE id=@id AND (last_error='' OR last_error_severity='warning');

-- name: ClearFeedError :exec
UPDATE feeds SET last_error='', last_error_at=NULL, last_error_severity='', updated_at=datetime('now') WHERE id=@id;

-- name: UpdateDigestConfigError :exec
UPDATE digest_configs SET last_error=@error, last_error_at=datetime('now'), updated_at=datetime('now') WHERE id=@id;

-- name: ClearDigestConfigError :exec
UPDATE digest_configs SET last_error='', last_error_at=NULL, updated_at=datetime('now') WHERE id=@id;
