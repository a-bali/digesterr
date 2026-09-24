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

// drainSharedCounters reads and discards the shared transport counters, so a
// sync pass does not leave its traffic sitting in the aggregate. Per-pass
// attribution comes from the scope in RunEmailSync; this exists only to keep the
// shared totals from double-counting once they are no longer how a pass is
// measured.
func (s *EmailSync) drainSharedCounters() {
	if !s.Configured() {
		return
	}
	s.client.consumeCounters()
}

// RunEmailSync performs one sync pass and emits its metrics record, so the
// mailbox sync is visible in GET /api/metrics as `email_sync` instead of being
// silently attributed to whichever feed happened to be polled next.
//
// Run itself must NOT record: only the real entry points use this. Metrics
// transport fixtures call Run directly, and a bare Run would leave this pass's
// traffic in the shared counters for a later consumer to claim (measured once:
// a ~30 KB RSS fetch reported bytes=1252698 while both email feeds showed 0).
func (s *EmailSync) RunEmailSync(ctx context.Context, record func(Record)) {
	if s == nil || !s.Configured() {
		return
	}
	inbox := s.client.InboxID()
	start := time.Now()
	// Attribute from the REQUESTS this pass makes, not from the shared counter.
	// The counters are still drained below so the aggregate they feed is not left
	// holding this pass's traffic — but the per-pass numbers come from the scope,
	// which no concurrent feed can contaminate.
	scope := metrics.NewScope()
	err := s.Run(metrics.WithScope(ctx, scope))
	bytes, reqs := scope.Totals()
	// Drain the shared counters AFTER the pass: draining first would read the
	// pre-sync counters (usually zero) and leave this pass's traffic in the
	// transport for the next consumer to claim.
	s.drainSharedCounters()
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

// PollEmailFeed is kept as the per-feed entry point for callers that already
// hold an account's credentials. It is NOT what the poller uses: with per-user
// AgentMail configuration the credentials have to be resolved from the feed's
// owner first, so Poller.PollFeed does that and calls the sync directly. This
// remains for the tests that exercise a sync in isolation.
func (p *Poller) PollEmailFeed(ctx context.Context, feed store.Feed, sync *EmailSync) {
	if feed.Type != "email" || sync == nil {
		return
	}
	sync.RunEmailSync(ctx, p.recordFn)
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
	// failedAt is the oldest message this pass could not fully stage.
	//
	// The watermark is never advanced past it. That is the whole fix: a message
	// the watermark steps over falls out of every later window, so its missing
	// half can never be filled in and the loss is permanent. Pinning the window
	// at the failure instead means the next pass lists it again and finishes it
	// -- the overlap re-read IS the repair, so there is no separate repair pass
	// to run and no retry budget to track.
	//
	// Only the oldest failure is kept: pinning there re-lists every newer
	// message too, and re-reading a complete message costs one list call and no
	// body fetch (see the completeness check below).
	failed, failedAt := false, ""
	fail := func(ref AgentMailMessageRef) {
		failed = true
		if ref.Timestamp != "" && (failedAt == "" || ref.Timestamp < failedAt) {
			failedAt = ref.Timestamp
		}
	}

	for _, ref := range refs {
		// Idempotency lives in the schema (UNIQUE inbox_id,message_id): a
		// re-read of the overlap returns ErrNoRows here. That says only that
		// the METADATA row exists, which is not the same as the message being
		// fully staged -- a pass that failed on the body or on the recipient
		// insert leaves exactly that shape. So ErrNoRows falls through to the
		// completeness check instead of skipping the work.
		_, err := s.queries.CreateEmailMessageIfNotExists(ctx, store.CreateEmailMessageIfNotExistsParams{
			InboxID:    inbox,
			MessageID:  ref.MessageID,
			ThreadID:   ref.ThreadID,
			FromAddr:   ref.From,
			ToAddrs:    encodeAddrs(ref.To),
			Subject:    ref.Subject,
			RawHeaders: encodeHeaders(ref.Headers),
			ReceivedAt: nsString(ref.Timestamp),
		})
		switch {
		case err == nil:
			// New row: incomplete until both halves land below.
		case errors.Is(err, sql.ErrNoRows):
			// The metadata row already exists. Skip it only if it is COMPLETE:
			// an incomplete row is exactly what this re-read is here to finish.
			//
			// The check is what keeps the overlap free. Without it every pass
			// would re-download the body of every message in the window, which
			// is the unbounded re-fetch this codebase has already been burned
			// by once.
			existing, gerr := s.queries.GetEmailMessageByMessageID(ctx, store.GetEmailMessageByMessageIDParams{
				InboxID: inbox, MessageID: ref.MessageID,
			})
			if gerr != nil {
				slog.Warn("failed to read existing staged message", "inbox", inbox,
					"message_id", ref.MessageID, "error", gerr)
				fail(ref)
				continue
			}
			if existing.CompleteAt.Valid {
				continue // whole: nothing to stage
			}
		default:
			slog.Warn("failed to stage email message", "inbox", inbox, "message_id", ref.MessageID, "error", err)
			fail(ref)
			continue
		}

		// Normalize recipients for the indexed routing query. INSERT OR IGNORE
		// so re-staging never duplicates; the LIKE-era to_addrs column stays
		// as the raw record until the old query is retired.
		routingFailed := false
		for _, addr := range ref.To {
			if addr == "" {
				continue
			}
			if err := s.queries.AddEmailMessageRecipient(ctx, store.AddEmailMessageRecipientParams{
				InboxID:   inbox,
				MessageID: ref.MessageID,
				Address:   addr,
			}); err != nil {
				slog.Warn("failed to stage email recipient", "inbox", inbox,
					"message_id", ref.MessageID, "address", addr, "error", err)
				routingFailed = true
				fail(ref)
			}
		}

		// Completeness is both halves: a message with no recipient row is
		// invisible to every feed, and one with no body renders as an empty
		// newsletter. Neither is marked complete below.
		if err := s.stageBody(ctx, inbox, ref); err != nil {
			slog.Warn("failed to stage email body", "inbox", inbox,
				"message_id", ref.MessageID, "error", err)
			fail(ref)
			continue
		}
		if routingFailed {
			fail(ref)
			continue
		}
		s.complete(ctx, inbox, ref.MessageID)
		staged++
		if nsString(ref.Timestamp).Valid && ref.Timestamp > newest {
			newest = ref.Timestamp
		}
	}

	// Advance the watermark only after the pass succeeded, so a partial failure
	// is retried rather than skipped. `newest` is preferred, but when nothing
	// new arrived the previous watermark is kept (re-sending the old value is
	// harmless and keeps the row's semantics simple).
	//
	// A staging failure pins the window at the failed message instead: leaving
	// the watermark on it is what makes the next pass re-list and finish it.
	// Without a timestamp to pin to there is nothing safe to compute, so the
	// watermark is left entirely alone and the same window is read again.
	watermark := newest
	if failed {
		watermark = failedAt
	}
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

	// Record the failure AFTER the watermark write: UpsertEmailSyncState clears
	// last_error on success, so recording it first would be immediately wiped
	// and the stall would be invisible.
	//
	// This is the visible half of failing fast. A pinned window means the inbox
	// stops making progress until the message is resolved, and that must not
	// look like a healthy sync: last_error is rendered in the UI, so the stall
	// is reported rather than inferred from a log line nobody reads.
	if failed {
		s.recordError(ctx, inbox, fmt.Errorf(
			"could not fully stage a message in the sync window; the window is pinned at %s and no newer mail will be staged until this clears",
			watermark))
	}

	if staged > 0 {
		slog.Info("staged email messages", "inbox", inbox, "count", staged, "seen", len(refs))
	}
	return nil
}

// stageBody fetches and stores one message's body.
//
// Bodies come from a single GetMessage. The list endpoint omits them and
// batch-get silently drops them, so this is the only route.
func (s *EmailSync) stageBody(ctx context.Context, inbox string, ref AgentMailMessageRef) error {
	msg, err := s.client.GetMessage(ctx, ref.MessageID)
	if err != nil {
		return fmt.Errorf("fetching body: %w", err)
	}
	if err := s.storeBody(ctx, inbox, ref, msg); err != nil {
		return fmt.Errorf("storing body: %w", err)
	}
	return nil
}

// complete marks a staged message as fully ingested, which is what lets a later
// overlap re-read skip it without re-downloading its body.
//
// A failure here is logged and not fatal: the row stays incomplete, so the next
// pass revisits it, which is the safe direction. Returning the error would stall
// the inbox over a marker write, and the re-read costs one duplicate body fetch.
func (s *EmailSync) complete(ctx context.Context, inbox, messageID string) {
	if _, err := s.queries.MarkEmailMessageComplete(ctx, store.MarkEmailMessageCompleteParams{
		InboxID: inbox, MessageID: messageID,
	}); err != nil {
		slog.Warn("failed to mark email message complete", "inbox", inbox, "message_id", messageID, "error", err)
	}
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

// encodeAddrs renders the recipient list as a JSON array, kept as the raw
// stored record (recipient rows carry the indexed routing copy).
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
const emailFetchLimit int64 = 200

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
	rows, err := f.queries.ListEmailMessagesByRecipientExact(ctx, store.ListEmailMessagesByRecipientExactParams{
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
