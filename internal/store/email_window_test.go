// Proves the email_messages read query's window and ordering semantics against
// real SQLite, using the SQL sqlc generated and the schema's actual TEXT
// timestamp columns.
//
// These tests exist because two failure modes here are silent:
//   - a typed time.Time bound into a TEXT column stores a different string than
//     the RFC3339 the API sends, so the poll window matches nothing and the
//     feed simply never updates;
//   - returning the wrong end of the ordering (oldest instead of newest) means
//     a mailbox with more history than the limit never shows a new newsletter.
package store

import (
	"context"
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"
)

// emailSchema mirrors the email-related parts of internal/schema/schema.sql.
// Kept inline so this test pins the SQL rather than whatever the generated
// query happens to be.
const emailSchema = `
CREATE TABLE email_messages (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    inbox_id    TEXT    NOT NULL,
    message_id  TEXT    NOT NULL,
    thread_id   TEXT    NOT NULL DEFAULT '',
    from_addr   TEXT    NOT NULL DEFAULT '',
    to_addrs    TEXT    NOT NULL DEFAULT '',
    subject     TEXT    NOT NULL DEFAULT '',
    text        TEXT    NOT NULL DEFAULT '',
    html        TEXT    NOT NULL DEFAULT '',
    raw_headers TEXT    NOT NULL DEFAULT '{}',
    received_at TEXT,
    complete_at DATETIME,
    staged_attempts INTEGER NOT NULL DEFAULT 0,
    created_at  DATETIME NOT NULL DEFAULT (DATETIME('now')),
    UNIQUE (inbox_id, message_id)
);
CREATE TABLE email_sync_state (
    inbox_id       TEXT    PRIMARY KEY,
    last_synced_at TEXT,
    last_error     TEXT    NOT NULL DEFAULT '',
    last_error_at  TEXT,
    updated_at     DATETIME NOT NULL DEFAULT (DATETIME('now'))
);
CREATE TABLE email_message_recipients (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    inbox_id   TEXT NOT NULL,
    message_id TEXT NOT NULL,
    address    TEXT NOT NULL,
    UNIQUE (inbox_id, message_id, address),
    FOREIGN KEY (inbox_id, message_id) REFERENCES email_messages(inbox_id, message_id) ON DELETE CASCADE
);
CREATE INDEX idx_email_recipients_inbox_address ON email_message_recipients(inbox_id, address);
`

func emailTestDB(t *testing.T) *Queries {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.Exec(emailSchema); err != nil {
		t.Fatalf("schema: %v", err)
	}
	return New(db)
}

// stage inserts a COMPLETE message with an explicit RFC3339 timestamp, the
// format the AgentMail API returns in ref.timestamp. Completeness is recorded
// because the read queries filter on it: a row without complete_at models a
// message a failed pass left half staged, which is not served.
func stage(t *testing.T, q *Queries, inbox, msgID, to, subject, received string) {
	t.Helper()
	ctx := context.Background()
	_, err := q.CreateEmailMessageIfNotExists(ctx, CreateEmailMessageIfNotExistsParams{
		InboxID: inbox, MessageID: msgID, ToAddrs: `["` + to + `"]`,
		Subject: subject, Text: subject, Html: "<p>" + subject + "</p>",
		ReceivedAt: ns(received),
	})
	if err != nil {
		t.Fatalf("stage %s: %v", msgID, err)
	}
	if _, err := q.MarkEmailMessageComplete(ctx, MarkEmailMessageCompleteParams{
		InboxID: inbox, MessageID: msgID,
	}); err != nil {
		t.Fatalf("stage complete %s: %v", msgID, err)
	}
	// The indexed routing query joins the normalized recipient table, so a
	// complete message needs its recipient row too.
	if err := q.AddEmailMessageRecipient(ctx, AddEmailMessageRecipientParams{
		InboxID: inbox, MessageID: msgID, Address: to,
	}); err != nil {
		t.Fatalf("stage recipient %s: %v", msgID, err)
	}
}

func listFor(t *testing.T, q *Queries, inbox, addr, since string, lim int64) []EmailMessage {
	t.Helper()
	rows, err := q.ListEmailMessagesByRecipient(context.Background(), ListEmailMessagesByRecipientParams{
		InboxID: inbox, Address: addr, Since: since, Lim: lim,
	})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	return rows
}

// ns builds the NullString sqlc generates for the TEXT timestamp columns: an
// empty string means NULL (no timestamp known).
func ns(s string) sql.NullString {
	return sql.NullString{String: s, Valid: s != ""}
}

func subjects(msgs []EmailMessage) []string {
	out := make([]string, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, m.Subject)
	}
	return out
}

