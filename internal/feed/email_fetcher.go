package feed

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"digesterr/internal/metrics"
	"digesterr/internal/store"
)

// emailSyncOverlap is rewound from the stored watermark before each poll.
//
// It is required, not defensive: the `after` filter's exclusivity is not
// documented and two messages can share a timestamp to the second, so a window
// starting exactly at the watermark can skip mail. Re-reading a short overlap is
// free because email_messages has UNIQUE (inbox_id, message_id) — a duplicate
// insert is a no-op, so the overlap trades nothing for the guarantee.
const emailSyncOverlap = 15 * time.Minute

// EmailSync polls the configured AgentMail inbox once per cycle and stages new
// messages into email_messages.
//
// It runs ONCE PER INBOX, not once per feed, and that is the whole point of the
// staging table: several email feeds share one mailbox, so a network call inside
// EmailFetcher.Fetch would re-download every message once per feed per cycle.
// That fan-out is exactly what caused the production Twitter 429s (a fetcher
// that implemented Enrich but not BulkEnricher). The network lives here; Fetch
// only reads rows.
//
// Failures are soft: a failing inbox records last_error and leaves the watermark
// alone so the next cycle retries the same window, while every other feed polls
// normally.
type EmailSync struct {
	queries *store.Queries
	client  *AgentMailClient
}

// NewEmailSync returns a sync for the given client, or nil when the email feed
// type is not configured. A nil *EmailSync is safe to use: its Run is a no-op,
// so callers do not need to branch.
func NewEmailSync(client *AgentMailClient, queries *store.Queries) *EmailSync {
	if client == nil || queries == nil {
		return nil
	}
	return &EmailSync{queries: queries, client: client}
}

// Configured reports whether a client was supplied.
func (s *EmailSync) Configured() bool { return s != nil && s.client != nil }

// setTransport passes the shared metrics transport to the client. Poller
// .SetTransport type-asserts for this method on fetchers, enrichers and comment
// fetchers; the sync is none of those, so the poller wires it explicitly. The
// method exists so the wiring has one obvious home and cannot be forgotten
// silently — without it the email feed is invisible in GET /api/metrics.
func (s *EmailSync) setTransport(t http.RoundTripper) {
	if s.Configured() {
		s.client.setTransport(t)
	}
}

// consume reads and resets the shared transport counters, so a sync pass can
// report its own traffic and leave nothing behind for the next feed to claim.
func (s *EmailSync) consume() (int64, int) {
	if !s.Configured() {
		return 0, 0
	}
	return s.client.consumeCounters()
}

// RunEmailSync performs one sync pass and emits its metrics record, so the
// mailbox sync is visible in GET /api/metrics as `email_sync` instead of being
// silently attributed to whichever feed happened to be polled next.
//
// Run itself must NOT record: PollAll calls Run and then polls the first feed,
// whose pollFeed reads the shared transport counters immediately. Anything the
// sync leaves behind is claimed by that feed (measured: a ~30 KB RSS fetch
// reported bytes=1252698 while both email feeds showed 0). Metrics transport
// fixtures therefore call Run directly; only the real entry points use this.
func (s *EmailSync) RunEmailSync(ctx context.Context, record func(Record)) {
	if s == nil || !s.Configured() {
		return
	}
	inbox := s.client.InboxID()
	start := time.Now()
	err := s.Run(ctx)
	// Consume AFTER the pass: consuming first would read the pre-sync counters
	// (usually zero) and leave this pass's traffic in the transport for the next
	// feed to claim — the exact bug this function exists to prevent.
	bytes, reqs := s.consume()
	if record == nil {
		return
	}
	errStr := ""
	status := 200
	if err != nil {
		errStr = err.Error()
		status = metrics.StatusFor(err)
	}
	record(Record{
		Timestamp:  start,
		URL:        s.client.BaseURL(),
		StatusCode: status,
		Bytes:      bytes,
		Requests:   reqs,
		Host:       s.client.Host(),
		DurationMs: time.Since(start).Milliseconds(),
		Reason:     "email_sync",
		FeedName:   inbox,
		FeedType:   "email",
		Error:      errStr,
	})
}

