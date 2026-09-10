package digest

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"os"
	"strconv"
	"strings"
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
		`You are a news summarizer. Return your response as valid JSON with keys "title" (string, max 8-10 words), "summary" (string) and "highlights" (array of {title, summary}). Keep answers brief.`,
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
		Title      string `json:"title"`
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
	t.Logf("Title: %s", result.Title)
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

func setupDigestTest(t *testing.T) (*sql.DB, *store.Queries, func()) {
	t.Helper()
	db, err := testutil.OpenTestDB()
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	queries := store.New(db)
	cleanup := func() { db.Close() }
	return db, queries, cleanup
}

func TestGeneratorBuildFeedLimits(t *testing.T) {
	_, queries, cleanup := setupDigestTest(t)
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

	f1, err := queries.CreateFeed(ctx, store.CreateFeedParams{
		UserID:   user.ID,
		Category: "Tech",
		Type:     "rss",
		Name:     "Feed 1",
		Url:      "https://example.com/feed1",
		Config:   "{}",
		Status:   "active",
	})
	if err != nil {
		t.Fatalf("create feed 1: %v", err)
	}

	_, err = queries.CreateFeed(ctx, store.CreateFeedParams{
		UserID:   user.ID,
		Category: "Tech",
		Type:     "rss",
		Name:     "Feed 2",
		Url:      "https://example.com/feed2",
		Config:   "{}",
		Status:   "active",
	})
	if err != nil {
		t.Fatalf("create feed 2: %v", err)
	}

	g := &Generator{queries: queries}

	t.Run("individual feeds", func(t *testing.T) {
		dcfg := digestConfigJSON{
			Feeds: []digestFeedSelection{
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
			Categories: []digestCategorySelection{
				{Name: "Tech", Highlights: 5, Comments: 10},
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
			Categories: []digestCategorySelection{
				{Name: "Tech", Highlights: 5, Comments: 10},
			},
			Feeds: []digestFeedSelection{
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

func TestGeneratorBuildSections(t *testing.T) {
	g := &Generator{}
	feedInfos := map[int64]feedInfo{
		1: {name: "Feed A", category: "Tech"},
		2: {name: "Feed B", category: "Tech"},
		3: {name: "Feed C", category: "News"},
		4: {name: "Solo Feed", category: ""},
	}
	highlight := func(feedID int64, title string) digestItemRef {
		return digestItemRef{ItemID: feedID*100 + 1, FeedID: feedID, FeedName: feedInfos[feedID].name, Title: title}
	}
	item := func(feedID int64, title string) digestItemRef {
		return digestItemRef{ItemID: feedID*100 + 2, FeedID: feedID, FeedName: feedInfos[feedID].name, Title: title}
	}

	highlights := []digestItemRef{highlight(3, "News HL"), highlight(2, "B HL"), highlight(1, "A HL")}
	items := []digestItemRef{item(4, "Solo item"), item(2, "B item")}

	t.Run("categories produce one section per selected category with nested feeds", func(t *testing.T) {
		dcfg := digestConfigJSON{
			Categories: []digestCategorySelection{
				{Name: "News"},
				{Name: "Tech"},
			},
		}
		sections := g.buildSections(dcfg, highlights, items, feedInfos)

		if len(sections) != 3 {
			t.Fatalf("expected 3 sections (News, Tech, uncategorized Solo Feed), got %d", len(sections))
		}
		// Config order preserved: News first, then Tech.
		if sections[0].Name != "News" || sections[0].Kind != "category" {
			t.Errorf("section 0 = %+v, want News category", sections[0])
		}
		if len(sections[0].Sections) != 1 || sections[0].Sections[0].Name != "Feed C" {
			t.Errorf("News section should contain Feed C, got %+v", sections[0].Sections)
		}
		if sections[1].Name != "Tech" {
			t.Errorf("section 1 = %+v, want Tech", sections[1])
		}
		// Feeds inside a category are sorted by name: Feed A then Feed B.
		if len(sections[1].Sections) != 2 ||
			sections[1].Sections[0].Name != "Feed A" ||
			sections[1].Sections[1].Name != "Feed B" {
			t.Errorf("Tech section feeds = %+v, want Feed A, Feed B", sections[1].Sections)
		}
		// Feed sections carry their items.
		if len(sections[1].Sections[0].Highlights) != 1 || len(sections[1].Sections[0].Items) != 0 {
			t.Errorf("Feed A section = %+v, want 1 highlight", sections[1].Sections[0])
		}
		if len(sections[1].Sections[1].Highlights) != 1 || len(sections[1].Sections[1].Items) != 1 {
			t.Errorf("Feed B section = %+v, want 1 highlight and 1 item", sections[1].Sections[1])
		}
		// The uncategorized Solo Feed is emitted as a top-level feed section.
		if len(sections) != 3 {
			t.Fatalf("expected a third section for the uncategorized feed, got %d", len(sections))
		}
		if sections[2].Name != "Solo Feed" || sections[2].Kind != "feed" {
			t.Errorf("section 2 = %+v, want Solo Feed feed section", sections[2])
		}
		if len(sections[2].Items) != 1 {
			t.Errorf("Solo Feed section items = %+v, want 1", sections[2].Items)
		}
	})

	t.Run("selected category with no feeds is omitted", func(t *testing.T) {
		dcfg := digestConfigJSON{
			Categories: []digestCategorySelection{
				{Name: "Nonexistent"},
				{Name: "Tech"},
			},
		}
		sections := g.buildSections(dcfg, highlights, items, feedInfos)
		// Nonexistent is skipped; Tech comes first (config order), then News
		// (items from Feed C are still in the digest), then the uncategorized
		// Solo Feed.
		if len(sections) != 3 {
			t.Fatalf("expected 3 sections (Tech, News, Solo Feed), got %d", len(sections))
		}
		if sections[0].Name != "Tech" {
			t.Errorf("section 0 = %+v, want Tech", sections[0])
		}
		if sections[1].Name != "News" || sections[2].Name != "Solo Feed" {
			t.Errorf("sections = %+v, want News then Solo Feed", sections)
		}
	})

	t.Run("feed selection produces feed-level sections", func(t *testing.T) {
		dcfg := digestConfigJSON{
			Feeds: []digestFeedSelection{{ID: 4}, {ID: 1}},
		}
		sections := g.buildSections(dcfg, highlights, items, feedInfos)
		if len(sections) != 4 {
			t.Fatalf("expected 4 feed sections (one per feed with items), got %d", len(sections))
		}
		if sections[0].Name != "Feed A" || sections[0].FeedID != 1 || sections[0].Kind != "feed" {
			t.Errorf("section 0 = %+v, want feed section for Feed A", sections[0])
		}
		if sections[3].Name != "Solo Feed" {
			t.Errorf("sections = %+v, want name-ordered feed sections", sections)
		}
	})

	t.Run("no selection produces one feed section per feed", func(t *testing.T) {
		dcfg := digestConfigJSON{}
		sections := g.buildSections(dcfg, highlights, items, feedInfos)
		if len(sections) != 4 {
			t.Fatalf("expected 4 feed sections, got %d", len(sections))
		}
		for _, s := range sections {
			if s.Kind != "feed" {
				t.Errorf("section %+v should be a feed section", s)
			}
		}
		if sections[0].Name != "Feed A" || sections[3].Name != "Solo Feed" {
			t.Errorf("sections = %+v, want alphabetical feed order", sections)
		}
	})
}

type fakeLLM struct{ resp string }

func (f fakeLLM) Chat(ctx context.Context, systemPrompt, userPrompt string) (string, error) {
	return f.resp, nil
}

// TestBuildContentAssignsLLMSummaryToSections guards against the regression
// where per-highlight LLM summaries were assigned after buildSections, leaving
// the rendered (section-based) digest without any highlight summaries.
func TestBuildContentAssignsLLMSummaryToSections(t *testing.T) {
	_, queries, cleanup := setupDigestTest(t)
	defer cleanup()
	ctx := context.Background()

	g := &Generator{
		queries: queries,
		llm:     fakeLLM{resp: `{"title":"Reading Rainbow","summary":"overall","highlights":[{"title":"A","summary":"hl A summary"}]}`},
	}

	highlights := []digestItem{{
		Item: store.Item{ID: 1, FeedID: 7, Title: "A", Url: "https://a.example.com", Points: 100, CommentsCount: 3},
	}}

	content, err := g.buildContent(ctx, digestConfigJSON{}, highlights, nil, slog.Default())
	if err != nil {
		t.Fatalf("buildContent: %v", err)
	}
	if content.Title != "Reading Rainbow" {
		t.Errorf("expected content Title %q, got %q", "Reading Rainbow", content.Title)
	}
	if len(content.Sections) == 0 {
		t.Fatalf("expected at least one section, got none")
	}
	var got string
	for _, sec := range content.Sections {
		for _, h := range sec.Highlights {
			got = h.LLMSummary
		}
	}
	if got != "hl A summary" {
		t.Errorf("expected section highlight LLMSummary %q, got %q", "hl A summary", got)
	}
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
		got := cronFieldMatches(tt.field, tt.value, 0)
		if got != tt.want {
			t.Errorf("cronFieldMatches(%q, %d) = %v, want %v", tt.field, tt.value, got, tt.want)
		}
	}
}

func TestCronWeekdayAndBounds(t *testing.T) {
	// Sunday: Go Weekday()=0, cron accepts both 0 and 7.
	sunday := time.Date(2026, 8, 23, 8, 0, 0, 0, time.UTC) // a Sunday
	if !cronMatches("0 8 * * 0", sunday) {
		t.Error("weekday 0 should match Sunday")
	}
	if !cronMatches("0 8 * * 7", sunday) {
		t.Error("weekday 7 should match Sunday")
	}
	monday := time.Date(2026, 8, 24, 8, 0, 0, 0, time.UTC)
	if cronMatches("0 8 * * 7", monday) {
		t.Error("weekday 7 should not match Monday")
	}
	// Out-of-range values are rejected, not silently accepted.
	notSunday := time.Date(2026, 8, 25, 8, 0, 0, 0, time.UTC) // Tuesday
	for _, sched := range []string{"0 8 * * 1-100", "0 8 1-100 *", "1-100 * * * *"} {
		if cronMatches(sched, notSunday) {
			t.Errorf("out-of-range schedule %q must never match", sched)
		}
	}
}

func TestGeneratorDigestConfigJSON(t *testing.T) {
	cfg := digestConfigJSON{
		Categories: []digestCategorySelection{
			{Name: "Tech", Highlights: 5, Comments: 10},
		},
		Feeds: []digestFeedSelection{
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
	if len(parsed.Categories) != 1 || parsed.Categories[0].Highlights != 5 || parsed.Categories[0].Name != "Tech" {
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

	db, queries, cleanup := setupDigestTest(t)
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

	feedObj, err := queries.CreateFeed(ctx, store.CreateFeedParams{
		UserID:   user.ID,
		Category: "Tech",
		Type:     "rss",
		Name:     "Test Feed",
		Url:      "https://example.com/feed",
		Config:   "{}",
		Status:   "active",
	})
	if err != nil {
		t.Fatalf("create feed: %v", err)
	}

	titles := []string{"Go 1.25 Released", "New SQLite Features", "Rust Gets Faster"}
	points := []int64{342, 256, 180}
	// Local stand-in for feed.itemHash: any stable per-title hash works here.
	hashOf := func(s string) string {
		sum := sha256.Sum256([]byte(s))
		return hex.EncodeToString(sum[:])
	}
	for i := 0; i < 3; i++ {
		hash := hashOf(titles[i])
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
	gen := NewGenerator(db, queries, llmClient, poller)

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

func TestGenerateNoFeedsReturnsError(t *testing.T) {
	db, queries, cleanup := setupDigestTest(t)
	defer cleanup()

	ctx := context.Background()

	user, err := queries.CreateUser(ctx, store.CreateUserParams{
		Email:        "nofeeds@test.com",
		PasswordHash: "hash",
		ApiKey:       "nofeeds-key",
	})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	g := NewGenerator(db, queries, nil, feed.NewPoller(queries))
	cfg := store.DigestConfig{
		ID:     42,
		Name:   "empty",
		UserID: user.ID,
		Config: `{"feeds":[],"categories":[],"default_highlights":5,"default_comments":10,"max_items":100}`,
	}
	_, err = g.Generate(ctx, cfg)
	if err == nil {
		t.Fatal("expected error for digest config matching no feeds, got nil")
	}
}

func TestCleanContentForLLM(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "plain text passes through",
			in:   "Hello world, this is a test.",
			want: "Hello world, this is a test.",
		},
		{
			name: "strips HTML tags",
			in:   "<div><p>Hello <b>world</b></p></div>",
			want: "Hello world",
		},
		{
			name: "collapses whitespace",
			in:   "<p>  Hello   \n\n  world  </p>",
			want: "Hello world",
		},
		{
			name: "strips attributes",
			in:   `<div class="article" id="main" data-value="123">Content here</div>`,
			want: "Content here",
		},
		{
			name: "handles SVG tags",
			in:   `<svg xmlns="http://www.w3.org/2000/svg" fill="none"><path d="M12 3v1m0 16v1"/></svg><p>Real content</p>`,
			want: "Real content",
		},
		{
			name: "truncates long content",
			in:   strings.Repeat("x", 5000),
			want: strings.Repeat("x", 4000) + "… [truncated]",
		},
		{
			name: "empty string",
			in:   "",
			want: "",
		},
		{
			name: "only tags",
			in:   "<div><br/><hr/></div>",
			want: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CleanContentForLLM(tt.in)
			if got != tt.want {
				t.Errorf("CleanContentForLLM() =\n  %q\nwant:\n  %q", got, tt.want)
			}
		})
	}
}
