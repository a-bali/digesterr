package digest

import (
	"context"
	"log/slog"
	"strings"
	"testing"

	"digesterr/internal/feed"
	"digesterr/internal/store"
)

// captureLLM records the prompts buildContentWithLLM sends and answers with
// an empty-but-valid digest reply.
type captureLLM struct {
	system, user string
}

func (c *captureLLM) Chat(_ context.Context, systemPrompt, userPrompt string) (string, error) {
	c.system, c.user = systemPrompt, userPrompt
	return `{"title":"T","summary":"s","highlights":[]}`, nil
}

// TestPromptShapeNoRestBlockCommentsFolded pins the prompt contract from the
// 2026-09-18 review: non-highlight items are never sent to the model, and
// the format instruction tells the model to fold top comments into each
// highlight.
func TestPromptShapeNoRestBlockCommentsFolded(t *testing.T) {
	_, queries, cleanup := setupDigestTest(t)
	defer cleanup()
	ctx := context.Background()

	g := &Generator{queries: queries}
	cap := &captureLLM{}

	highlights := []digestItem{{
		Item: store.Item{
			ID: 1, FeedID: 7, Title: "Alpha story",
			Url: "https://example.com/alpha", Points: 100, CommentsCount: 3,
		},
		Highlights: 5,
		Comments:   3,
		FetchedComments: []feed.Comment{
			{Author: "alice", Text: "good point", Points: 5, ValidPoints: true},
		},
	}}
	rest := []digestItem{{
		Item: store.Item{
			ID: 2, FeedID: 7, Title: "Beta context story",
			Url: "https://example.com/beta", Points: 10, CommentsCount: 0,
		},
	}}

	if _, err := g.buildContentWithLLM(ctx, digestConfigJSON{}, highlights, rest, slog.Default(), cap); err != nil {
		t.Fatalf("buildContentWithLLM: %v", err)
	}

	if strings.Contains(cap.user, "Additional articles") {
		t.Error("prompt sends a non-highlight context block; rest must not be sent to the model")
	}
	if strings.Contains(cap.user, "Beta context story") {
		t.Error("prompt contains a non-highlight title; rest must not be sent to the model")
	}
	if !strings.Contains(cap.user, "Top comments:") || !strings.Contains(cap.user, "alice") {
		t.Error("prompt dropped the highlight's fetched comments")
	}
	for _, want := range []string{
		"fold what they add into the highlight",
		"surface corrections, counterpoints",
		"{article, url, title, summary}",
		"exactly one entry per article",
	} {
		if !strings.Contains(cap.user, want) {
			t.Errorf("format instruction missing %q", want)
		}
	}
	if !strings.Contains(cap.user, "(1..1)") {
		t.Error("format instruction does not carry the article range (1..1)")
	}
}
