package feed

import (
	"strings"
	"testing"
)

// The sanitizer is the boundary between an untrusted publisher and a live DOM
// (rendered with v-html in the reader and re-served by the Reader API), so these
// tests are deliberately adversarial: each case is an injection shape that has
// worked against naive sanitizers, not a cosmetic check.
//
// Assertions target the RAW TAG (<script, <iframe) rather than a substring like
// "onerror=" — escaped output is inert yet still contains the substring, so an
// over-strict assertion would fail on correct code.
func TestSanitizeNewsletterHTMLRemovesDangerousContent(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []string // must NOT appear in the output
	}{
		{"script tag", `<p>hi</p><script>alert(1)</script>`, []string{"<script", "alert(1)"}},
		{"script with attributes", `<script src="//evil.example/x.js"></script>`, []string{"<script", "evil.example"}},
		{"nested script", `<div><span><script>alert(1)</script></span></div>`, []string{"<script", "alert(1)"}},
		{"iframe", `<iframe src="https://evil.example"></iframe>`, []string{"<iframe"}},
		{"object", `<object data="x.swf"></object>`, []string{"<object"}},
		{"embed", `<embed src="x.swf">`, []string{"<embed"}},
		{"form", `<form action="https://evil.example"><input name="p"></form>`, []string{"<form", "<input"}},
		{"style tag", `<style>body{display:none}</style>`, []string{"<style", "display:none"}},
		{"svg with script", `<svg><script>alert(1)</script></svg>`, []string{"<svg", "<script", "alert(1)"}},
		{"onerror handler", `<img src="x" onerror="alert(1)">`, []string{"onerror"}},
		{"onclick handler", `<a href="https://ok.example" onclick="alert(1)">x</a>`, []string{"onclick"}},
		{"onload on body", `<div onload="alert(1)">x</div>`, []string{"onload"}},
		{"uppercase handler", `<IMG SRC="x" ONERROR="alert(1)">`, []string{"onerror", "ONERROR"}},
		{"mixed case tag", `<ScRiPt>alert(1)</ScRiPt>`, []string{"<script", "<ScRiPt", "alert(1)"}},
		{"javascript href", `<a href="javascript:alert(1)">click</a>`, []string{"javascript:"}},
		{"javascript href with spaces", `<a href="  javascript:alert(1)">click</a>`, []string{"javascript:"}},
		{"javascript href with tab", "<a href=\"java\tscript:alert(1)\">click</a>", []string{"javascript:"}},
		{"data uri href", `<a href="data:text/html;base64,PHNjcmlwdD4=">x</a>`, []string{"data:"}},
		{"vbscript href", `<a href="vbscript:msgbox(1)">x</a>`, []string{"vbscript:"}},
		{"javascript img src", `<img src="javascript:alert(1)">`, []string{"javascript:"}},
		{"data uri img src", `<img src="data:image/svg+xml;base64,PHN2Zz4=">`, []string{"data:"}},
		{"style attribute", `<div style="background:url(javascript:alert(1))">x</div>`, []string{"style=", "javascript:"}},
		{"srcdoc", `<iframe srcdoc="<script>alert(1)</script>"></iframe>`, []string{"srcdoc", "<iframe"}},
		{"template", `<template><script>alert(1)</script></template>`, []string{"<template", "<script", "alert(1)"}},
		{"base tag", `<base href="https://evil.example/">`, []string{"<base"}},
		{"meta refresh", `<meta http-equiv="refresh" content="0;url=https://evil.example">`, []string{"<meta", "http-equiv"}},
		{"link tag", `<link rel="stylesheet" href="https://evil.example/x.css">`, []string{"<link"}},
		{"comment smuggling", `<!-- <script>alert(1)</script> -->`, []string{"<!--", "<script", "alert(1)"}},
		{"conditional comment", `<!--[if IE]><script>alert(1)</script><![endif]-->`, []string{"<script", "alert(1)"}},
		{"xlink href", `<svg><a xlink:href="javascript:alert(1)">x</a></svg>`, []string{"xlink", "javascript:"}},
		{"formaction", `<button formaction="javascript:alert(1)">x</button>`, []string{"formaction", "<button"}},
		{"xmlns abuse", `<div xmlns="http://www.w3.org/1999/xhtml" xmlns:xlink="x">x</div>`, []string{"xmlns"}},
		{"meta in body", `<p>x</p><meta charset="utf-8">`, []string{"<meta"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := SanitizeNewsletterHTML(tc.in)
			lower := strings.ToLower(got)
			for _, bad := range tc.want {
				if strings.Contains(lower, strings.ToLower(bad)) {
					t.Errorf("sanitized output still contains %q\n  in:  %s\n  out: %s", bad, tc.in, got)
				}
			}
		})
	}
}

