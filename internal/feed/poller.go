package feed

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"digesterr/internal/metrics"
	"digesterr/internal/store"
)

type Poller struct {
	queries         *store.Queries
	fetchers        map[string]Fetcher
	enrichers       map[string]Enricher
	commentFetchers map[string]CommentFetcher
	// recordFn is called for each outbound HTTP request made during
	// feed fetching/enrichment. Nil means no recording.
	recordFn RecordFunc
	// sharedTransport is the shared HTTP transport for byte tracking.
	sharedTransport http.RoundTripper
	// expireItems deletes items older than this during refresh/generation
	// (saved items are exempt); expireDigests deletes digest results older
	// than this. Zero disables expiry.
	expireItems   time.Duration
	expireDigests time.Duration
	// enrichmentWindow bounds how long after insertion an item stays
	// eligible for periodic score re-enrichment. Zero means 24h.
	enrichmentWindow time.Duration
	// emailClientFactory builds the AgentMail client for one account. Tests
	// substitute a factory pointing at an httptest server; production uses the
	// default. See SetEmailClientFactory.
	emailClientFactory EmailClientFactory
	// emailSyncs holds one sync stage per (inbox, token) pair, created on first
	// use. AgentMail configuration is per user, so there is no single sync to
	// attach: the same mailbox shared by two accounts is still drained once,
	// because the pair is the same.
	//
	// Guarded by emailSyncsMu: PollAll (background ticker), PollFeed (the
	// refresh button / post-create auto-fetch), and SetTransport all touch it
	// concurrently. Entries are evicted after an hour of disuse so API-key
	// rotations and account churn cannot grow the map without bound.
	emailSyncsMu sync.Mutex
	emailSyncs   map[string]*emailSyncEntry
	// pollConcurrency bounds how many feeds are fetched at once. Writes still
	// serialize on the single SQLite connection; concurrency is for the
	// network fetch/enrich/extract phases, so one slow feed no longer stalls
	// every feed behind it.
	pollConcurrency int
	// pollTimeout bounds a single feed's whole poll (fetch + enrich + extract).
	// A feed that overruns is cancelled and recorded as a failure, so it backs
	// off instead of holding a worker forever.
	pollTimeout time.Duration
}

// emailSyncEntry is one memoized AgentMail sync stage plus its last-use time,
// so idle stages (rotated keys, deleted accounts) can be evicted.
type emailSyncEntry struct {
	sync     *EmailSync
	lastUsed time.Time
}

// emailSyncTTL bounds how long an unused (inbox, token) stage stays memoized.
// Rotating an API key orphans the old pair; without eviction every rotation
// leaks one client for the life of the process.
const emailSyncTTL = time.Hour

// EmailAccount is one user's AgentMail credentials, resolved from their users
// row. The email feed type is configured per account, not per instance.
type EmailAccount struct {
	UserID  int64
	InboxID string
	APIKey  string
}

// EmailClientFactory builds the AgentMail client for one account.
//
// Production leaves it at the default (the real API host). Tests substitute a
// factory that points the client at an httptest server, which is the ONLY reason
// this seam exists: AgentMailClient.baseURL is unexported and lives in this
// package, exactly as RedditFetcher.baseURL and TwitterFetcher.baseURL do. There
// is deliberately no YAML/env counterpart — do not turn it into a config knob.
type EmailClientFactory func(account EmailAccount) *AgentMailClient

// fetchedCandidate pairs a freshly fetched item with its duplicate-detection
// fingerprint, so scores can be refreshed before the insert decision.
type fetchedCandidate struct {
	item Item
	hash string
}

func NewPoller(queries *store.Queries) *Poller {
	rssFetcher := NewRSSFetcher()
	twitterFetcher := NewTwitterFetcher()
	redditFetcher := NewRedditFetcher()
	hnEnricher := NewHNEnricher()
	lobstersEnricher := NewLobstersEnricher()
	redditEnricher := NewRedditEnricher(queries)
	noop := &NoopEnricher{}
	return &Poller{
		queries:         queries,
		emailSyncs:      map[string]*emailSyncEntry{},
		pollConcurrency: 4,
		pollTimeout:     5 * time.Minute,
		fetchers: map[string]Fetcher{
			"rss":      rssFetcher,
			"atom":     rssFetcher,
			"hn":       rssFetcher,
			"lobsters": rssFetcher,
			"reddit":   redditFetcher,
			"twitter":  twitterFetcher,
		},
		enrichers: map[string]Enricher{
			"rss":      noop,
			"atom":     noop,
			"hn":       hnEnricher,
			"lobsters": lobstersEnricher,
			"reddit":   redditEnricher,
			"twitter":  twitterFetcher,
		},
		commentFetchers: map[string]CommentFetcher{
			"rss":      noop,
			"atom":     noop,
			"hn":       hnEnricher,
			"lobsters": lobstersEnricher,
			"reddit":   redditEnricher,
			"twitter":  twitterFetcher,
		},
	}
}

// SetEmailClientFactory overrides how AgentMail clients are built. Passing nil
// restores the default (the real API host).
//
// The email feed type is configured per user, so there is no "set the sync"
// call any more: credentials are resolved from the owner's users row at poll
// time and the sync stage is created on first use. Tests use this to point the
// client at a stub server.
func (p *Poller) SetEmailClientFactory(f EmailClientFactory) {
	p.emailSyncsMu.Lock()
	defer p.emailSyncsMu.Unlock()
	p.emailClientFactory = f
	// Drop the memoized syncs: they hold clients built by the previous factory.
	p.emailSyncs = map[string]*emailSyncEntry{}
}

// defaultEmailClientFactory builds a client against the real AgentMail API,
// returning nil when the account has no usable credentials.
func defaultEmailClientFactory(acct EmailAccount) *AgentMailClient {
	return NewAgentMailClient(acct.APIKey, acct.InboxID)
}

// emailAccountFor resolves the AgentMail credentials of the account that owns a
// feed. ok=false means the account has not configured the email feed type (or
// the user row is gone), which callers treat as "skip and report", never as a
// hard error: one unconfigured account must not stop every other feed from
// polling.
func (p *Poller) emailAccountFor(ctx context.Context, userID int64) (EmailAccount, bool) {
	user, err := p.queries.GetUserByID(ctx, userID)
	if err != nil {
		return EmailAccount{}, false
	}
	inbox := strings.TrimSpace(user.EmailInbox)
	key := strings.TrimSpace(user.EmailApiKey)
	if inbox == "" || key == "" {
		return EmailAccount{}, false
	}
	return EmailAccount{UserID: user.ID, InboxID: inbox, APIKey: key}, true
}

// emailSyncFor returns the sync stage for an account, creating it on first use.
//
// The stage is memoized per (inbox, token) pair rather than per account: two
// accounts sharing one mailbox share the drain (it is keyed by inbox_id
// server-side anyway), while a token changed in the settings dialog yields a
// fresh client instead of reusing one bound to the old credential.
//
// The whole lookup-create-evict sequence holds emailSyncsMu: emailSyncFor runs
// on both the background poll ticker and request-driven PollFeed calls, so an
// unlocked map read+write here is a fatal concurrent-map panic. Stages idle
// past emailSyncTTL are evicted on the way through, so rotated keys and
// deleted accounts cannot accumulate clients forever. The factory call runs
// under the lock — it is a pure constructor (no I/O), so this cannot block
// polling.
func (p *Poller) emailSyncFor(acct EmailAccount) *EmailSync {
	key := acct.InboxID + "\x00" + acct.APIKey
	now := time.Now()
	p.emailSyncsMu.Lock()
	defer p.emailSyncsMu.Unlock()
	for k, e := range p.emailSyncs {
		if now.Sub(e.lastUsed) > emailSyncTTL {
			delete(p.emailSyncs, k)
		}
	}
	if e, ok := p.emailSyncs[key]; ok {
		e.lastUsed = now
		return e.sync
	}
	factory := p.emailClientFactory
	if factory == nil {
		factory = defaultEmailClientFactory
	}
	client := factory(acct)
	if client == nil {
		return nil
	}
	s := NewEmailSync(client, p.queries)
	if s == nil {
		return nil
	}
	// A lazily-created sync still needs the shared metrics transport, or this
	// inbox's traffic becomes invisible in GET /api/metrics.
	if p.sharedTransport != nil {
		s.setTransport(p.sharedTransport)
	}
	p.emailSyncs[key] = &emailSyncEntry{sync: s, lastUsed: now}
	return s
}

