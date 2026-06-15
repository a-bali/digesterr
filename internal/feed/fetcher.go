package feed

import (
	"context"
	"time"

	"digesterr/internal/store"
)

type Item struct {
	GUID        string
	Title       string
	URL         string
	Content     string
	CommentURL  string
	Author      string
	PublishedAt time.Time
	Points      int
	CommentsCnt int
}

type Fetcher interface {
	Fetch(ctx context.Context, feedURL string) ([]Item, error)
}

type Enricher interface {
	Enrich(ctx context.Context, item *store.Item) error
}
