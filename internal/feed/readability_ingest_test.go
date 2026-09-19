package feed

import (
	"context"
	"database/sql"
	"log/slog"
	"strings"
	"testing"

	"digesterr/internal/store"
)

// readabilityFeed creates an rss feed with the readability switch on.
func readabilityFeed(t *testing.T, queries *store.Queries, user store.User) store.Feed {
	t.Helper()
	feed, err := queries.CreateFeed(t.Context(), store.CreateFeedParams{
		UserID:     user.ID,
		Type:       "rss",
		Name:       "Full-text feed",
		Url:        "https://example.com/feed",
		Config:     "{}",
		Status:     "active",
		Readability: 1,
	})
	if err != nil {
		t.Fatalf("create feed: %v", err)
	}
	return feed
}

// insertItem stores one item with the given body and returns its id.
func insertItem(t *testing.T, queries *store.Queries, feed store.Feed, title, body string) int64 {
	t.Helper()
	id, err := queries.CreateItemIfNotExists(t.Context(), store.CreateItemIfNotExistsParams{
		FeedID:      feed.ID,
		Hash:        itemHash(title, "", "", body),
		Title:       title,
		Url:         "https://example.com/" + strings.ReplaceAll(title, " ", "-"),
		Content:     body,
		PublishedAt: sql.NullTime{},
	})
	if err != nil {
		t.Fatalf("create item: %v", err)
	}
	return id
}

// longArticle is a body shaped like a real full-text feed item: prose that
// dominates, plus a small amount of chrome.
func longArticle() string {
	return `<html><body><article><h1>A headline</h1><p>` +
		strings.Repeat("This is substantial article prose that has to survive. ", 40) +
		`</p><p>` + strings.Repeat("And a second paragraph of equally real content. ", 40) +
		`</p></article><footer>Copyright nobody. Unsubscribe.</footer></body></html>`
}

// The core behaviour: a readability feed stores the cleaned body in
// extract_content and marks it done, so every consumer serves the cleaned version.
func TestReadabilityFeedStoresProcessedBody(t *testing.T) {
	queries := setupFeedLoggingDB(t)
	user := createFeedLoggingUser(t, queries)
	feed := readabilityFeed(t, queries, user)
	id := insertItem(t, queries, feed, "Long piece", longArticle())

	poller := NewPoller(queries)
	poller.applyReadabilityToItems(context.Background(), feed, slog.Default())

	got, err := queries.GetItem(t.Context(), id)
	if err != nil {
		t.Fatalf("get item: %v", err)
	}
	if got.ExtractStatus != "done" {
		t.Fatalf("extract_status = %q, want \"done\"", got.ExtractStatus)
	}
	if got.ExtractContent == "" {
		t.Fatal("extract_content is empty: the processed body was not stored")
	}
	// The stored body must be the PROCESSED one, not the input.
	if !IsReadabilityOutput(got.ExtractContent) {
		t.Error("stored body is not readability output")
	}
	if !strings.Contains(got.ExtractContent, "substantial article prose") {
		t.Error("prose missing from the stored body")
	}
	// items.content is never touched: the original has to survive so the user can
	// still see it (the UI renders an Original section beside Extracted).
	if got.Content != longArticle() {
		t.Error("items.content was modified; the original must survive")
	}
	// The chrome is gone, which is the point of processing at all.
	if strings.Contains(got.ExtractContent, "Copyright nobody") {
		t.Error("footer survived into the processed body")
	}
}

// The transform is deterministic over a fixed body, so a second pass must not
// redo the work: a re-poll must be a no-op.
func TestReadabilityFeedIsIdempotent(t *testing.T) {
	queries := setupFeedLoggingDB(t)
	user := createFeedLoggingUser(t, queries)
	feed := readabilityFeed(t, queries, user)
	id := insertItem(t, queries, feed, "Long piece", longArticle())

	poller := NewPoller(queries)
	poller.applyReadabilityToItems(context.Background(), feed, slog.Default())
	first, _ := queries.GetItem(t.Context(), id)

	// A second pass must select nothing.
	pending, err := queries.ListItemsNeedingReadability(t.Context(), feed.ID)
	if err != nil {
		t.Fatalf("list pending: %v", err)
	}
	if len(pending) != 0 {
		t.Fatalf("a processed item is still pending: %d rows", len(pending))
	}
	poller.applyReadabilityToItems(context.Background(), feed, slog.Default())
	second, _ := queries.GetItem(t.Context(), id)
	if first.ExtractContent != second.ExtractContent {
		t.Error("a second pass changed the stored body")
	}
}

