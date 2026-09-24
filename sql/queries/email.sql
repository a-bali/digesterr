-- name: CreateEmailMessageIfNotExists :one
INSERT OR IGNORE INTO email_messages (
    inbox_id, message_id, thread_id, from_addr, to_addrs,
    subject, text, html, raw_headers, received_at
)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
RETURNING id;

-- name: MarkEmailMessageComplete :execrows
-- Marks a staged message as fully ingested. Guarded on complete_at IS NULL so a
-- repeated call is a no-op rather than a write.
--
-- This is what makes the overlap re-read cheap: an incomplete row is re-staged
-- (and its body re-fetched) on the next pass, a complete one is skipped.
UPDATE email_messages
SET complete_at = DATETIME('now')
WHERE inbox_id = ? AND message_id = ? AND complete_at IS NULL;

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
  AND email_messages.complete_at IS NOT NULL
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

-- name: DeleteEmailMessagesForInbox :execrows
DELETE FROM email_messages WHERE inbox_id = ?;

-- name: DeleteEmailSyncStateForInbox :execrows
DELETE FROM email_sync_state WHERE inbox_id = ?;

-- name: DeleteExpiredEmailMessages :execrows
-- Retention sweep: staged mail older than the cutoff.
--
-- DATETIME() on BOTH sides, for two reasons:
--
--  1. received_at is nullable. A message whose provider timestamp was missing
--     was stored as NULL, and `received_at < ?` is NULL (never true) for
--     those rows, so they were exempt from retention and accumulated forever.
--     COALESCE onto created_at gives them an age to be swept by.
--  2. The two columns are stored in DIFFERENT formats: received_at holds
--     RFC3339 ("2026-09-23T18:35:03.000Z") and created_at holds SQLite's
--     DATETIME form ("2026-09-23 18:35:03"). Compared as raw TEXT, ' ' (0x20)
--     sorts before 'T' (0x54), so a same-day row compares as older than it is
--     and can be deleted early. DATETIME() normalizes both to a comparable
--     form.
DELETE FROM email_messages
WHERE DATETIME(COALESCE(received_at, created_at)) < DATETIME(CAST(sqlc.arg(cutoff) AS TEXT));

-- name: AddEmailMessageRecipient :exec
-- Records one recipient of a staged message. INSERT OR IGNORE so re-staging
-- (idempotent ingest) never duplicates rows.
INSERT OR IGNORE INTO email_message_recipients (inbox_id, message_id, address)
VALUES (?, ?, ?);

-- name: ListEmailMessagesByRecipientExact :many
-- Indexed twin of ListEmailMessagesByRecipient: exact equality over the
-- normalized recipient table instead of a leading-wildcard LIKE on the JSON
-- array. Same contract (inbox + address + since window, newest first), plus the
-- completeness filter: a half-staged row has no recipient row yet, so it never
-- reaches the reader as an empty newsletter.
SELECT m.*
FROM email_messages m
JOIN email_message_recipients r
  ON r.inbox_id = m.inbox_id AND r.message_id = m.message_id
WHERE m.inbox_id = sqlc.arg(inbox_id)
  AND r.address = sqlc.arg(address)
  AND m.complete_at IS NOT NULL
  AND (m.received_at IS NULL OR m.received_at > CAST(sqlc.arg(since) AS TEXT))
ORDER BY m.received_at DESC, m.id DESC
LIMIT sqlc.arg(lim);
