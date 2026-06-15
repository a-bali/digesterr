package feed

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"time"

	"digesterr/internal/store"
)

type Poller struct {
	queries  *store.Queries
	fetchers map[string]Fetcher
}

func NewPoller(queries *store.Queries) *Poller {
	rssFetcher := NewRSSFetcher()
	return &Poller{
		queries: queries,
		fetchers: map[string]Fetcher{
			"rss":  rssFetcher,
			"atom": rssFetcher,
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
			logger.Error("failed to create item", "guid", item.GUID, "error", err)
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

	logger.Info("feed polled", "total_items", len(items), "new_items", newCount)
	return nil
}
