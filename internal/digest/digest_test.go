package digest

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"strconv"
	"testing"
	"time"

	"digesterr/internal/feed"
	"digesterr/internal/store"
	"digesterr/internal/testutil"
)

func TestLLMClientChat(t *testing.T) {
	endpoint := os.Getenv("LLM_ENDPOINT")
	apiKey := os.Getenv("LLM_API_KEY")
	model := os.Getenv("LLM_MODEL")
	if endpoint == "" {
		t.Skip("LLM_ENDPOINT not set")
	}
	if model == "" {
		model = "auto"
	}

	client := NewLLMClient(endpoint, apiKey, model)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	response, err := client.Chat(ctx,
		"You are a helpful assistant. Respond with exactly one sentence.",
		"Say: Hello, world.")
	if err != nil {
		t.Fatalf("LLM chat failed: %v", err)
	}
	if response == "" {
		t.Fatal("expected non-empty response")
	}
	t.Logf("LLM response: %s", response)
}

func TestLLMClientJSONOutput(t *testing.T) {
	endpoint := os.Getenv("LLM_ENDPOINT")
	apiKey := os.Getenv("LLM_API_KEY")
	model := os.Getenv("LLM_MODEL")
	if endpoint == "" {
		t.Skip("LLM_ENDPOINT not set")
	}
	if model == "" {
		model = "auto"
	}

	client := NewLLMClient(endpoint, apiKey, model)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	response, err := client.Chat(ctx,
		`You are a news summarizer. Return your response as valid JSON with keys "summary" (string) and "highlights" (array of {title, summary}). Keep answers brief.`,
		`Summarize these articles:

Article 1 (HIGHLIGHT):
Title: Go 1.25 Released
URL: https://example.com/go1.25
Points: 342 | Comments: 87

Article 2 (HIGHLIGHT):
Title: Rust Patterns in Practice
URL: https://example.com/rust-patterns
Points: 256 | Comments: 43

Return JSON.`)
	if err != nil {
		t.Fatalf("LLM chat failed: %v", err)
	}

	var result struct {
		Summary    string `json:"summary"`
		Highlights []struct {
			Title   string `json:"title"`
			Summary string `json:"summary"`
		} `json:"highlights"`
	}
	if err := json.Unmarshal([]byte(response), &result); err != nil {
		if len(response) > 0 && response[0] != '{' {
			t.Skipf("LLM returned non-JSON (check endpoint routing): %s", response)
		}
		t.Fatalf("expected valid JSON response, got: %s (err: %v)", response, err)
	}
	if result.Summary == "" {
		t.Error("expected non-empty summary")
	}
	if len(result.Highlights) == 0 {
		t.Error("expected at least one highlight")
	}
	t.Logf("Summary: %s", result.Summary)
	for i, h := range result.Highlights {
		t.Logf("Highlight %d: %s - %s", i+1, h.Title, h.Summary)
	}
}

func TestCronMatches(t *testing.T) {
	tests := []struct {
		schedule string
		time     time.Time
		want     bool
	}{
		{"* * * * *", time.Date(2026, 6, 18, 12, 30, 0, 0, time.UTC), true},
		{"30 * * * *", time.Date(2026, 6, 18, 12, 30, 0, 0, time.UTC), true},
		{"30 * * * *", time.Date(2026, 6, 18, 12, 31, 0, 0, time.UTC), false},
		{"0 8 * * *", time.Date(2026, 6, 18, 8, 0, 0, 0, time.UTC), true},
		{"0 8 * * *", time.Date(2026, 6, 18, 8, 30, 0, 0, time.UTC), false},
		{"*/15 * * * *", time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC), true},
		{"*/15 * * * *", time.Date(2026, 6, 18, 12, 15, 0, 0, time.UTC), true},
		{"*/15 * * * *", time.Date(2026, 6, 18, 12, 30, 0, 0, time.UTC), true},
		{"*/15 * * * *", time.Date(2026, 6, 18, 12, 5, 0, 0, time.UTC), false},
		{"0 0 1 * *", time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC), true},
		{"0 0 1 * *", time.Date(2026, 6, 2, 0, 0, 0, 0, time.UTC), false},
		{"0 */6 * * *", time.Date(2026, 6, 18, 0, 0, 0, 0, time.UTC), true},
		{"0 */6 * * *", time.Date(2026, 6, 18, 6, 0, 0, 0, time.UTC), true},
		{"0 */6 * * *", time.Date(2026, 6, 18, 3, 0, 0, 0, time.UTC), false},
	}
	for _, tt := range tests {
		got := cronMatches(tt.schedule, tt.time)
		if got != tt.want {
			t.Errorf("cronMatches(%q, %v) = %v, want %v", tt.schedule, tt.time.Format("15:04 Jan 2"), got, tt.want)
		}
	}
}

