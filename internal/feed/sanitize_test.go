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