// failEmailFeed records a visible failure on a feed whose owner cannot be
// polled, so the sidebar's error icon shows why it is empty instead of the feed
// silently returning nothing.
func (p *Poller) failEmailFeed(ctx context.Context, feed store.Feed, msg string) error {
	logger := slog.With("feed_id", feed.ID, "feed_name", feed.Name, "feed_url", feed.Url)
	logger.Warn("email feed cannot be polled", "error", msg)
	if err := p.queries.UpdateFeedError(ctx, store.UpdateFeedErrorParams{
		ID:    feed.ID,
		Error: msg,
	}); err != nil {
		logger.Error("failed to record email feed error", "error", err)
	}
	if _, err := p.queries.UpdateFeedStatus(ctx, store.UpdateFeedStatusParams{
		ID:     feed.ID,
		Status: "error",
	}); err != nil {
		logger.Error("failed to update feed status", "error", err)
	}
	return errors.New(msg)
}

// emailFetcherFor returns the staging-table reader for an email feed, scoped to
// its OWNER's inbox.
//
// Before AgentMail credentials became per-user there was one inbox on the
// instance, so the fetcher could be registered once in the fetchers map. It
// cannot be now: ListEmailMessagesByRecipient filters by inbox_id, and that
// filter is what stops one account's feed from reading another account's staged
// mail.
func (p *Poller) emailFetcherFor(acct EmailAccount) Fetcher {
	// Read-only over the staging table: no client needed, so Fetch cannot
	// accidentally gain I/O (see EmailFetcher's doc comment).
	return NewEmailFetcher(p.queries, acct.InboxID)
}

func (p *Poller) Enrichers() map[string]Enricher {
	return p.enrichers
}

// SetExpiry configures data retention: items older than itemsAge and digest
// results older than digestsAge are deleted during feed refresh and digest
// generation. A zero duration disables the respective expiry.
func (p *Poller) SetExpiry(itemsAge, digestsAge time.Duration) {
	p.expireItems = itemsAge
	p.expireDigests = digestsAge
}

// SetRecordFunc sets the callback for recording outbound HTTP request metrics.
func (p *Poller) SetRecordFunc(fn RecordFunc) {
	p.recordFn = fn
}

// SetTransport sets the HTTP transport on all fetchers and enrichers for
// metric byte tracking.
func (p *Poller) SetTransport(transport http.RoundTripper) {
	p.sharedTransport = transport
	for _, f := range p.fetchers {
		setClientTransport(f, transport)
	}
	for _, e := range p.enrichers {
		setClientTransport(e, transport)
	}
	for _, cf := range p.commentFetchers {
		setClientTransport(cf, transport)
	}
	// The email syncs are not in any of those maps (they are a stage, not a
	// fetcher) but they are what actually issue the AgentMail requests. They are
	// created lazily, so apply the transport to the ones that already exist —
	// emailSyncFor does the same on creation. Locked: these entries are read
	// and written by concurrent poll paths (see emailSyncFor).
	p.emailSyncsMu.Lock()
	defer p.emailSyncsMu.Unlock()
	for _, e := range p.emailSyncs {
		e.sync.setTransport(transport)
	}
}

// MetricsTransport returns the shared metrics transport for use in
// creating HTTP clients that should be tracked.
func (p *Poller) MetricsTransport() http.RoundTripper {
	return p.metricsTransport()
}

// metricsTransport returns the shared transport, falling back to http.DefaultTransport.
func (p *Poller) metricsTransport() http.RoundTripper {
	if p.sharedTransport != nil {
		return p.sharedTransport
	}
	return http.DefaultTransport
}

// setClientTransport sets the Transport on an HTTP client embedded in a
// fetcher, enricher, or comment fetcher via type assertion.
func setClientTransport(v interface{}, transport http.RoundTripper) {
	type transportSetter interface {
		setTransport(http.RoundTripper)
	}
	if s, ok := v.(transportSetter); ok {
		s.setTransport(transport)
	}
}

// recordEnrich records an enrichment operation.
func (p *Poller) recordEnrich(start time.Time, feed store.Feed, host string, respBytes int64, requests int, err error) {
	if p.recordFn == nil {
		return
	}
	errStr := ""
	statusCode := 200
	if err != nil {
		errStr = err.Error()
		statusCode = metrics.StatusFor(err)
	}
	p.recordFn(Record{
		Timestamp:  start,
		Host:       host,
		StatusCode: statusCode,
		Bytes:      respBytes,
		Requests:   requests,
		DurationMs: time.Since(start).Milliseconds(),
		Reason:     "enrichment",
		FeedID:     feed.ID,
		FeedName:   feed.Name,
		FeedType:   feed.Type,
		Error:      errStr,
	})
}

// hostFromURL extracts a hostname from a URL string. It handles bare
// hostnames (no scheme) by prepending https://.
func hostFromURL(raw string) string {
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err == nil && u.Hostname() != "" {
		return u.Hostname()
	}
	// Bare hostname like "sdf" — try with https:// prefix.
	u, err = url.Parse("https://" + raw)
	if err == nil && u.Hostname() != "" {
		return u.Hostname()
	}
	return ""
}

// ConsumeCounters reads and resets the shared transport counters, returning
// response bytes and the number of outbound HTTP requests they came from.
//
// Per-operation callers should NOT use this: feeds poll concurrently and the
// digest runs alongside them, so these counters can hold another operation's
// traffic. Carry a metrics.Scope on the request context instead (metrics.WithScope)
// and read its Totals. Retained because the process-wide aggregate is still
// reported, and it is the only place traffic from unscoped paths is visible.
func (p *Poller) ConsumeCounters() (respBytes int64, requests int) {
	if t, ok := p.sharedTransport.(*metrics.Transport); ok {
		b, r := t.Consume()
		return b, int(r)
	}
	return 0, 0
}

// Quotas reports the rate-limit state of every provider that advertises a
// budget. Providers whose client is not registered are omitted, so an
// instance with no quota-bound feeds reports nothing rather than a banner of
// zeroes. Providers that publish no rate-limit headers (RSS, Hacker News,
// Lobsters) never appear.
func (p *Poller) Quotas() []QuotaSnapshot {
	quotas := make([]QuotaSnapshot, 0, 2)
	if tf, ok := p.fetchers["twitter"].(*TwitterFetcher); ok {
		quotas = append(quotas, tf.QuotaSnapshot(twitterQuotaReserve))
	}
	if rf, ok := p.fetchers["reddit"].(*RedditFetcher); ok {
		quotas = append(quotas, rf.QuotaSnapshot(redditQuotaReserve))
	}
	return quotas
}

// SetEnrichmentWindow configures how long after insertion items are
// re-enriched each cycle to track score/comment growth. A zero value
// means the default of 24 hours.
func (p *Poller) SetEnrichmentWindow(d time.Duration) {
	if d <= 0 {
		d = 24 * time.Hour
	}
	p.enrichmentWindow = d
}

// SetRedditCredentials overrides the Reddit API credentials used by the
// reddit enricher. The enricher is constructed with environment fallbacks;
// the merged config (file + env) wins here.
func (p *Poller) SetRedditCredentials(clientID, clientSecret string) {
	if e, ok := p.enrichers["reddit"]; ok {
		if re, ok := e.(*RedditEnricher); ok {
			re.SetCredentials(clientID, clientSecret)
		}
	}
	if f, ok := p.fetchers["reddit"]; ok {
		if rf, ok := f.(*RedditFetcher); ok {
			rf.SetCredentials(clientID, clientSecret)
		}
	}
}