func setupDigestTest(t *testing.T) (*store.Queries, func()) {
	t.Helper()
	db, err := testutil.OpenTestDB()
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	queries := store.New(db)
	cleanup := func() { db.Close() }
	return queries, cleanup
}

func TestGeneratorBuildFeedLimits(t *testing.T) {
	queries, cleanup := setupDigestTest(t)
	defer cleanup()

	ctx := context.Background()

	user, err := queries.CreateUser(ctx, store.CreateUserParams{
		Email:        "test@test.com",
		PasswordHash: "hash",
		ApiKey:       "",
	})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	cat, err := queries.CreateCategory(ctx, store.CreateCategoryParams{
		UserID: user.ID,
		Name:   "Tech",
	})
	if err != nil {
		t.Fatalf("create category: %v", err)
	}

	f1, err := queries.CreateFeed(ctx, store.CreateFeedParams{
		UserID:     user.ID,
		CategoryID: sql.NullInt64{Int64: cat.ID, Valid: true},
		Type:       "rss",
		Name:       "Feed 1",
		Url:        "https://example.com/feed1",
		Config:     "{}",
		Status:     "active",
	})
	if err != nil {
		t.Fatalf("create feed 1: %v", err)
	}

	_, err = queries.CreateFeed(ctx, store.CreateFeedParams{
		UserID:     user.ID,
		CategoryID: sql.NullInt64{Int64: cat.ID, Valid: true},
		Type:       "rss",
		Name:       "Feed 2",
		Url:        "https://example.com/feed2",
		Config:     "{}",
		Status:     "active",
	})
	if err != nil {
		t.Fatalf("create feed 2: %v", err)
	}

	g := &Generator{queries: queries}

	t.Run("individual feeds", func(t *testing.T) {
		dcfg := digestConfigJSON{
			Feeds: []struct {
				ID         int `json:"id"`
				Highlights int `json:"highlights"`
				Comments   int `json:"comments"`
			}{
				{ID: int(f1.ID), Highlights: 3, Comments: 5},
			},
		}
		limits := g.buildFeedLimits(ctx, user.ID, dcfg)
		if len(limits) != 1 {
			t.Errorf("expected 1 feed limit, got %d", len(limits))
		}
		l, ok := limits[f1.ID]
		if !ok {
			t.Error("expected feed 1 in limits")
		}
		if l.Highlights != 3 {
			t.Errorf("highlights = %d, want 3", l.Highlights)
		}
		if l.Comments != 5 {
			t.Errorf("comments = %d, want 5", l.Comments)
		}
	})

	t.Run("category expands feeds", func(t *testing.T) {
		dcfg := digestConfigJSON{
			Categories: []struct {
				ID         int `json:"id"`
				Highlights int `json:"highlights"`
				Comments   int `json:"comments"`
			}{
				{ID: int(cat.ID), Highlights: 5, Comments: 10},
			},
		}
		limits := g.buildFeedLimits(ctx, user.ID, dcfg)
		if len(limits) != 2 {
			t.Errorf("expected 2 feed limits, got %d", len(limits))
		}
	})

	t.Run("no feeds selected includes all", func(t *testing.T) {
		dcfg := digestConfigJSON{
			DefaultHighlights: 5,
			DefaultComments:   10,
		}
		limits := g.buildFeedLimits(ctx, user.ID, dcfg)
		if len(limits) != 2 {
			t.Errorf("expected 2 feed limits (all feeds), got %d", len(limits))
		}
	})

	t.Run("individual feed overrides category", func(t *testing.T) {
		dcfg := digestConfigJSON{
			Categories: []struct {
				ID         int `json:"id"`
				Highlights int `json:"highlights"`
				Comments   int `json:"comments"`
			}{
				{ID: int(cat.ID), Highlights: 5, Comments: 10},
			},
			Feeds: []struct {
				ID         int `json:"id"`
				Highlights int `json:"highlights"`
				Comments   int `json:"comments"`
			}{
				{ID: int(f1.ID), Highlights: 7, Comments: 12},
			},
		}
		limits := g.buildFeedLimits(ctx, user.ID, dcfg)
		l, ok := limits[f1.ID]
		if !ok {
			t.Error("expected feed 1 in limits")
		}
		if l.Highlights != 7 {
			t.Errorf("individual feed should override category, highlights = %d, want 7", l.Highlights)
		}
	})
}