// The fix must not over-correct: legitimate newsletter markup has to survive, or
// the feature is useless.
func TestSanitizeNewsletterHTMLKeepsLegitimateMarkup(t *testing.T) {
	cases := []struct {
		name      string
		in        string
		mustKeep  []string
		mustNotBe string // the whole output must not equal this (i.e. not empty)
	}{
		{
			name:     "paragraph and emphasis",
			in:       `<p>Hello <strong>world</strong> and <em>more</em>.</p>`,
			mustKeep: []string{"<p>", "<strong>", "<em>", "Hello"},
		},
		{
			name:     "heading and link",
			in:       `<h2>Title</h2><p><a href="https://example.com/article">Read more</a></p>`,
			mustKeep: []string{"<h2>", "Title", `<a href="https://example.com/article"`, "Read more"},
		},
		{
			name:     "image with dimensions",
			in:       `<img src="https://cdn.example.com/a.png" alt="Chart" width="600" height="400">`,
			mustKeep: []string{`src="https://cdn.example.com/a.png"`, `alt="Chart"`, `width="600"`, `height="400"`, "loading=", `referrerpolicy="no-referrer"`},
		},
		{
			name:     "table layout",
			in:       `<table width="100%" cellpadding="0"><tr><td align="center">Cell</td></tr></table>`,
			mustKeep: []string{"<table", "Cell", "<td", `align="center"`},
		},
		{
			name:     "list",
			in:       `<ul><li>one</li><li>two</li></ul><ol start="3"><li>three</li></ol>`,
			mustKeep: []string{"<ul>", "<li>", "one", `<ol start="3"`, "three"},
		},
		{
			name:     "blockquote with cite",
			in:       `<blockquote cite="https://example.com/src">Quoted</blockquote>`,
			mustKeep: []string{"<blockquote", `cite="https://example.com/src"`, "Quoted"},
		},
		{
			name:     "line breaks and hr",
			in:       `<p>a<br>b</p><hr><p>c</p>`,
			mustKeep: []string{"<br>", "<hr>", "a", "b", "c"},
		},
		{
			name:     "code and pre",
			in:       `<pre><code>func main() {}</code></pre>`,
			mustKeep: []string{"<pre>", "<code>", "func main()"},
		},
		{
			name:     "mailto link",
			in:       `<a href="mailto:news@example.com">Contact</a>`,
			mustKeep: []string{`href="mailto:news@example.com"`, "Contact"},
		},
		{
			name:     "relative link is preserved",
			in:       `<a href="/archive/1">Old issue</a>`,
			mustKeep: []string{`href="/archive/1"`, "Old issue"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := SanitizeNewsletterHTML(tc.in)
			if got == "" {
				t.Fatalf("sanitizer returned empty output for %s", tc.in)
			}
			for _, keep := range tc.mustKeep {
				if !strings.Contains(got, keep) {
					t.Errorf("sanitized output lost %q\n  in:  %s\n  out: %s", keep, tc.in, got)
				}
			}
		})
	}
}

// The real payload a newsletter actually carries: inline styles and a branded
// footer link. The image and text must survive; the style attributes must not.
func TestSanitizeNewsletterHTMLRealPayload(t *testing.T) {
	raw := `<div dir="ltr">Automated routing probe from the Hermes agent.</div>` +
		`<div style="margin-top:16px;padding-top:8px;border-top:1px solid #eee;font-size:12px;color:#999;">` +
		`Sent via <a href="https://agentmail.to?utm_source=agentmail&amp;utm_medium=email" style="color:#999;">AgentMail</a></div>`

	got := SanitizeNewsletterHTML(raw)
	if !strings.Contains(got, "Automated routing probe") {
		t.Errorf("body text lost: %s", got)
	}
	if !strings.Contains(got, "Sent via") || !strings.Contains(got, "AgentMail") {
		t.Errorf("footer text lost: %s", got)
	}
	if !strings.Contains(got, "https://agentmail.to?utm_source=agentmail") {
		t.Errorf("footer link lost or mangled: %s", got)
	}
	if strings.Contains(got, "style=") {
		t.Errorf("style attribute survived: %s", got)
	}
	if strings.Contains(got, "margin-top") {
		t.Errorf("CSS survived outside a style attribute: %s", got)
	}
}

