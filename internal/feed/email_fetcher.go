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
	// attempted records the messages whose body this pass already tried, so the
	// repair pass does not fetch the same body twice in one cycle. Bounded by
	// the window, and it keeps the request count honest: this codebase has
	// already paid for an unbounded re-fetch.
	attempted := make(map[string]bool, len(refs))
	for _, ref := range refs {
		attempted[ref.MessageID] = true
		// Idempotency lives in the schema (UNIQUE inbox_id,message_id): a
		// re-read of the overlap returns ErrNoRows here and costs nothing.
		//
		// ErrNoRows means only that the METADATA row already exists, which is
		// not the same as the message being fully staged: a pass that failed on
		// the body or on the recipient insert leaves exactly this shape. So the
		// recipient and body work below runs either way, and completeness is
		// what marks the row done.
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
			if !errors.Is(err, sql.ErrNoRows) {
				slog.Warn("failed to stage email message", "inbox", inbox, "message_id", ref.MessageID, "error", err)
				continue
			}
			// The metadata row already exists. That is only a reason to skip
			// the rest of the work if the row is COMPLETE: an incomplete row is
			// exactly what this re-read is here to repair.
			//
			// The completeness check is what keeps the overlap free. Without
			// it every pass would re-download the body of every message in the
			// window, which is the unbounded re-fetch this codebase has already
			// been burned by once.
			if existing, gerr := s.queries.GetEmailMessageByMessageID(ctx, store.GetEmailMessageByMessageIDParams{
				InboxID: inbox, MessageID: ref.MessageID,
			}); gerr == nil && existing.CompleteAt.Valid {
				continue // whole: nothing to stage, nothing to repair
			} else if gerr != nil {
				slog.Warn("failed to read existing staged message", "inbox", inbox,
					"message_id", ref.MessageID, "error", gerr)
				continue
			}
		}

		// Normalize recipients for the indexed routing query. INSERT OR IGNORE
		// so re-staging never duplicates; the LIKE-era to_addrs column stays
		// as the raw record until the old query is retired.
		//
		// A failure here must NOT advance the watermark past this message: the
		// recipient is the routing key, so a message with no recipient row is
		// invisible to every feed and the loss is permanent. Tracked and
		// retried instead.
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
				slog.Warn("failed to stage email recipient", "inbox", inbox, "message_id", ref.MessageID, "error", err)
				routingFailed = true
			}
		}

		outcome := s.stageBody(ctx, inbox, ref)
		if outcome == stageDeferred {
			// The row is repairable and the body is missing: leave it
			// incomplete and let repair fetch it. Do not advance the watermark
			// for it, so the next pass re-reads it even if repair cannot.
			//
			// The attempt budget is NOT charged here. This pass runs every
			// cycle over the overlap, so charging it would spend a healthily
			// retried message's budget on window re-reads alone and surrender
			// mail that was never actually unreachable. The budget belongs to
			// the repair pass, which is what retries a message once the window
			// has moved past it.
			slog.Warn("email body not available yet, leaving the message for repair",
				"inbox", inbox, "message_id", ref.MessageID)
			continue
		}
		// The body landed. Routing is the other half of completeness, so only
		// mark the row done when both succeeded: completing a message with no
		// recipient row would hide it from every feed, which is worse than an
		// empty body. Repair re-derives the recipients from the stored list.
		if routingFailed {
			continue
		}
		s.complete(ctx, inbox, ref.MessageID)
		staged++
		if nsString(ref.Timestamp).Valid && ref.Timestamp > newest {
			newest = ref.Timestamp
		}
	}

	// Repair anything a previous pass left half staged. This is deliberately
	// NOT limited to the API window: a body that failed more than one overlap
	// ago is never returned by ListMessages again, so without this the loss is
	// permanent.
	s.repairIncomplete(ctx, inbox, attempted)

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

// emailRepairMaxAttempts bounds how many times a half-staged message is
// re-attempted before it is surrendered.
//
// Bounded deliberately: a body the provider will never return must not be
// fetched every cycle forever. This codebase has already paid for that lesson
// once (an unbounded re-fetch produced a provider rate-limit incident), so the
// budget is small and the row is surrendered rather than retried indefinitely.
// Surrendering keeps the pre-fix behavior for that message, which is the
// conservative choice: it still surfaces as an item instead of being lost.
const emailRepairMaxAttempts = 5

// emailRepairBatch caps how many half-staged rows one cycle repairs, so a large
// backlog cannot turn a single poll into a request storm against the provider.
// Oldest first, so the rows nearest to aging out of retention are repaired first
// and the backlog drains across cycles.
const emailRepairBatch int64 = 50

// stageOutcome is what a body-fetch attempt concluded.
//
// There is deliberately no "permanently failed" outcome: a body-fetch error is
// never classified as unrecoverable, because guessing wrong discards mail for
// good. The retry budget in repairIncomplete is what bounds the cost of a body
// that never arrives.
type stageOutcome int

const (
	// stageOK: the body was fetched and stored.
	stageOK stageOutcome = iota
	// stageDeferred: the body is missing; a later attempt may still get it.
	stageDeferred
)