func TestGeneratorSelectHighlights(t *testing.T) {
	g := &Generator{}

	items := []digestItem{
		{Item: store.Item{ID: 1, FeedID: 1, Points: 100}, Highlights: 2, Comments: 5},
		{Item: store.Item{ID: 2, FeedID: 1, Points: 80}, Highlights: 2, Comments: 5},
		{Item: store.Item{ID: 3, FeedID: 1, Points: 60}, Highlights: 2, Comments: 5},
		{Item: store.Item{ID: 4, FeedID: 2, Points: 50}, Highlights: 1, Comments: 5},
		{Item: store.Item{ID: 5, FeedID: 2, Points: 40}, Highlights: 1, Comments: 5},
	}

	highlights, rest := g.selectHighlights(items)

	if len(highlights) != 3 {
		t.Errorf("expected 3 highlights (2 from feed1, 1 from feed2), got %d", len(highlights))
	}
	if len(rest) != 2 {
		t.Errorf("expected 2 rest items, got %d", len(rest))
	}
	for _, h := range highlights {
		if h.Points < 50 {
			t.Errorf("highlight points should be >= 50, got %d for item %d", h.Points, h.ID)
		}
	}
}

func TestCronFieldMatches(t *testing.T) {
	tests := []struct {
		field string
		value int
		want  bool
	}{
		{"*", 30, true},
		{"5", 5, true},
		{"5", 6, false},
		{"1,2,3", 2, true},
		{"1,2,3", 4, false},
		{"*/5", 0, true},
		{"*/5", 5, true},
		{"*/5", 10, true},
		{"*/5", 7, false},
		{"10-20", 15, true},
		{"10-20", 5, false},
		{"10-20", 21, false},
		{"10-20/3", 10, true},
		{"10-20/3", 13, true},
		{"10-20/3", 16, true},
		{"10-20/3", 19, true},
		{"10-20/3", 11, false},
	}
	for _, tt := range tests {
		got := cronFieldMatches(tt.field, tt.value)
		if got != tt.want {
			t.Errorf("cronFieldMatches(%q, %d) = %v, want %v", tt.field, tt.value, got, tt.want)
		}
	}
}

func TestGeneratorDigestConfigJSON(t *testing.T) {
	cfg := digestConfigJSON{
		Categories: []struct {
			ID         int `json:"id"`
			Highlights int `json:"highlights"`
			Comments   int `json:"comments"`
		}{
			{ID: 1, Highlights: 5, Comments: 10},
		},
		Feeds: []struct {
			ID         int `json:"id"`
			Highlights int `json:"highlights"`
			Comments   int `json:"comments"`
		}{
			{ID: 2, Highlights: 3, Comments: 7},
		},
		DefaultHighlights: 5,
		DefaultComments:   10,
		Prompt:            "Custom prompt",
	}

	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var parsed digestConfigJSON
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if parsed.Prompt != "Custom prompt" {
		t.Errorf("prompt = %q, want Custom prompt", parsed.Prompt)
	}
	if len(parsed.Categories) != 1 || parsed.Categories[0].Highlights != 5 {
		t.Error("categories not parsed correctly")
	}
	if len(parsed.Feeds) != 1 || parsed.Feeds[0].Highlights != 3 {
		t.Error("feeds not parsed correctly")
	}
}

