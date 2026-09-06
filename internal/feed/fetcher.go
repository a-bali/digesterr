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

// Record is an outbound HTTP request metric recorded by the poller.
type Record struct {
	Timestamp  time.Time
	URL        string
	Host       string
	StatusCode int
	Bytes      int64
	DurationMs int64
	Reason     string // "fetch", "enrichment", "extraction"
	FeedID     int64
	FeedName   string
	FeedType   string
	Error      string
}

// RecordFunc is called for each outbound HTTP request during feed operations.
type RecordFunc func(Record)

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
	// CommentTargetURL returns the base API URL used for comment fetching.
	CommentTargetURL() string
}

type NoopEnricher struct{}

func (e *NoopEnricher) Enrich(_ context.Context, _ *store.Item) error       { return nil }
func (e *NoopEnricher) BulkEnrich(_ context.Context, _ []*store.Item) error { return nil }
func (e *NoopEnricher) FetchComments(_ context.Context, _ store.Item, _ int) ([]Comment, error) {
	return nil, nil
}
func (e *NoopEnricher) CommentTargetURL() string { return "" }
