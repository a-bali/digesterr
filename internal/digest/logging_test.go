package digest

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"digesterr/internal/feed"
	"digesterr/internal/store"
	"digesterr/internal/testutil"
)

// llmTestServer returns an httptest server that answers OpenAI-style
// /chat/completions calls with the given content JSON.
func llmTestServer(t *testing.T, content string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"choices": []map[string]interface{}{
				{"message": map[string]interface{}{"content": content}},
			},
		})
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestGeneratorDigestInfoLogs verifies digest generation logs at info with
// config id/name, item counts, LLM input/output sizes, and response time.
func TestGeneratorDigestInfoLogs(t *testing.T) {
	queries, cleanup := setupDigestTest(t)
	defer cleanup()
	ctx := context.Background()

	user, err := queries.CreateUser(ctx, store.CreateUserParams{
		Email:        "digest-logging@example.com",
		PasswordHash: "hash",
		ApiKey:       "test-api-key",
	})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	feedObj, err := queries.CreateFeed(ctx, store.CreateFeedParams{
		UserID: user.ID,
		Type:   "rss",
		Name:   "Digest Feed",
		Url:    "https://example.com/feed.xml",
		Config: "{}",
		Status: "active",
	})
	if err != nil {
		t.Fatalf("create feed: %v", err)
	}

	titles := []string{"Go 1.25 Released", "New SQLite Features", "Rust Gets Faster"}
	points := []int64{100, 80, 60}
	for i := range titles {
		_, err := queries.CreateItem(ctx, store.CreateItemParams{
			FeedID:        feedObj.ID,
			Hash:          "h" + strconv.Itoa(i),
			Title:         titles[i],
			Url:           "https://example.com/" + titles[i],
			Content:       "Content for " + titles[i],
			Points:        points[i],
			CommentsCount: 10,
			Status:        "unread",
		})
		if err != nil {
			t.Fatalf("create item %d: %v", i, err)
		}
	}

	llmBody := `{"title":"Go Time","summary":"Overall digest","highlights":[{"title":"Go 1.25 Released","summary":"s1"},{"title":"New SQLite Features","summary":"s2"}]}`
	llmSrv := llmTestServer(t, llmBody)
	llm := NewLLMClient(llmSrv.URL, "test-key", "test-model")

	cfg, err := queries.CreateDigestConfig(ctx, store.CreateDigestConfigParams{
		UserID:   user.ID,
		Name:     "Daily Digest",
		Schedule: "0 8 * * *",
		Config:   `{"feeds":[{"id":` + strconv.FormatInt(feedObj.ID, 10) + `,"highlights":2,"comments":5}]}`,
	})
	if err != nil {
		t.Fatalf("create digest config: %v", err)
	}

	capture := testutil.CaptureSlog(t, slog.LevelInfo)
	gen := NewGenerator(queries, llm, feed.NewPoller(queries))
	result, err := gen.Generate(ctx, cfg)
	if err != nil {
		t.Fatalf("Generate failed: %v", err)
	}
	if result.ID == 0 {
		t.Fatal("expected non-zero result id")
	}

	genRec, ok := capture.Find("digest generated")
	if !ok {
		t.Fatal("missing info 'digest generated' record")
	}
	if genRec.Level != slog.LevelInfo {
		t.Errorf("digest generated level = %v, want info", genRec.Level)
	}
	genAttrs := testutil.AttrMap(genRec)
	if genAttrs["result_id"] == "" || genAttrs["result_id"] == "0" {
		t.Errorf("digest generated result_id = %q, want non-zero", genAttrs["result_id"])
	}
	if genAttrs["config_id"] != strconv.FormatInt(cfg.ID, 10) {
		t.Errorf("config_id = %q, want %d", genAttrs["config_id"], cfg.ID)
	}
	if genAttrs["config_name"] != "Daily Digest" {
		t.Errorf("config_name = %q, want %q", genAttrs["config_name"], "Daily Digest")
	}
	if genAttrs["highlights"] != "2" || genAttrs["items"] != "1" {
		t.Errorf("digest generated counts = highlights %q items %q, want 2 and 1", genAttrs["highlights"], genAttrs["items"])
	}
	if _, err := time.ParseDuration(genAttrs["duration"]); err != nil {
		t.Errorf("duration %q not parseable: %v", genAttrs["duration"], err)
	}
	if genAttrs["category"] != "digest" {
		t.Errorf("digest generated category = %q, want digest", genAttrs["category"])
	}

	// The generated digest title is stored in the result content JSON.
	var stored struct {
		Title string `json:"title"`
	}
	if err := json.Unmarshal([]byte(result.Content), &stored); err != nil {
		t.Fatalf("parsing stored digest content: %v", err)
	}
	if stored.Title != "Go Time" {
		t.Errorf("stored digest title = %q, want %q", stored.Title, "Go Time")
	}

	// LLM input/output sizes and duration are on the info records from the
	// generator (the llm.go debug records stay hidden at info level).
	inRec, ok := capture.Find("calling LLM for digest")
	if !ok {
		t.Fatal("missing info 'calling LLM for digest' record")
	}
	if n, err := strconv.Atoi(testutil.AttrMap(inRec)["input_bytes"]); err != nil || n <= 0 {
		t.Errorf("calling LLM input_bytes = %q, want a positive number", testutil.AttrMap(inRec)["input_bytes"])
	}
	outRec, ok := capture.Find("LLM digest response received")
	if !ok {
		t.Fatal("missing info 'LLM digest response received' record")
	}
	outAttrs := testutil.AttrMap(outRec)
	if n, err := strconv.Atoi(outAttrs["output_bytes"]); err != nil || n <= 0 {
		t.Errorf("LLM digest response output_bytes = %q, want a positive number", outAttrs["output_bytes"])
	}
	if _, err := time.ParseDuration(outAttrs["duration"]); err != nil {
		t.Errorf("LLM digest response duration %q not parseable: %v", outAttrs["duration"], err)
	}
	if _, ok := capture.Find("calling LLM"); ok {
		t.Error("debug 'calling LLM' record emitted at info level")
	}
	testutil.AssertAllCategorized(t, capture)
}

