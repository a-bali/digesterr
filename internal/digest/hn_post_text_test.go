package digest

import (
	"context"
	"encoding/json"
	"strconv"
	"testing"

	"digesterr/internal/feed"
	"digesterr/internal/store"
)

// fakePostTextEnricher mimics the HN enricher for text posts: it fills in the
// post body that the RSS feed does not carry.
type fakePostTextEnricher struct {
	text map[int64]string // itemID -> post body
}

func (f *fakePostTextEnricher) Enrich(_ context.Context, item *store.Item) error {
	if body, ok := f.text[item.ID]; ok {
		item.Content = body
	}
	return nil
}

func (f *fakePostTextEnricher) BulkEnrich(ctx context.Context, items []*store.Item) error {
	for _, item := range items {
		if err := f.Enrich(ctx, item); err != nil {
			return err
		}
	}
	return nil
}

// TestGeneratePersistsHNPostTextFromEnricher covers digest generation for HN
// items whose body only exists in the HN API: a body supplied at generation
// time must be persisted on the item and rendered into the digest.
func TestGeneratePersistsHNPostTextFromEnricher(t *testing.T) {
	db, queries, cleanup := setupDigestTest(t)
	defer cleanup()
	ctx := context.Background()

	user, err := queries.CreateUser(ctx, store.CreateUserParams{
		Email:        "hn-post-text@test.com",
		PasswordHash: "hash",
		ApiKey:       "hn-post-text-key",
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

	// What news.ycombinator.com/rss stores: a comments link, no post body.
	const linkOnly = `<a href="https://news.ycombinator.com/item?id=777">Comments</a>`
	const postText = "<p>DSeek plans to officially release the V4.1 Flash model.</p>"

	item, err := queries.CreateItem(ctx, store.CreateItemParams{
		FeedID:        feedObj.ID,
		Hash:          "hn:777",
		Title:         "DeepSeek launching v4.1 flash",
		Url:           "https://news.ycombinator.com/item?id=777",
		Content:       linkOnly,
		CommentUrl:    "https://news.ycombinator.com/item?id=777",
		Points:        345,
		CommentsCount: 179,
		Status:        "unread",
	})
	if err != nil {
		t.Fatalf("create item: %v", err)
	}

	cfg, err := queries.CreateDigestConfig(ctx, store.CreateDigestConfigParams{
		UserID:   user.ID,
		Schedule: "0 8 * * *",
		Config:   `{"feeds":[{"id":` + strconv.FormatInt(feedObj.ID, 10) + `,"highlights":5,"comments":5}]}`,
	})
	if err != nil {
		t.Fatalf("create digest config: %v", err)
	}

	g := &Generator{
		db:      db,
		queries: queries,
		llm:     fakeLLM{resp: `{"title":"T","summary":"s","highlights":[{"title":"t","summary":"h"}]}`},
		enrichers: map[string]feed.Enricher{
			"hn": &fakePostTextEnricher{text: map[int64]string{item.ID: postText}},
		},
		commentFetchers: map[string]feed.CommentFetcher{},
	}

	result, err := g.Generate(ctx, cfg)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	// The body supplied during generation is persisted on the item.
	stored, err := queries.GetItem(ctx, item.ID)
	if err != nil {
		t.Fatalf("get item: %v", err)
	}
	if stored.Content != postText {
		t.Errorf("stored item content = %q, want %q", stored.Content, postText)
	}

	// And the digest renders that body rather than the comments link.
	var content digestResultContent
	if err := json.Unmarshal([]byte(result.Content), &content); err != nil {
		t.Fatalf("unmarshal content: %v", err)
	}
	if len(content.Highlights) != 1 {
		t.Fatalf("expected 1 highlight, got %d", len(content.Highlights))
	}
	if got := content.Highlights[0].ExtractedContent; got != postText {
		t.Errorf("digest highlight content = %q, want %q", got, postText)
	}
}