// SetTwitterCredentials overrides the Twitter/X session cookies used by the
// twitter fetcher. The fetcher is constructed without credentials; the
// merged config (file + env) wins here.
func (p *Poller) SetTwitterCredentials(ct0, authToken string) {
	if f, ok := p.fetchers["twitter"]; ok {
		if tf, ok := f.(*TwitterFetcher); ok {
			tf.SetCredentials(ct0, authToken)
		}
	}
}

// SetUserAgent overrides the User-Agent header for all outbound HTTP
// requests across fetchers and enrichers.
func (p *Poller) SetUserAgent(ua string) {
	if ua == "" {
		return
	}
	for _, f := range p.fetchers {
		switch ff := f.(type) {
		case *RSSFetcher:
			ff.SetUserAgent(ua)
		case *TwitterFetcher:
			ff.SetUserAgent(ua)
		case *RedditFetcher:
			ff.SetUserAgent(ua)
		}
	}
	for _, e := range p.enrichers {
		if re, ok := e.(*RedditEnricher); ok {
			re.SetUserAgent(ua)
		}
	}
}

// SetTwitterCount overrides how many tweets to fetch per poll.
func (p *Poller) SetTwitterCount(n int) {
	if f, ok := p.fetchers["twitter"]; ok {
		if tf, ok := f.(*TwitterFetcher); ok {
			tf.SetCount(n)
		}
	}
}

// SetRedditCount overrides how many posts to fetch per subreddit poll.
func (p *Poller) SetRedditCount(n int) {
	if f, ok := p.fetchers["reddit"]; ok {
		if rf, ok := f.(*RedditFetcher); ok {
			rf.SetLimit(n)
		}
	}
}

// SetHNEnrichCount overrides the max number of HN items to resolve per
// enrichment batch. Zero means unbounded (all pending items).
func (p *Poller) SetHNEnrichCount(n int) {
	for _, e := range p.enrichers {
		if he, ok := e.(*HNEnricher); ok {
			he.SetMaxHits(n)
		}
	}
}

// SetLobstersEnrichCount overrides how many Lobste.rs stories to enrich
// per poll. Zero means all items returned by the RSS feed.
func (p *Poller) SetLobstersEnrichCount(n int) {
	for _, e := range p.enrichers {
		if le, ok := e.(*LobstersEnricher); ok {
			le.SetMaxItems(n)
		}
	}
}

// ExpireOldData deletes items and digest results older than the configured
// retention windows. Saved items and items still referenced by digest
// results are never deleted. Items linked to expiring digest results flip
// from digested back to read first (same transaction), so expiry never
// strands items in an invisible state. Returns the number of rows deleted.
// Expiry is skipped for a window of zero.
func (p *Poller) ExpireOldData(ctx context.Context) (itemsDeleted, digestsDeleted int64, err error) {
	db := p.queries.DB()
	if db == nil {
		return 0, 0, fmt.Errorf("expiry requires a *sql.DB-backed Queries")
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, 0, fmt.Errorf("beginning expiry: %w", err)
	}
	defer tx.Rollback()
	qtx := p.queries.WithTx(tx)

	if p.expireItems > 0 {
		n, err := qtx.DeleteExpiredItems(ctx, time.Now().Add(-p.expireItems))
		if err != nil {
			return itemsDeleted, digestsDeleted, fmt.Errorf("deleting expired items: %w", err)
		}
		itemsDeleted = n
	}
	if p.expireDigests > 0 {
		cutoff := time.Now().Add(-p.expireDigests)
		// Flip each expiring result's linked items back to read before the
		// results (and their join rows) are deleted.
		ids, err := qtx.ListExpiredDigestResultIDs(ctx, cutoff)
		if err != nil {
			return itemsDeleted, digestsDeleted, fmt.Errorf("listing expired digest results: %w", err)
		}
		for _, id := range ids {
			if err := qtx.MarkDigestResultItemsRead(ctx, id); err != nil {
				return itemsDeleted, digestsDeleted, fmt.Errorf("flipping items of expired digest %d to read: %w", id, err)
			}
		}
		n, err := qtx.DeleteExpiredDigestResults(ctx, cutoff)
		if err != nil {
			return itemsDeleted, digestsDeleted, fmt.Errorf("deleting expired digest results: %w", err)
		}
		digestsDeleted = n
	}
	// Expired refresh tokens are dead weight (rotation creates a new row per
	// refresh); sweep them on the same cadence. A fixed 30-day cutoff matches
	// the refresh token lifetime with margin.
	if n, err := qtx.DeleteExpiredRefreshTokens(ctx, time.Now()); err != nil {
		slog.Warn("failed to delete expired refresh tokens", "error", err)
	} else if n > 0 {
		slog.Debug("deleted expired refresh tokens", "count", n)
	}
	// Staged email older than the item retention window is dead weight: it was
	// already ingested into feed items (or skipped).
	//
	// The cutoff is a plain string and the comparison happens in SQL, because
	// received_at may be NULL (a missing provider timestamp) and the two
	// timestamp columns are stored in different formats; see the query's own
	// comment. Passing a bound time.Time here would compare unequal against
	// every row, which is what the other timestamp note in this file warns
	// about.
	if p.expireItems > 0 {
		cutoff := time.Now().Add(-p.expireItems).UTC().Format(time.RFC3339Nano)
		if n, err := qtx.DeleteExpiredEmailMessages(ctx, cutoff); err != nil {
			slog.Warn("failed to delete expired staged email", "error", err)
		} else if n > 0 {
			slog.Debug("deleted expired staged email", "count", n)
		}
	}
	if err := tx.Commit(); err != nil {
		return itemsDeleted, digestsDeleted, fmt.Errorf("committing expiry: %w", err)
	}
	return itemsDeleted, digestsDeleted, nil
}

func (p *Poller) CommentFetchers() map[string]CommentFetcher {
	return p.commentFetchers
}

func (p *Poller) ExtractItem(ctx context.Context, itemID int64) error {
	item, err := p.queries.GetItem(ctx, itemID)
	if err != nil {
		return fmt.Errorf("getting item %d: %w", itemID, err)
	}
	feed, _ := p.queries.GetFeedByID(ctx, item.FeedID)
	p.setItemExtractionPending(ctx, itemID, slog.With("item_id", itemID))
	client := &http.Client{Timeout: 30 * time.Second}
	p.extractSingleItem(ctx, client, item, feed, slog.With("item_id", itemID))
	return nil
}

func (p *Poller) Start(ctx context.Context, interval time.Duration) {
	slog.Info("starting feed poller", "interval", interval)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	p.PollAll(ctx)

	for {
		select {
		case <-ticker.C:
			p.PollAll(ctx)
		case <-ctx.Done():
			slog.Info("feed poller stopped")
			return
		}
	}
}

// expireOldData runs ExpireOldData and logs the outcome. Called at the start
// of every feed refresh cycle and from digest generation.
func (p *Poller) expireOldData(ctx context.Context) {
	items, digests, err := p.ExpireOldData(ctx)
	if err != nil {
		slog.Error("data expiry failed", "error", err)
		return
	}
	if items > 0 || digests > 0 {
		slog.Info("expired old data", "items", items, "digest_results", digests)
	}
}

// SetPollConcurrency bounds how many feeds are fetched at once. Values <= 0
// fall back to 1 (serial, the historical behavior).
func (p *Poller) SetPollConcurrency(n int) {
	if n <= 0 {
		n = 1
	}
	p.pollConcurrency = n
}

// SetPollTimeout bounds one feed's whole poll (fetch + enrich + extract).
// Values <= 0 fall back to 5 minutes.
func (p *Poller) SetPollTimeout(d time.Duration) {
	if d <= 0 {
		d = 5 * time.Minute
	}
	p.pollTimeout = d
}

