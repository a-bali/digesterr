package httpapi

import (
	"strings"
	"testing"
)

// digestWithComments builds a minimal digest result content JSON whose highlight
// carries a top comment and an LLM summary containing the shapes found in
// stored digests: a bare URL and an entity-encoded query separator.
const digestWithComments = `{"title":"Go Time","summary":"Today's top stories","highlights":[` +
	`{"item_id":1,"feed_id":1,"feed_name":"Hacker News","title":"Top HN story",` +
	`"url":"https://news.ycombinator.com/item?id=1","comment_url":"https://news.ycombinator.com/item?id=1",` +
	`"points":100,"comments_count":50,"published_at":"2024-01-01T00:00:00Z",` +
	`"llm_summary":"See https://example.com/analysis for the details.",` +
	`"top_comments":[{"author":"reticulates","points":12,"url":"https://news.ycombinator.com/item?id=2",` +
	`"text":"No, it doesn't.\nhttps://colo.to/exercise.pdf\nThey exercised 15,625 options."},` +
	`{"author":"peri-cl","points":4,"url":"https://news.ycombinator.com/item?id=3",` +
	`"text":"[Itt](https://www.youtube.com/live/BU6rXYv726I?si=U50DG_vDKNazLq92&amp;t=30918) is the video"}]}]}`

func TestRenderDigestHTMLLinkifiesCommentURLs(t *testing.T) {
	got := renderDigestHTML(digestWithComments, "https://digesterr.example")

	// Bare URL in a comment body.
	if !strings.Contains(got, `<a href="https://colo.to/exercise.pdf" target="_blank" rel="noopener noreferrer">https://colo.to/exercise.pdf</a>`) {
		t.Fatalf("expected bare comment URL linked, got: %s", got)
	}
	// Markdown link in a comment body: label is the link text, not the URL.
	if !strings.Contains(got, `<a href="https://www.youtube.com/live/BU6rXYv726I?si=U50DG_vDKNazLq92&amp;t=30918" target="_blank" rel="noopener noreferrer">Itt</a>`) {
		t.Fatalf("expected markdown comment link rendered with its label, got: %s", got)
	}
	// The query separator must survive as one `&`, not `&amp;amp;` (the previous
	// pipeline escaped and then decoded, producing an href whose parameter was
	// literally named `amp;t`).
	if strings.Contains(got, "&amp;amp;") {
		t.Fatalf("comment link href was double-escaped, got: %s", got)
	}
}

func TestRenderDigestHTMLLinkifiesSummaryURLs(t *testing.T) {
	got := renderDigestHTML(digestWithComments, "https://digesterr.example")
	if !strings.Contains(got, `href="https://example.com/analysis"`) {
		t.Fatalf("expected bare URL in the LLM summary linked, got: %s", got)
	}
}

func TestRenderDigestHTMLLeavesItemTitlesAlone(t *testing.T) {
	// Only comment bodies and summaries are linkified. A title that contains
	// something URL-like must not gain markup: it is rendered into an <h3>
	// anchor already, and a nested <a> would be invalid.
	content := `{"title":"T","summary":"","highlights":[{"item_id":1,"feed_id":1,"feed_name":"F",` +
		`"title":"Story about www.example.com and https://example.com/x","url":"https://example.com/story",` +
		`"points":1,"comments_count":1,"published_at":"2024-01-01T00:00:00Z",` +
		`"llm_summary":"","top_comments":[]}]}`
	got := renderDigestHTML(content, "https://digesterr.example")
	if !strings.Contains(got, `<h3><a href="https://example.com/story">Story about www.example.com and https://example.com/x</a></h3>`) {
		t.Fatalf("expected title rendered as a single anchor, got: %s", got)
	}
	// Two anchors: the title and the feed-name link. Nothing in the title text
	// gained markup of its own.
	if n := strings.Count(got, "<a href="); n != 2 {
		t.Fatalf("expected 2 anchors (title + feed name), got %d: %s", n, got)
	}
}

func TestRenderDigestHTMLEscapesCommentMarkup(t *testing.T) {
	content := `{"title":"T","summary":"","highlights":[{"item_id":1,"feed_id":1,"feed_name":"F",` +
		`"title":"Story","url":"https://example.com/story","points":1,"comments_count":1,` +
		`"published_at":"2024-01-01T00:00:00Z","llm_summary":"",` +
		`"top_comments":[{"author":"mallory","text":"&lt;script&gt;alert(1)&lt;/script&gt;"}]}]}`
	got := renderDigestHTML(content, "https://digesterr.example")
	if strings.Contains(got, "<script>") {
		t.Fatalf("entity-encoded script became raw markup in the digest body: %s", got)
	}
}
