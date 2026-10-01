package httpapi

import (
	"html"
	"strings"
	"testing"
	"time"

	"digesterr/internal/feed"
)

func TestCommentCache(t *testing.T) {
	c := newCommentCache(50 * time.Millisecond)

	if _, ok := c.get(1); ok {
		t.Fatal("expected cache miss on empty cache")
	}

	comments := []feed.Comment{
		{Author: "alice", Text: "hello", Points: 5},
		{Author: "bob", Text: "world", Points: 3},
	}
	c.set(1, comments)

	got, ok := c.get(1)
	if !ok {
		t.Fatal("expected cache hit")
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 comments, got %d", len(got))
	}
	if got[0].Author != "alice" {
		t.Fatalf("expected author alice, got %s", got[0].Author)
	}

	// Mutating the returned slice should not affect cache.
	got[0].Author = "mutated"
	got2, _ := c.get(1)
	if got2[0].Author != "alice" {
		t.Fatal("cache was mutated through returned slice")
	}

	time.Sleep(60 * time.Millisecond)
	if _, ok := c.get(1); ok {
		t.Fatal("expected cache miss after expiry")
	}
}

func TestRenderCommentTextPlain(t *testing.T) {
	got := renderCommentText("Hello <b>world</b> & \"friends\"", "hn")
	if !strings.Contains(got, "&lt;b&gt;") {
		t.Fatalf("expected HTML escaped, got: %s", got)
	}
}

func TestRenderCommentTextRedditMarkdownImage(t *testing.T) {
	text := "Great post! ![screenshot](https://i.redd.it/abc123.jpg) shows the issue."
	got := renderCommentText(text, "reddit")

	if !strings.Contains(got, `<img src="https://i.redd.it/abc123.jpg"`) {
		t.Fatalf("expected img tag, got: %s", got)
	}
	if !strings.Contains(got, `max-width:400px`) {
		t.Fatalf("expected size constraints, got: %s", got)
	}
	if !strings.Contains(got, `alt="screenshot"`) {
		t.Fatalf("expected alt text, got: %s", got)
	}
}

func TestRenderCommentTextRedditBareGiphy(t *testing.T) {
	// media.giphy.com (standard)
	text := "Check this out https://media.giphy.com/media/xT9IgzoKnwFNmISR8I/giphy.gif funny right"
	got := renderCommentText(text, "reddit")
	if !strings.Contains(got, `<img src="https://media.giphy.com/media/xT9IgzoKnwFNmISR8I/giphy.gif"`) {
		t.Fatalf("expected giphy img tag, got: %s", got)
	}
	if !strings.Contains(got, "Check this out") {
		t.Fatalf("expected surrounding text preserved, got: %s", got)
	}
}

func TestRenderCommentTextRedditInternalGiphy(t *testing.T) {
	// Reddit's internal giphy syntax: ![gif](giphy|ID)
	text := "![gif](giphy|whhCAcuxtAXcrQhHTk)\n\n"
	got := renderCommentText(text, "reddit")
	if !strings.Contains(got, `<img src="https://media.giphy.com/media/whhCAcuxtAXcrQhHTk/giphy.gif"`) {
		t.Fatalf("expected converted giphy img tag, got: %s", got)
	}
}

func TestRenderCommentTextRedditBareGiphyNumberedSubdomain(t *testing.T) {
	// media0/media1.giphy.com (numbered subdomains)
	text := "lol https://media1.giphy.com/media/l0HlBO7eyXzSZkJri/giphy.gif absolute classic"
	got := renderCommentText(text, "reddit")
	if !strings.Contains(got, `<img src="https://media1.giphy.com/media/l0HlBO7eyXzSZkJri/giphy.gif"`) {
		t.Fatalf("expected media1 giphy img tag, got: %s", got)
	}
}

func TestRenderCommentTextRedditGiphyShareURL(t *testing.T) {
	// giphy.com/gifs/... share URL (no file extension)
	text := "see https://giphy.com/gifs/pointless-ken-jeong-tingler-3ohhwxmNcPvwyRqYKI"
	got := renderCommentText(text, "reddit")
	if !strings.Contains(got, `<img src="https://giphy.com/gifs/pointless-ken-jeong-tingler-3ohhwxmNcPvwyRqYKI"`) {
		t.Fatalf("expected giphy share URL img tag, got: %s", got)
	}
}

func TestRenderCommentTextRedditBareImgur(t *testing.T) {
	text := "look at this https://i.imgur.com/abc123.png pretty cool"
	got := renderCommentText(text, "reddit")

	if !strings.Contains(got, `<img src="https://i.imgur.com/abc123.png"`) {
		t.Fatalf("expected imgur img tag, got: %s", got)
	}
}