func (p *Poller) PollAll(ctx context.Context) {
	p.expireOldData(ctx)

	feeds, err := p.queries.ListDueFeeds(ctx)
	if err != nil {
		slog.Error("failed to list due feeds", "error", err)
		return
	}

	slog.Info("polling feeds", "count", len(feeds))
	start := time.Now()

	// The email feed type is configured per user, and the mailboxes to drain are
	// derived from FEEDS (not users), so an account that stored credentials but
	// owns no email feed issues no requests. Each distinct mailbox is drained
	// once, before the feed loop: doing it per feed would re-download the whole
	// mailbox once per email feed, the N-fold fan-out that caused the production
	// Twitter 429s. A sync failure is recorded on the inbox and does not stop the
	// other feeds from polling.
	//
	// RunEmailSync (not Run) so the sync's HTTP traffic is recorded as
	// `email_sync` instead of being left in the shared transport counters for a
	// feed poll to claim.
	p.drainEmailInboxes(ctx)

	// Fetch feeds concurrently, bounded by pollConcurrency: one slow feed no
	// longer stalls every feed behind it. Database writes still serialize on
	// the single SQLite connection, so concurrency here buys network overlap
	// (the dominant cost) without write contention.
	sem := make(chan struct{}, p.pollConcurrency)
	var wg sync.WaitGroup
	for _, feed := range feeds {
		var fetcher Fetcher
		ok := true
		if feed.Type == "email" {
			// Resolved per feed, because the fetcher is scoped to the owner's
			// inbox. Missing credentials are a visible feed failure, not a
			// silent skip: the owner sees why the feed is empty.
			acct, configured := p.emailAccountFor(ctx, feed.UserID)
			if !configured {
				if err := p.failEmailFeed(ctx, feed, emailUnconfiguredMsg); err != nil {
					slog.Debug("email feed failure recorded", "feed_id", feed.ID, "error", err)
				}
				continue
			}
			fetcher = p.emailFetcherFor(acct)
		} else {
			var found bool
			fetcher, found = p.fetchers[feed.Type]
			ok = found
		}
		if !ok {
			slog.Warn("no fetcher for feed type", "type", feed.Type, "feed_id", feed.ID)
			continue
		}

		// Acquire before spawning so the number of live poll goroutines is
		// bounded; a cancelled ctx (shutdown) stops waiting for a slot.
		select {
		case sem <- struct{}{}:
		case <-ctx.Done():
			wg.Wait()
			return
		}
		wg.Add(1)
		go func(feed store.Feed, fetcher Fetcher) {
			defer wg.Done()
			defer func() { <-sem }()
			feedCtx, cancel := context.WithTimeout(ctx, p.pollTimeout)
			defer cancel()
			p.pollFeed(feedCtx, feed, fetcher)
		}(feed, fetcher)
	}
	wg.Wait()

	slog.Info("polled feeds", "count", len(feeds), "duration", time.Since(start))
}

func (p *Poller) PollFeed(ctx context.Context, feed store.Feed) error {
	// An email feed's Fetch only reads the staging table, so the owner's mailbox
	// must be drained first or a per-feed poll always returns zero new items.
	// This is the path the UI's refresh button and the post-create auto-fetch
	// take; the 15-minute PollAll cycle would otherwise be the only thing that
	// ever delivered new mail, making "Refresh" look broken for email feeds.
	//
	// Synchronous on purpose: the request must not report success before the
	// mailbox has actually been read.
	if feed.Type == "email" {
		acct, ok := p.emailAccountFor(ctx, feed.UserID)
		if !ok {
			return p.failEmailFeed(ctx, feed, emailUnconfiguredMsg)
		}
		sync := p.emailSyncFor(acct)
		if sync == nil {
			return p.failEmailFeed(ctx, feed, emailUnconfiguredMsg)
		}
		sync.RunEmailSync(ctx, p.recordFn)

		return p.pollFeed(ctx, feed, p.emailFetcherFor(acct))
	}

	fetcher, ok := p.fetchers[feed.Type]
	if !ok {
		return fmt.Errorf("no fetcher for feed type: %s", feed.Type)
	}
	return p.pollFeed(ctx, feed, fetcher)
}

// emailUnconfiguredMsg is the feed-level error text for an email feed whose
// owner has no AgentMail configuration. It is user-facing (the sidebar shows
// last_error in a tooltip), so it names the fix rather than the internals.
const emailUnconfiguredMsg = "email feeds need an AgentMail inbox and API key: configure them in Settings"

// drainEmailInboxes runs one sync pass per distinct AgentMail mailbox that has
// at least one live email feed.
//
// The account list comes from ListEmailAccountsInUse, which joins feeds to their
// owners: credentials stored by an account with no email feeds cost nothing, and
// a mailbox shared by two accounts is drained once because the query groups by
// inbox.
func (p *Poller) drainEmailInboxes(ctx context.Context) {
	accounts, err := p.queries.ListEmailAccountsInUse(ctx)
	if err != nil {
		slog.Warn("failed to list email accounts to drain", "error", err)
		return
	}
	for _, acct := range accounts {
		sync := p.emailSyncFor(EmailAccount{
			UserID:  acct.UserID,
			InboxID: acct.InboxID,
			APIKey:  acct.ApiKey,
		})
		if sync == nil {
			continue
		}
		sync.RunEmailSync(ctx, p.recordFn)
	}
}

