package store

import (
	"context"
	"testing"
	"time"
)

// Retention must not be bypassed by a NULL received_at.
//
// This is the bug: the sweep required `received_at IS NOT NULL`, and a message
// whose provider timestamp was missing was stored with NULL, so it was exempt
// from retention and accumulated forever.
func TestDeleteExpiredEmailMessagesReapsNullReceivedAt(t *testing.T) {
	q := emailTestDB(t)
	ctx := context.Background()
	const inbox = "name@agentmail.to"
	const addr = "name+sport@agentmail.to"

	if _, err := q.CreateEmailMessageIfNotExists(ctx, CreateEmailMessageIfNotExistsParams{
		InboxID: inbox, MessageID: "<undated@x>", ToAddrs: `["` + addr + `"]`,
		Subject: "no provider timestamp", ReceivedAt: ns(""),
	}); err != nil {
		t.Fatalf("stage undated: %v", err)
	}
	// Age it well past any cutoff. created_at is the fallback the sweep uses
	// when received_at is NULL.
	if _, err := q.db.ExecContext(ctx, `UPDATE email_messages SET created_at = ? WHERE message_id = '<undated@x>'`,
		time.Now().Add(-90*24*time.Hour).Format("2006-01-02 15:04:05")); err != nil {
		t.Fatalf("age row: %v", err)
	}

	cutoff := time.Now().Add(-30 * 24 * time.Hour).UTC().Format(time.RFC3339Nano)
	n, err := q.DeleteExpiredEmailMessages(ctx, cutoff)
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if n != 1 {
		t.Errorf("reaped %d rows, want 1: a NULL received_at must not exempt a row from retention", n)
	}
}

// A recent undated message must survive: the fallback is an age, not a licence
// to delete.
func TestDeleteExpiredEmailMessagesKeepsRecentNullTimestamp(t *testing.T) {
	q := emailTestDB(t)
	ctx := context.Background()
	if _, err := q.CreateEmailMessageIfNotExists(ctx, CreateEmailMessageIfNotExistsParams{
		InboxID: "name@agentmail.to", MessageID: "<recent@x>",
		ToAddrs: `["name+sport@agentmail.to"]`, Subject: "recent undated", ReceivedAt: ns(""),
	}); err != nil {
		t.Fatalf("stage: %v", err)
	}

	cutoff := time.Now().Add(-30 * 24 * time.Hour).UTC().Format(time.RFC3339Nano)
	n, err := q.DeleteExpiredEmailMessages(ctx, cutoff)
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if n != 0 {
		t.Errorf("reaped %d rows, want 0: a message created moments ago is not expired", n)
	}
}

// The comparison must not be fooled by the two timestamp columns using
// different formats. received_at holds RFC3339 ("2026-09-23T18:35:03.000Z") and
// created_at holds SQLite's DATETIME form ("2026-09-23 18:35:03"). Compared as
// raw TEXT, ' ' (0x20) sorts before 'T' (0x54), so a row can look older than it
// is and be reaped early.
func TestDeleteExpiredEmailMessagesComparesFormatsCorrectly(t *testing.T) {
	q := emailTestDB(t)
	ctx := context.Background()
	const addr = "name+sport@agentmail.to"

	base := time.Date(2026, 9, 23, 18, 35, 3, 0, time.UTC)
	// received_at at base, created_at at base too: both columns describe the
	// same instant. A cutoff 1h before base must not reap it, whichever column
	// the sweep consults.
	if _, err := q.CreateEmailMessageIfNotExists(ctx, CreateEmailMessageIfNotExistsParams{
		InboxID: "name@agentmail.to", MessageID: "<same@x>", ToAddrs: `["` + addr + `"]`,
		Subject: "same instant", ReceivedAt: ns(base.Format(time.RFC3339Nano)),
	}); err != nil {
		t.Fatalf("stage: %v", err)
	}
	if _, err := q.db.ExecContext(ctx, `UPDATE email_messages SET created_at = ? WHERE message_id = '<same@x>'`,
		base.Format("2006-01-02 15:04:05")); err != nil {
		t.Fatalf("set created_at: %v", err)
	}

	cutoff := base.Add(-time.Hour).UTC().Format(time.RFC3339Nano)
	n, err := q.DeleteExpiredEmailMessages(ctx, cutoff)
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if n != 0 {
		t.Errorf("reaped %d rows, want 0: the row is NEWER than the cutoff, so a format mismatch must not delete it early", n)
	}
}

