-- name: CreateEmailMessageIfNotExists :one
INSERT OR IGNORE INTO email_messages (
    inbox_id, message_id, thread_id, from_addr, to_addrs,
    subject, text, html, raw_headers, received_at
)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
RETURNING id;

-- name: MarkEmailMessageComplete :execrows
-- Marks a staged message as fully ingested, which stops the repair pass from
-- revisiting it. Guarded on complete_at IS NULL so a repeated call is a no-op
-- rather than a write, and so the count is meaningful (0 = was already whole).
UPDATE email_messages
SET complete_at = DATETIME('now')
WHERE inbox_id = ? AND message_id = ? AND complete_at IS NULL;

-- name: BumpEmailMessageStageAttempt :one
-- Charges one attempt against the repair budget and returns the new count, so
-- the caller can decide whether this row has been retried enough to surrender.
--
-- RETURNING, not RowsAffected: an UPDATE's affected-row count is 1 either way,
-- so :execrows could never tell the caller how many attempts a row has had.
--
-- Separate from MarkEmailMessageComplete because the two happen on different
-- outcomes: an attempt is recorded on failure too, and only success completes.
UPDATE email_messages
SET staged_attempts = staged_attempts + 1
WHERE inbox_id = ? AND message_id = ?
RETURNING staged_attempts;

-- name: ListIncompleteEmailMessages :many
-- Messages a previous pass left half staged: metadata is present but the row is
-- neither complete nor surrendered, and the retry budget is not spent.
--
-- This is what makes the repair independent of the API window. Overlap re-reads
-- only cover a few minutes, so a body that failed earlier than that is never
-- returned by ListMessages again; without this query the loss is permanent.
--
-- ReceivedAt carries the value the API DID supply on the pass that failed, so
-- the repair can refetch a body by id. Trying to rebuild it is impossible.
-- Oldest first: the oldest row is the one closest to aging out of retention, so
-- it is the most urgent to repair.
SELECT * FROM email_messages
WHERE complete_at IS NULL
  AND staged_attempts < sqlc.arg(max_attempts)
ORDER BY created_at ASC, id ASC
LIMIT sqlc.arg(lim);

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
