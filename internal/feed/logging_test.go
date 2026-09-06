package feed

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"digesterr/internal/store"
	"digesterr/internal/testutil"
)

func setupFeedLoggingDB(t *testing.T) *store.Queries {
	t.Helper()
	db, err := testutil.OpenTestDB()
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return store.New(db)
}

func createFeedLoggingUser(t *testing.T, queries *store.Queries) store.User {
	t.Helper()
	user, err := queries.CreateUser(t.Context(), store.CreateUserParams{
		Email:        "feed-logging@example.com",
		PasswordHash: "hash",
		ApiKey:       "test-api-key",
	})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	return user
}

func rssBodyServer(t *testing.T, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestPollFeedInfoLogs verifies a successful feed refresh logs at info with
// the feed id/name/url and item counts.
func TestPollFeedInfoLogs(t *testing.T) {
	queries := setupFeedLoggingDB(t)
	user := createFeedLoggingUser(t, queries)
	rssSrv := rssBodyServer(t, testRSS)

	feedObj, err := queries.CreateFeed(t.Context(), store.CreateFeedParams{
		UserID: user.ID,
		Type:   "rss",
		Name:   "Logging Feed",
		Url:    rssSrv.URL,
		Config: "{}",
		Status: "active",
	})
	if err != nil {
		t.Fatalf("create feed: %v", err)
	}

	capture := testutil.CaptureSlog(t, slog.LevelInfo)
	poller := NewPoller(queries)
	if err := poller.PollFeed(context.Background(), feedObj); err != nil {
		t.Fatalf("PollFeed failed: %v", err)
	}

	rec, ok := capture.Find("feed polled")
	if !ok {
		t.Fatal("missing info 'feed polled' record")
	}
	if rec.Level != slog.LevelInfo {
		t.Errorf("feed polled level = %v, want info", rec.Level)
	}
	attrs := testutil.AttrMap(rec)
	if attrs["feed_id"] != strconv.FormatInt(feedObj.ID, 10) {
		t.Errorf("feed_id = %q, want %d", attrs["feed_id"], feedObj.ID)
	}
	if attrs["feed_name"] != "Logging Feed" {
		t.Errorf("feed_name = %q, want %q", attrs["feed_name"], "Logging Feed")
	}
	if attrs["feed_url"] != rssSrv.URL {
		t.Errorf("feed_url = %q, want %q", attrs["feed_url"], rssSrv.URL)
	}
	if attrs["total_items"] != "2" {
		t.Errorf("total_items = %q, want 2", attrs["total_items"])
	}
	if attrs["new_items"] != "2" {
		t.Errorf("new_items = %q, want 2", attrs["new_items"])
	}
	if _, err := time.ParseDuration(attrs["duration"]); err != nil {
		t.Errorf("duration %q not parseable: %v", attrs["duration"], err)
	}
	if attrs["category"] != "feed" {
		t.Errorf("category = %q, want feed", attrs["category"])
	}
	testutil.AssertAllCategorized(t, capture)
}

// TestPollFeedDebugLogs verifies the per-feed debug line carries the feed
// context and that debug stays hidden at the default info level.
func TestPollFeedDebugLogs(t *testing.T) {
	queries := setupFeedLoggingDB(t)
	user := createFeedLoggingUser(t, queries)
	rssSrv := rssBodyServer(t, testRSS)

	feedObj, err := queries.CreateFeed(t.Context(), store.CreateFeedParams{
		UserID: user.ID,
		Type:   "rss",
		Name:   "Debug Feed",
		Url:    rssSrv.URL,
		Config: "{}",
		Status: "active",
	})
	if err != nil {
		t.Fatalf("create feed: %v", err)
	}

	poller := NewPoller(queries)

	t.Run("debug level shows polling feed", func(t *testing.T) {
		capture := testutil.CaptureSlog(t, slog.LevelDebug)
		if err := poller.PollFeed(context.Background(), feedObj); err != nil {
			t.Fatalf("PollFeed failed: %v", err)
		}
		rec, ok := capture.Find("polling feed")
		if !ok {
			t.Fatal("missing debug 'polling feed' record")
		}
		if rec.Level != slog.LevelDebug {
			t.Errorf("polling feed level = %v, want debug", rec.Level)
		}
		attrs := testutil.AttrMap(rec)
		if attrs["feed_id"] != strconv.FormatInt(feedObj.ID, 10) || attrs["feed_name"] != "Debug Feed" {
			t.Errorf("polling feed missing feed context, got %v", attrs)
		}
		testutil.AssertAllCategorized(t, capture)
	})

	t.Run("info level hides polling feed", func(t *testing.T) {
		capture := testutil.CaptureSlog(t, slog.LevelInfo)
		if err := poller.PollFeed(context.Background(), feedObj); err != nil {
			t.Fatalf("PollFeed failed: %v", err)
		}
		if _, ok := capture.Find("polling feed"); ok {
			t.Error("debug 'polling feed' record emitted at info level")
		}
	})
}

// TestPollFeedFetchFailureWarns verifies a failed feed refresh is logged at
// warn with feed context and the error.
func TestPollFeedFetchFailureWarns(t *testing.T) {
	queries := setupFeedLoggingDB(t)
	user := createFeedLoggingUser(t, queries)
	failSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("boom"))
	}))
	defer failSrv.Close()

	feedObj, err := queries.CreateFeed(t.Context(), store.CreateFeedParams{
		UserID: user.ID,
		Type:   "rss",
		Name:   "Broken Feed",
		Url:    failSrv.URL,
		Config: "{}",
		Status: "active",
	})
	if err != nil {
		t.Fatalf("create feed: %v", err)
	}

	capture := testutil.CaptureSlog(t, slog.LevelInfo)
	poller := NewPoller(queries)
	if err := poller.PollFeed(context.Background(), feedObj); err == nil {
		t.Fatal("expected PollFeed to fail for broken server")
	}

	rec, ok := capture.Find("failed to fetch feed")
	if !ok {
		t.Fatal("missing warn 'failed to fetch feed' record")
	}
	if rec.Level != slog.LevelWarn {
		t.Errorf("failed to fetch feed level = %v, want warn", rec.Level)
	}
	attrs := testutil.AttrMap(rec)
	if attrs["feed_id"] != strconv.FormatInt(feedObj.ID, 10) {
		t.Errorf("feed_id = %q, want %d", attrs["feed_id"], feedObj.ID)
	}
	if attrs["feed_name"] != "Broken Feed" {
		t.Errorf("feed_name = %q, want %q", attrs["feed_name"], "Broken Feed")
	}
	if attrs["error"] == "" {
		t.Errorf("failed to fetch feed missing error attr, got %v", attrs)
	}
	if attrs["category"] != "feed" {
		t.Errorf("category = %q, want feed", attrs["category"])
	}
	testutil.AssertAllCategorized(t, capture)
}

