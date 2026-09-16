package digest

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"strings"
	"testing"
)

// TestBuildContentLiveGatewayAlignment drives the real buildContent against the
// configured gateway using the shipped prompt, then asserts the invariant that
// the prod bug violated: every summary that rendered belongs to the article it
// rendered under. Skipped unless LLM_ENDPOINT and LLM_API_KEY are set, so it
// never fails CI on a machine with no gateway.
//
// Run with: go test ./internal/digest -run TestBuildContentLiveGatewayAlignment -v
func TestBuildContentLiveGatewayAlignment(t *testing.T) {
	endpoint := os.Getenv("LLM_ENDPOINT")
	apiKey := os.Getenv("LLM_API_KEY")
	model := os.Getenv("LLM_MODEL")
	if endpoint == "" || apiKey == "" {
		t.Skip("LLM env vars not set")
	}
	if model == "" {
		model = "auto"
	}

	_, queries, cleanup := setupDigestTest(t)
	defer cleanup()
	ctx := context.Background()

	articles := summaryArticles()
	// Give each article a body so the prompt resembles a real digest.
	for i := range articles {
		articles[i].Content = "Body text for " + articles[i].Title + ". " + strings.Repeat("Detail. ", 20)
	}

	genLogger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	g := &Generator{
		queries: queries,
		llm:     NewLLMClient(endpoint, apiKey, model),
	}

	content, err := g.buildContent(ctx, digestConfigJSON{
		DefaultComments: 3,
	}, articles, nil, genLogger)
	if err != nil {
		t.Fatalf("buildContent against live gateway: %v", err)
	}

	// Distinctive subject per article, used to check where a summary landed.
	subject := []string{"Jev", "Wayback", "Gemini", "GPU", "License", "GEFS", "A/I", "Tricks", "Hotspot", "Ubuntu"}
	lower := func(s string) string { return strings.ToLower(s) }

	summarized, blank := 0, 0
	for i, h := range content.Highlights {
		if h.LLMSummary == "" {
			blank++
			t.Logf("article %d %q: NO SUMMARY (acceptable)", i, h.Title)
			continue
		}
		summarized++
		// The summary must not be dominated by another article's subject.
		ownText := lower(h.Title + " " + h.LLMSummary)
		ownHits := strings.Count(ownText, lower(subject[i]))
		for j, other := range subject {
			if j == i {
				continue
			}
			if strings.Count(ownText, lower(other)) >= 1 && ownHits == 0 {
				t.Errorf("article %d %q carries a summary about %q: %q",
					i, h.Title, articles[j].Title, h.LLMSummary)
				break
			}
		}
	}
	t.Logf("live run: %d articles, %d summarized, %d blank", len(content.Highlights), summarized, blank)

	// The digest must still be usable: title, summary and one entry per article.
	if content.Title == "" {
		t.Error("digest title is empty")
	}
	if content.Summary == "" {
		t.Error("digest summary is empty")
	}
	if len(content.Highlights) != len(articles) {
		t.Errorf("highlights = %d, want %d", len(content.Highlights), len(articles))
	}
	if summarized == 0 {
		t.Error("no article received a summary at all")
	}

	// Sections must agree with the flat array.
	for _, sec := range content.Sections {
		for _, h := range sec.Highlights {
			for _, flat := range content.Highlights {
				if flat.ItemID == h.ItemID && flat.LLMSummary != h.LLMSummary {
					t.Errorf("section summary for %q = %q, flat array has %q",
						h.Title, h.LLMSummary, flat.LLMSummary)
				}
			}
		}
	}

	raw, _ := json.MarshalIndent(content, "", "  ")
	t.Logf("content:\n%s", string(raw)[:min(2000, len(raw))])
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