// PollEmailFeed runs the mailbox sync when the feed is an email feed. An email
// feed's Fetch is a pure read of the staging table, so without this a per-feed
// poll — the UI's refresh button, and the auto-fetch after creating a feed —
// would always return zero new items until the next 15-minute cycle. The user
// sees a working "Refresh" that never shows newly delivered mail.
//
// It is synchronous on purpose: the request must not report success before the
// mailbox has actually been read.
func (p *Poller) PollEmailFeed(ctx context.Context, feed store.Feed) {
	if feed.Type != "email" || p.emailSync == nil {
		return
	}
	p.emailSync.RunEmailSync(ctx, p.recordFn)
}

// Run performs one sync pass for this inbox.
func (s *EmailSync) Run(ctx context.Context) error {
	if !s.Configured() {
		return nil
	}
	inbox := s.client.InboxID()

	after, err := s.windowStart(ctx, inbox)
	if err != nil {
		return err
	}

	// ListMessages already follows next_page_token to completion, so one call
	// returns the whole window. Do not wrap this in a "page" loop: a loop that
	// cannot advance is dead code that reads like a safety net.
	refs, err := s.client.ListMessages(ctx, after, agentMailPageLimit)
	if err != nil {
		s.recordError(ctx, inbox, err)
		return err
	}

	staged, newest := 0, ""
	for _, ref := range refs {
		// Idempotency lives in the schema (UNIQUE inbox_id,message_id): a
		// re-read of the overlap returns ErrNoRows here and costs nothing.
		if _, err := s.queries.CreateEmailMessageIfNotExists(ctx, store.CreateEmailMessageIfNotExistsParams{
			InboxID:    inbox,
			MessageID:  ref.MessageID,
			ThreadID:   ref.ThreadID,
			FromAddr:   ref.From,
			ToAddrs:    encodeAddrs(ref.To),
			Subject:    ref.Subject,
			RawHeaders: encodeHeaders(ref.Headers),
			ReceivedAt: nsString(ref.Timestamp),
		}); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				continue // already staged
			}
			slog.Warn("failed to stage email message", "inbox", inbox, "message_id", ref.MessageID, "error", err)
			continue
		}

		// Bodies come from a single GetMessage. The list endpoint omits them and
		// batch-get silently drops them, so this is the only route.
		msg, err := s.client.GetMessage(ctx, ref.MessageID)
		if err != nil {
			// The row is already staged with metadata; a body failure must not
			// discard the message. Leave text/html empty and let the next poll
			// re-attempt via the stored row rather than re-inserting.
			slog.Warn("failed to fetch email body", "inbox", inbox, "message_id", ref.MessageID, "error", err)
			continue
		}
		if err := s.storeBody(ctx, inbox, ref, msg); err != nil {
			slog.Warn("failed to store email body", "inbox", inbox, "message_id", ref.MessageID, "error", err)
			continue
		}

		staged++
		if nsString(ref.Timestamp).Valid && ref.Timestamp > newest {
			newest = ref.Timestamp
		}
	}

	// Advance the watermark only after the pass succeeded, so a partial failure
	// is retried rather than skipped. `newest` is preferred, but when nothing
	// new arrived the previous watermark is kept (re-sending the old value is
	// harmless and keeps the row's semantics simple).
	watermark := newest
	if watermark == "" {
		if prev, err := s.queries.GetEmailSyncState(ctx, inbox); err == nil && prev.LastSyncedAt.Valid {
			watermark = prev.LastSyncedAt.String
		}
	}
	if watermark == "" {
		watermark = time.Now().UTC().Format(time.RFC3339Nano)
	}
	if err := s.queries.UpsertEmailSyncState(ctx, store.UpsertEmailSyncStateParams{
		InboxID:      inbox,
		LastSyncedAt: nsString(watermark),
	}); err != nil {
		slog.Warn("failed to record email sync watermark", "inbox", inbox, "error", err)
	}

	if staged > 0 {
		slog.Info("staged email messages", "inbox", inbox, "count", staged, "seen", len(refs))
	}
	return nil
}

