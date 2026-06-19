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
}

func NewPoller(queries *store.Queries) *Poller {
	rssFetcher := NewRSSFetcher()
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
		},
		enrichers: map[string]Enricher{
			"rss":      noop,
			"atom":     noop,
			"hn":       hnEnricher,
			"lobsters": lobstersEnricher,
			"reddit":   redditEnricher,
		},
		commentFetchers: map[string]CommentFetcher{
			"rss":      noop,
			"atom":     noop,
			"hn":       hnEnricher,
			"lobsters": lobstersEnricher,
			"reddit":   redditEnricher,
		},
	}
}

func (p *Poller) Enrichers() map[string]Enricher {
	return p.enrichers
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

func (p *Poller) PollAll(ctx context.Context) {
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
	logger := slog.With("feed_id", feed.ID, "feed_url", feed.Url)
	logger.Debug("polling feed")

	items, err := fetcher.Fetch(ctx, feed.Url)
	if err != nil {
		logger.Error("failed to fetch feed", "error", err)
		if _, ue := p.queries.UpdateFeedStatus(ctx, store.UpdateFeedStatusParams{
			ID:     feed.ID,
			Status: "error",
		}); ue != nil {
			logger.Error("failed to update feed status", "error", ue)
		}
		return err
	}

	newCount := 0
	for _, item := range items {
		// Normalize GUID for feed types that need it.
		if feed.Type == "hn" {
			if hnID := extractHNID(item.CommentURL, item.URL); hnID != 0 {
				item.GUID = fmt.Sprintf("hn:%d", hnID)
			}
		}
		h := itemHash(item.GUID, item.Title, item.URL, item.Content)
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
		p.enrichFeedItems(ctx, feed, enricher)
	}

	if feed.ExtractContent != 0 {
		p.extractFeedContent(ctx, feed, logger)
	}

	logger.Info("feed polled", "total_items", len(items), "new_items", newCount)
	return nil
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

func (p *Poller) extractSingleItem(ctx context.Context, client *http.Client, item store.Item, logger *slog.Logger) {
	logger = logger.With("item_id", item.ID, "url", item.Url)

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
}

func (p *Poller) extractDone(ctx context.Context, item store.Item, content string) {
	_, err := p.queries.UpdateItemExtraction(ctx, store.UpdateItemExtractionParams{
		ID:             item.ID,
		ExtractContent: content,
		ExtractStatus:  "done",
		ExtractRetries: 0,
	})
	if err != nil {
		slog.Debug("failed to save extraction result", "item_id", item.ID, "error", err)
	}
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

func itemHash(guid, title, url, content string) string {
	s := sha256.New()
	switch {
	case guid != "":
		s.Write([]byte(guid + title))
	case url != "":
		s.Write([]byte(url))
	default:
		plain := stripRe.ReplaceAllString(content, "")
		s.Write([]byte(title + plain))
	}
	return hex.EncodeToString(s.Sum(nil))
}

func (p *Poller) enrichFeedItems(ctx context.Context, feed store.Feed, enricher Enricher) {
	logger := slog.With("feed_id", feed.ID)
	cutoff := time.Now().Add(-24 * time.Hour)
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
	if b, ok := enricher.(BulkEnricher); ok {
		if err := b.BulkEnrich(ctx, ptrs); err != nil {
			logger.Debug("bulk enrich failed", "error", err)
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
	for i := range items {
		if items[i].Points == snapshots[i].Points &&
			items[i].CommentsCount == snapshots[i].CommentsCount &&
			items[i].CommentUrl == snapshots[i].CommentUrl {
			skipped++
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
	logger.Info("enrichment completed", "type", feed.Type, "items", len(items), "success", success, "skipped", skipped, "error", errcount)
}

func ItemHash(guid, title, url, content string) string {
	return itemHash(guid, title, url, content)
}