// Text that looks like markup must be escaped, not turned into live elements.
func TestSanitizeNewsletterHTMLEscapesLoneText(t *testing.T) {
	got := SanitizeNewsletterHTML(`<p>5 &lt; 6 and a &amp; b</p>`)
	if !strings.Contains(got, "5") || !strings.Contains(got, "6") {
		t.Errorf("text content lost: %s", got)
	}
	// The literal ampersand must be re-escaped exactly once.
	if strings.Contains(got, "&amp;amp;") {
		t.Errorf("double-escaped ampersand: %s", got)
	}
}

// A bare text fragment (no elements) must still be escaped and returned.
//
// The fixture deliberately avoids `<word>`: the HTML parser treats that as a
// tag (and unwrapping an unknown tag is correct), so it cannot demonstrate text
// escaping. A `<` followed by a non-letter is unambiguous text.
func TestSanitizeNewsletterHTMLPlainText(t *testing.T) {
	got := SanitizeNewsletterHTML(`Plain text with 5 < 6 and 7 > 4 & an ampersand`)
	if !strings.Contains(got, "&lt;") {
		t.Errorf("less-than not escaped: %s", got)
	}
	if !strings.Contains(got, "&gt;") {
		t.Errorf("greater-than not escaped: %s", got)
	}
	if !strings.Contains(got, "&amp;") {
		t.Errorf("ampersand not escaped: %s", got)
	}
	if !strings.Contains(got, "Plain text with 5") {
		t.Errorf("text content lost: %s", got)
	}
}

func TestSanitizeNewsletterHTMLEmpty(t *testing.T) {
	for _, in := range []string{"", "   ", "\n\t"} {
		if got := SanitizeNewsletterHTML(in); got != "" {
			t.Errorf("SanitizeNewsletterHTML(%q) = %q, want empty", in, got)
		}
	}
}

// An unknown element is unwrapped, not dropped: its text must remain visible
// rather than the whole fragment vanishing.
func TestSanitizeNewsletterHTMLUnwrapsUnknownElements(t *testing.T) {
	got := SanitizeNewsletterHTML(`<customthing>visible text</customthing>`)
	if !strings.Contains(got, "visible text") {
		t.Errorf("unknown element's text was dropped: %s", got)
	}
	if strings.Contains(got, "customthing") {
		t.Errorf("unknown element tag survived: %s", got)
	}
}

// Void elements must not get a closing tag (invalid HTML that confuses parsers
// downstream, including the Reader API's renderer).
func TestSanitizeNewsletterHTMLVoidElementsNoClosingTag(t *testing.T) {
	got := SanitizeNewsletterHTML(`<p>a<br>b</p><img src="https://x.example/i.png">`)
	if strings.Contains(got, "</br>") {
		t.Errorf("br got a closing tag: %s", got)
	}
	if strings.Contains(got, "</img>") {
		t.Errorf("img got a closing tag: %s", got)
	}
}

// --- Reader layout: images and invisible filler -----------------------------
//
// Two independent defects made a newsletter render as unwrapped, clipped text in
// a Reader client, and both are properties of the STORED MARKUP rather than of
// one publisher's template. Measured in bare Chromium (no stylesheet of ours,
// which is what a third-party Reader client gives the body): the affected body
// overran a 720px pane by 1372px and a 360px pane by 1732px before these rules,
// and by 0px after.

// An image must never be able to exceed the width of the article, because that
// is what forces the whole body into horizontal scroll instead of wrapping.
func TestSanitizeConstrainsImageWidth(t *testing.T) {
	got := SanitizeFeedHTML(`<p><img src="https://x.example/wide.png" width="2000" height="442"></p>`)
	if !strings.Contains(got, "max-width:100%") {
		t.Errorf("image is not constrained to the pane width: %s", got)
	}
	if !strings.Contains(got, "height:auto") {
		t.Errorf("image constraint would squash the aspect ratio without height:auto: %s", got)
	}
}

