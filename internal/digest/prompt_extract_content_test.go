package digest

import (
	"context"
	"log/slog"
	"strings"
	"testing"

	"digesterr/internal/store"
)

// A FAILED extraction keeps its error log in extract_content (poller.failItemExtraction
// appends "[timestamp] jina HTTP 403" lines to that column). The column never holds a
// status, so comparing it against status literals is always true and the log is sent
// to the model as if it were the article.
//
// The prompt must carry the item's real Content instead.
func TestPromptUsesOriginalBodyWhenExtractionFailed(t *testing.T) {
	_, queries, cleanup := setupDigestTest(t)
	defer cleanup()

	g := &Generator{queries: queries}
	cap := &captureLLM{}

	highlights := []digestItem{{
		Item: store.Item{
			ID: 1, FeedID: 7, Title: "Failed extraction",
			Url: "https://example.com/a", Points: 5,
			// Both fields populated exactly as the poller leaves them after a failure.
			ExtractStatus:  "failed",
			ExtractContent: "[2026-08-14 16:47] jina HTTP 403\n[2026-08-14 17:47] jina HTTP 403",
			Content:        "<p>The real article body the reader should see.</p>",
		},
		Highlights: 1,
	}}

	if _, err := g.buildContentWithLLM(context.Background(), digestConfigJSON{}, highlights, nil, slog.Default(), cap); err != nil {
		t.Fatalf("buildContentWithLLM: %v", err)
	}

	if strings.Contains(cap.user, "jina HTTP 403") {
		t.Error("the extraction error log was sent to the model as article content")
	}
	if !strings.Contains(cap.user, "The real article body") {
		t.Error("the item's original content was not sent; the model got nothing to summarize")
	}
}

// The mirror case, so the fix cannot be "always use Content": a successful
// extraction's body IS the content the model should see.
func TestPromptUsesExtractedBodyWhenExtractionSucceeded(t *testing.T) {
	_, queries, cleanup := setupDigestTest(t)
	defer cleanup()

	g := &Generator{queries: queries}
	cap := &captureLLM{}

	highlights := []digestItem{{
		Item: store.Item{
			ID: 1, FeedID: 7, Title: "Extracted story",
			Url: "https://example.com/a", Points: 5,
			ExtractStatus:  "done",
			ExtractContent: "<p>The extracted article body.</p>",
			Content:        "<p>Teaser from the feed.</p>",
		},
		Highlights: 1,
	}}

	if _, err := g.buildContentWithLLM(context.Background(), digestConfigJSON{}, highlights, nil, slog.Default(), cap); err != nil {
		t.Fatalf("buildContentWithLLM: %v", err)
	}

	if !strings.Contains(cap.user, "The extracted article body") {
		t.Error("the successful extraction body was not sent to the model")
	}
	if strings.Contains(cap.user, "Teaser from the feed") {
		t.Error("the raw teaser was sent even though a successful extraction exists")
	}
}

// A pending extraction holds no usable body: the item's own Content must be used.
func TestPromptUsesOriginalBodyWhileExtractionPending(t *testing.T) {
	_, queries, cleanup := setupDigestTest(t)
	defer cleanup()

	g := &Generator{queries: queries}
	cap := &captureLLM{}

	highlights := []digestItem{{
		Item: store.Item{
			ID: 1, FeedID: 7, Title: "Pending",
			Url: "https://example.com/a", Points: 5,
			ExtractStatus:  "pending",
			ExtractContent: "pending",
			Content:        "<p>Body available right now.</p>",
		},
		Highlights: 1,
	}}

	if _, err := g.buildContentWithLLM(context.Background(), digestConfigJSON{}, highlights, nil, slog.Default(), cap); err != nil {
		t.Fatalf("buildContentWithLLM: %v", err)
	}
	if !strings.Contains(cap.user, "Body available right now") {
		t.Error("the item's own content was not sent while extraction is pending")
	}
	if strings.Contains(cap.user, "Content: pending") {
		t.Error("the status word was sent as the article body")
	}
}