// The window comparison must work on the API's RFC3339 strings.
func TestEmailWindowUsesTextComparison(t *testing.T) {
	q := emailTestDB(t)
	const inbox = "name@agentmail.to"
	const addr = "name+sport@agentmail.to"

	stage(t, q, inbox, "<m1@x>", addr, "old", "2026-09-15T10:00:00.000Z")
	stage(t, q, inbox, "<m2@x>", addr, "mid", "2026-09-15T12:00:00.000Z")
	stage(t, q, inbox, "<m3@x>", addr, "new", "2026-09-15T14:00:00.000Z")

	all := listFor(t, q, inbox, addr, "", 50)
	if len(all) != 3 {
		t.Fatalf("no-window returned %d rows, want 3", len(all))
	}
	if all[0].Subject != "new" || all[2].Subject != "old" {
		t.Fatalf("order = %v, want newest first", subjects(all))
	}

	// Strictly after the middle message: the newer one only.
	after := listFor(t, q, inbox, addr, "2026-09-15T12:00:00.000Z", 50)
	if len(after) != 1 || after[0].Subject != "new" {
		t.Fatalf("after-window = %v, want exactly the newest message", subjects(after))
	}

	// The watermark's next-window shape: everything strictly newer than the
	// last sync, which is what the overlap exists to protect.
	next := listFor(t, q, inbox, addr, "2026-09-15T10:00:00.000Z", 50)
	if len(next) != 2 {
		t.Fatalf("next-window = %v, want mid and new", subjects(next))
	}

	// A truncated window must take the NEWEST rows, never the oldest.
	top := listFor(t, q, inbox, addr, "", 1)
	if len(top) != 1 || top[0].Subject != "new" {
		t.Fatalf("LIMIT 1 = %v, want the newest row", subjects(top))
	}
}

// Address matching selects only this feed's mail, including when the address
// appears alongside others in the recipient list.
func TestEmailRecipientMatching(t *testing.T) {
	q := emailTestDB(t)
	const inbox = "name@agentmail.to"
	const sport = "name+sport@agentmail.to"

	stage(t, q, inbox, "<s1@x>", sport, "sport mail", "2026-09-15T10:00:00.000Z")
	stage(t, q, inbox, "<n1@x>", "name+news@agentmail.to", "news mail", "2026-09-15T10:00:01.000Z")
	stage(t, q, inbox, "<s2@x>", "name+sport-extra@agentmail.to", "sport extra", "2026-09-15T10:00:02.000Z")

	got := listFor(t, q, inbox, sport, "", 50)
	if len(got) != 1 || got[0].Subject != "sport mail" {
		t.Fatalf("address match = %v, want exactly [sport mail]", subjects(got))
	}

	// A feed address appearing with other recipients still matches.
	_, err := q.CreateEmailMessageIfNotExists(context.Background(), CreateEmailMessageIfNotExistsParams{
		InboxID: inbox, MessageID: "<multi@x>",
		ToAddrs:    `["someone@else.com","name+news@agentmail.to"]`,
		Subject:    "multi recipient",
		ReceivedAt: ns("2026-09-15T11:00:00.000Z"),
	})
	if err != nil {
		t.Fatalf("stage multi: %v", err)
	}
	if _, err := q.MarkEmailMessageComplete(context.Background(), MarkEmailMessageCompleteParams{
		InboxID: inbox, MessageID: "<multi@x>",
	}); err != nil {
		t.Fatalf("complete multi: %v", err)
	}
	news := listFor(t, q, inbox, "name+news@agentmail.to", "", 50)
	if len(news) != 2 {
		t.Fatalf("news feed = %v, want both news messages", subjects(news))
	}

	// Messages from another inbox must never leak in.
	if other := listFor(t, q, "other@agentmail.to", "other+sport@agentmail.to", "", 50); len(other) != 0 {
		t.Fatalf("cross-inbox leakage: %v", subjects(other))
	}
}

// A message with no received_at must still be delivered rather than stranded
// outside every window (a NULL sort key satisfies neither > nor <).
func TestEmailNullReceivedAtStillReturned(t *testing.T) {
	q := emailTestDB(t)
	const inbox = "name@agentmail.to"
	const addr = "name+sport@agentmail.to"

	stage(t, q, inbox, "<null@x>", addr, "undated", "")
	stage(t, q, inbox, "<dated@x>", addr, "dated", "2026-09-15T12:00:00.000Z")

	for _, since := range []string{"", "2026-09-15T00:00:00.000Z"} {
		got := listFor(t, q, inbox, addr, since, 50)
		found := false
		for _, m := range got {
			if m.Subject == "undated" {
				found = true
			}
		}
		if !found {
			t.Fatalf("since=%q dropped the undated message: %v", since, subjects(got))
		}
	}
}

