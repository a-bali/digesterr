package feed

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestExcerptText(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{
			name: "plain text",
			body: "<p>Hello world</p>",
			want: "Hello world",
		},
		{
			name: "empty body",
			body: "",
			want: "",
		},
		{
			// The WordPress shape: the body opens with a YouTube iframe whose
			// fallback children are RAW TEXT. They must not reach the excerpt.
			name: "raw-text iframe fallback markup is dropped",
			body: `<p><iframe src="https://www.youtube.com/embed/abc">` +
				`<span data-mce-type="bookmark" id="mce_SELRES_start">&#65279;</span></iframe></p>` +
				`<p>Real article opening words</p>`,
			want: "Real article opening words",
		},
		{
			name: "script and style content is dropped",
			body: `<style>.a{color:red}</style><script>var x=1</script><p>Body text</p>`,
			want: "Body text",
		},
		{
			// Words must not glue across block boundaries.
			name: "block boundaries separate words",
			body: "<p>one</p><p>two</p>",
			want: "one two",
		},
		{
			name: "br separates words",
			body: "<p>line one<br>line two</p>",
			want: "line one line two",
		},
		{
			name: "caption and body do not glue",
			body: `<div class="wp-caption"><p>Caption</p></div><p>here</p><p>body</p>`,
			want: "Caption here body",
		},
		{
			name: "inline elements stay joined",
			body: "<p>Hello <b>bold</b> and <i>italic</i> text</p>",
			want: "Hello bold and italic text",
		},
		{
			name: "display:none subtree is excluded",
			body: `<div style="display: none">hidden junk</div><p>Visible body</p>`,
			want: "Visible body",
		},
		{
			name: "visibility:hidden subtree is excluded",
			body: `<div style="visibility: hidden">hidden junk</div><p>Visible body</p>`,
			want: "Visible body",
		},
		{
			// Deliberate divergence from the frontend helper, which showed this
			// text: an SVG <title> is a TOOLTIP, not flow text, and it leaked
			// into the row (a loading spinner's "Loader" became the excerpt of
			// real articles). SVG <text> IS flow text and is kept.
			name: "svg title is dropped as tooltip text, svg text is kept",
			body: `<svg><title>Diagram of the thing</title><text>Rendered label</text></svg><p>Body</p>`,
			want: "Rendered label Body",
		},
		{
			// An image/video-only post has no visible text at all. That is a
			// legitimately empty excerpt, not a bug to paper over.
			name: "media-only body yields no excerpt",
			body: `<p><img src="https://i.redd.it/x.jpeg" loading="lazy" style="max-width:100%"></p>`,
			want: "",
		},
		{
			// A body whose first word sits behind kilobytes of srcset markup:
			// the excerpt must still find it.
			name: "text after attribute-heavy markup is found",
			body: `<p><img src="a.jpg" srcset="` + strings.Repeat("https://example.com/i,w_424,c_limit,f_webp,q_auto:good ", 60) +
				`"></p><p>Article opening sentence here</p>`,
			want: "Article opening sentence here",
		},
		{
			// Text exactly at the raw cap is the one shape the cap can lose: the
			// parser closes the truncated open element and the following tag's
			// line break lands first. Documented so the cap cannot shrink
			// unnoticed; at 8192 bytes it needs 8 KB of raw HTML with no text.
			name: "text flush against the raw cap is out of reach",
			body: strings.Repeat("x", ExcerptMaxBody) + `<p>Opening words</p>`,
			want: "xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx",
		},
		{
			name: "entities are decoded",
			body: "<p>Tom &amp; Jerry &lt;3</p>",
			want: "Tom & Jerry <3",
		},
		{
			name: "whitespace-only nodes do not stop the walk early",
			body: "<p>   </p>\n\n<div>   </div><p>Actual text</p>",
			want: "Actual text",
		},
		{
			name: "unclosed tags still parse",
			body: "<div><p>Truncated mid-doc",
			want: "Truncated mid-doc",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := excerptText(tt.body); got != tt.want {
				t.Errorf("excerptText() = %q, want %q", got, tt.want)
			}
		})
	}
}