// TestLLMClientDebugLogs verifies the low-level LLM HTTP logs are debug with
// input/output byte sizes and response time, and hidden at info.
func TestLLMClientDebugLogs(t *testing.T) {
	llmSrv := llmTestServer(t, `{"summary":"ok","highlights":[]}`)
	llm := NewLLMClient(llmSrv.URL, "test-key", "test-model")

	t.Run("debug level logs LLM call", func(t *testing.T) {
		capture := testutil.CaptureSlog(t, slog.LevelDebug)
		if _, err := llm.Chat(context.Background(), "system prompt", "user prompt"); err != nil {
			t.Fatalf("Chat failed: %v", err)
		}

		inRec, ok := capture.Find("calling LLM")
		if !ok {
			t.Fatal("missing debug 'calling LLM' record")
		}
		if inRec.Level != slog.LevelDebug {
			t.Errorf("calling LLM level = %v, want debug", inRec.Level)
		}
		inAttrs := testutil.AttrMap(inRec)
		if n, err := strconv.Atoi(inAttrs["input_bytes"]); err != nil || n <= 0 {
			t.Errorf("calling LLM input_bytes = %q, want a positive number", inAttrs["input_bytes"])
		}
		if inAttrs["category"] != "digest" {
			t.Errorf("calling LLM category = %q, want digest", inAttrs["category"])
		}

		outRec, ok := capture.Find("LLM response received")
		if !ok {
			t.Fatal("missing debug 'LLM response received' record")
		}
		outAttrs := testutil.AttrMap(outRec)
		if n, err := strconv.Atoi(outAttrs["output_bytes"]); err != nil || n <= 0 {
			t.Errorf("LLM response output_bytes = %q, want a positive number", outAttrs["output_bytes"])
		}
		if _, err := time.ParseDuration(outAttrs["duration"]); err != nil {
			t.Errorf("LLM response duration %q not parseable: %v", outAttrs["duration"], err)
		}
		testutil.AssertAllCategorized(t, capture)
	})

	t.Run("info level hides LLM debug logs", func(t *testing.T) {
		capture := testutil.CaptureSlog(t, slog.LevelInfo)
		if _, err := llm.Chat(context.Background(), "system prompt", "user prompt"); err != nil {
			t.Fatalf("Chat failed: %v", err)
		}
		if _, ok := capture.Find("calling LLM"); ok {
			t.Error("debug 'calling LLM' record emitted at info level")
		}
		if _, ok := capture.Find("LLM response received"); ok {
			t.Error("debug 'LLM response received' record emitted at info level")
		}
	})
}