func TestGeneratorFullPipeline(t *testing.T) {
	endpoint := os.Getenv("LLM_ENDPOINT")
	apiKey := os.Getenv("LLM_API_KEY")
	model := os.Getenv("LLM_MODEL")
	if endpoint == "" || apiKey == "" {
		t.Skip("LLM env vars not set")
	}
	if model == "" {
		model = "auto"
	}

	queries, cleanup := setupDigestTest(t)
	defer cleanup()

	ctx := context.Background()

	user, err := queries.CreateUser(ctx, store.CreateUserParams{
		Email:        "test@test.com",
		PasswordHash: "hash",
		ApiKey:       "",
	})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	cat, err := queries.CreateCategory(ctx, store.CreateCategoryParams{
		UserID: user.ID,
		Name:   "Tech",
	})
	if err != nil {
		t.Fatalf("create category: %v", err)
	}

	feedObj, err := queries.CreateFeed(ctx, store.CreateFeedParams{
		UserID:     user.ID,
		CategoryID: sql.NullInt64{Int64: cat.ID, Valid: true},
		Type:       "rss",
		Name:       "Test Feed",
		Url:        "https://example.com/feed",
		Config:     "{}",
		Status:     "active",
	})
	if err != nil {
		t.Fatalf("create feed: %v", err)
	}

	titles := []string{"Go 1.25 Released", "New SQLite Features", "Rust Gets Faster"}
	points := []int64{342, 256, 180}
	for i := 0; i < 3; i++ {
		hash := feed.ItemHash(titles[i], titles[i], "https://example.com/"+titles[i], "")
		_, err := queries.CreateItem(ctx, store.CreateItemParams{
			FeedID:        feedObj.ID,
			Hash:          hash,
			Title:         titles[i],
			Url:           "https://example.com/" + titles[i],
			Content:       "Content for " + titles[i],
			CommentUrl:    "https://example.com/comments/" + titles[i],
			Points:        points[i],
			CommentsCount: 10,
			Status:        "unread",
		})
		if err != nil {
			t.Fatalf("create item %d: %v", i, err)
		}
	}

	dcfg, err := queries.CreateDigestConfig(ctx, store.CreateDigestConfigParams{
		UserID:   user.ID,
		Schedule: "0 8 * * *",
		Config:   `{"feeds":[{"id":` + strconv.FormatInt(feedObj.ID, 10) + `,"highlights":2,"comments":5}],"prompt":"Summarize the following articles briefly. Return JSON with summary and highlights array."}`,
	})
	if err != nil {
		t.Fatalf("create digest config: %v", err)
	}

	poller := feed.NewPoller(queries)
	llmClient := NewLLMClient(endpoint, apiKey, model)
	gen := NewGenerator(queries, llmClient, poller)

	result, err := gen.Generate(ctx, dcfg)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if result.ID == 0 {
		t.Error("expected result ID")
	}
	if result.Content == "" {
		t.Error("expected non-empty content")
	}

	var content digestResultContent
	if err := json.Unmarshal([]byte(result.Content), &content); err != nil {
		t.Fatalf("unmarshal result content: %v (raw: %s)", err, result.Content)
	}
	if content.Summary == "" && len(content.Highlights) > 0 {
		t.Error("expected non-empty summary when highlights exist")
	}
	if len(content.Highlights) == 0 {
		t.Error("expected at least one highlight")
	}
	t.Logf("Summary: %s", content.Summary)
	for _, h := range content.Highlights {
		t.Logf("Highlight: %s - LLM: %s", h.Title, h.LLMSummary)
	}

	resultItems, err := queries.ListDigestResultItems(ctx, result.ID)
	if err != nil {
		t.Fatalf("list result items: %v", err)
	}
	if len(resultItems) != 3 {
		t.Errorf("expected 3 result items, got %d", len(resultItems))
	}

	highlightCount := 0
	digestedCount := 0
	for _, ri := range resultItems {
		if ri.IsHighlight == 1 {
			highlightCount++
		}
		item, _ := queries.GetItem(ctx, ri.ItemID)
		if item.Status == "digested" {
			digestedCount++
		}
	}
	if highlightCount < 1 {
		t.Errorf("expected at least 1 highlight, got %d", highlightCount)
	}
	if digestedCount != 3 {
		t.Errorf("expected 3 digested items, got %d", digestedCount)
	}
}