// A half-staged message must not be served as an item, since its body is empty
// and it renders as a blank newsletter.
func TestIncompleteEmailMessageIsNotServed(t *testing.T) {
	q := emailTestDB(t)
	ctx := context.Background()
	const inbox = "name@agentmail.to"
	const addr = "name+sport@agentmail.to"

	stage(t, q, inbox, "<whole@x>", addr, "whole", "2026-09-15T10:00:00.000Z")

	// Metadata only, exactly as a pass that failed on the body leaves it.
	if _, err := q.CreateEmailMessageIfNotExists(ctx, CreateEmailMessageIfNotExistsParams{
		InboxID: inbox, MessageID: "<half@x>", ToAddrs: `["` + addr + `"]`,
		Subject: "half", ReceivedAt: ns("2026-09-15T11:00:00.000Z"),
	}); err != nil {
		t.Fatalf("stage half: %v", err)
	}
	if err := q.AddEmailMessageRecipient(ctx, AddEmailMessageRecipientParams{
		InboxID: inbox, MessageID: "<half@x>", Address: addr,
	}); err != nil {
		t.Fatalf("stage recipient: %v", err)
	}

	rows, err := q.ListEmailMessagesByRecipientExact(ctx, ListEmailMessagesByRecipientExactParams{
		InboxID: inbox, Address: addr, Since: "", Lim: 50,
	})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	for _, r := range rows {
		if r.MessageID == "<half@x>" {
			t.Error("an incomplete message was served: it renders as an empty newsletter")
		}
	}
	if len(rows) != 1 {
		t.Errorf("rows = %d, want 1 (the complete message only)", len(rows))
	}

	// The legacy LIKE twin must agree, or the two implementations diverge.
	legacy, err := q.ListEmailMessagesByRecipient(ctx, ListEmailMessagesByRecipientParams{
		InboxID: inbox, Address: addr, Since: "", Lim: 50,
	})
	if err != nil {
		t.Fatalf("legacy list: %v", err)
	}
	if len(legacy) != len(rows) {
		t.Errorf("legacy twin returned %d rows, exact returned %d: the twins diverge on completeness", len(legacy), len(rows))
	}
}

// Marking complete is idempotent and guarded, so a repeat call reports that
// nothing changed rather than rewriting the timestamp.
func TestMarkEmailMessageCompleteIsGuarded(t *testing.T) {
	q := emailTestDB(t)
	ctx := context.Background()
	const inbox = "name@agentmail.to"

	if _, err := q.CreateEmailMessageIfNotExists(ctx, CreateEmailMessageIfNotExistsParams{
		InboxID: inbox, MessageID: "<m@x>", ToAddrs: `["name+sport@agentmail.to"]`,
		Subject: "s", ReceivedAt: ns("2026-09-15T10:00:00.000Z"),
	}); err != nil {
		t.Fatalf("stage: %v", err)
	}

	n, err := q.MarkEmailMessageComplete(ctx, MarkEmailMessageCompleteParams{
		InboxID: inbox, MessageID: "<m@x>",
	})
	if err != nil {
		t.Fatalf("mark: %v", err)
	}
	if n != 1 {
		t.Fatalf("first mark affected %d rows, want 1", n)
	}
	n, err = q.MarkEmailMessageComplete(ctx, MarkEmailMessageCompleteParams{
		InboxID: inbox, MessageID: "<m@x>",
	})
	if err != nil {
		t.Fatalf("second mark: %v", err)
	}
	if n != 0 {
		t.Errorf("second mark affected %d rows, want 0: the guard must make it a no-op", n)
	}
}
