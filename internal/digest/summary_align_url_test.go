package digest

import (
	"encoding/json"
	"strings"
	"testing"

	"digesterr/internal/store"
)

// urlArticles returns articles with distinct URLs for URL-echo alignment tests.
func urlArticles() []digestItem {
	titles := []string{"First story", "Second story", "Third story"}
	items := make([]digestItem, len(titles))
	for i, t := range titles {
		items[i] = digestItem{Item: store.Item{
			ID:    int64(1 + i),
			Title: t,
			Url:   "https://example.com/story-" + strings.ToLower(strings.ReplaceAll(t, " ", "-")),
		}}
	}
	return items
}

// TestAlignURLEchoParaphrasedTitles is the core URL-key case: entry titles
// paraphrased beyond title matching still land correctly via exact URL echo.
func TestAlignURLEchoParaphrasedTitles(t *testing.T) {
	articles := urlArticles()
	entries := []llmHighlight{
		{Title: "Something about whales", Summary: "first", URL: "https://example.com/story-first-story"},
		{Title: "Pottery techniques", Summary: "second", URL: "https://example.com/story-second-story"},
		{Title: "Unrelated chatter", Summary: "third", URL: "https://example.com/story-third-story"},
	}
	assigned, missing := alignHighlights(articles, entries)
	if missing != 0 {
		t.Fatalf("missing = %d, want 0: URL echo must place paraphrased entries", missing)
	}
	for i, entryIdx := range assigned {
		want := []string{"first", "second", "third"}[i]
		if got := entries[entryIdx].Summary; got != want {
			t.Errorf("article %d summary = %q, want %q", i, got, want)
		}
	}
}

// TestAlignURLEchoMistypedURLFallsBackToTitle checks that a mistyped URL is
// dropped to title matching, never guessed at: the entry with a distinctive
// title still lands via title match, while an entry whose URL is unknown
// and whose title matches nothing leaves its article unassigned.
func TestAlignURLEchoMistypedURLFallsBackToTitle(t *testing.T) {
	articles := []digestItem{
		{Item: store.Item{ID: 1, Title: "Zebra migration patterns", Url: "https://example.com/zebra"}},
		{Item: store.Item{ID: 2, Title: "Quantum baking recipes", Url: "https://example.com/baking"}},
		{Item: store.Item{ID: 3, Title: "Sourdough starter science", Url: "https://example.com/sourdough"}},
	}
	entries := []llmHighlight{
		{Title: "Quantum baking recipes", Summary: "second", URL: "https://example.com/BAKING-typo"},
		{Title: "Completely unrelated whales", Summary: "unrelated", URL: "https://example.com/nope"},
	}
	assigned, missing := alignHighlights(articles, entries)
	if missing != 2 {
		t.Fatalf("missing = %d, want 2 (only the baking entry lands, via title)", missing)
	}
	if assigned[1] == unassigned || entries[assigned[1]].Summary != "second" {
		t.Errorf("article 1 = %d, want the baking entry via title fallback", assigned[1])
	}
	if assigned[0] != unassigned || assigned[2] != unassigned {
		t.Errorf("articles 0/2 = %d/%d, want unassigned (whales matches nothing)", assigned[0], assigned[2])
	}
}

// TestAlignURLEchoDuplicateClaimResolvesToNothing covers two entries echoing
// the same URL: the collision is unresolvable by URL, so neither anchors on
// it (title matching may still place them).
func TestAlignURLEchoDuplicateClaimResolvesToNothing(t *testing.T) {
	articles := urlArticles()
	entries := []llmHighlight{
		{Title: "First story", Summary: "first", URL: "https://example.com/story-first-story"},
		{Title: "First story retold", Summary: "first-dup", URL: "https://example.com/story-first-story"},
	}
	assigned, missing := alignHighlights(articles, entries)
	if missing != 1 {
		t.Fatalf("missing = %d, want 1 (one URL, two claimants — one stays out)", missing)
	}
	if assigned[0] == unassigned {
		t.Error("article 0 unassigned, want the first claimant placed")
	}
}

// TestAlignArticleNumberAsString is the virtual-free wire shape: entries
// numbered {"article":"1"} (string) must decode and align like numbers.
func TestAlignArticleNumberAsString(t *testing.T) {
	articles := []digestItem{
		{Item: store.Item{ID: 1, Title: "First story"}},
		{Item: store.Item{ID: 2, Title: "Second story"}},
		{Item: store.Item{ID: 3, Title: "Third story"}},
	}
	raw := `{"title":"T","summary":"s","highlights":[
		{"article":"3","title":"zzz","summary":"third"},
		{"article":"1","title":"aaa","summary":"first"},
		{"article":"2","title":"bbb","summary":"second"}]}`
	var output llmDigestOutput
	if err := json.Unmarshal([]byte(raw), &output); err != nil {
		t.Fatalf("decode string-numbered reply: %v", err)
	}
	assigned, missing := alignHighlights(articles, output.Highlights)
	if missing != 0 {
		t.Fatalf("missing = %d, want 0", missing)
	}
	for i, want := range []string{"first", "second", "third"} {
		if got := output.Highlights[assigned[i]].Summary; got != want {
			t.Errorf("article %d summary = %q, want %q", i, got, want)
		}
	}
}

// TestAlignArticleNumberGarbageIsMalformed pins the boundary: a genuinely
// non-numeric article value is a malformed reply, not a silent zero.
func TestAlignArticleNumberGarbageIsMalformed(t *testing.T) {
	raw := `{"title":"T","summary":"s","highlights":[{"article":"one","title":"x","summary":"y"}]}`
	var output llmDigestOutput
	if err := json.Unmarshal([]byte(raw), &output); err == nil {
		t.Error("expected a decode error for article:\"one\", got nil")
	}
}

// TestAlignURLBeatsTitleMismatch checks key precedence: when the URL echo
// points at article A but the paraphrased title happens to token-match
// article B, the URL wins.
func TestAlignURLBeatsTitleMismatch(t *testing.T) {
	articles := []digestItem{
		{Item: store.Item{ID: 1, Title: "Rust coreutils migration lands", Url: "https://example.com/rust"}},
		{Item: store.Item{ID: 2, Title: "Small programming tricks", Url: "https://example.com/tricks"}},
	}
	entries := []llmHighlight{
		{Title: "Small programming tricks with Rust", Summary: "about rust", URL: "https://example.com/rust"},
		{Title: "Small programming tricks", Summary: "about tricks", URL: "https://example.com/tricks"},
	}
	assigned, missing := alignHighlights(articles, entries)
	if missing != 0 {
		t.Fatalf("missing = %d, want 0", missing)
	}
	if got := entries[assigned[0]].Summary; got != "about rust" {
		t.Errorf("article 0 summary = %q, want %q (URL over title-tokens)", got, "about rust")
	}
}