// The excerpt feeds a one-line row: the visible text it collects must stay
// within the budget, so a whole-article body cannot drag the article in.
//
// The budget counts NON-whitespace characters, so the returned string may be a
// few characters longer once block separators are included — never of a
// different order.
func TestExcerptTextBudget(t *testing.T) {
	body := "<p>" + strings.Repeat("word ", 20000) + "</p>"
	got := excerptText(body)
	if !strings.HasPrefix(got, "word word") {
		t.Fatalf("excerpt = %q, want it to start at the body's opening", got)
	}
	visible := 0
	for _, r := range got {
		if !isExcerptSpace(r) {
			visible++
		}
	}
	if visible > ExcerptTextBudget+1 {
		t.Errorf("visible characters = %d, want <= %d", visible, ExcerptTextBudget)
	}
}

// Multibyte text must be cut on a rune boundary, never mid-character: a
// half-rune yields U+FFFD in the row and breaks the excerpt's byte accounting.
func TestExcerptTextMultibyteBudget(t *testing.T) {
	body := "<p>" + strings.Repeat("árvíztűrő ", 100) + "</p>"
	got := excerptText(body)
	if !utf8.ValidString(got) {
		t.Fatalf("excerpt is not valid UTF-8: %q", got)
	}
	visible := 0
	for _, r := range got {
		if !isExcerptSpace(r) {
			visible++
		}
	}
	if visible > ExcerptTextBudget+1 {
		t.Errorf("visible runes = %d, want <= %d", visible, ExcerptTextBudget)
	}
}

// The raw-HTML cap must be large enough to reach prose that sits behind a
// dense markup opening — the reported failure was that a 500-char cut did not.
func TestExcerptReachesTextBehindMarkupOpening(t *testing.T) {
	// ~3 KB of srcset attributes before the first word of prose.
	padding := strings.Repeat("https://publisher.example/img/very-long-name,w_1024,f_webp ", 45)
	doc := `<div id="readability-page-1" class="page"><picture><source srcset="` + padding +
		`"></picture></div><div><p>Believe me, the words you are looking for are here.</p></div>`

	// The old 500-char cut is exactly what produced the bug: it renders nothing.
	if got := excerptText(doc[:500]); got != "" {
		t.Fatalf("expected the 500-char cut to be empty (the reported bug), got %q", got)
	}
	got := excerptText(doc)
	if !strings.HasPrefix(got, "Believe me, the words") {
		t.Errorf("excerptText() = %q, want it to start with the article's opening words", got)
	}
}

func TestExcerptForPreviewSourceSelection(t *testing.T) {
	tests := []struct {
		name          string
		body          string
		extractBody   string
		extractStatus string
		want          string
	}{
		{
			name:          "extracted body is preferred when done",
			body:          "<p>Feed body</p>",
			extractBody:   "<p>Extracted body</p>",
			extractStatus: "done",
			want:          "Extracted body",
		},
		{
			// A failed extraction stores no body. Showing an empty excerpt
			// there would hide a perfectly good feed body.
			name:          "failed extraction falls back to the feed body",
			body:          "<p>Feed body</p>",
			extractBody:   "",
			extractStatus: "failed",
			want:          "Feed body",
		},
		{
			name:          "done with an empty extracted body falls back",
			body:          "<p>Feed body</p>",
			extractBody:   "",
			extractStatus: "done",
			want:          "Feed body",
		},
		{
			name:          "cleared extraction falls back",
			body:          "<p>Feed body</p>",
			extractBody:   "",
			extractStatus: "cleared",
			want:          "Feed body",
		},
		{
			name:          "pending extraction falls back",
			body:          "<p>Feed body</p>",
			extractBody:   "",
			extractStatus: "pending",
			want:          "Feed body",
		},
		{
			name:          "no bodies at all",
			body:          "",
			extractBody:   "",
			extractStatus: "",
			want:          "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ExcerptForPreview(tt.body, tt.extractBody, tt.extractStatus); got != tt.want {
				t.Errorf("ExcerptForPreview() = %q, want %q", got, tt.want)
			}
		})
	}
}