// Publisher CSS must not be able to reach that slot: the style attribute is
// stripped from the input, so the constraint above is the only one present.
func TestSanitizeImageStyleIsOursNotThePublishers(t *testing.T) {
	got := SanitizeFeedHTML(`<img src="https://x.example/i.png" style="width:4000px;position:fixed">`)
	if strings.Contains(got, "4000px") || strings.Contains(got, "position:fixed") {
		t.Fatalf("publisher style survived the sanitizer: %s", got)
	}
	if !strings.Contains(got, "max-width:100%") {
		t.Errorf("our constraint is missing: %s", got)
	}
}

// An image with no alt renders a 16x16 broken-image glyph when it fails to load;
// the same image with alt="" renders nothing. Ad beacons ship without alt and
// without dimensions, so this is what leaves garbage boxes in a body whose image
// host is blocked or offline.
func TestSanitizeFillsMissingImageAlt(t *testing.T) {
	got := SanitizeFeedHTML(`<img src="https://tracker.example/imp?s=1">`)
	if !strings.Contains(got, `alt=""`) {
		t.Errorf("image with no alt got no empty alt: %s", got)
	}

	// An author's real alt text must be preserved verbatim.
	kept := SanitizeFeedHTML(`<img src="https://x.example/c.png" alt="Revenue chart">`)
	if !strings.Contains(kept, `alt="Revenue chart"`) {
		t.Errorf("existing alt text was replaced: %s", kept)
	}
	if strings.Count(kept, "alt=") != 1 {
		t.Errorf("alt attribute was duplicated: %s", kept)
	}
}

// A 1x1 read receipt is dropped outright: it carries no content, and keeping it
// only creates a request that reports the read.
func TestSanitizeDropsTrackingPixels(t *testing.T) {
	got := SanitizeFeedHTML(`<p>x</p><img src="https://t.example/q" width="1" height="1" alt="">`)
	if strings.Contains(got, "t.example") {
		t.Errorf("1x1 tracking pixel survived: %s", got)
	}

	// The match is narrow on purpose: a 1x1 WITH alt text, or a 2x1, is content.
	withAlt := SanitizeFeedHTML(`<img src="https://x.example/dot.png" width="1" height="1" alt="bullet">`)
	if !strings.Contains(withAlt, "dot.png") {
		t.Errorf("a 1x1 image with alt text is content and was dropped: %s", withAlt)
	}
	notTiny := SanitizeFeedHTML(`<img src="https://x.example/b.png" width="2" height="1">`)
	if !strings.Contains(notTiny, "b.png") {
		t.Errorf("a non-1x1 image was dropped: %s", notTiny)
	}
}

// A run of NBSP/zero-width characters is invisible but has no break opportunity,
// so it sets a minimum content width for the whole block. This is the actual
// cause of "the text is not word-wrapped any more": nothing was clipped, the
// block was simply wider than the pane.
func TestSanitizeCollapsesInvisibleFillerRun(t *testing.T) {
	run := strings.Repeat("\u00a0\u200c", 250)
	got := SanitizeFeedHTML(`<div>Summary line.` + run + `</div><p>body</p>`)
	if strings.Contains(got, "\u00a0\u00a0") || strings.Contains(got, "\u200c\u200c") {
		t.Errorf("filler run survived: %d bytes of output", len(got))
	}
	if !strings.Contains(got, "Summary line.") {
		t.Errorf("visible text was lost: %s", got)
	}
	if len(got) > 200 {
		t.Errorf("output is still dominated by filler (%d bytes): %q", len(got), got)
	}
}

// A single nbsp is ordinary typography (it keeps "10 kg" on one line) and must
// not be touched — only runs are filler.
func TestSanitizeKeepsSingleNbsp(t *testing.T) {
	got := SanitizeFeedHTML(`<p>10&nbsp;kg of coffee</p>`)
	if !strings.Contains(got, "10\u00a0kg") {
		t.Errorf("single nbsp between values was rewritten: %q", got)
	}
}

// A run made only of zero-width characters must vanish rather than become a
// space: it never occupied visible width, so a space would invent separation.
func TestSanitizeZeroWidthOnlyRunLeavesNothing(t *testing.T) {
	got := SanitizeFeedHTML("<p>a" + strings.Repeat("\u200b\u200c", 4) + "b</p>")
	if strings.Contains(got, "a b") {
		t.Errorf("zero-width run was turned into a visible space: %q", got)
	}
	if !strings.Contains(got, "ab") {
		t.Errorf("text around the run was altered: %q", got)
	}
}

