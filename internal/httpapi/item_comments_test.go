package httpapi

import (
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
	// Reddit HTML-encodes > < & in comment bodies.
	text := "&gt;quoted text&lt; with &amp; ampersand"
	got := renderCommentText(text, "reddit")
	if !strings.Contains(got, ">quoted text<") {
		t.Fatalf("expected decoded entities, got: %s", got)
	}
	if !strings.Contains(got, "& ampersand") {
		t.Fatalf("expected decoded ampersand, got: %s", got)
	}
	// Should NOT be double-escaped.
	if strings.Contains(got, "&gt;") {
		t.Fatalf("entities were not decoded, got: %s", got)
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