// windowStart returns the `after` value for this pass: the stored watermark
// rewound by the overlap, or "" on the first run (no lower bound).
func (s *EmailSync) windowStart(ctx context.Context, inbox string) (string, error) {
	st, err := s.queries.GetEmailSyncState(ctx, inbox)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", nil // first sync: read what the API offers
		}
		return "", fmt.Errorf("reading email sync state: %w", err)
	}
	if !st.LastSyncedAt.Valid || st.LastSyncedAt.String == "" {
		return "", nil
	}
	t, err := time.Parse(time.RFC3339Nano, st.LastSyncedAt.String)
	if err != nil {
		// An unparseable watermark must not wedge the feed: fall back to no
		// lower bound, which re-reads recent mail (idempotent) rather than
		// risking a permanent stall.
		slog.Warn("unparseable email sync watermark, syncing without a lower bound",
			"inbox", inbox, "value", st.LastSyncedAt.String, "error", err)
		return "", nil
	}
	return t.Add(-emailSyncOverlap).UTC().Format(time.RFC3339Nano), nil
}

// storeBody updates the staged row with its body, sanitized.
//
// Sanitization happens HERE, on the ingest path, not at render time: the stored
// HTML is served to the reader and re-served by the Reader API, so raw
// publisher markup must never reach the database in the first place.
func (s *EmailSync) storeBody(ctx context.Context, inbox string, ref AgentMailMessageRef, msg *AgentMailMessage) error {
	html := SanitizeNewsletterHTML(msg.RawHTML())
	text := msg.BodyText()
	return s.queries.UpdateEmailMessageBody(ctx, store.UpdateEmailMessageBodyParams{
		InboxID:   inbox,
		MessageID: ref.MessageID,
		Text:      text,
		Html:      html,
	})
}

func (s *EmailSync) recordError(ctx context.Context, inbox string, cause error) {
	if err := s.queries.SetEmailSyncError(ctx, store.SetEmailSyncErrorParams{
		InboxID:   inbox,
		LastError: cause.Error(),
	}); err != nil {
		slog.Warn("failed to record email sync error", "inbox", inbox, "error", err)
	}
	slog.Warn("email sync failed", "inbox", inbox, "error", cause)
}

// nsString maps an empty string to SQL NULL, since the generated time columns
// are NullString (TEXT columns holding RFC3339 — see the schema comment on why
// a typed time would silently break the poll window).
func nsString(s string) sql.NullString {
	return sql.NullString{String: s, Valid: s != ""}
}

// encodeAddrs renders the recipient list as a JSON array, the form the
// ListEmailMessagesByRecipient substring match expects.
func encodeAddrs(addrs []string) string {
	b, err := json.Marshal(addrs)
	if err != nil {
		return "[]"
	}
	return string(b)
}

// encodeHeaders renders message headers as a JSON object. Header values are
// stored as data only and never parsed back into behaviour.
func encodeHeaders(h map[string]string) string {
	if len(h) == 0 {
		return "{}"
	}
	b, err := json.Marshal(h)
	if err != nil {
		return "{}"
	}
	return string(b)
}

// EmailFetcher turns staged messages into items for one email feed.
//
// It performs NO network I/O: the mailbox is drained once per cycle by
// EmailSync, and this reads only the rows addressed to its own feed. See the
// EmailSync doc comment for why that split exists.
type EmailFetcher struct {
	queries *store.Queries
	inboxID string
}