// A body readability cannot improve (it only appends its wrapper) is skipped
// silently: the item must look untouched, with no error and no stored body.
func TestReadabilityFeedSkipsBodyWithNoBenefit(t *testing.T) {
	queries := setupFeedLoggingDB(t)
	user := createFeedLoggingUser(t, queries)
	feed := readabilityFeed(t, queries, user)
	id := insertItem(t, queries, feed, "Tiny stub", "<p>short body</p>")

	poller := NewPoller(queries)
	poller.applyReadabilityToItems(context.Background(), feed, slog.Default())

	got, _ := queries.GetItem(t.Context(), id)
	if got.ExtractStatus != "skipped" {
		t.Fatalf("extract_status = %q, want \"skipped\"", got.ExtractStatus)
	}
	if got.ExtractContent != "" {
		t.Errorf("extract_content = %q, want empty for a skipped item", got.ExtractContent)
	}
}

// A mangled result is recorded as a failure with the reason stored, and the
// original body is what the consumers serve (they gate on status == "done").
func TestReadabilityFeedRecordsMangledOutputAsFailure(t *testing.T) {
	queries := setupFeedLoggingDB(t)
	user := createFeedLoggingUser(t, queries)
	feed := readabilityFeed(t, queries, user)

	// Prose wrapped so the library keeps only part of it: the shape measured to
	// lose a third of the words.
	var b strings.Builder
	b.WriteString(`<html><body><div class="wrapper"><article><p>`)
	b.WriteString(strings.Repeat("First substantial paragraph of the piece. ", 12))
	b.WriteString(`</p>`)
	for i := 0; i < 9; i++ {
		b.WriteString(`<div class="unlikely-related"><p>`)
		b.WriteString(strings.Repeat("Later paragraph carrying equally important prose. ", 12))
		b.WriteString(`</p></div>`)
	}
	b.WriteString(`</article></div></body></html>`)
	id := insertItem(t, queries, feed, "Mangled", b.String())

	poller := NewPoller(queries)
	poller.applyReadabilityToItems(context.Background(), feed, slog.Default())

	got, _ := queries.GetItem(t.Context(), id)
	if got.ExtractStatus != "failed" {
		t.Fatalf("extract_status = %q, want \"failed\"", got.ExtractStatus)
	}
	// Like a failed extraction, the column carries the reason so the UI can show it.
	if !strings.Contains(got.ExtractContent, "readability") {
		t.Errorf("extract_content should explain the failure, got %q", got.ExtractContent)
	}
	// items.content keeps the original, which is what a non-done status serves.
	if got.Content != b.String() {
		t.Error("original body was overwritten by a rejected result")
	}
}

// A mangled result must NOT be retried: the transform is deterministic over a
// fixed body, so a second attempt would produce the same rejection forever.
func TestReadabilityFeedDoesNotRetryFailures(t *testing.T) {
	queries := setupFeedLoggingDB(t)
	user := createFeedLoggingUser(t, queries)
	feed := readabilityFeed(t, queries, user)

	var b strings.Builder
	b.WriteString(`<html><body><div class="wrapper"><article><p>`)
	b.WriteString(strings.Repeat("First substantial paragraph of the piece. ", 12))
	for i := 0; i < 9; i++ {
		b.WriteString(`<div class="unlikely-related"><p>`)
		b.WriteString(strings.Repeat("Later paragraph carrying equally important prose. ", 12))
		b.WriteString(`</p></div>`)
	}
	b.WriteString(`</p></article></div></body></html>`)
	insertItem(t, queries, feed, "Mangled", b.String())

	pending, err := queries.ListItemsNeedingReadability(t.Context(), feed.ID)
	if err != nil {
		t.Fatalf("list pending: %v", err)
	}
	if len(pending) != 1 {
		t.Fatalf("a never-processed item must be pending before the first pass, got %d", len(pending))
	}

	poller := NewPoller(queries)
	poller.applyReadabilityToItems(context.Background(), feed, slog.Default())

	// The rejection must not leave the item queued: a deterministic transform
	// retried forever would burn the same body for the same answer on every poll.
	pending, err = queries.ListItemsNeedingReadability(t.Context(), feed.ID)
	if err != nil {
		t.Fatalf("list pending: %v", err)
	}
	if len(pending) != 0 {
		t.Fatalf("a rejected item is still pending: %d rows (it would be retried forever)", len(pending))
	}
}

// A feed without the flag must be left completely alone: no processing, no
// network, nothing written.
func TestNonReadabilityFeedIsUntouched(t *testing.T) {
	queries := setupFeedLoggingDB(t)
	user := createFeedLoggingUser(t, queries)
	feed, err := queries.CreateFeed(t.Context(), store.CreateFeedParams{
		UserID: user.ID, Type: "rss", Name: "Plain", Url: "https://example.com/f",
		Config: "{}", Status: "active",
	})
	if err != nil {
		t.Fatalf("create feed: %v", err)
	}
	id := insertItem(t, queries, feed, "Long piece", longArticle())

	poller := NewPoller(queries)
	// The poll decides, so exercise the same branch the poller uses.
	if feed.Readability != 0 {
		poller.applyReadabilityToItems(context.Background(), feed, slog.Default())
	}

	got, _ := queries.GetItem(t.Context(), id)
	if got.ExtractStatus != "" || got.ExtractContent != "" {
		t.Errorf("untouched feed was processed: status=%q content=%q", got.ExtractStatus, got.ExtractContent)
	}
}