// Re-staging the same Message-ID must not create a second row and must report
// "already have it" rather than an error. This is what makes the poll window's
// overlap safe.
func TestEmailMessageInsertIsIdempotent(t *testing.T) {
	q := emailTestDB(t)
	ctx := context.Background()
	const inbox = "name@agentmail.to"
	const addr = "name+sport@agentmail.to"

	first, err := q.CreateEmailMessageIfNotExists(ctx, CreateEmailMessageIfNotExistsParams{
		InboxID: inbox, MessageID: "<m1@x>", ToAddrs: `["` + addr + `"]`,
		Subject: "one", ReceivedAt: ns("2026-09-15T10:00:00.000Z"),
	})
	if err != nil {
		t.Fatalf("first insert: %v", err)
	}
	if first == 0 {
		t.Fatal("first insert returned id 0")
	}
	// Complete it, so the read queries below see it: they filter on
	// completeness, and an incomplete row is intentionally not served.
	if _, err := q.MarkEmailMessageComplete(ctx, MarkEmailMessageCompleteParams{
		InboxID: inbox, MessageID: "<m1@x>",
	}); err != nil {
		t.Fatalf("complete: %v", err)
	}

	// Same message again, as an overlapping poll window delivers it.
	_, err = q.CreateEmailMessageIfNotExists(ctx, CreateEmailMessageIfNotExistsParams{
		InboxID: inbox, MessageID: "<m1@x>", ToAddrs: `["` + addr + `"]`,
		Subject: "one", ReceivedAt: ns("2026-09-15T10:00:00.000Z"),
	})
	if err != sql.ErrNoRows {
		t.Fatalf("duplicate insert error = %v, want sql.ErrNoRows", err)
	}
	if rows := listFor(t, q, inbox, addr, "", 50); len(rows) != 1 {
		t.Fatalf("after duplicate insert there are %d rows, want 1", len(rows))
	}

	// The uniqueness key is (inbox_id, message_id), so the same Message-ID in
	// a different inbox is a different message.
	if _, err := q.CreateEmailMessageIfNotExists(ctx, CreateEmailMessageIfNotExistsParams{
		InboxID: "other@agentmail.to", MessageID: "<m1@x>",
		ToAddrs: `["other+sport@agentmail.to"]`, Subject: "one",
		ReceivedAt: ns("2026-09-15T10:00:00.000Z"),
	}); err != nil {
		t.Fatalf("same Message-ID in another inbox should insert: %v", err)
	}
}

// A failure must not advance the watermark (or the mail it failed on is
// skipped forever); a success must clear the previous error.
func TestEmailSyncStateTransitions(t *testing.T) {
	q := emailTestDB(t)
	ctx := context.Background()
	const inbox = "name@agentmail.to"

	if err := q.SetEmailSyncError(ctx, SetEmailSyncErrorParams{
		InboxID: inbox, LastError: "connection refused",
	}); err != nil {
		t.Fatalf("set error: %v", err)
	}
	st, err := q.GetEmailSyncState(ctx, inbox)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if st.LastError != "connection refused" {
		t.Fatalf("last_error = %q, want the recorded failure", st.LastError)
	}
	if st.LastSyncedAt.Valid && st.LastSyncedAt.String != "" {
		t.Fatalf("a failed sync must not advance last_synced_at, got %q", st.LastSyncedAt.String)
	}

	if err := q.UpsertEmailSyncState(ctx, UpsertEmailSyncStateParams{
		InboxID: inbox, LastSyncedAt: ns("2026-09-15T10:00:00.000Z"),
	}); err != nil {
		t.Fatalf("upsert success: %v", err)
	}
	st, err = q.GetEmailSyncState(ctx, inbox)
	if err != nil {
		t.Fatalf("get after success: %v", err)
	}
	if st.LastError != "" {
		t.Fatalf("last_error = %q, want cleared after a successful sync", st.LastError)
	}
	if !st.LastSyncedAt.Valid || st.LastSyncedAt.String != "2026-09-15T10:00:00.000Z" {
		t.Fatalf("last_synced_at = %v, want the recorded watermark", st.LastSyncedAt)
	}
	// A second success keeps the watermark moving forward.
	if err := q.UpsertEmailSyncState(ctx, UpsertEmailSyncStateParams{
		InboxID: inbox, LastSyncedAt: ns("2026-09-15T12:00:00.000Z"),
	}); err != nil {
		t.Fatalf("second upsert: %v", err)
	}
	st, _ = q.GetEmailSyncState(ctx, inbox)
	if st.LastSyncedAt.String != "2026-09-15T12:00:00.000Z" {
		t.Fatalf("last_synced_at = %q, want the newer watermark", st.LastSyncedAt.String)
	}
}