// stageBody fetches and stores one message's body, reporting whether the
// attempt succeeded, should be retried, or should be given up on.
//
// Bodies come from a single GetMessage. The list endpoint omits them and
// batch-get silently drops them, so this is the only route.
func (s *EmailSync) stageBody(ctx context.Context, inbox string, ref AgentMailMessageRef) stageOutcome {
	msg, err := s.client.GetMessage(ctx, ref.MessageID)
	if err != nil {
		// Deliberately NOT classified by status code. A 4xx looks permanent,
		// but treating it as such would permanently discard a body that a
		// provider returned transiently (or that a proxy mangled), and that
		// loss is unrecoverable. The retry budget in repairIncomplete already
		// bounds the cost of a body that never arrives, so the conservative
		// choice costs a few requests and can never lose mail.
		slog.Warn("failed to fetch email body", "inbox", inbox, "message_id", ref.MessageID, "error", err)
		return stageDeferred
	}
	if err := s.storeBody(ctx, inbox, ref, msg); err != nil {
		slog.Warn("failed to store email body", "inbox", inbox, "message_id", ref.MessageID, "error", err)
		return stageDeferred
	}
	return stageOK
}

// complete marks a staged message as fully ingested, which is what stops the
// repair pass from revisiting it. Failures are logged, not fatal: the row simply
// stays incomplete and is re-attempted, which is the safe direction.
func (s *EmailSync) complete(ctx context.Context, inbox, messageID string) {
	if _, err := s.queries.MarkEmailMessageComplete(ctx, store.MarkEmailMessageCompleteParams{
		InboxID: inbox, MessageID: messageID,
	}); err != nil {
		slog.Warn("failed to mark email message complete", "inbox", inbox, "message_id", messageID, "error", err)
	}
}

// repairIncomplete re-attempts messages a previous pass left half staged.
//
// This is the part that makes the fix complete rather than partial. Overlap
// re-reads only cover emailSyncOverlap, so a message whose body failed earlier
// than that is never returned by ListMessages again: without a repair driven
// from the staged rows themselves, that message is lost permanently, which is
// the bug this exists to close.
//
// Recipients are re-derived from the stored to_addrs JSON, which the failed pass
// wrote in full before it failed, so no list call is needed for routing.
func (s *EmailSync) repairIncomplete(ctx context.Context, inbox string, attempted map[string]bool) {
	rows, err := s.queries.ListIncompleteEmailMessages(ctx, store.ListIncompleteEmailMessagesParams{
		MaxAttempts: emailRepairMaxAttempts,
		Lim:         emailRepairBatch,
	})
	if err != nil {
		slog.Warn("failed to list incomplete staged email", "inbox", inbox, "error", err)
		return
	}
	repaired, surrendered := 0, 0
	for _, row := range rows {
		// Recipients first: a row can be complete on the body and still be
		// unroutable, and routing is the reason a feed can see it at all.
		routingFailed := false
		for _, addr := range decodeAddrs(row.ToAddrs) {
			if err := s.queries.AddEmailMessageRecipient(ctx, store.AddEmailMessageRecipientParams{
				InboxID:   row.InboxID,
				MessageID: row.MessageID,
				Address:   addr,
			}); err != nil {
				slog.Warn("failed to repair email recipient", "inbox", row.InboxID,
					"message_id", row.MessageID, "error", err)
				routingFailed = true
			}
		}

		// A body already present means a previous attempt stored it and only
		// the completion marker (or routing) was missing: do not re-fetch.
		if row.Text != "" || row.Html != "" {
			if !routingFailed {
				s.complete(ctx, row.InboxID, row.MessageID)
				repaired++
			}
			continue
		}

		// This pass already tried this body in the window above. Trying again
		// in the same cycle would double the request cost of every failing
		// message for no gain.
		if attempted[row.MessageID] {
			continue
		}

		ref := AgentMailMessageRef{MessageID: row.MessageID}

		// Charge the attempt BEFORE fetching, and act on the count it returns:
		// if this attempt exhausts the budget the row is surrendered even when
		// the fetch failed, which is what stops a permanently unavailable body
		// from being retried forever.
		attempts, aerr := s.queries.BumpEmailMessageStageAttempt(ctx, store.BumpEmailMessageStageAttemptParams{
			InboxID: row.InboxID, MessageID: row.MessageID,
		})
		if aerr != nil {
			slog.Warn("failed to record email stage attempt", "inbox", row.InboxID,
				"message_id", row.MessageID, "error", aerr)
			// Without a recorded attempt the budget cannot be enforced, so do
			// not fetch: retrying unboundedly is the failure mode this budget
			// exists to prevent.
			continue
		}

		out := s.stageBody(ctx, row.InboxID, ref)
		switch {
		case out == stageOK:
			repaired++
		case attempts >= emailRepairMaxAttempts:
			// The budget is spent. Give up on the body and let the message
			// surface as an item, as it did before this fix, rather than
			// leaving it invisible forever.
			surrendered++
			slog.Warn("surrendering staged email after exhausting the retry budget",
				"inbox", row.InboxID, "message_id", row.MessageID, "attempts", attempts)
		default:
			continue // still retryable: leave complete_at NULL
		}
		s.complete(ctx, row.InboxID, row.MessageID)
	}
	if repaired > 0 || surrendered > 0 {
		slog.Info("repaired staged email", "inbox", inbox, "repaired", repaired,
			"surrendered", surrendered, "incomplete_seen", len(rows))
	}
}

// decodeAddrs parses the stored JSON recipient array.
//
// A malformed value yields nil rather than an error: the to_addrs column is a
// record, and a row with unparseable JSON is better left for the next attempt
// than treated as fatal mid-poll.
func decodeAddrs(raw string) []string {
	if raw == "" {
		return nil
	}
	var addrs []string
	if err := json.Unmarshal([]byte(raw), &addrs); err != nil {
		return nil
	}
	return addrs
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