// deleteDisposableItem removes an item the caller judged disposable, and
// reports whether it was actually removed.
//
// DeleteItem refuses to remove a saved or digest-linked row (it returns no
// rows), because either condition can appear between the moment the caller
// listed the item and the moment it deletes it: the user can save it, and a
// concurrent digest generation can link it into a result (the link is ON DELETE
// CASCADE, so the delete would silently strip the item out of a generated
// digest). Refusal is the guard working, not an error, so it is reported as
// kept rather than logged as a failure.
func (p *Poller) deleteDisposableItem(ctx context.Context, itemID int64) (bool, error) {
	_, err := p.queries.DeleteItem(ctx, itemID)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// recordPollFailure applies the failure bookkeeping for a failed poll and
// returns the error unchanged. Two rules make it correct:
//
//   - A caller-cancelled poll (SIGTERM, client disconnect) says nothing about
//     the feed. Recording it would back off a perfectly healthy source and
//     show the user an error they did not cause, so it is skipped. Our own
//     per-feed timeout arrives as DeadlineExceeded and IS a real failure.
//
//   - The writes run on a DETACHED context. A poll that failed because its
//     context expired (the poll timeout) would otherwise issue its backoff
//     through that expired context, the driver would reject the write, and the
//     feed would be retried on every single cycle forever with no backoff and
//     no visible error. Detaching also keeps a cancelled-at-shutdown write from
//     landing half-applied.
func (p *Poller) recordPollFailure(ctx context.Context, feed store.Feed, err error, logger *slog.Logger) error {
	if errors.Is(err, context.Canceled) {
		logger.Debug("poll cancelled by caller; not recording a feed failure", "error", err)
		return err
	}

	bctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()

	// Back off the feed so a persistently broken source is retried with
	// growing gaps instead of every cycle.
	if e := p.queries.RecordFeedFailure(bctx, feed.ID); e != nil {
		logger.Debug("failed to record feed backoff", "error", e)
	}
	// feeds.last_error is rendered in the sidebar tooltip: keep the full
	// error in the log, but store a bounded copy like digest configs do.
	msg := err.Error()
	if len(msg) > 500 {
		msg = msg[:500] + "…"
	}
	if e := p.queries.UpdateFeedError(bctx, store.UpdateFeedErrorParams{
		ID:    feed.ID,
		Error: msg,
	}); e != nil {
		logger.Error("failed to record feed error", "error", e)
	}
	if _, ue := p.queries.UpdateFeedStatus(bctx, store.UpdateFeedStatusParams{
		ID:     feed.ID,
		Status: "error",
	}); ue != nil {
		logger.Error("failed to update feed status", "error", ue)
	}
	return err
}

func (p *Poller) pollFeed(ctx context.Context, feed store.Feed, fetcher Fetcher) error {
	start := time.Now()
	logger := slog.With("feed_id", feed.ID, "feed_name", feed.Name, "feed_url", feed.Url)
	logger.Debug("polling feed")

	// Attribute this feed's traffic from the REQUESTS it makes, not from the
	// shared counter: feeds poll concurrently now, so a shared counter would let
	// one feed claim another's requests and bytes.
	scope := metrics.NewScope()
	items, err := fetcher.Fetch(metrics.WithScope(ctx, scope), feed.Url)
	fetchDur := time.Since(start)
	fetchBytes, fetchReqs := scope.Totals()
	// Derive host from feed URL.
	fetchHost := hostFromURL(feed.Url)
	if p.recordFn != nil {
		errStr := ""
		statusCode := 200
		if err != nil {
			errStr = err.Error()
			statusCode = metrics.StatusFor(err)
		}
		p.recordFn(Record{
			Timestamp:  start,
			URL:        feed.Url,
			StatusCode: statusCode,
			Bytes:      fetchBytes,
			Requests:   fetchReqs,
			Host:       fetchHost,
			DurationMs: fetchDur.Milliseconds(),
			Reason:     "fetch",
			FeedID:     feed.ID,
			FeedName:   feed.Name,
			FeedType:   feed.Type,
			Error:      errStr,
		})
	}
	if err != nil {
		logger.Warn("failed to fetch feed", "error", err)
		return p.recordPollFailure(ctx, feed, err, logger)
	}

	newCount := 0
	// Item writes that failed for a reason other than "already exists". A poll
	// whose writes all failed must not be recorded as a success: it would clear
	// the backoff, stamp last_fetched_at, and hide a feed that can store nothing.
	writeFailures := 0
	var firstWriteErr error
	// Normalize GUID/comment URL for feed types that need it, and fingerprint
	// every fetched item up front so scores can be refreshed before insert.
	candidates := make([]fetchedCandidate, 0, len(items))
	for _, item := range items {
		if feed.Type == "hn" {
			if hnID := extractHNID(item.CommentURL, item.URL); hnID != 0 {
				item.GUID = fmt.Sprintf("hn:%d", hnID)
			}
		}
		if feed.Type == "reddit" && item.CommentURL == "" && item.GUID != "" {
			u, err := url.Parse(item.GUID)
			if err == nil && strings.Contains(u.Path, "/comments/") {
				item.CommentURL = item.GUID
			}
		}
		candidates = append(candidates, fetchedCandidate{
			item: item,
			hash: itemHash(item.GUID, item.Title, item.URL, item.Content),
		})
	}

	// Enrich candidates + existing items in one BulkEnrich call.
	// Returns scored candidates for min_score and which hashes were enriched.
	var scored map[string]*store.Item
	var enrichedHashes map[string]bool
	scored, enrichedHashes = p.enrichForPoll(ctx, feed, candidates, logger)

	// Hashes of rows inserted by THIS poll. The min_score cleanup below may
	// only remove these — never pre-existing rows (which the user may have
	// already read, saved, or linked into a digest).
	insertedThisPoll := make(map[string]int64, len(candidates))
	for _, c := range candidates {
		item := c.item
		// enriched tracks whether a real score was obtained for this
		// candidate before insertion. Unenriched items are stored with
		// NULL points so the post-insert enrichment pass retries them.
		if si, ok := scored[c.hash]; ok {
			// Apply the refreshed score (when available) at insert time.
			item.Points = int(si.Points)
			item.CommentsCnt = int(si.CommentsCount)
			if si.CommentUrl != "" {
				item.CommentURL = si.CommentUrl
			}
			// The enricher can supply a body the feed payload lacks (HN text
			// posts: news.ycombinator.com/rss only ships a "Comments" link).
			if si.Content != "" && si.Content != item.Content {
				item.Content = si.Content
			}
			// Only apply min_score to items the enricher actually scored.
			// Items the enricher couldn't score (missed or Noop) keep
			// the RSS value and always pass — unknown scores are not
			// grounds for rejection.
			if si.Points > 0 && int64(si.Points) < feed.MinScore {
				logger.Debug("item below min_score, skipping insert", "title", item.Title, "points", item.Points, "min_score", feed.MinScore)
				continue
			}
		}
		h := c.hash
		pubAt := sql.NullTime{}
		if !item.PublishedAt.IsZero() {
			pubAt = sql.NullTime{Time: item.PublishedAt, Valid: true}
		}
		id, err := p.queries.CreateItemIfNotExists(ctx, store.CreateItemIfNotExistsParams{
			FeedID:        feed.ID,
			Hash:          h,
			Title:         item.Title,
			Url:           item.URL,
			Content:       item.Content,
			CommentUrl:    item.CommentURL,
			Author:        item.Author,
			PublishedAt:   pubAt,
			Points:        int64(item.Points),
			CommentsCount: int64(item.CommentsCnt),
		})
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				logger.Debug("item already exists", "hash", h)
			} else {
				// A real write failure, not a duplicate: the feed is not being
				// ingested. Counted so the poll cannot be recorded as a success.
				logger.Error("failed to create item", "hash", h, "error", err)
				writeFailures++
				firstWriteErr = err
			}
			continue
		}
		if id > 0 {
			insertedThisPoll[h] = id
			newCount++
			if feed.ExtractContent != 0 {
				p.setItemExtractionPending(ctx, id, logger)
			}
		} else if feed.Type == "twitter" {
			// Twitter titles carry volatile state (📌 pinned badge, RT
			// provenance). The item hash excludes the title, so an existing
			// row is invisible to CreateItemIfNotExists — refresh its title
			// when the fetched one changed (e.g. tweet got pinned/unpinned).
			if err := p.refreshTwitterTitle(ctx, feed.ID, item.GUID, item.Title); err != nil {
				logger.Debug("twitter title refresh failed", "guid", item.GUID, "error", err)
			}
		}
	}

	// Clean up unenriched zero-score rows inserted by this poll: when
	// min_score > 0 and a real enricher ran, rows the enricher didn't
	// score (not in enrichedHashes) with score=0 are removed again. Only
	// rows inserted above (same-cycle ids) are eligible — a re-poll must
	// never delete a pre-existing row the user may have read, saved, or
	// linked into a digest (that deletion re-inserted the item as unread
	// on the next cycle, prod 2026-09-18: read rows on r/kiszamolo kept
	// reappearing). Deleting by id also pins the exact row instead of
	// re-matching by (feed_id, hash).
	if feed.MinScore > 0 && enrichedHashes != nil {
		for _, c := range candidates {
			if !enrichedHashes[c.hash] && c.item.Points == 0 {
				if id, ok := insertedThisPoll[c.hash]; ok {
					if _, err := p.deleteDisposableItem(ctx, id); err != nil {
						logger.Warn("failed to clean up unenriched zero-score item", "item_id", id, "hash", c.hash, "error", err)
					}
				}
			}
		}
	}

	// A poll whose item writes failed is NOT a success: recording one would
	// clear the backoff and stamp last_fetched_at, so a feed that can never
	// store anything (full disk, broken table) would look perfectly healthy
	// while delivering nothing. Report it through the same path as a fetch
	// failure, which also applies the backoff.
	if writeFailures > 0 {
		logger.Error("poll completed with item write failures", "failed", writeFailures, "new_items", newCount)
		return p.recordPollFailure(ctx, feed, fmt.Errorf("storing %d of %d fetched items failed: %w",
			writeFailures, len(candidates), firstWriteErr), logger)
	}

	// Success clears the backoff window (and stamps last_fetched_at).
	if err := p.queries.RecordFeedSuccess(ctx, feed.ID); err != nil {
		logger.Error("failed to record feed success", "error", err)
	}

	// A successful poll clears whatever the last failure left behind, at EITHER
	// severity. Called unconditionally: the query is a no-op when there is
	// nothing to clear, and this path cannot know the row's current error state
	// — the `feed` struct was read before the failure was recorded, so gating on
	// feed.LastError left a fresh error in place forever.
	//
	// Clearing only when status was 'error' was the original bug: enrichment
	// failures record a WARNING and never touch status, so their last_error was
	// permanent — it survived every successful poll and had no other clear path.
	_ = p.queries.ClearFeedError(ctx, feed.ID)
	// Only an errored feed returns to 'active'. A feed the user paused must not
	// be un-paused by a poll, and this is about the error, not the lifecycle.
	if feed.Status == "error" {
		_, _ = p.queries.UpdateFeedStatus(ctx, store.UpdateFeedStatusParams{
			ID:     feed.ID,
			Status: "active",
		})
	}

	if enricher, ok := p.enrichers[feed.Type]; ok {
		// Twitter gets an extra feed-scoped pass refreshing scores for existing
		// unread items. enrichForPoll already scored the freshly fetched
		// candidates via BulkEnrich; this covers older items the timeline still
		// lists. Skipped when the account's quota runs low.
		if tf, ok := enricher.(*TwitterFetcher); ok {
			p.enrichTwitterItems(ctx, feed, tf)
		}
	}

	if feed.Readability != 0 {
		// Readability is the alternative to extraction, not a companion: both write
		// items.extract_content and both run the same library. A readability feed
		// already receives the article in its body, so fetching item.url would
		// discard the good stored body and fetch a worse copy.
		p.applyReadabilityToItems(ctx, feed, logger)
	} else if feed.ExtractContent != 0 {
		p.extractFeedContent(ctx, feed, logger)
	}

	logger.Info("feed polled", "total_items", len(items), "new_items", newCount, "duration", time.Since(start))
	return nil
}

