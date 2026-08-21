package digest

import (
	"context"
	"encoding/json"
	"log/slog"
	"strconv"
	"testing"

	"digesterr/internal/feed"
	"digesterr/internal/store"
)

func TestValidateConfig(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		wantErr bool
	}{
		{"empty is fine", "", false},
		{"empty object is fine", "{}", false},
		{"valid min_score", `{"min_score": 50}`, false},
		{"zero min_score is fine", `{"min_score": 0}`, false},
		{"negative min_score rejected", `{"min_score": -1}`, true},
		{"negative min_score with other fields", `{"feeds":[{"id":1}],"min_score":-5}`, true},
		{"invalid json rejected", `{"min_score": `, true},
		{"non-object rejected", `[1,2]`, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateConfig(tt.raw)
			if tt.wantErr && err == nil {
				t.Fatalf("ValidateConfig(%q) = nil, want error", tt.raw)
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("ValidateConfig(%q) = %v, want nil", tt.raw, err)
			}
		})
	}
}

func TestFilterByMinScore(t *testing.T) {
	_, queries, cleanup := setupDigestTest(t)
	defer cleanup()
	ctx := context.Background()

	user, err := queries.CreateUser(ctx, store.CreateUserParams{
		Email:        "min-score@test.com",
		PasswordHash: "hash",
		ApiKey:       "min-score-key",
	})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	hnFeed, err := queries.CreateFeed(ctx, store.CreateFeedParams{
		UserID: user.ID, Category: "Tech", Type: "hn",
		Name: "HN", Url: "https://news.ycombinator.com/rss", Config: "{}", Status: "active",
	})
	if err != nil {
		t.Fatalf("create hn feed: %v", err)
	}
	rssFeed, err := queries.CreateFeed(ctx, store.CreateFeedParams{
		UserID: user.ID, Category: "Tech", Type: "rss",
		Name: "RSS", Url: "https://example.com/rss", Config: "{}", Status: "active",
	})
	if err != nil {
		t.Fatalf("create rss feed: %v", err)
	}

	// Real enrichers: HN/Lobsters/Reddit produce scores, RSS/Atom do not.
	g := &Generator{queries: queries, enrichers: feed.NewPoller(queries).Enrichers()}

	item := func(feedID int64, id int64, points int64) digestItem {
		return digestItem{Item: store.Item{ID: id, FeedID: feedID, Points: points}}
	}

	t.Run("min_score 0 keeps everything", func(t *testing.T) {
		items := []digestItem{item(hnFeed.ID, 1, 0), item(hnFeed.ID, 2, 100), item(rssFeed.ID, 3, 0)}
		got := g.filterByMinScore(ctx, items, 0, slog.Default())
		if len(got) != 3 {
			t.Fatalf("expected 3 items kept, got %d", len(got))
		}
	})

	t.Run("below threshold excluded, boundary kept", func(t *testing.T) {
		items := []digestItem{
			item(hnFeed.ID, 1, 10),
			item(hnFeed.ID, 2, 29),
			item(hnFeed.ID, 3, 30), // boundary: exactly min_score is kept
			item(hnFeed.ID, 4, 45),
		}
		got := g.filterByMinScore(ctx, items, 30, slog.Default())
		if len(got) != 2 {
			t.Fatalf("expected 2 items kept, got %d", len(got))
		}
		for _, it := range got {
			if it.Points < 30 {
				t.Errorf("item %d points %d kept but below threshold", it.ID, it.Points)
			}
		}
	})

	t.Run("items without score data always pass", func(t *testing.T) {
		// RSS items have no score data (points stay 0) and must pass.
		items := []digestItem{
			item(rssFeed.ID, 1, 0),
			item(rssFeed.ID, 2, 0),
			item(hnFeed.ID, 3, 10), // below threshold -> excluded
		}
		got := g.filterByMinScore(ctx, items, 50, slog.Default())
		if len(got) != 2 {
			t.Fatalf("expected 2 items (both RSS) kept, got %d", len(got))
		}
		for _, it := range got {
			if it.FeedID == hnFeed.ID {
				t.Errorf("HN item %d should have been excluded", it.ID)
			}
		}
	})

	t.Run("unknown feed treated as no score data", func(t *testing.T) {
		items := []digestItem{item(99999, 1, 0)}
		got := g.filterByMinScore(ctx, items, 10, slog.Default())
		if len(got) != 1 {
			t.Fatalf("expected 1 item kept, got %d", len(got))
		}
	})
}

// fakeScoreEnricher refreshes item points at generation time (like the real
// HN/Lobsters/Reddit enrichers do) so tests can verify that the min-score
// filter runs against generation-time scores, not stale stored ones.
type fakeScoreEnricher struct {
	scores map[int64]int64 // itemID -> refreshed points
}

func (f *fakeScoreEnricher) Enrich(_ context.Context, item *store.Item) error {
	if p, ok := f.scores[item.ID]; ok {
		item.Points = p
	}
	return nil
}

