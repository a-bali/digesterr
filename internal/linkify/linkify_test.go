package linkify

import (
	"html"
	"strings"
	"testing"
)

// rendered returns the text as a reader would see it: the HTML is parsed as
// markup, so entities that survive in the source (`&gt;`) count as the
// character they render as (`>`). Assertions about user-visible text use this,
// so a change in *which* encoding is used does not silently look like a change
// in behaviour.
func rendered(t *testing.T, s string) string {
	t.Helper()
	return html.UnescapeString(strings.ReplaceAll(s, "<br>", "\n"))
}

func TestTextLinkifiesBareURL(t *testing.T) {
	got := Text("No, it doesn't.\nhttps://colo.to/exercise.pdf\nThey exercised 15,625 options.")
	if !strings.Contains(got, `<a href="https://colo.to/exercise.pdf" target="_blank" rel="noopener noreferrer">https://colo.to/exercise.pdf</a>`) {
		t.Fatalf("expected bare URL to be linked, got: %s", got)
	}
	if !strings.Contains(rendered(t, got), "They exercised 15,625 options.") {
		t.Fatalf("expected surrounding text preserved, got: %s", got)
	}
}

func TestTextLinkifiesEveryURLOnALine(t *testing.T) {
	got := Text("first https://example.com/a then https://example.com/b end")
	if n := strings.Count(got, `<a href="https://example.com/`); n != 2 {
		t.Fatalf("expected 2 links, got %d: %s", n, got)
	}
}

func TestTextLinkifiesShortenerAndWikipedia(t *testing.T) {
	// Real comment fixture: a t.co link and a bare wikipedia URL.
	got := Text("@OpenRouter @stripe why you lie https://t.co/zktIrzIbyR")
	if !strings.Contains(got, `href="https://t.co/zktIrzIbyR"`) {
		t.Fatalf("expected t.co link, got: %s", got)
	}
	got = Text("https://en.wikipedia.org/wiki/Cambridge_Analytica")
	if !strings.Contains(got, `href="https://en.wikipedia.org/wiki/Cambridge_Analytica"`) {
		t.Fatalf("expected wikipedia link, got: %s", got)
	}
}

func TestTextLinkifiesWWWWithoutScheme(t *testing.T) {
	got := Text("go to www.example.com/x?y=1 for details")
	if !strings.Contains(got, `<a href="https://www.example.com/x?y=1" target="_blank" rel="noopener noreferrer">www.example.com/x?y=1</a>`) {
		t.Fatalf("expected www URL linked with an explicit scheme, got: %s", got)
	}
}

func TestTextDoesNotLinkifyWWWInsideAWord(t *testing.T) {
	got := Text("awww.example.com is not a host")
	if strings.Contains(got, "<a href=") {
		t.Fatalf("expected no link, got: %s", got)
	}
}

func TestTextMarkdownLinkUsesLabelAndHref(t *testing.T) {
	got := Text("[Itt](https://www.youtube.com/live/BU6rXYv726I?si=U50DG_vDKNazLq92) is the video")
	if !strings.Contains(got, `<a href="https://www.youtube.com/live/BU6rXYv726I?si=U50DG_vDKNazLq92" target="_blank" rel="noopener noreferrer">Itt</a>`) {
		t.Fatalf("expected markdown link rendered with its label, got: %s", got)
	}
}

// The regression this guards: the raw comment body stores `&amp;` for a literal
// ampersand, so a URL query separator arrives as `&amp;`. Escaping it a second
// time produced `&amp;amp;`, which the browser decodes to a parameter literally
// named `amp;t` — a broken link that still looks fine in the source.
func TestTextMarkdownLinkWithAmpersandInQuery(t *testing.T) {
	got := Text("[Itt](https://www.youtube.com/live/BU6rXYv726I?si=U50DG_vDKNazLq92&amp;t=30918)")
	if !strings.Contains(got, `href="https://www.youtube.com/live/BU6rXYv726I?si=U50DG_vDKNazLq92&amp;t=30918"`) {
		t.Fatalf("expected exactly one level of escaping in href, got: %s", got)
	}
	if strings.Contains(got, "&amp;amp;") {
		t.Fatalf("href was double-escaped, got: %s", got)
	}
	if !strings.Contains(rendered(t, got), "https://www.youtube.com/live/BU6rXYv726I?si=U50DG_vDKNazLq92&t=30918") {
		t.Fatalf("rendered href is not the original URL, got: %s", rendered(t, got))
	}
}

func TestTextBareURLWithAmpersandInQuery(t *testing.T) {
	got := Text("https://news.ycombinator.com/x?width=320&amp;format=pjpg")
	if !strings.Contains(got, `href="https://news.ycombinator.com/x?width=320&amp;format=pjpg"`) {
		t.Fatalf("expected single-escaped href, got: %s", got)
	}
	if strings.Contains(got, "&amp;amp;") {
		t.Fatalf("href was double-escaped, got: %s", got)
	}
}

func TestTextMarkdownLinkWithPlainAmpersand(t *testing.T) {
	got := Text("[kifli](https://kifli.hu/kereses?q=a&b=2)")
	if !strings.Contains(got, `href="https://kifli.hu/kereses?q=a&amp;b=2"`) {
		t.Fatalf("expected plain ampersand escaped once, got: %s", got)
	}
}

