package httpapi

import (
	"strings"
	"testing"
)

// TestNormalizeDetail covers how a request's detail level is resolved. The UI
// names it explicitly; a caller that only sends a length window gets the level
// inferred so that older clients keep producing the right character.
func TestNormalizeDetail(t *testing.T) {
	cases := []struct {
		requested string
		max       int
		want      string
	}{
		// Explicit wins over the window.
		{"gist", 18, detailGist},
		{"balanced", 2, detailBalanced},
		{"detailed", 1, detailDetailed},
		{"DETAILED", 1, detailDetailed},
		{"  gist  ", 18, detailGist},
		// Derived from the window when absent or unrecognised.
		{"", 2, detailGist},
		{"", 3, detailGist},
		{"", 5, detailBalanced},
		{"", 7, detailBalanced},
		{"", 9, detailBalanced},
		{"", 12, detailDetailed},
		{"", 18, detailDetailed},
		{"", 0, detailGist},
		{"nonsense", 12, detailDetailed},
	}
	for _, c := range cases {
		if got := normalizeDetail(c.requested, c.max); got != c.want {
			t.Errorf("normalizeDetail(%q, %d) = %q, want %q", c.requested, c.max, got, c.want)
		}
	}
}

// TestDetailStylesAreDistinct guards the point of the feature: the three levels
// must ask for visibly different things, not the same summary at three lengths.
func TestDetailStylesAreDistinct(t *testing.T) {
	seen := map[string]string{}
	for _, d := range []string{detailGist, detailBalanced, detailDetailed} {
		s := detailStyle(d)
		if s == "" {
			t.Fatalf("detailStyle(%q) is empty", d)
		}
		if prev, dup := seen[s]; dup {
			t.Errorf("detailStyle(%q) is identical to detailStyle(%q)", d, prev)
		}
		seen[s] = d
	}
	// An unknown level falls back to the balanced wording.
	if detailStyle("nonsense") != detailStyle(detailBalanced) {
		t.Error("an unknown detail level should fall back to the balanced style")
	}
}

func TestRenderSummaryPromptSubstitutesStyle(t *testing.T) {
	tmpl := "Content in {content_sentences}, comments in {comment_sentences}. Aim for {style}."
	got := renderSummaryPrompt(tmpl, detailStyle(detailGist), "1-2", "1")
	want := "Content in 1-2, comments in 1. Aim for a one-line gist: only the single most important takeaway, with no supporting detail."
	if got != want {
		t.Errorf("renderSummaryPrompt()\n got %q\nwant %q", got, want)
	}
}

// A custom prompt written before {style} existed must keep working unchanged.
func TestRenderSummaryPromptWithoutStylePlaceholder(t *testing.T) {
	tmpl := "Content in {content_sentences} sentences."
	got := renderSummaryPrompt(tmpl, "ignored", "5-7", "1-2")
	if got != "Content in 5-7 sentences." {
		t.Errorf("renderSummaryPrompt() = %q", got)
	}
	if strings.Contains(got, "ignored") {
		t.Error("the style text leaked into a prompt that has no {style} placeholder")
	}
}

// The default prompt must actually carry the style instruction, or every
// preset would fall back to differing by length alone again.
func TestDefaultPromptCarriesStyle(t *testing.T) {
	rendered := renderSummaryPrompt(defaultSummaryPrompt, detailStyle(detailGist), "1-2", "1")
	if strings.Contains(rendered, "{style}") {
		t.Error("the default prompt still contains an unsubstituted {style}")
	}
	if !strings.Contains(rendered, "one-line gist") {
		t.Errorf("the default prompt does not mention the style: %q", rendered)
	}
}