// NewEmailFetcher returns a fetcher for the given inbox.
func NewEmailFetcher(queries *store.Queries, inboxID string) *EmailFetcher {
	return &EmailFetcher{queries: queries, inboxID: inboxID}
}

// emailFetchLimit caps how many messages one feed reads per poll.
const emailFetchLimit = 200

// Fetch returns the items for feedURL (an email feed's full plus-address).
//
// It deliberately passes no lower bound. Email feeds carry a whole mailbox in
// one call, unlike every other fetcher, so there is no trailing "which of these
// is new" page: dedup is the (feed_id, hash) unique index, and the digest
// generator walks digest results rather than fetched pages. The query orders
// newest-first with a LIMIT, so a mailbox holding more history than the limit
// returns the most recent messages instead of its oldest ones.
func (f *EmailFetcher) Fetch(ctx context.Context, feedURL string) ([]Item, error) {
	if f.queries == nil || feedURL == "" {
		return nil, nil
	}
	rows, err := f.queries.ListEmailMessagesByRecipient(ctx, store.ListEmailMessagesByRecipientParams{
		InboxID: f.inboxID,
		Address: feedURL,
		Since:   "",
		Lim:     emailFetchLimit,
	})
	if err != nil {
		return nil, fmt.Errorf("listing staged email: %w", err)
	}

	items := make([]Item, 0, len(rows))
	for _, row := range rows {
		items = append(items, emailRowToItem(row))
	}
	// The query returns newest-first so the LIMIT keeps recent mail; items are
	// stored in chronological order, so reverse to match every other fetcher.
	for i, j := 0, len(items)-1; i < j; i, j = i+1, j-1 {
		items[i], items[j] = items[j], items[i]
	}
	return items, nil
}

// emailRowToItem converts a staged message into an Item.
//
// GUID is the RFC 5322 Message-ID, which itemHash prefers, so re-reading the
// same message can never create a second item even across polls.
func emailRowToItem(row store.EmailMessage) Item {
	content := row.Html
	if content == "" {
		// A text-only newsletter still has to render as something readable.
		content = "<p>" + strings.ReplaceAll(escapeNewsletterText(row.Text), "\n", "<br>") + "</p>"
	}
	item := Item{
		GUID:        row.MessageID,
		Title:       row.Subject,
		Content:     content,
		Author:      row.FromAddr,
		URL:         firstLinkIn(row.Html),
		PublishedAt: parseEmailTime(row.ReceivedAt),
	}
	if item.Title == "" {
		item.Title = "(no subject)"
	}
	return item
}

// parseEmailTime parses the API's RFC3339 timestamp. An unparseable value yields
// the zero time, which the poller stores as NULL rather than as a bogus date.
func parseEmailTime(s sql.NullString) time.Time {
	if !s.Valid || s.String == "" {
		return time.Time{}
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339} {
		if t, err := time.Parse(layout, s.String); err == nil {
			return t.UTC()
		}
	}
	return time.Time{}
}

// firstLinkIn returns the first http(s) href in already-sanitized HTML, used as
// the item's web URL so the reader's "open original" action works. Returns ""
// when the newsletter has no outbound link, which the UI already handles.
func firstLinkIn(sanitizedHTML string) string {
	if sanitizedHTML == "" {
		return ""
	}
	// The HTML has been through the allowlist, so any href present is already
	// scheme-checked; this only picks the first one.
	lower := strings.ToLower(sanitizedHTML)
	for {
		i := strings.Index(lower, `href="`)
		if i < 0 {
			return ""
		}
		rest := sanitizedHTML[i+len(`href="`):]
		end := strings.Index(rest, `"`)
		if end < 0 {
			return ""
		}
		href := rest[:end]
		if strings.HasPrefix(href, "http://") || strings.HasPrefix(href, "https://") {
			return href
		}
		// Try the next one.
		sanitizedHTML = rest[end:]
		lower = strings.ToLower(sanitizedHTML)
	}
}