func TestTextMarkdownImageBeatsMarkdownLink(t *testing.T) {
	got := Text("![screenshot](https://i.redd.it/abc123.jpg) shows the issue.")
	if !strings.Contains(got, `<img src="https://i.redd.it/abc123.jpg"`) {
		t.Fatalf("expected img tag, got: %s", got)
	}
	if strings.Contains(got, "<a href=") {
		t.Fatalf("markdown image must not also become a link, got: %s", got)
	}
	if !strings.Contains(got, `alt="screenshot"`) {
		t.Fatalf("expected alt text, got: %s", got)
	}
}

func TestTextBareImageHostStaysAnImage(t *testing.T) {
	got := Text("look at this https://i.imgur.com/abc123.png pretty cool")
	if !strings.Contains(got, `<img src="https://i.imgur.com/abc123.png"`) {
		t.Fatalf("expected img tag, got: %s", got)
	}
	if strings.Contains(got, `<a href="https://i.imgur.com/abc123.png"`) {
		t.Fatalf("bare image host must not become a link, got: %s", got)
	}
	if !strings.Contains(got, "pretty cool") {
		t.Fatalf("expected surrounding text preserved, got: %s", got)
	}
}

func TestTextRejectsNonHTTPSchemes(t *testing.T) {
	for _, raw := range []string{
		"javascript:alert(1)",
		"data:text/html;base64,PHNjcmlwdD4=",
		"vbscript:msgbox(1)",
	} {
		got := Text("click [here](" + raw + ") and " + raw)
		if strings.Contains(got, "<a href=") {
			t.Fatalf("expected no link for %q, got: %s", raw, got)
		}
	}
}

func TestTextEscapesHTMLOnce(t *testing.T) {
	got := Text("Nice <script>alert(1)</script> comment & more")
	if strings.Contains(got, "<script>") {
		t.Fatalf("raw script tag survived, got: %s", got)
	}
	if !strings.Contains(rendered(t, got), "Nice <script>alert(1)</script> comment & more") {
		t.Fatalf("text did not round-trip, got: %s", rendered(t, got))
	}
}

// Reddit stores blockquote markers as `&gt;`; the reader must keep seeing `>`.
func TestTextDecodesRedditEntitiesExactlyOnce(t *testing.T) {
	got := Text("&gt;quoted text&lt; with &amp; ampersand &quot;and&quot; &#39;quotes&#39;")
	if !strings.Contains(rendered(t, got), ">quoted text< with & ampersand \"and\" 'quotes'") {
		t.Fatalf("expected entities decoded once, got: %s", rendered(t, got))
	}
	if strings.Contains(got, "&amp;amp;") || strings.Contains(rendered(t, got), "&amp;") {
		t.Fatalf("entities were double-decoded, got: %s", got)
	}
}

// A comment that literally reads "&amp;lt;" must render as that text, not as "<".
func TestTextDoesNotDoubleDecodeEntities(t *testing.T) {
	got := Text("write &amp;lt;div&amp;gt; to open a div")
	if !strings.Contains(rendered(t, got), "write &lt;div&gt; to open a div") {
		t.Fatalf("expected single decode, got: %s", rendered(t, got))
	}
}

func TestTextTrimsTrailingPunctuation(t *testing.T) {
	got := Text("see https://example.com/page. Then continue")
	if !strings.Contains(got, `href="https://example.com/page"`) {
		t.Fatalf("expected trailing dot excluded from the URL, got: %s", got)
	}
	// The full stop stays in the text, right after the link.
	if !strings.Contains(got, `</a>. Then continue`) {
		t.Fatalf("expected trailing punctuation kept as text, got: %s", got)
	}
}

func TestTextKeepsBalancedParenthesesInURL(t *testing.T) {
	got := Text("(see https://en.wikipedia.org/wiki/Foo_(bar) for more)")
	if !strings.Contains(got, `href="https://en.wikipedia.org/wiki/Foo_(bar)"`) {
		t.Fatalf("expected balanced parens inside the URL, got: %s", got)
	}
}

func TestTextConvertsNewlinesToBreaks(t *testing.T) {
	got := Text("line one\nline two")
	if !strings.Contains(got, "line one<br>line two") {
		t.Fatalf("expected <br> between lines, got: %s", got)
	}
}

func TestTextFullCommentFixture(t *testing.T) {
	// Shape taken from a stored X comment: mention, prose, and a short link.
	got := Text("@OpenRouter It's fast, cheap, and easy to integrate.\nTry https://openrouter.ai/models?order=newest&amp;format=json")
	if !strings.Contains(got, "@OpenRouter It&#39;s fast, cheap, and easy to integrate.") &&
		!strings.Contains(got, "@OpenRouter It's fast, cheap, and easy to integrate.") {
		t.Fatalf("expected prose preserved, got: %s", got)
	}
	if !strings.Contains(got, `href="https://openrouter.ai/models?order=newest&amp;format=json"`) {
		t.Fatalf("expected single-escaped query separator, got: %s", got)
	}
}

func TestSafeHref(t *testing.T) {
	cases := map[string]bool{
		"https://example.com":      true,
		"http://example.com/a?b=1": true,
		"HTTPS://example.com":      true,
		"javascript:alert(1)":      false,
		"data:text/html,x":         false,
		"//example.com":            false,
		"https://example.com/a b":  false,
		"https://example.com/a\"b": false,
		" https://example.com ":    true,
	}
	for raw, want := range cases {
		if got := SafeHref(raw); got != want {
			t.Errorf("SafeHref(%q) = %v, want %v", raw, got, want)
		}
	}
}
