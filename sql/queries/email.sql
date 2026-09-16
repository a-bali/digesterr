-- name: CreateEmailMessageIfNotExists :one
INSERT OR IGNORE INTO email_messages (
    inbox_id, message_id, thread_id, from_addr, to_addrs,
    subject, text, html, raw_headers, received_at
)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
RETURNING id;

-- name: GetEmailMessageByMessageID :one
SELECT * FROM email_messages WHERE inbox_id = ? AND message_id = ?;

-- name: ListEmailMessagesByRecipient :many
-- Messages addressed to one feed's plus-address, newest first.
--
-- `address` is the feed's full URL (name+suffix@domain) and to_addrs is a JSON
-- array of recipients, so this is a substring match over that array. That is
-- sound because the address namespace is instance-wide unique: the partial
-- unique index idx_feeds_email_url guarantees no two feeds share a suffix, so
-- an address cannot select another feed's mail.
--
-- `since` is compared as TEXT, never bound as a time.Time: SQLite stores these
-- columns as TEXT holding RFC3339, so a Go time.Time (which binds as
-- "2006-01-02 15:04:05+00:00") compares unequal against every row and the
-- window silently matches nothing. sqlc.arg + CAST keeps the parameter a plain
-- string -- a bare @since here is inferred as time.Time.
--
-- DESC + LIMIT returns the NEWEST rows, which matters: a mailbox holding more
-- history than the limit would otherwise return its oldest messages and never
-- surface a new newsletter. The caller reverses this for chronological order.
SELECT * FROM email_messages
WHERE email_messages.inbox_id = @inbox_id
  AND email_messages.to_addrs LIKE '%' || CAST(sqlc.arg(address) AS TEXT) || '%'
  AND (email_messages.received_at IS NULL
       OR CAST(email_messages.received_at AS TEXT) > CAST(sqlc.arg(since) AS TEXT))
ORDER BY email_messages.received_at DESC, email_messages.id DESC
LIMIT @lim;

-- name: GetEmailSyncState :one
SELECT * FROM email_sync_state WHERE inbox_id = ?;

-- name: UpsertEmailSyncState :exec
-- Records a successful sync and clears any previous error.
INSERT INTO email_sync_state (inbox_id, last_synced_at, last_error, last_error_at, updated_at)
VALUES (?, ?, '', NULL, DATETIME('now'))
ON CONFLICT(inbox_id) DO UPDATE SET
    last_synced_at = excluded.last_synced_at,
    last_error = '',
    last_error_at = NULL,
    updated_at = DATETIME('now');

-- name: SetEmailSyncError :exec
-- Records a failed sync without moving the watermark, so the next cycle
-- retries the same window rather than skipping the mail it failed on.
INSERT INTO email_sync_state (inbox_id, last_error, last_error_at, updated_at)
VALUES (?, ?, DATETIME('now'), DATETIME('now'))
ON CONFLICT(inbox_id) DO UPDATE SET
    last_error = excluded.last_error,
    last_error_at = excluded.last_error_at,
    updated_at = DATETIME('now');

-- name: ListEmailFeedURLs :many
-- Every email feed address, for reporting which recipients already have a feed.
SELECT id, user_id, url FROM feeds WHERE type = 'email' AND url != '' ORDER BY url;

-- name: UpdateEmailMessageBody :exec
-- Attaches the fetched body to an already-staged row. Bodies are fetched in a
-- separate call because the list endpoint does not return them, and batch-get
-- silently drops them; see internal/feed/email_client.go.
UPDATE email_messages
SET text = ?, html = ?
WHERE inbox_id = ? AND message_id = ?;
