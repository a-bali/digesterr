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

	"digesterr/internal/store"
)

type Poller struct {
	queries         *store.Queries
	fetchers        map[string]Fetcher
	enrichers       map[string]Enricher
	commentFetchers map[string]CommentFetcher
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
			"reddit":   rssFetcher,
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
	p.setItemExtractionPending(ctx, itemID, slog.With("item_id", itemID))
	client := &http.Client{Timeout: 30 * time.Second}
	p.extractSingleItem(ctx, client, item, slog.With("item_id", itemID))
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
	if err != nil {
		logger.Warn("failed to fetch feed", "error", err)
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

	// When a minimum score is configured, refresh each item's score before
	// insertion and skip items that fall below the threshold. Scores are
	// re-evaluated on every fetch: an item skipped on one poll may rise above
	// the threshold and be inserted on a later one.
	var scored map[string]*store.Item
	if feed.MinScore > 0 {
		scored = p.scoreFetchedItems(ctx, feed, candidates, logger)
	}

	for _, c := range candidates {
		item := c.item
		// enriched tracks whether a real score was obtained for this
		// candidate before insertion. Unenriched items are stored with
		// NULL points so the post-insert enrichment pass retries them.
		enriched := false
		if si, ok := scored[c.hash]; ok {
			// Apply the refreshed score (when available) at insert time.
			if si.Points.Valid || si.CommentsCount.Valid {
				enriched = true
			}
			item.Points = int(si.Points.Int64)
			item.CommentsCnt = int(si.CommentsCount.Int64)
			if si.CommentUrl != "" {
				item.CommentURL = si.CommentUrl
			}
			// Items without score data (NULL) are treated as zero: the
			// threshold applies uniformly and unknown-score items fail it.
			if si.Points.Int64 < feed.MinScore {
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
			Points:        sql.NullInt64{Int64: int64(item.Points), Valid: enriched},
			CommentsCount: sql.NullInt64{Int64: int64(item.CommentsCnt), Valid: enriched},
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

	if _, err := p.queries.UpdateFeedFetchTime(ctx, feed.ID); err != nil {
		logger.Error("failed to update feed fetch time", "error", err)
	}

	if feed.Status == "error" {
		_, _ = p.queries.UpdateFeedStatus(ctx, store.UpdateFeedStatusParams{
			ID:     feed.ID,
			Status: "active",
		})
	}

	if enricher, ok := p.enrichers[feed.Type]; ok {
		// Always run the post-insert enrichment pass for score-capable
		// feeds: scores and comment counts move over time, failed
		// enrichments must be retried, and items inserted with unknown
		// (NULL) scores need to catch up. The 1h last_enriched_at window
		// in ListUnreadItemsForEnrichment bounds the API traffic.
		p.enrichFeedItems(ctx, feed, enricher)
	}

	if feed.ExtractContent != 0 {
		p.extractFeedContent(ctx, feed, logger)
	}

	logger.Info("feed polled", "total_items", len(items), "new_items", newCount, "duration", time.Since(start))
	return nil
}

// scoreFetchedItems refreshes scores for freshly fetched items before they
// are inserted, so the min_score threshold can be applied at insert time.
// Items the enricher cannot score keep Points == 0 ("no score data") and are
// treated as passing the threshold. Returns a map keyed by item hash, or nil
// when scoring is not possible (unknown or non-score feed types).
func (p *Poller) scoreFetchedItems(ctx context.Context, feed store.Feed, candidates []fetchedCandidate, logger *slog.Logger) map[string]*store.Item {
	enricher, ok := p.enrichers[feed.Type]
	if !ok {
		return nil
	}
	if _, ok := enricher.(*NoopEnricher); ok {
		return nil
	}

	scored := make(map[string]*store.Item, len(candidates))
	ptrs := make([]*store.Item, 0, len(candidates))
	for _, c := range candidates {
		// HN scores come from enrichment (Firebase/Algolia), not RSS,
		// so temp items start as NULL (Valid=false) to trigger the
		// per-item fallback before the insert decision. Other feeds
		// (Lobsters, Reddit, Twitter) provide inline scores in the
		// parsed item; use them as-is.
		hasInlineScores := feed.Type != "hn"
		si := &store.Item{
			Hash:          c.hash,
			Title:         c.item.Title,
			Url:           c.item.URL,
			Content:       c.item.Content,
			CommentUrl:    c.item.CommentURL,
			Author:        c.item.Author,
			Points:        sql.NullInt64{Int64: int64(c.item.Points), Valid: hasInlineScores},
			CommentsCount: sql.NullInt64{Int64: int64(c.item.CommentsCnt), Valid: hasInlineScores},
		}
		if !c.item.PublishedAt.IsZero() {
			si.PublishedAt = sql.NullTime{Time: c.item.PublishedAt, Valid: true}
		}
		scored[c.hash] = si
		ptrs = append(ptrs, si)
	}

	logger.Debug("refreshing scores before insert", "count", len(ptrs), "min_score", feed.MinScore)
	if b, ok := enricher.(BulkEnricher); ok {
		if err := b.BulkEnrich(ctx, ptrs); err != nil {
			// NULL-scored candidates are treated as zero at the min_score
			// threshold, so a total scoring outage drops items instead of
			// admitting everything — make that visible.
			logger.Warn("pre-insert score refresh failed", "error", err)
		}
		// Per-item fallback for items the bulk call missed (e.g. Algolia
		// index stall while the Firebase API still serves fresh scores).
		// HN's BulkEnrich already falls back internally; this covers
		// enrichers whose bulk path has no built-in retry.
		for _, si := range ptrs {
			if si.Points.Valid {
				continue
			}
			if err := enricher.Enrich(ctx, si); err != nil {
				logger.Debug("pre-insert fallback failed for item", "title", si.Title, "error", err)
			}
		}
	} else {
		for _, si := range ptrs {
			if err := enricher.Enrich(ctx, si); err != nil {
				logger.Debug("pre-insert score refresh failed for item", "title", si.Title, "error", err)
			}
		}
	}
	return scored
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
	client := &http.Client{Timeout: 30 * time.Second}
	for _, item := range items {
		if item.Url == "" {
			continue
		}
		p.extractSingleItem(ctx, client, item, logger)
	}
}

// ExtractSingleItem extracts content for one item and persists the result
// (or the failure, with retry accounting). Shared by the feed poll cycle and
// the per-item extract endpoint.
func (p *Poller) ExtractSingleItem(ctx context.Context, client *http.Client, item store.Item, logger *slog.Logger) {
	p.extractSingleItem(ctx, client, item, logger)
}

func (p *Poller) extractSingleItem(ctx context.Context, client *http.Client, item store.Item, logger *slog.Logger) {
	logger = logger.With("item_id", item.ID, "url", item.Url)
	start := time.Now()

	content, err := ExtractContent(ctx, item.Url, client, logger)
	if err != nil {
		logger.Debug("extraction failed", "error", err)
		p.failItemExtraction(item, fmt.Sprintf("[%s] %v", time.Now().Format("2006-01-02 15:04"), err))
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

func (p *Poller) enrichFeedItems(ctx context.Context, feed store.Feed, enricher Enricher) {
	logger := slog.With("feed_id", feed.ID, "feed_name", feed.Name)
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
		Points        sql.NullInt64
		CommentsCount sql.NullInt64
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
	if b, ok := enricher.(*TwitterFetcher); ok {
		// Twitter enrichment is feed-scoped: one timeline request updates
		// all items still visible on the timeline.
		if err := b.EnrichFeed(ctx, feed.Url, ptrs); err != nil {
			logger.Debug("twitter enrich failed", "error", err)
		}
	} else if b, ok := enricher.(BulkEnricher); ok {
		if err := b.BulkEnrich(ctx, ptrs); err != nil {
			logger.Debug("bulk enrich failed", "error", err)
		}
		// Per-item fallback for items left unscored (bulk outage or
		// individual misses) so never-enriched items still get retried.
		for _, si := range ptrs {
			if si.Points.Valid {
				continue
			}
			if err := enricher.Enrich(ctx, si); err != nil {
				logger.Debug("post-insert fallback failed for item", "item_id", si.ID, "error", err)
			}
		}
	} else {
		for i := range items {
			if err := enricher.Enrich(ctx, &items[i]); err != nil {
				logger.Debug("failed to enrich item", "item_id", items[i].ID, "error", err)
			}
		}
	}

	success := 0
	skipped := 0
	errcount := 0
	removed := 0
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
		if !snapshots[i].Points.Valid && items[i].Points.Valid &&
			feed.MinScore > 0 && items[i].Points.Int64 < feed.MinScore {
			if _, err := p.queries.DeleteItem(ctx, items[i].ID); err != nil {
				logger.Warn("failed to delete below-threshold item", "item_id", items[i].ID, "error", err)
				errcount++
			} else {
				logger.Info("removed item below min_score after enrichment",
					"title", items[i].Title, "points", items[i].Points.Int64, "min_score", feed.MinScore)
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
	logger.Info("enrichment completed", "type", feed.Type, "items", len(items), "success", success, "skipped", skipped, "removed", removed, "error", errcount)
}
