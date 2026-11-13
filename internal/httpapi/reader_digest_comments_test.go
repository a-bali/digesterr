package httpapi

import (
	"strings"
	"testing"

	"digesterr/internal/feed"
)

// The digest highlight's comments and a feed item's comments must render
// identically. They used to be separate implementations: the feed-item path
// emitted a quote-bar list (border-left, no bullet, tight spacing) while the
// digest path emitted a bare `<ul>` with default list indentation, which read
// as a different and more padded component. These tests pin both surfaces to
// the same markup so they cannot drift apart again.

// TestDigestHighlightCommentsUseSharedQuoteBarMarkup asserts the digest
// renderer emits the quote-bar list item, not its own looser list.
func TestDigestHighlightCommentsUseSharedQuoteBarMarkup(t *testing.T) {
	got := renderDigestHTML(digestWithComments, "https://digesterr.example")

	// The quote bar is the defining visual: a left border with no bullet.
	if !strings.Contains(got, `border-left:2px solid #ddd`) {
		t.Fatalf("digest comments missing the quote bar, got: %s", got)
	}
	// Author in bold, score as a muted parenthetical, body in a reduced span.
	if !strings.Contains(got, `<strong><a href="https://news.ycombinator.com/item?id=2">reticulates</a></strong>`) {
		t.Fatalf("digest comment author not rendered as a bold link, got: %s", got)
	}
	if !strings.Contains(got, `<span style="color:#888;font-size:0.85em">(12)</span>`) {
		t.Fatalf("digest comment points not rendered as a muted parenthetical, got: %s", got)
	}
	// The wrapper must suppress default list indentation, which is where the
	// wasted whitespace came from.
	if !strings.Contains(got, `<ul style="list-style:none;padding:0;margin:0">`) {
		t.Fatalf("digest comment list does not suppress default list styling, got: %s", got)
	}
	// The old rendering used an em dash between author and text.
	if strings.Contains(got, " — ") {
		t.Fatalf("digest comments still use the old em-dash layout, got: %s", got)
	}
	// The old rendering had no wrapper div.
	if !strings.Contains(got, `<div class="digest-comments"`) {
		t.Fatalf("digest comments missing the wrapper div, got: %s", got)
	}
}

// TestCommentMarkupIsIdenticalOnBothSurfaces is the direct anti-drift check:
// the same comment fed through the feed-item path and through the digest path
// must produce byte-identical list markup.
func TestCommentMarkupIsIdenticalOnBothSurfaces(t *testing.T) {
	const author = "reticulates"
	const text = "A comment body with a link https://example.com/x"
	const url = "https://news.ycombinator.com/item?id=2"
	const points = 12

	fromItem := topCommentsHTML([]feed.Comment{
		{Author: author, Text: text, Points: points, ValidPoints: true, URL: url},
	}, "hn")

	digestJSON := `{"title":"T","summary":"s","highlights":[{"item_id":1,"feed_id":1,"feed_name":"HN",` +
		`"title":"X","url":"https://example.com/a","points":100,"comments_count":5,` +
		`"top_comments":[{"author":"` + author + `","points":` + itoaForTest(points) + `,"url":"` + url + `",` +
		`"text":"` + text + `"}]}]}`
	fromDigest := renderDigestHTML(digestJSON, "https://digesterr.example")

	// Extract the single <li> from each and compare.
	itemLI := extractLI(t, fromItem)
	digestLI := extractLI(t, fromDigest)

	if itemLI != digestLI {
		t.Fatalf("comment markup differs between surfaces:\n item  : %s\n digest: %s", itemLI, digestLI)
	}
}

// TestDigestCommentWithoutPointsOmitsScore covers the nil-Points case: a digest
// that recorded no score for a comment must not render a misleading "(0)".
func TestDigestCommentWithoutPointsOmitsScore(t *testing.T) {
	content := `{"title":"T","summary":"s","highlights":[{"item_id":1,"feed_id":1,"feed_name":"HN",` +
		`"title":"X","url":"https://example.com/a","points":100,"comments_count":5,` +
		`"top_comments":[{"author":"someone","url":"https://example.com/c","text":"no score here"}]}]}`
	got := renderDigestHTML(content, "https://digesterr.example")

	if !strings.Contains(got, "no score here") {
		t.Fatalf("comment text missing, got: %s", got)
	}
	if strings.Contains(got, "(0)") {
		t.Fatalf("comment without a recorded score rendered a misleading (0), got: %s", got)
	}
}

// TestFeedItemCommentsKeepQuoteBar is a guard on the feed-item path itself, so
// a refactor of the shared helper cannot silently change the reference look.
func TestFeedItemCommentsKeepQuoteBar(t *testing.T) {
	got := topCommentsHTML([]feed.Comment{
		{Author: "a", Text: "b", Points: 3, ValidPoints: true, URL: "https://example.com/c"},
	}, "hn")
	for _, want := range []string{
		`<div class="digest-comments"`,
		`<ul style="list-style:none;padding:0;margin:0">`,
		`border-left:2px solid #ddd`,
		`<strong>`,
		`(3)`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("feed-item comments missing %q, got: %s", want, got)
		}
	}
	// Zero points with ValidPoints=false must not render a score.
	noScore := topCommentsHTML([]feed.Comment{{Author: "a", Text: "b"}}, "hn")
	if strings.Contains(noScore, "(0)") {
		t.Errorf("feed-item comment without points rendered (0): %s", noScore)
	}
}

// extractLI returns the first <li>...</li> substring of s.
func extractLI(t *testing.T, s string) string {
	t.Helper()
	start := strings.Index(s, "<li ")
	if start < 0 {
		t.Fatalf("no <li> in %q", s)
	}
	end := strings.Index(s[start:], "</li>")
	if end < 0 {
		t.Fatalf("unterminated <li> in %q", s)
	}
	return s[start : start+end+len("</li>")]
}

func itoaForTest(n int) string {
	if n == 0 {
		return "0"
	}
	var digits []byte
	neg := n < 0
	if neg {
		n = -n
	}
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	if neg {
		return "-" + string(digits)
	}
	return string(digits)
}