// --- Upgrading already-stored bodies ---------------------------------------

// Bodies written before these rules must render the same as new ones, and the
// pass must be idempotent so repeated reads do not accumulate attributes.
func TestUpgradeStoredBodyIsIdempotent(t *testing.T) {
	body := `<div>Pre.` + strings.Repeat("\u00a0\u200c", 100) + `</div>` +
		`<img src="https://x.example/a.png" width="2000">` +
		`<img src="https://t.example/imp" width="1" height="1" alt="">`
	once := UpgradeStoredBody(body)
	twice := UpgradeStoredBody(once)
	if once != twice {
		t.Fatalf("not idempotent:\n once: %s\n twice: %s", once, twice)
	}
	if !strings.Contains(once, "max-width:100%") || !strings.Contains(once, "Pre.") {
		t.Errorf("upgrade did not apply the rules: %s", once)
	}
}

// The upgrade must be additive: only img tags and filler runs are rewritten.
// Re-sanitizing the whole body would delete the <video> players that the Reddit
// and Twitter ingest paths build, which the sanitizer's allowlist does not know
// about.
func TestUpgradeStoredBodyPreservesNonSanitizerMarkup(t *testing.T) {
	body := `<p>x</p><video controls poster="https://p/1.jpg"><source src="https://v/1.m3u8"></video>` +
		`<blockquote class="reddit-card">quote</blockquote>`
	got := UpgradeStoredBody(body)
	for _, want := range []string{"<video", `poster="https://p/1.jpg"`, "<source", "m3u8", "reddit-card"} {
		if !strings.Contains(got, want) {
			t.Errorf("upgrade dropped %q from a stored body: %s", want, got)
		}
	}
}

// A body with no image and no filler cannot be changed, so the upgrade must
// return it untouched — the read path renders up to 100 bodies per request and
// must not pay for a tokenizer run on the common case.
func TestUpgradeStoredBodyLeavesPlainBodiesAlone(t *testing.T) {
	body := `<p>A plain body with no images and no filler.</p>`
	if got := UpgradeStoredBody(body); got != body {
		t.Errorf("plain body was rewritten:\n got: %s\nwant: %s", got, body)
	}
}

// A body may carry its own style attribute, and a tag can only have ONE. The
// constraint has to be merged into it, not appended as a second attribute:
// browsers keep the first style and silently discard the rest, so a duplicate
// would leave the image unconstrained while looking fixed in the source.
func TestUpgradeStoredBodyMergesIntoAnExistingStyle(t *testing.T) {
	got := UpgradeStoredBody(`<p><img src="https://x.example/1.png" style="border:0"></p>`)
	if n := strings.Count(got, "style="); n != 1 {
		t.Errorf("image has %d style attributes, want exactly 1: %s", n, got)
	}
	if !strings.Contains(got, "max-width:100%") {
		t.Errorf("constraint was not merged into the existing style: %s", got)
	}
	if !strings.Contains(got, "border:0") {
		t.Errorf("the body's own style was dropped: %s", got)
	}
}

// A body that already constrains its images must not get a second declaration:
// the linkify image builder emits max-width:100% itself.
func TestUpgradeStoredBodyRespectsAnExistingMaxWidth(t *testing.T) {
	got := UpgradeStoredBody(`<p><img src="https://x.example/2.png" style="max-width:100%;border-radius:4px"></p>`)
	if n := strings.Count(got, "max-width"); n != 1 {
		t.Errorf("image carries %d max-width declarations, want 1: %s", n, got)
	}
	if !strings.Contains(got, "border-radius:4px") {
		t.Errorf("the body's own style was dropped: %s", got)
	}
}

// Tag names are case insensitive, so the pre-check must not miss <IMG>.
func TestUpgradeStoredBodyHandlesUppercaseImages(t *testing.T) {
	got := UpgradeStoredBody(`<IMG SRC="https://x.example/3.png">`)
	if !strings.Contains(got, "max-width:100%") || !strings.Contains(got, `alt=""`) {
		t.Errorf("uppercase img was not upgraded: %s", got)
	}
}
