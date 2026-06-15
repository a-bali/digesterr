package feed

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"digesterr/internal/store"
)

type Poller struct {
	queries   *store.Queries
	fetchers  map[string]Fetcher
	enrichers map[string]Enricher
}

func NewPoller(queries *store.Queries) *Poller {
	rssFetcher := NewRSSFetcher()
	return &Poller{
		queries: queries,
		fetchers: map[string]Fetcher{
			"rss":  rssFetcher,
			"atom": rssFetcher,
		},
		enrichers: map[string]Enricher{
			"hn": NewHNEnricher(),
		},
	}
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

	for _, feed := range feeds {
		fetcher, ok := p.fetchers[feed.Type]
		if !ok {
			slog.Warn("no fetcher for feed type", "type", feed.Type, "feed_id", feed.ID)
			continue
		}

		p.pollFeed(ctx, feed, fetcher)
	}
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
		pubAt := sql.NullTime{}
		if !item.PublishedAt.IsZero() {
			pubAt = sql.NullTime{Time: item.PublishedAt, Valid: true}
		}
		id, err := p.queries.CreateItemIfNotExists(ctx, store.CreateItemIfNotExistsParams{
			FeedID:        feed.ID,
			GUID:          item.GUID,
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
				// Item already exists (INSERT OR IGNORE dedup) — expected.
				logger.Debug("item already exists", "guid", item.GUID)
			} else {
				logger.Error("failed to create item", "guid", item.GUID, "error", err)
			}
			continue
		}
		if id > 0 {
			newCount++
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

	logger.Info("feed polled", "total_items", len(items), "new_items", newCount)
	return nil
}

func (p *Poller) enrichFeedItems(ctx context.Context, feed store.Feed, enricher Enricher) {
	logger := slog.With("feed_id", feed.ID)
	cutoff := time.Now().Add(-24 * time.Hour)
	recent := time.Now().Add(-1 * time.Hour)

	items, err := p.queries.ListUnreadItemsForEnrichment(ctx, store.ListUnreadItemsForEnrichmentParams{
		FeedID:             feed.ID,
		FetchedAfter:       cutoff,
		LastEnrichedBefore: recent,
	})
	if err != nil {
		logger.Error("failed to list items for enrichment", "error", err)
		return
	}

	if len(items) == 0 {
		return
	}

	for i := range items {
		item := items[i]
		if err := enricher.Enrich(ctx, &item); err != nil {
			logger.Debug("failed to enrich item", "item_id", item.ID, "error", err)
			continue
		}
		if _, err := p.queries.UpdateItemPointsAndComments(ctx, store.UpdateItemPointsAndCommentsParams{
			ID:            item.ID,
			Points:        item.Points,
			CommentsCount: item.CommentsCount,
		}); err != nil {
			logger.Debug("failed to update enriched item", "item_id", item.ID, "error", err)
		}
	}

	logger.Debug("enriched items", "count", len(items))
}