// TestPollFeedContentExtractionInfoLogs verifies content extraction during a
// feed refresh is logged at info with item url and content size.
func TestPollFeedContentExtractionInfoLogs(t *testing.T) {
	queries := setupFeedLoggingDB(t)
	user := createFeedLoggingUser(t, queries)

	plainSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte("This is the extracted plain text body for the item."))
	}))
	defer plainSrv.Close()

	rssBody := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0">
  <channel>
    <title>Extract Feed</title>
    <link>https://example.com</link>
    <description>d</description>
    <item>
      <title>Extractable Item</title>
      <link>%s</link>
      <guid>extract-1</guid>
      <description>body</description>
    </item>
  </channel>
</rss>`, plainSrv.URL)
	rssSrv := rssBodyServer(t, rssBody)

	feedObj, err := queries.CreateFeed(t.Context(), store.CreateFeedParams{
		UserID:         user.ID,
		Type:           "rss",
		Name:           "Extract Feed",
		Url:            rssSrv.URL,
		Config:         "{}",
		Status:         "active",
		ExtractContent: 1,
	})
	if err != nil {
		t.Fatalf("create feed: %v", err)
	}

	capture := testutil.CaptureSlog(t, slog.LevelInfo)
	poller := NewPoller(queries)
	if err := poller.PollFeed(context.Background(), feedObj); err != nil {
		t.Fatalf("PollFeed failed: %v", err)
	}

	start, ok := capture.Find("extracting content")
	if !ok {
		t.Fatal("missing info 'extracting content' record")
	}
	startAttrs := testutil.AttrMap(start)
	if startAttrs["count"] != "1" {
		t.Errorf("extracting content count = %q, want 1", startAttrs["count"])
	}

	done, ok := capture.Find("content extracted")
	if !ok {
		t.Fatal("missing info 'content extracted' record")
	}
	doneAttrs := testutil.AttrMap(done)
	if doneAttrs["item_id"] == "" {
		t.Errorf("content extracted missing item_id, got %v", doneAttrs)
	}
	if doneAttrs["url"] != plainSrv.URL {
		t.Errorf("content extracted url = %q, want %q", doneAttrs["url"], plainSrv.URL)
	}
	if n, err := strconv.Atoi(doneAttrs["bytes"]); err != nil || n <= 0 {
		t.Errorf("content extracted bytes = %q, want a positive number", doneAttrs["bytes"])
	}
	if _, err := time.ParseDuration(doneAttrs["duration"]); err != nil {
		t.Errorf("duration %q not parseable: %v", doneAttrs["duration"], err)
	}
	if doneAttrs["category"] != "feed" {
		t.Errorf("content extracted category = %q, want feed", doneAttrs["category"])
	}
	testutil.AssertAllCategorized(t, capture)
}

// fakeBulkEnricher simulates a score/comment fetch that updates items.
type fakeBulkEnricher struct{}

func (fakeBulkEnricher) Enrich(_ context.Context, item *store.Item) error {
	item.Points = 42
	item.CommentsCount = 7
	return nil
}

func (fakeBulkEnricher) BulkEnrich(_ context.Context, items []*store.Item) error {
	for _, it := range items {
		it.Points = 42
		it.CommentsCount = 7
	}
	return nil
}

// TestEnrichmentInfoLogs verifies score/comment enrichment is logged at info
// with feed context and item counts.
func TestEnrichmentInfoLogs(t *testing.T) {
	queries := setupFeedLoggingDB(t)
	user := createFeedLoggingUser(t, queries)
	rssSrv := rssBodyServer(t, testRSS)

	feedObj, err := queries.CreateFeed(t.Context(), store.CreateFeedParams{
		UserID: user.ID,
		Type:   "hn",
		Name:   "Enrich Feed",
		Url:    rssSrv.URL,
		Config: "{}",
		Status: "active",
	})
	if err != nil {
		t.Fatalf("create feed: %v", err)
	}

	_, err = queries.CreateItem(t.Context(), store.CreateItemParams{
		FeedID:        feedObj.ID,
		Hash:          "hash-1",
		Title:         "Enrichable Item",
		Url:           "https://news.ycombinator.com/item?id=1",
		Content:       "body",
		Status:        "unread",
		Points:        0,
		CommentsCount: 0,
	})
	if err != nil {
		t.Fatalf("create item: %v", err)
	}

	capture := testutil.CaptureSlog(t, slog.LevelDebug)
	poller := NewPoller(queries)
	poller.enrichers["hn"] = fakeBulkEnricher{}
	poller.enrichForPoll(context.Background(), feedObj, nil, slog.Default())

	start, ok := capture.Find("enriching items")
	if !ok {
		t.Fatal("missing info 'enriching items' record")
	}
	startAttrs := testutil.AttrMap(start)
	if startAttrs["candidates"] != "0" || startAttrs["existing"] != "1" {
		t.Errorf("enriching items attrs = %v, want candidates=0 existing=1", startAttrs)
	}

	done, ok := capture.Find("enrichment completed")
	if !ok {
		t.Fatal("missing info 'enrichment completed' record")
	}
	doneAttrs := testutil.AttrMap(done)
	if doneAttrs["updated"] != "1" {
		t.Errorf("enrichment completed updated = %q, want 1", doneAttrs["updated"])
	}
	if doneAttrs["category"] != "feed" {
		t.Errorf("enrichment completed category = %q, want feed", doneAttrs["category"])
	}
	testutil.AssertAllCategorized(t, capture)
}

// withJinaURL points the Jina fallback at an httptest server for the duration
// of a test and restores the real URL afterwards.
func withJinaURL(t *testing.T, srv *httptest.Server) {
	t.Helper()
	old := jinaReaderURL
	jinaReaderURL = srv.URL
	t.Cleanup(func() { jinaReaderURL = old })
}

func jinaMarkdownServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte("# Extracted via Jina\n\nBody text from the fallback reader."))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestExtractionFallbackToJinaNotWarn verifies that falling back from a
// failed direct content fetch to Jina is logged at info/debug, never warn.
func TestExtractionFallbackToJinaNotWarn(t *testing.T) {
	jina := jinaMarkdownServer(t)
	withJinaURL(t, jina)

	direct := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer direct.Close()

	capture := testutil.CaptureSlog(t, slog.LevelDebug)
	client := &http.Client{Timeout: 5 * time.Second}
	content, err := ExtractContent(context.Background(), direct.URL, client, slog.Default())
	if err != nil {
		t.Fatalf("ExtractContent failed: %v", err)
	}
	if content == "" {
		t.Fatal("expected non-empty extracted content")
	}

	fallback, ok := capture.Find("content extraction: direct fetch failed, trying Jina.ai")
	if !ok {
		t.Fatal("missing info 'content extraction: direct fetch failed, trying Jina.ai' record")
	}
	if fallback.Level != slog.LevelInfo {
		t.Errorf("fallback record level = %v, want info", fallback.Level)
	}
	if attrs := testutil.AttrMap(fallback); attrs["status"] != "500" {
		t.Errorf("fallback status = %q, want 500", attrs["status"])
	}

	okRec, ok := capture.Find("content extraction: jina succeeded")
	if !ok {
		t.Fatal("missing debug 'content extraction: jina succeeded' record")
	}
	if okRec.Level != slog.LevelDebug {
		t.Errorf("jina succeeded level = %v, want debug", okRec.Level)
	}
	if n, err := strconv.Atoi(testutil.AttrMap(okRec)["bytes"]); err != nil || n <= 0 {
		t.Errorf("jina succeeded bytes = %q, want a positive number", testutil.AttrMap(okRec)["bytes"])
	}

	if warns := capture.Warns(); len(warns) > 0 {
		for _, w := range warns {
			t.Errorf("unexpected warn record during jina fallback: %q", w.Message)
		}
	}
	testutil.AssertAllCategorized(t, capture)
}

// TestExtractionPDFFallbackToJinaNotWarn verifies the PDF→Jina fallback is
// logged at info, never warn.
func TestExtractionPDFFallbackToJinaNotWarn(t *testing.T) {
	jina := jinaMarkdownServer(t)
	withJinaURL(t, jina)

	direct := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/pdf")
	}))
	defer direct.Close()

	capture := testutil.CaptureSlog(t, slog.LevelDebug)
	client := &http.Client{Timeout: 5 * time.Second}
	content, err := ExtractContent(context.Background(), direct.URL, client, slog.Default())
	if err != nil {
		t.Fatalf("ExtractContent failed: %v", err)
	}
	if !strings.Contains(content, "Extracted via Jina") {
		t.Errorf("expected jina-extracted content, got %q", content)
	}

	if _, ok := capture.Find("content extraction: pdf detected, trying Jina.ai"); !ok {
		t.Fatal("missing info 'content extraction: pdf detected, trying Jina.ai' record")
	}
	if warns := capture.Warns(); len(warns) > 0 {
		for _, w := range warns {
			t.Errorf("unexpected warn record during pdf fallback: %q", w.Message)
		}
	}
	testutil.AssertAllCategorized(t, capture)
}

// TestExtractionDirectSuccessDebugLogs verifies successful direct extraction
// is logged at debug with the content size.
func TestExtractionDirectSuccessDebugLogs(t *testing.T) {
	direct := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte("plain text body"))
	}))
	defer direct.Close()

	capture := testutil.CaptureSlog(t, slog.LevelDebug)
	client := &http.Client{Timeout: 5 * time.Second}
	if _, err := ExtractContent(context.Background(), direct.URL, client, slog.Default()); err != nil {
		t.Fatalf("ExtractContent failed: %v", err)
	}

	rec, ok := capture.Find("content extraction: plain text captured")
	if !ok {
		t.Fatal("missing debug 'content extraction: plain text captured' record")
	}
	if rec.Level != slog.LevelDebug {
		t.Errorf("plain text captured level = %v, want debug", rec.Level)
	}
	if n, err := strconv.Atoi(testutil.AttrMap(rec)["bytes"]); err != nil || n <= 0 {
		t.Errorf("plain text captured bytes = %q, want a positive number", testutil.AttrMap(rec)["bytes"])
	}
	if warns := capture.Warns(); len(warns) > 0 {
		t.Errorf("unexpected warn record during direct success: %q", warns[0].Message)
	}
	testutil.AssertAllCategorized(t, capture)
}