// enrichForPoll consolidates pre-insert scoring and post-insert enrichment
// into a single BulkEnrich call. It enriches both newly fetched candidates
// and existing unread items in one API round-trip, then returns scored
// candidates for min_score filtering and a hash set of enriched candidates.
func (p *Poller) enrichForPoll(ctx context.Context, feed store.Feed, candidates []fetchedCandidate, logger *slog.Logger) (map[string]*store.Item, map[string]bool) {
	enricher, ok := p.enrichers[feed.Type]
	if !ok {
		return nil, nil
	}
	if _, ok := enricher.(*NoopEnricher); ok {
		return nil, nil
	}

	// Build scored map for candidates.
	scored := make(map[string]*store.Item, len(candidates))
	for _, c := range candidates {
		si := &store.Item{
			Hash:          c.hash,
			Title:         c.item.Title,
			Url:           c.item.URL,
			Content:       c.item.Content,
			CommentUrl:    c.item.CommentURL,
			Author:        c.item.Author,
			Points:        int64(c.item.Points),
			CommentsCount: int64(c.item.CommentsCnt),
		}
		if !c.item.PublishedAt.IsZero() {
			si.PublishedAt = sql.NullTime{Time: c.item.PublishedAt, Valid: true}
		}
		scored[c.hash] = si
	}

	// Collect existing items needing refresh (not Twitter — feed-scoped).
	var existing []store.Item
	if _, isTwitter := enricher.(*TwitterFetcher); !isTwitter {
		window := p.enrichmentWindow
		if window <= 0 {
			window = 24 * time.Hour
		}
		cutoff := time.Now().Add(-window)
		recent := time.Now().Add(-1 * time.Hour)
		var err error
		existing, err = p.queries.ListUnreadItemsForEnrichment(ctx, store.ListUnreadItemsForEnrichmentParams{
			FeedID:         feed.ID,
			FetchedAt:      cutoff,
			LastEnrichedAt: sql.NullTime{Time: recent, Valid: true},
		})
		if err != nil {
			logger.Error("failed to list items for enrichment", "error", err)
		}
	}

	// Merge candidates + existing, dedup by hash.
	//
	// The existing row WINS a hash collision. A candidate is a placeholder built
	// from the feed payload: it has no row id, so putting it in the list means
	// enrichment updates the placeholder and every DB write that keys on the id
	// (UpdateItemPointsAndComments) matches nothing — the stored score silently
	// never refreshes, and the enriched score is thrown away. Re-polling a story
	// is the normal case for a score feed, so this was the common path, not an
	// edge: the fix is for the real row to take the slot and be enriched.
	haveHash := make(map[string]bool, len(candidates))
	for i := range existing {
		haveHash[existing[i].Hash] = true
	}
	allPtrs := make([]*store.Item, 0, len(candidates)+len(existing))
	for i := range existing {
		allPtrs = append(allPtrs, &existing[i])
	}
	for _, c := range candidates {
		if haveHash[c.hash] {
			continue
		}
		haveHash[c.hash] = true
		allPtrs = append(allPtrs, scored[c.hash])
	}

	if len(allPtrs) == 0 {
		return scored, nil
	}

	logger.Debug("enriching items", "candidates", len(candidates), "existing", len(existing), "total", len(allPtrs))

	// Single BulkEnrich for all items.
	origScores := make(map[string]int64, len(allPtrs))
	origContent := make(map[string]string, len(allPtrs))
	for _, si := range allPtrs {
		origScores[si.Hash] = si.Points
		origContent[si.Hash] = si.Content
	}
	enrichStart := time.Now()
	// ONE scope spans the bulk pass and the per-item fallback: the fallback issues
	// real requests too, so reading the totals before it would drop them (and,
	// with feeds polling concurrently, leave them to be claimed by another feed).
	enrichScope := metrics.NewScope()
	enrichCtx := metrics.WithScope(ctx, enrichScope)
	var enrichErr error
	if b, ok := enricher.(BulkEnricher); ok {
		enrichErr = b.BulkEnrich(enrichCtx, allPtrs)
		if enrichErr != nil {
			logger.Warn("bulk enrich failed", "error", enrichErr)
		}
		// Per-item fallback for unscored items.
		for _, si := range allPtrs {
			if si.Points > 0 {
				continue
			}
			if err := enricher.Enrich(enrichCtx, si); err != nil {
				logger.Debug("enrich fallback failed", "hash", si.Hash, "error", err)
			}
		}
	} else {
		for _, si := range allPtrs {
			if err := enricher.Enrich(enrichCtx, si); err != nil {
				logger.Debug("enrich failed", "hash", si.Hash, "error", err)
			}
		}
	}
	enrichBytes, enrichReqs := enrichScope.Totals()
	p.recordEnrich(enrichStart, feed, hostFromURL(feed.Url), enrichBytes, enrichReqs, enrichErr)

	// Track which candidates the enricher modified.
	enrichedHashes := make(map[string]bool, len(candidates))
	for _, c := range candidates {
		if si, ok := scored[c.hash]; ok {
			if si.Points != origScores[si.Hash] {
				enrichedHashes[c.hash] = true
			}
		}
	}

	// Update existing items in DB.
	updated, skipped, removed, errcount := 0, 0, 0, 0
	existingByID := make(map[int64]*store.Item, len(existing))
	for i := range existing {
		// Point to the snapshot (pre-enrichment copy) so the
		// comparison below detects actual score changes.
		existingByID[existing[i].ID] = &store.Item{
			ID:            existing[i].ID,
			Points:        origScores[existing[i].Hash],
			CommentsCount: existing[i].CommentsCount,
			CommentUrl:    existing[i].CommentUrl,
			Title:         existing[i].Title,
			Content:       origContent[existing[i].Hash],
		}
	}
	for _, si := range allPtrs {
		if ei, ok := existingByID[si.ID]; ok {
			contentChanged := si.Content != ei.Content
			if si.Points == ei.Points && si.CommentsCount == ei.CommentsCount && si.CommentUrl == ei.CommentUrl && !contentChanged {
				skipped++
				continue
			}
			if ei.Points == 0 && si.Points > 0 && feed.MinScore > 0 && si.Points < feed.MinScore {
				deleted, err := p.deleteDisposableItem(ctx, si.ID)
				if err != nil {
					errcount++
				} else if deleted {
					removed++
				}
				continue
			}
			if _, err := p.queries.UpdateItemPointsAndComments(ctx, store.UpdateItemPointsAndCommentsParams{
				ID:            si.ID,
				Points:        si.Points,
				CommentsCount: si.CommentsCount,
				CommentUrl:    si.CommentUrl,
			}); err != nil {
				errcount++
			} else {
				updated++
			}
			if contentChanged {
				if err := p.queries.UpdateItemContent(ctx, store.UpdateItemContentParams{
					ID:      si.ID,
					Content: si.Content,
				}); err != nil {
					errcount++
				}
			}
		}
	}
	if errcount > 0 {
		p.queries.UpdateFeedWarning(ctx, store.UpdateFeedWarningParams{
			ID:    feed.ID,
			Error: fmt.Sprintf("enrichment failed for %d items", errcount),
		})
	}
	logger.Info("enrichment completed", "type", feed.Type, "items", len(allPtrs), "updated", updated, "skipped", skipped, "removed", removed, "error", errcount)

	return scored, enrichedHashes
}