// fakeCommentFetcher returns a fixed comment list for comment-fetch logging
// tests.
type fakeCommentFetcher struct{}

func (fakeCommentFetcher) FetchComments(_ context.Context, _ store.Item, _ int) ([]feed.Comment, error) {
	return []feed.Comment{
		{Author: "alice", Text: "first comment", Points: 5, ValidPoints: true},
		{Author: "bob", Text: "second comment", Points: 3, ValidPoints: true},
	}, nil
}

// TestGeneratorFetchCommentsInfoLogs verifies comment fetching for digest
// highlights logs at info with item url and comment count.
func TestGeneratorFetchCommentsInfoLogs(t *testing.T) {
	queries, cleanup := setupDigestTest(t)
	defer cleanup()
	ctx := context.Background()

	user, err := queries.CreateUser(ctx, store.CreateUserParams{
		Email:        "comments-logging@example.com",
		PasswordHash: "hash",
		ApiKey:       "test-api-key",
	})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	feedObj, err := queries.CreateFeed(ctx, store.CreateFeedParams{
		UserID: user.ID,
		Type:   "hn",
		Name:   "HN Feed",
		Url:    "https://news.ycombinator.com/rss",
		Config: "{}",
		Status: "active",
	})
	if err != nil {
		t.Fatalf("create feed: %v", err)
	}

	item, err := queries.CreateItem(ctx, store.CreateItemParams{
		FeedID:    feedObj.ID,
		Hash:      "h1",
		Title:     "HN Story",
		Url:       "https://news.ycombinator.com/item?id=1",
		Content:   "body",
		Status:    "unread",
		Points:    42,
		CommentsCount: 10,
	})
	if err != nil {
		t.Fatalf("create item: %v", err)
	}

	capture := testutil.CaptureSlog(t, slog.LevelInfo)
	g := &Generator{queries: queries, commentFetchers: map[string]feed.CommentFetcher{"hn": fakeCommentFetcher{}}}
	g.fetchCommentsForHighlights(ctx, []digestItem{
		{Item: store.Item{ID: item.ID, FeedID: feedObj.ID, Url: "https://news.ycombinator.com/item?id=1"}},
	}, slog.Default())

	rec, ok := capture.Find("fetched comments for highlight")
	if !ok {
		t.Fatal("missing info 'fetched comments for highlight' record")
	}
	if rec.Level != slog.LevelInfo {
		t.Errorf("fetched comments level = %v, want info", rec.Level)
	}
	attrs := testutil.AttrMap(rec)
	if attrs["item_id"] != strconv.FormatInt(item.ID, 10) {
		t.Errorf("item_id = %q, want %d", attrs["item_id"], item.ID)
	}
	if attrs["url"] != "https://news.ycombinator.com/item?id=1" {
		t.Errorf("url = %q, want item url", attrs["url"])
	}
	if attrs["count"] != "2" {
		t.Errorf("count = %q, want 2", attrs["count"])
	}
	if attrs["category"] != "digest" {
		t.Errorf("category = %q, want digest", attrs["category"])
	}
	testutil.AssertAllCategorized(t, capture)
}