func (f *fakeScoreEnricher) BulkEnrich(_ context.Context, items []*store.Item) error {
	for _, item := range items {
		if p, ok := f.scores[item.ID]; ok {
			item.Points = p
		}
	}
	return nil
}

// TestGenerateMinScoreRefreshesScoresThenFilters exercises the full generation
// pipeline: items start below the threshold in the DB, the enricher refreshes
// their scores at generation time, and the digest only includes items whose
// fresh score is at or above min_score. The excluded items keep their
// refreshed scores persisted but stay unread.
func TestGenerateMinScoreRefreshesScoresThenFilters(t *testing.T) {
	db, queries, cleanup := setupDigestTest(t)
	defer cleanup()
	ctx := context.Background()

	user, err := queries.CreateUser(ctx, store.CreateUserParams{
		Email:        "min-score-gen@test.com",
		PasswordHash: "hash",
		ApiKey:       "min-score-gen-key",
	})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	feedObj, err := queries.CreateFeed(ctx, store.CreateFeedParams{
		UserID: user.ID, Category: "Tech", Type: "hn",
		Name: "HN", Url: "https://news.ycombinator.com/rss", Config: "{}", Status: "active",
	})
	if err != nil {
		t.Fatalf("create feed: %v", err)
	}

	// Initial stored points are all below the threshold; the enricher will
	// refresh them at generation time.
	titles := []string{"A", "B", "C", "D"}
	initial := []int64{1, 2, 3, 4}
	fresh := []int64{10, 25, 30, 60} // refreshed at generation time
	var itemIDs []int64
	for i := 0; i < 4; i++ {
		it, err := queries.CreateItem(ctx, store.CreateItemParams{
			FeedID:  feedObj.ID,
			Hash:    "min-score-" + titles[i],
			Title:   titles[i],
			Url:     "https://example.com/" + titles[i],
			Points:  initial[i],
			Status:  "unread",
		})
		if err != nil {
			t.Fatalf("create item %d: %v", i, err)
		}
		itemIDs = append(itemIDs, it.ID)
	}

	scores := make(map[int64]int64, len(itemIDs))
	for i, id := range itemIDs {
		scores[id] = fresh[i]
	}

	cfg, err := queries.CreateDigestConfig(ctx, store.CreateDigestConfigParams{
		UserID:   user.ID,
		Schedule: "0 8 * * *",
		Config: `{"feeds":[{"id":` + strconv.FormatInt(feedObj.ID, 10) + `,"highlights":5,"comments":5}],"min_score":30}`,
	})
	if err != nil {
		t.Fatalf("create digest config: %v", err)
	}

	g := &Generator{
		db:      db,
		queries: queries,
		llm: fakeLLM{resp: `{"title":"Filtered","summary":"s","highlights":[]}`},
		enrichers: map[string]feed.Enricher{
			"hn": &fakeScoreEnricher{scores: scores},
		},
		commentFetchers: map[string]feed.CommentFetcher{},
	}

	result, err := g.Generate(ctx, cfg)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	resultItems, err := queries.ListDigestResultItems(ctx, result.ID)
	if err != nil {
		t.Fatalf("list result items: %v", err)
	}
	// Items C (30, boundary) and D (60) pass; A (10) and B (25) are excluded.
	if len(resultItems) != 2 {
		t.Fatalf("expected 2 result items, got %d", len(resultItems))
	}

	inResult := make(map[int64]bool)
	for _, ri := range resultItems {
		inResult[ri.ItemID] = true
	}
	if !inResult[itemIDs[2]] || !inResult[itemIDs[3]] {
		t.Errorf("expected items %d and %d in digest, got %v", itemIDs[2], itemIDs[3], inResult)
	}
	if inResult[itemIDs[0]] || inResult[itemIDs[1]] {
		t.Errorf("expected items %d and %d excluded, got %v", itemIDs[0], itemIDs[1], inResult)
	}

	// Score refresh happened at generation time and was persisted for ALL
	// items, including the excluded ones.
	for i, id := range itemIDs {
		it, err := queries.GetItem(ctx, id)
		if err != nil {
			t.Fatalf("get item %d: %v", id, err)
		}
		if it.Points != fresh[i] {
			t.Errorf("item %d points = %d, want refreshed %d", id, it.Points, fresh[i])
		}
		wantStatus := "unread"
		if inResult[id] {
			wantStatus = "digested"
		}
		if it.Status != wantStatus {
			t.Errorf("item %d status = %q, want %q", id, it.Status, wantStatus)
		}
	}

	var content digestResultContent
	if err := json.Unmarshal([]byte(result.Content), &content); err != nil {
		t.Fatalf("unmarshal content: %v", err)
	}
	if len(content.Highlights)+len(content.Items) != 2 {
		t.Errorf("expected 2 items in content, got %d highlights + %d items", len(content.Highlights), len(content.Items))
	}
}