// refreshTwitterTitle updates an existing twitter item's title when the
// freshly fetched title differs (pinned badge added/removed, RT marker).
func (p *Poller) refreshTwitterTitle(ctx context.Context, feedID int64, guid, newTitle string) error {
	current, err := p.queries.GetItemTitleByHash(ctx, store.GetItemTitleByHashParams{
		FeedID: feedID,
		Hash:   itemHash(guid, "", "", ""), // hash is GUID-anchored for twitter items
	})
	if err != nil {
		return err // not stored yet or query failed — nothing to refresh
	}
	if current == newTitle {
		return nil
	}
	return p.queries.UpdateItemTitle(ctx, store.UpdateItemTitleParams{
		Title:  newTitle,
		FeedID: feedID,
		Hash:   itemHash(guid, "", "", ""),
	})
}

func (p *Poller) setItemExtractionPending(ctx context.Context, itemID int64, logger *slog.Logger) {
	_, err := p.queries.UpdateItemExtraction(ctx, store.UpdateItemExtractionParams{
		ID:             itemID,
		ExtractContent: "",
		ExtractStatus:  "pending",
		ExtractRetries: 0,
	})
	if err != nil {
		logger.Debug("failed to set extraction pending", "item_id", itemID, "error", err)
	}
}

// ReadabilityApplyFailedPrefix marks the extract_content text written when
// readability REJECTED its own output. Stored like an extraction error log — the
// consumers all gate on extract_status == "done", so the original body keeps being
// served everywhere — but worded for the guard's different failure mode.
const ReadabilityApplyFailedPrefix = "readability declined its output"

// applyReadabilityToItems runs readability over bodies the feed already stored and
// writes the clean output to items.extract_content.
//
// This is the "content we already have" path: nothing is fetched, because the input
// is items.content. The sibling of extractFeedContent rather than a branch inside it,
// because the preconditions differ — extraction needs a URL, this needs a body, and a
// feed that receives full articles in its body wants this one (see the call site).
//
// Outcomes, mirroring how extraction reports itself so every existing consumer behaves:
//
//	accept      extract_content = cleaned body    extract_status = "done"
//	no benefit  extract_content = ""              extract_status = "skipped"   (silent)
//	lossy       extract_content = the reason      extract_status = "failed"
//	error       extract_content = the error       extract_status = "failed"
//
// A rejected or failed result is never retried: the transform is deterministic over
// a fixed body, so a second attempt would reach the same conclusion forever.
func (p *Poller) applyReadabilityToItems(ctx context.Context, feed store.Feed, logger *slog.Logger) {
	items, err := p.queries.ListItemsNeedingReadability(ctx, feed.ID)
	if err != nil {
		logger.Debug("failed to list items for readability", "error", err)
		return
	}
	if len(items) == 0 {
		return
	}
	logger.Info("applying readability", "count", len(items))
	for _, item := range items {
		if strings.TrimSpace(item.Content) == "" {
			continue
		}
		res, verdict, rerr := ApplyReadabilityGuarded(item.Content, item.Url)
		switch {
		case rerr != nil:
			logger.Debug("readability failed", "item_id", item.ID, "error", rerr)
			p.setReadabilityOutcome(ctx, item.ID, rerr.Error(), "failed", logger)
		case verdict == ReadabilityLossy:
			// Content was lost, not just chrome: keep the original. Say so in the
			// same column an extraction failure uses its error log, so the UI can
			// show the reason without a new field.
			reason := fmt.Sprintf("%s: dropped %d%% of the text (kept %d of %d words)",
				ReadabilityApplyFailedPrefix,
				100-100*wordCount(res.Content)/max(wordCount(item.Content), 1),
				wordCount(res.Content), wordCount(item.Content))
			logger.Info("readability declined its output", "item_id", item.ID, "reason", reason)
			p.setReadabilityOutcome(ctx, item.ID, reason, "failed", logger)
		case verdict == ReadabilityNoBenefit:
			// Silent: the item should look untouched, not broken.
			p.setReadabilityOutcome(ctx, item.ID, "", "skipped", logger)
		default:
			p.setReadabilityOutcome(ctx, item.ID, res.Content, "done", logger)
		}
	}
}

// setReadabilityOutcome persists one readability decision.
//
// extract_retries is pinned to 1 on failure so it can never reach the extraction
// retry budget of 3: a readability rejection is deterministic, not a transient
// network error, and retrying it would burn the same body three times for the same
// answer. The status also keeps the item out of ListItemsNeedingReadability, which
// only takes never-processed items.
func (p *Poller) setReadabilityOutcome(ctx context.Context, itemID int64, content, status string, logger *slog.Logger) {
	retries := int64(0)
	if status == "failed" {
		retries = 1
	}
	if _, err := p.queries.UpdateItemExtraction(ctx, store.UpdateItemExtractionParams{
		ID:             itemID,
		ExtractContent: content,
		ExtractStatus:  status,
		ExtractRetries: retries,
	}); err != nil {
		logger.Debug("failed to store readability outcome", "item_id", itemID, "error", err)
	}
}

func (p *Poller) extractFeedContent(ctx context.Context, feed store.Feed, logger *slog.Logger) {
	items, err := p.queries.ListItemsNeedingExtraction(ctx, feed.ID)
	if err != nil {
		logger.Debug("failed to list items for extraction", "error", err)
		return
	}
	if len(items) == 0 {
		return
	}
	logger.Info("extracting content", "count", len(items))
	client := &http.Client{Timeout: 30 * time.Second, Transport: p.metricsTransport()}
	for _, item := range items {
		if item.Url == "" {
			continue
		}
		p.extractSingleItem(ctx, client, item, feed, logger)
	}
}

// ExtractSingleItem extracts content for one item and persists the result
// (or the failure, with retry accounting). Shared by the feed poll cycle and
// the per-item extract endpoint.
func (p *Poller) ExtractSingleItem(ctx context.Context, client *http.Client, item store.Item, feed store.Feed, logger *slog.Logger) {
	p.extractSingleItem(ctx, client, item, feed, logger)
}