// TestSchedulerGenerationFailureLogs verifies a failed scheduled digest
// generation is logged at error with config context.
func TestSchedulerGenerationFailureLogs(t *testing.T) {
	queries, cleanup := setupDigestTest(t)
	defer cleanup()
	ctx := context.Background()

	user, err := queries.CreateUser(ctx, store.CreateUserParams{
		Email:        "scheduler-logging@example.com",
		PasswordHash: "hash",
		ApiKey:       "test-api-key",
	})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	feedObj, err := queries.CreateFeed(ctx, store.CreateFeedParams{
		UserID: user.ID,
		Type:   "rss",
		Name:   "Scheduler Feed",
		Url:    "https://example.com/feed.xml",
		Config: "{}",
		Status: "active",
	})
	if err != nil {
		t.Fatalf("create feed: %v", err)
	}

	cfg, err := queries.CreateDigestConfig(ctx, store.CreateDigestConfigParams{
		UserID:   user.ID,
		Name:     "Hourly Digest",
		Schedule: "* * * * *",
		Config:   `{"feeds":[{"id":` + strconv.FormatInt(feedObj.ID, 10) + `,"highlights":2,"comments":5}]}`,
	})
	if err != nil {
		t.Fatalf("create digest config: %v", err)
	}

	capture := testutil.CaptureSlog(t, slog.LevelInfo)
	s := &Scheduler{queries: queries, generator: &Generator{queries: queries}}
	s.tick(ctx)

	start, ok := capture.Find("scheduled digest generation")
	if !ok {
		t.Fatal("missing info 'scheduled digest generation' record")
	}
	startAttrs := testutil.AttrMap(start)
	if startAttrs["config_id"] != strconv.FormatInt(cfg.ID, 10) || startAttrs["config_name"] != "Hourly Digest" {
		t.Errorf("scheduled digest generation missing config context, got %v", startAttrs)
	}

	fail, ok := capture.Find("scheduled digest generation failed")
	if !ok {
		t.Fatal("missing error 'scheduled digest generation failed' record")
	}
	if fail.Level != slog.LevelError {
		t.Errorf("scheduled digest generation failed level = %v, want error", fail.Level)
	}
	failAttrs := testutil.AttrMap(fail)
	if failAttrs["config_id"] != strconv.FormatInt(cfg.ID, 10) || failAttrs["error"] == "" {
		t.Errorf("scheduled digest generation failed missing context, got %v", failAttrs)
	}
	if failAttrs["category"] != "digest" {
		t.Errorf("scheduled digest generation failed category = %q, want digest", failAttrs["category"])
	}
	testutil.AssertAllCategorized(t, capture)
}

// TestGeneratorMissingLLMKeyLogsError verifies that digest generation logs an
// error when the LLM is needed but no API key is configured (llm.api_key /
// LLM_API_KEY empty). The generation itself still proceeds; the error is a
// loud signal that the request will fail downstream.
func TestGeneratorMissingLLMKeyLogsError(t *testing.T) {
	queries, cleanup := setupDigestTest(t)
	defer cleanup()
	ctx := context.Background()

	user, err := queries.CreateUser(ctx, store.CreateUserParams{
		Email:        "missing-llm-key@example.com",
		PasswordHash: "hash",
		ApiKey:       "missing-llm-key",
	})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	feedObj, err := queries.CreateFeed(ctx, store.CreateFeedParams{
		UserID: user.ID,
		Type:   "rss",
		Name:   "LLM Key Feed",
		Url:    "https://example.com/feed.xml",
		Config: "{}",
		Status: "active",
	})
	if err != nil {
		t.Fatalf("create feed: %v", err)
	}
	for i, title := range []string{"Alpha Story", "Beta Story"} {
		if _, err := queries.CreateItem(ctx, store.CreateItemParams{
			FeedID:        feedObj.ID,
			Hash:          "missingkey-h" + strconv.Itoa(i),
			Title:         title,
			Url:           "https://example.com/" + title,
			Content:       "content",
			Points:        50,
			CommentsCount: 5,
			Status:        "unread",
		}); err != nil {
			t.Fatalf("create item %d: %v", i, err)
		}
	}
	cfg, err := queries.CreateDigestConfig(ctx, store.CreateDigestConfigParams{
		UserID:   user.ID,
		Name:     "No Key Digest",
		Schedule: "0 8 * * *",
		Config:   `{"feeds":[{"id":` + strconv.FormatInt(feedObj.ID, 10) + `,"highlights":1,"comments":2}]}`,
	})
	if err != nil {
		t.Fatalf("create digest config: %v", err)
	}

	llmBody := `{"summary":"Overall digest","highlights":[{"title":"Alpha Story","summary":"s1"}]}`
	llmSrv := llmTestServer(t, llmBody)
	// Note the empty API key — the LLM is needed but not configured.
	llm := NewLLMClient(llmSrv.URL, "", "test-model")

	capture := testutil.CaptureSlog(t, slog.LevelError)
	gen := NewGenerator(queries, llm, feed.NewPoller(queries))
	result, err := gen.Generate(ctx, cfg)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if result == nil || result.ID == 0 {
		t.Fatal("expected a generated digest result")
	}

	rec, ok := capture.Find("LLM API key is not configured (llm.api_key / LLM_API_KEY); digest generation will fail")
	if !ok {
		t.Fatal("missing 'LLM API key is not configured' error record")
	}
	if rec.Level != slog.LevelError {
		t.Errorf("missing-key record level = %v, want error", rec.Level)
	}
	attrs := testutil.AttrMap(rec)
	if attrs["category"] != "digest" {
		t.Errorf("missing-key record category = %q, want digest", attrs["category"])
	}
}