func TestRenderCommentTextRedditHTMLEntities(t *testing.T) {
	// Reddit HTML-encodes > < & in comment bodies. They must reach the reader as
	// the characters themselves — decoding them is what makes a blockquote
	// marker render as `>` instead of the literal text `&gt;`.
	text := "&gt;quoted text&lt; with &amp; ampersand"
	got := renderCommentText(text, "reddit")
	// Assert on the rendered text: entities in the source are decoded by the
	// client, so asserting the source spelling would pass for either encoding.
	visible := html.UnescapeString(strings.ReplaceAll(got, "<br>", "\n"))
	if !strings.Contains(visible, ">quoted text< with & ampersand") {
		t.Fatalf("expected decoded entities, got: %s (rendered: %s)", got, visible)
	}
	if strings.Contains(got, "&amp;amp;") {
		t.Fatalf("entities were encoded twice, got: %s", got)
	}
}

// Regression: the previous pipeline escaped the text and THEN decoded a fixed
// list of double-escaped entities, which turned an entity-encoded `<script>`
// back into a raw tag inside the Reader API body — a feed reader renders that
// body as HTML. The old escaping test missed it because it only used a RAW
// `<script>`, which the escape pass genuinely did neutralize.
func TestRenderCommentTextDoesNotUnescapeIntoMarkup(t *testing.T) {
	for _, text := range []string{
		"&lt;script&gt;alert(1)&lt;/script&gt;",
		"&lt;img src=x onerror=alert(1)&gt;",
		"&lt;a href=\"javascript:alert(1)\"&gt;click&lt;/a&gt;",
	} {
		got := renderCommentText(text, "reddit")
		for _, raw := range []string{"<script>", "<img", "<a href"} {
			if strings.Contains(got, raw) {
				t.Fatalf("entity-encoded markup %q became raw %q in output: %s", text, raw, got)
			}
		}
	}
}

func TestRenderCommentTextLinkifiesBareURL(t *testing.T) {
	// Real fixture: an HN comment with a bare URL and no markup at all.
	got := renderCommentText("No, it doesn't.\nhttps://colo.to/exercise.pdf\nThey exercised 15,625.", "hn")
	if !strings.Contains(got, `<a href="https://colo.to/exercise.pdf" target="_blank" rel="noopener noreferrer">https://colo.to/exercise.pdf</a>`) {
		t.Fatalf("expected bare URL linked, got: %s", got)
	}
}

func TestRenderCommentTextMarkdownLink(t *testing.T) {
	// Real fixture: a Reddit comment with a markdown link.
	got := renderCommentText("[kifli.hu](http://kifli.hu)", "reddit")
	if !strings.Contains(got, `<a href="http://kifli.hu" target="_blank" rel="noopener noreferrer">kifli.hu</a>`) {
		t.Fatalf("expected markdown link rendered, got: %s", got)
	}
}

func TestRenderCommentTextMarkdownLinkKeepsQuerySeparator(t *testing.T) {
	// Real fixture: the ampersand arrives entity-encoded and must survive as a
	// single `&` in the href, not `&amp;amp;`.
	got := renderCommentText("[Itt](https://www.youtube.com/live/BU6rXYv726I?si=U50DG_vDKNazLq92&amp;t=30918)", "reddit")
	if !strings.Contains(got, `href="https://www.youtube.com/live/BU6rXYv726I?si=U50DG_vDKNazLq92&amp;t=30918"`) {
		t.Fatalf("expected single-escaped href, got: %s", got)
	}
	if strings.Contains(got, "&amp;amp;") {
		t.Fatalf("href was double-escaped, got: %s", got)
	}
}

func TestRenderCommentTextRedditHTMLEscaping(t *testing.T) {
	// Actual HTML in comment should still be escaped after entity decoding.
	text := "Nice <script>alert(1)</script> comment"
	got := renderCommentText(text, "reddit")
	if strings.Contains(got, "<script>") {
		t.Fatalf("HTML was not escaped, got: %s", got)
	}
}

func TestRenderCommentTextRedditNoImages(t *testing.T) {
	text := "Just a normal comment with no images."
	got := renderCommentText(text, "reddit")
	if !strings.Contains(got, "Just a normal comment") {
		t.Fatalf("expected text preserved, got: %s", got)
	}
}

func TestTopCommentsHTMLWithFeedType(t *testing.T) {
	comments := []feed.Comment{
		{Author: "alice", Text: "![img](https://example.com/pic.jpg)", Points: 5, ValidPoints: true, URL: "https://example.com/comment/1"},
	}
	got := topCommentsHTML(comments, "reddit")
	if !strings.Contains(got, `<img src="https://example.com/pic.jpg"`) {
		t.Fatalf("expected img tag in HTML output, got: %s", got)
	}
	if !strings.Contains(got, `class="digest-comments"`) {
		t.Fatalf("expected digest-comments class, got: %s", got)
	}
}