func (p *Poller) extractSingleItem(ctx context.Context, client *http.Client, item store.Item, feed store.Feed, logger *slog.Logger) {
	logger = logger.With("item_id", item.ID, "url", item.Url)
	start := time.Now()

	// Reddit-hosted content (galleries, videos, images) is already fully
	// captured by the Reddit API fetcher — skip extraction.
	if isRedditHostedURL(item.Url) {
		logger.Debug("extraction skipped: reddit-hosted URL", "duration", time.Since(start))
		_, _ = p.queries.UpdateItemExtraction(ctx, store.UpdateItemExtractionParams{
			ID:             item.ID,
			ExtractContent: "",
			ExtractStatus:  "skipped",
			ExtractRetries: 0,
		})
		return
	}

	// HN-hosted content (Ask HN, Show HN, etc.) is already captured by
	// the RSS feed's description — skip extraction to avoid fetching the
	// full page with comments and site chrome.
	if isHNHostedURL(item.Url) {
		logger.Debug("extraction skipped: HN-hosted URL", "duration", time.Since(start))
		_, _ = p.queries.UpdateItemExtraction(ctx, store.UpdateItemExtractionParams{
			ID:             item.ID,
			ExtractContent: "",
			ExtractStatus:  "skipped",
			ExtractRetries: 0,
		})
		return
	}

	extractScope := metrics.NewScope()
	content, err := ExtractContent(metrics.WithScope(ctx, extractScope), item.Url, client, logger)
	extractBytes, extractReqs := extractScope.Totals()
	// Record extraction metrics.
	if p.recordFn != nil {
		errStr := ""
		statusCode := 200
		if err != nil {
			errStr = err.Error()
			statusCode = metrics.StatusFor(err)
		}
		// Derive host from item URL.
		extractHost := hostFromURL(item.Url)
		p.recordFn(Record{
			Timestamp:  start,
			URL:        item.Url,
			StatusCode: statusCode,
			Bytes:      extractBytes,
			Requests:   extractReqs,
			Host:       extractHost,
			DurationMs: time.Since(start).Milliseconds(),
			Reason:     "extraction",
			FeedID:     item.FeedID,
			FeedName:   feed.Name,
			FeedType:   feed.Type,
			Error:      errStr,
		})
	}
	if err != nil {
		logger.Debug("extraction failed", "error", err)
		p.failItemExtraction(item, fmt.Sprintf("[%s] %v", time.Now().Format("2006-01-02 15:04"), err))
		return
	}
	if content == "" {
		logger.Debug("extraction returned empty", "duration", time.Since(start))
		return
	}

	_, err = p.queries.UpdateItemExtraction(ctx, store.UpdateItemExtractionParams{
		ID:             item.ID,
		ExtractContent: content,
		ExtractStatus:  "done",
		ExtractRetries: 0,
	})
	if err != nil {
		logger.Debug("failed to save extracted content", "error", err)
		return
	}
	logger.Info("content extracted", "bytes", len(content), "duration", time.Since(start))
}

func (p *Poller) failItemExtraction(item store.Item, errorLine string) {
	newContent := strings.TrimSpace(item.ExtractContent)
	if newContent != "" {
		newContent += "\n"
	}
	newContent += errorLine

	_, err := p.queries.UpdateItemExtraction(context.Background(), store.UpdateItemExtractionParams{
		ID:             item.ID,
		ExtractContent: newContent,
		ExtractStatus:  "failed",
		ExtractRetries: item.ExtractRetries + 1,
	})
	if err != nil {
		slog.Debug("failed to update extraction failure", "item_id", item.ID, "error", err)
	}
}

var stripRe = regexp.MustCompile(`<[^>]*>`)

// itemHash fingerprints an item for duplicate detection. The hash is anchored
// on the most stable identity available: the feed's GUID when present (a title
// edit must not change the identity — HN stories, for example, routinely get
// their titles rewritten, and including the title here caused the same story
// to be inserted twice), then the URL, then the title + stripped content.
func itemHash(guid, title, url, content string) string {
	s := sha256.New()
	switch {
	case guid != "":
		s.Write([]byte(guid))
	case url != "":
		s.Write([]byte(url))
	default:
		plain := stripRe.ReplaceAllString(content, "")
		s.Write([]byte(title + plain))
	}
	return hex.EncodeToString(s.Sum(nil))
}

// enrichTwitterItems handles Twitter's feed-scoped enrichment: one timeline
// request updates all items still visible on the timeline.
func (p *Poller) enrichTwitterItems(ctx context.Context, feed store.Feed, tf *TwitterFetcher) {
	logger := slog.With("feed_id", feed.ID, "feed_name", feed.Name)

	// This pass only refreshes scores for items already stored, so losing it
	// costs staleness and nothing else — whereas losing Fetch loses posts.
	// When X's advertised budget runs low, spend what is left on ingestion.
	if tf.BudgetLow(twitterUserTweetsQuery, twitterQuotaReserve) {
		limit, remaining, reset := tf.Quota(twitterUserTweetsQuery)
		logger.Info("skipping twitter enrichment: rate-limit budget low",
			"limit", limit, "remaining", remaining, "reset", reset.UTC().Format(time.RFC3339))
		return
	}

	window := p.enrichmentWindow
	if window <= 0 {
		window = 24 * time.Hour
	}
	cutoff := time.Now().Add(-window)
	recent := time.Now().Add(-1 * time.Hour)

	items, err := p.queries.ListUnreadItemsForEnrichment(ctx, store.ListUnreadItemsForEnrichmentParams{
		FeedID:         feed.ID,
		FetchedAt:      cutoff,
		LastEnrichedAt: sql.NullTime{Time: recent, Valid: true},
	})
	if err != nil {
		logger.Error("failed to list items for enrichment", "error", err)
		return
	}

	if len(items) == 0 {
		return
	}

	logger.Info("starting enrichment", "type", feed.Type, "items", len(items))

	type itemSnapshot struct {
		Points        int64
		CommentsCount int64
		CommentUrl    string
	}
	snapshots := make([]itemSnapshot, len(items))
	for i := range items {
		snapshots[i] = itemSnapshot{items[i].Points, items[i].CommentsCount, items[i].CommentUrl}
	}

	ptrs := make([]*store.Item, len(items))
	for i := range items {
		ptrs[i] = &items[i]
	}
	enrichStart := time.Now()
	twScope := metrics.NewScope()
	twErr := tf.EnrichFeed(metrics.WithScope(ctx, twScope), feed.Url, ptrs)
	twBytes, twReqs := twScope.Totals()
	twHost := hostFromURL(feed.Url)
	p.recordEnrich(enrichStart, feed, twHost, twBytes, twReqs, twErr)
	if twErr != nil {
		logger.Debug("twitter enrich failed", "error", twErr)
	}

	// Update scores for Twitter items.
	success, skipped, errcount, removed := 0, 0, 0, 0
	for i := range items {
		if items[i].Points == snapshots[i].Points &&
			items[i].CommentsCount == snapshots[i].CommentsCount &&
			items[i].CommentUrl == snapshots[i].CommentUrl {
			skipped++
			continue
		}
		// A never-enriched item that now has a real score gets its
		// definitive min_score verdict: below the threshold it is removed
		// rather than kept with a failing score. (Previously-inserted
		// items that already had scores are only updated, never removed.)
		if snapshots[i].Points == 0 && items[i].Points > 0 &&
			feed.MinScore > 0 && items[i].Points < feed.MinScore {
			deleted, err := p.deleteDisposableItem(ctx, items[i].ID)
			if err != nil {
				logger.Warn("failed to delete below-threshold item", "item_id", items[i].ID, "error", err)
				errcount++
			} else if deleted {
				logger.Info("removed item below min_score after enrichment",
					"title", items[i].Title, "points", items[i].Points, "min_score", feed.MinScore)
				removed++
			}
			continue
		}
		if _, err := p.queries.UpdateItemPointsAndComments(ctx, store.UpdateItemPointsAndCommentsParams{
			ID:            items[i].ID,
			Points:        items[i].Points,
			CommentsCount: items[i].CommentsCount,
			CommentUrl:    items[i].CommentUrl,
		}); err != nil {
			logger.Debug("failed to update enriched item", "item_id", items[i].ID, "error", err)
			errcount++
		} else {
			success++
		}
	}
	// Record enrichment warnings — only writes when no fetch error exists.
	if errcount > 0 {
		p.queries.UpdateFeedWarning(ctx, store.UpdateFeedWarningParams{
			ID:    feed.ID,
			Error: fmt.Sprintf("enrichment failed for %d items", errcount),
		})
	}
	logger.Info("enrichment completed", "type", feed.Type, "items", len(items), "success", success, "skipped", skipped, "removed", removed, "error", errcount)
}