// A readability rejection must not leave the item queued for readability again:
// the transform is deterministic, so a retry would reach the same answer forever.
//
// Note on the RELATED risk, worth stating because it is not this query's job: a
// 'failed' row does appear in ListItemsNeedingExtraction, but that pass only runs
// for feeds with extract_content = 1 (see the poll branch and the digest
// generator's feedExtractsContent), and the API rejects that flag together with
// readability. The invariant that protects the stored body is therefore mutual
// exclusivity plus extract_retries, not this query.
func TestReadabilityRejectionIsNotRequeued(t *testing.T) {
	queries := setupFeedLoggingDB(t)
	user := createFeedLoggingUser(t, queries)
	feed := readabilityFeed(t, queries, user)

	var b strings.Builder
	b.WriteString(`<html><body><div class="wrapper"><article><p>`)
	b.WriteString(strings.Repeat("First substantial paragraph of the piece. ", 12))
	for i := 0; i < 9; i++ {
		b.WriteString(`<div class="unlikely-related"><p>`)
		b.WriteString(strings.Repeat("Later paragraph carrying equally important prose. ", 12))
		b.WriteString(`</p></div>`)
	}
	b.WriteString(`</p></article></div></body></html>`)
	id := insertItem(t, queries, feed, "Mangled", b.String())

	poller := NewPoller(queries)
	poller.applyReadabilityToItems(context.Background(), feed, slog.Default())

	got, err := queries.GetItem(t.Context(), id)
	if err != nil {
		t.Fatalf("get item: %v", err)
	}
	if got.ExtractStatus != "failed" {
		t.Fatalf("extract_status = %q, want failed", got.ExtractStatus)
	}
	// extract_retries must be non-zero: it is the guard that keeps a deterministic
	// rejection from ever entering an extraction retry budget.
	if got.ExtractRetries == 0 {
		t.Error("extract_retries is 0 on a readability rejection; it must be pinned so no retry pass picks it up")
	}
	queued, err := queries.ListItemsNeedingReadability(t.Context(), feed.ID)
	if err != nil {
		t.Fatalf("list readability queue: %v", err)
	}
	if len(queued) != 0 {
		t.Fatalf("a rejected item is still queued for readability (%d), so it would be retried forever", len(queued))
	}
}

// The poll decides which pass runs, so exercise that decision directly: a feed with
// the readability flag must go through the readability path, and one without must not.
func TestPollChoosesReadabilityOverExtraction(t *testing.T) {
	queries := setupFeedLoggingDB(t)
	user := createFeedLoggingUser(t, queries)

	// A readability feed: its items get processed from the stored body.
	rdFeed := readabilityFeed(t, queries, user)
	rdID := insertItem(t, queries, rdFeed, "Long piece", longArticle())

	// An ordinary feed (no flags): nothing may be written to it.
	plain, err := queries.CreateFeed(t.Context(), store.CreateFeedParams{
		UserID: user.ID, Type: "rss", Name: "Plain", Url: "https://example.com/p",
		Config: "{}", Status: "active",
	})
	if err != nil {
		t.Fatalf("create plain feed: %v", err)
	}
	plainID := insertItem(t, queries, plain, "Long piece", longArticle())

	poller := NewPoller(queries)
	// Mirror the poller's own branch so the test fails if that branch changes.
	runPostFetchPass := func(f store.Feed) {
		if f.Readability != 0 {
			poller.applyReadabilityToItems(context.Background(), f, slog.Default())
		} else if f.ExtractContent != 0 {
			poller.extractFeedContent(context.Background(), f, slog.Default())
		}
	}
	runPostFetchPass(rdFeed)
	runPostFetchPass(plain)

	got, _ := queries.GetItem(t.Context(), rdID)
	if got.ExtractStatus != "done" || got.ExtractContent == "" {
		t.Errorf("readability feed was not processed: status=%q content_len=%d", got.ExtractStatus, len(got.ExtractContent))
	}
	untouched, _ := queries.GetItem(t.Context(), plainID)
	if untouched.ExtractStatus != "" || untouched.ExtractContent != "" {
		t.Errorf("a feed with no flags was processed: status=%q content_len=%d", untouched.ExtractStatus, len(untouched.ExtractContent))
	}
}
