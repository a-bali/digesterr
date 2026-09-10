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
}

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
		queries: queries,
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
// Exported for use by the digest generator after enrichment/comment-fetch
// operations.
func (p *Poller) ConsumeCounters() (respBytes int64, requests int) {
	if t, ok := p.sharedTransport.(*metrics.Transport); ok {
		b, r := t.Consume()
		return b, int(r)
	}
	return 0, 0
}

// ConsumeBytes reads and resets the shared transport byte counter.
func (p *Poller) ConsumeBytes() int64 {
	b, _ := p.ConsumeCounters()
	return b
}

// consumeCounters reads and resets the shared transport counters.
func (p *Poller) consumeCounters() (int64, int) {
	return p.ConsumeCounters()
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
// results are never deleted. Returns the number of rows deleted. Expiry is
// skipped for a window of zero.
func (p *Poller) ExpireOldData(ctx context.Context) (itemsDeleted, digestsDeleted int64, err error) {
	if p.expireItems > 0 {
		n, err := p.queries.DeleteExpiredItems(ctx, time.Now().Add(-p.expireItems))
		if err != nil {
			return itemsDeleted, digestsDeleted, fmt.Errorf("deleting expired items: %w", err)
		}
		itemsDeleted = n
	}
	if p.expireDigests > 0 {
		n, err := p.queries.DeleteExpiredDigestResults(ctx, time.Now().Add(-p.expireDigests))
		if err != nil {
			return itemsDeleted, digestsDeleted, fmt.Errorf("deleting expired digest results: %w", err)
		}
		digestsDeleted = n
	}
	// Expired refresh tokens are dead weight (rotation creates a new row per
	// refresh); sweep them on the same cadence. A fixed 30-day cutoff matches
	// the refresh token lifetime with margin.
	if n, err := p.queries.DeleteExpiredRefreshTokens(ctx, time.Now()); err != nil {
		slog.Warn("failed to delete expired refresh tokens", "error", err)
	} else if n > 0 {
		slog.Debug("deleted expired refresh tokens", "count", n)
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

func (p *Poller) PollAll(ctx context.Context) {
	p.expireOldData(ctx)

	feeds, err := p.queries.ListActiveFeeds(ctx)
	if err != nil {
		slog.Error("failed to list active feeds", "error", err)
		return
	}

	slog.Info("polling feeds", "count", len(feeds))
	start := time.Now()

	for _, feed := range feeds {
		fetcher, ok := p.fetchers[feed.Type]
		if !ok {
			slog.Warn("no fetcher for feed type", "type", feed.Type, "feed_id", feed.ID)
			continue
		}

		p.pollFeed(ctx, feed, fetcher)
	}

	slog.Info("polled feeds", "count", len(feeds), "duration", time.Since(start))
}

func (p *Poller) PollFeed(ctx context.Context, feed store.Feed) error {
	fetcher, ok := p.fetchers[feed.Type]
	if !ok {
		return fmt.Errorf("no fetcher for feed type: %s", feed.Type)
	}
	return p.pollFeed(ctx, feed, fetcher)
}

func (p *Poller) pollFeed(ctx context.Context, feed store.Feed, fetcher Fetcher) error {
	start := time.Now()
	logger := slog.With("feed_id", feed.ID, "feed_name", feed.Name, "feed_url", feed.Url)
	logger.Debug("polling feed")

	items, err := fetcher.Fetch(ctx, feed.Url)
	fetchDur := time.Since(start)
	fetchBytes, fetchReqs := p.consumeCounters()
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
		p.queries.UpdateFeedError(ctx, store.UpdateFeedErrorParams{
			ID:    feed.ID,
			Error: err.Error(),
		})
		if _, ue := p.queries.UpdateFeedStatus(ctx, store.UpdateFeedStatusParams{
			ID:     feed.ID,
			Status: "error",
		}); ue != nil {
			logger.Error("failed to update feed status", "error", ue)
		}
		return err
	}

	newCount := 0
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
				logger.Error("failed to create item", "hash", h, "error", err)
			}
			continue
		}
		if id > 0 {
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

	// Clean up newly-inserted items the enricher couldn't reach: when
	// min_score > 0 and a real enricher ran, items the enricher didn't
	// modify (not in enrichedHashes) with score=0 should be removed.
	if feed.MinScore > 0 && enrichedHashes != nil {
		for _, c := range candidates {
			if !enrichedHashes[c.hash] && c.item.Points == 0 {
				if _, err := p.queries.DB().ExecContext(ctx,
					`DELETE FROM items WHERE feed_id = ? AND hash = ?`,
					feed.ID, c.hash); err != nil {
					logger.Warn("failed to clean up unenriched zero-score item", "hash", c.hash, "error", err)
				}
			}
		}
	}

	if _, err := p.queries.UpdateFeedFetchTime(ctx, feed.ID); err != nil {
		logger.Error("failed to update feed fetch time", "error", err)
	}

	if feed.Status == "error" {
		_ = p.queries.ClearFeedError(ctx, feed.ID)
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

	if feed.ExtractContent != 0 {
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
	haveHash := make(map[string]bool, len(candidates))
	for _, c := range candidates {
		haveHash[c.hash] = true
	}
	allPtrs := make([]*store.Item, 0, len(candidates)+len(existing))
	for _, c := range candidates {
		allPtrs = append(allPtrs, scored[c.hash])
	}
	for i := range existing {
		if !haveHash[existing[i].Hash] {
			haveHash[existing[i].Hash] = true
			allPtrs = append(allPtrs, &existing[i])
		}
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
	if b, ok := enricher.(BulkEnricher); ok {
		bulkErr := b.BulkEnrich(ctx, allPtrs)
		enrichBytes, enrichReqs := p.consumeCounters()
		enrichHost := hostFromURL(feed.Url)
		p.recordEnrich(enrichStart, feed, enrichHost, enrichBytes, enrichReqs, bulkErr)
		if bulkErr != nil {
			logger.Warn("bulk enrich failed", "error", bulkErr)
		}
		// Per-item fallback for unscored items.
		for _, si := range allPtrs {
			if si.Points > 0 {
				continue
			}
			if err := enricher.Enrich(ctx, si); err != nil {
				logger.Debug("enrich fallback failed", "hash", si.Hash, "error", err)
			}
		}
	} else {
		for _, si := range allPtrs {
			if err := enricher.Enrich(ctx, si); err != nil {
				logger.Debug("enrich failed", "hash", si.Hash, "error", err)
			}
		}
	}

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
				if _, err := p.queries.DeleteItem(ctx, si.ID); err != nil {
					errcount++
				} else {
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

	content, err := ExtractContent(ctx, item.Url, client, logger)
	extractBytes, extractReqs := p.consumeCounters()
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
	if tf.BudgetLow(twitterQuotaReserve) {
		limit, remaining, reset := tf.Quota()
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
	twErr := tf.EnrichFeed(ctx, feed.Url, ptrs)
	twBytes, twReqs := p.consumeCounters()
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
			if _, err := p.queries.DeleteItem(ctx, items[i].ID); err != nil {
				logger.Warn("failed to delete below-threshold item", "item_id", items[i].ID, "error", err)
				errcount++
			} else {
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
