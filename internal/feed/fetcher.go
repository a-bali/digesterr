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

type BulkEnricher interface {
	BulkEnrich(ctx context.Context, items []*store.Item) error
}

type Comment struct {
	ID          int64
	Author      string
	Text        string
	Points      int
	ValidPoints bool
	Depth       int
	URL         string
}

type CommentFetcher interface {
	FetchComments(ctx context.Context, item store.Item, maxComments int) ([]Comment, error)
}

type NoopEnricher struct{}

func (e *NoopEnricher) Enrich(_ context.Context, _ *store.Item) error  { return nil }
func (e *NoopEnricher) BulkEnrich(_ context.Context, _ []*store.Item) error { return nil }
func (e *NoopEnricher) FetchComments(_ context.Context, _ store.Item, _ int) ([]Comment, error) {
	return nil, nil
}
