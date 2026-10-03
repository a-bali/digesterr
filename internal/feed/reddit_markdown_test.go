package feed

import (
	"strings"
	"testing"
)

// TestRedditMarkdownBlockConstructs pins the block-level markdown constructs
// the renderer must produce. Before this change every one of them was emitted
// literally: a "# Heading" line reached the reader as the text "# Heading", a
// bullet list as "- a<br />- b", a quote as "&gt; quote".
//
// Each case asserts the element is produced AND that the literal marker is gone
// — a renderer that emits the tag but leaves the marker (or vice versa) is the
// failure mode this guards.
func TestRedditMarkdownBlockConstructs(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		contains []string
		excludes []string
	}{
		{
			name:     "h1",
			input:    "# Title",
			contains: []string{"<h1>Title</h1>"},
			excludes: []string{"# Title"},
		},
		{
			name:     "h2",
			input:    "## Heading two",
			contains: []string{"<h2>Heading two</h2>"},
			excludes: []string{"## Heading two"},
		},
		{
			name:     "h6",
			input:    "###### Six",
			contains: []string{"<h6>Six</h6>"},
			excludes: []string{"###### Six"},
		},
		{
			name:     "heading with closing hashes",
			input:    "## Title ##",
			contains: []string{"<h2>Title</h2>"},
			excludes: []string{"##"},
		},
		{
			// No space after the hashes: not a heading in markdown.
			name:     "hash without space is not a heading",
			input:    "#noSpace here",
			contains: []string{"#noSpace here"},
			excludes: []string{"<h1>"},
		},
		{
			name:     "dash bullet list",
			input:    "- one\n- two\n- three",
			contains: []string{"<ul>", "<li>one</li>", "<li>two</li>", "<li>three</li>", "</ul>"},
			excludes: []string{"- one<br />"},
		},
		{
			name:     "star bullet list",
			input:    "* one\n* two",
			contains: []string{"<ul>", "<li>one</li>", "<li>two</li>"},
			excludes: []string{"* one<br />"},
		},
		{
			name:     "ordered list",
			input:    "1. first\n2. second\n3. third",
			contains: []string{"<ol>", "<li>first</li>", "<li>third</li>", "</ol>"},
			excludes: []string{"1. first<br />"},
		},
		{
			// The number is a marker, not content: "1." must not survive.
			name:     "ordered list marker is not content",
			input:    "1. only",
			contains: []string{"<ol>", "<li>only</li>"},
			excludes: []string{"<li>1."},
		},
		{
			name:     "nested list",
			input:    "- a\n  - b\n- c",
			contains: []string{"<ul>", "<li>a", "<li>b</li>", "<li>c</li>"},
		},
		{
			name:     "dynamic bullet list",
			input:    "Because:\n\n1. first reason\n2. second reason",
			contains: []string{"<p>Because:</p>", "<ol>", "<li>first reason</li>"},
		},
		{
			name:     "blockquote",
			input:    "> quoted line",
			contains: []string{"<blockquote>", "quoted line", "</blockquote>"},
			excludes: []string{"&gt; quoted"},
		},
		{
			name:     "multi-line blockquote",
			input:    "> line one\n> line two",
			contains: []string{"<blockquote>", "line one", "line two", "</blockquote>"},
			excludes: []string{"&gt;"},
		},
		{
			name:     "blockquote without space after marker",
			input:    ">tight quote",
			contains: []string{"<blockquote>", "tight quote"},
		},
		{
			name:     "fenced code block",
			input:    "```\ncode line\nsecond line\n```",
			contains: []string{"<pre>", "<code>", "code line", "second line", "</code>", "</pre>"},
			excludes: []string{"```"},
		},
		{
			name:     "fenced code block with language",
			input:    "```go\nfunc main() {}\n```",
			contains: []string{"<pre>", "<code>", "func main() {}"},
			excludes: []string{"```go"},
		},
		{
			name:     "unclosed fence runs to end",
			input:    "```\nstill code",
			contains: []string{"<pre>", "<code>", "still code"},
			excludes: []string{"```"},
		},
		{
			name:     "inline code",
			input:    "use `git rebase` here",
			contains: []string{"<code>git rebase</code>"},
			excludes: []string{"`git rebase`"},
		},
		{
			name:     "strikethrough",
			input:    "~~struck~~",
			contains: []string{"<del>struck</del>"},
			excludes: []string{"~~struck~~"},
		},
		{
			name:     "thematic break dashes",
			input:    "before\n\n---\n\nafter",
			contains: []string{"<hr />", "<p>before</p>", "<p>after</p>"},
			excludes: []string{"<p>---</p>"},
		},
		{
			name:     "thematic break asterisks",
			input:    "***",
			contains: []string{"<hr />"},
			excludes: []string{"***"},
		},
		{
			name:     "blank line separates paragraphs",
			input:    "first para\n\nsecond para",
			contains: []string{"<p>first para</p>", "<p>second para</p>"},
		},
		{
			// Reddit treats a single newline as a hard break, not a paragraph
			// break — that behaviour must survive the block renderer.
			name:     "single newline is a hard break",
			input:    "line one\nline two",
			contains: []string{"<p>line one<br />line two</p>"},
			excludes: []string{"<p>line one</p>"},
		},
		{
			name:  "list item keeps inline marks",
			input: "- **bold item**\n- [link](https://example.com/a)",
			contains: []string{
				"<li><strong>bold item</strong></li>",
				`<li><a href="https://example.com/a"`,
			},
		},
		{
			name:     "heading keeps inline marks",
			input:    "## See [docs](https://example.com/a)",
			contains: []string{`<h2>See <a href="https://example.com/a"`},
		},
		{
			name:     "blockquote keeps inline marks",
			input:    "> **quoted bold**",
			contains: []string{"<blockquote><strong>quoted bold</strong></blockquote>"},
		},
		{
			// Found by sweeping 105 real bodies: a body that opens with prose
			// and THEN a heading was wrapped in a paragraph by the caller, so
			// the output began <p><p>. Paragraphs are emitted per block here,
			// so no wrapper may be added around the result.
			name:     "prose followed by a heading is not double-wrapped",
			input:    "Intro line\n\n## Heading",
			contains: []string{"<p>Intro line</p>", "<h2>Heading</h2>"},
			excludes: []string{"<p><p>", "</p></p>", "<p><h2>"},
		},
		{
			// CommonMark lets an ORDERED list interrupt a paragraph only when it
			// starts at 1. "2024. We shipped X" is prose; treating it as a list
			// item silently deletes the number and mangles the sentence.
			name:     "a numbered sentence in prose is not a list",
			input:    "Release notes follow.\n2024. We shipped the beta.",
			contains: []string{"2024. We shipped the beta."},
			excludes: []string{"<li>We shipped", "<ol>"},
		},
		{
			// The counterpart: a bullet list DOES interrupt a paragraph.
			name:     "a bullet list interrupts a paragraph",
			input:    "Reasons:\n- first\n- second",
			contains: []string{"<p>Reasons:</p>", "<ul>", "<li>first</li>"},
		},
		{
			// A list whose numbering does not start at 1 keeps its start.
			name:     "ordered list keeps its start number",
			input:    "3. third\n4. fourth",
			contains: []string{`<ol start="3">`, "<li>third</li>", "<li>fourth</li>"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := renderRedditMarkdown(tt.input)
			for _, s := range tt.contains {
				if !strings.Contains(got, s) {
					t.Errorf("rendered output missing %q\ninput: %s\ngot:   %s", s, tt.input, got)
				}
			}
			for _, s := range tt.excludes {
				if strings.Contains(got, s) {
					t.Errorf("rendered output should not contain %q\ninput: %s\ngot:   %s", s, tt.input, got)
				}
			}
		})
	}
}

// TestRedditMarkdownCodeIsVerbatim pins the one place markdown syntax must NOT
// be interpreted: inside code. A body whose code sample contains markdown
// markers (very common on a programming subreddit) must show them literally,
// and any HTML in the sample must be escaped rather than dropped — dropping it
// would silently delete text, which is worse than showing it escaped.
func TestRedditMarkdownCodeIsVerbatim(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		contains []string
		excludes []string
	}{
		{
			name:     "markdown markers stay literal inside a fence",
			input:    "```\n## not a heading\n- not a list\n**not bold**\n```",
			contains: []string{"<pre>", "## not a heading", "- not a list", "**not bold**"},
			excludes: []string{"<h2>", "<ul>", "<strong>", "<li>"},
		},
		{
			name:     "html in a fence is escaped not dropped",
			input:    "```\n<script>alert(1)</script>\n```",
			contains: []string{"&lt;script&gt;alert(1)&lt;/script&gt;"},
			excludes: []string{"<script>"},
		},
		{
			name:     "markdown markers stay literal inside a code span",
			input:    "use `**x**` and `# y` literally",
			contains: []string{"<code>**x**</code>", "<code># y</code>"},
			excludes: []string{"<strong>", "<h1>"},
		},
		{
			name:     "html inside a code span is escaped",
			input:    "`<img src=x onerror=alert(1)>`",
			contains: []string{"&lt;img"},
			excludes: []string{"<img"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := renderRedditMarkdown(tt.input)
			for _, s := range tt.contains {
				if !strings.Contains(got, s) {
					t.Errorf("rendered output missing %q\ninput: %s\ngot:   %s", s, tt.input, got)
				}
			}
			for _, s := range tt.excludes {
				if strings.Contains(got, s) {
					t.Errorf("rendered output should not contain %q\ninput: %s\ngot:   %s", s, tt.input, got)
				}
			}
		})
	}
}

// TestRedditMarkdownLeavesNoPlaceholderOnBlocks extends the placeholder guard to
// the block layer: every generated tag is stashed as \x00PH_n\x00 and restored
// innermost-first, so any construct combination that defeats the restore leaves
// literal NUL bytes in the STORED body.
func TestRedditMarkdownLeavesNoPlaceholderOnBlocks(t *testing.T) {
	inputs := []string{
		"## Title with **bold**",
		"- item with `code`\n- item with **bold**",
		"> quote with [link](https://example.com/a)",
		"```\nfence with ** and ` inside\n```",
		"~~struck with **bold** inside~~",
		"# heading with ![alt](https://i.redd.it/a.png)",
		"1. **bold** in a list\n2. `code` in a list",
		"| H | V |\n| --- | --- |\n| **b** | https://example.com/x |",
		"text before\n\n## heading\n\n- list\n\n> quote\n\n```\ncode\n```",
	}
	for _, in := range inputs {
		got := renderRedditMarkdown(in)
		if strings.ContainsRune(got, 0) {
			t.Errorf("a placeholder survived into the rendered body\ninput: %s\ngot:   %q", in, got)
		}
	}
}

// TestRenderPostSelftextDoesNotNestParagraphs pins the wrapper contract: the
// renderer emits its own top-level block elements, so wrapping its output in
// another <p> would produce <p><ul>...</ul></p> — invalid markup that the
// browser repairs by hoisting the list out of the paragraph, breaking the
// typography container's spacing.
func TestRenderPostSelftextDoesNotNestParagraphs(t *testing.T) {
	tests := []struct {
		name     string
		selftext string
		contains []string
		excludes []string
	}{
		{
			name:     "list body is not wrapped in a paragraph",
			selftext: "- one\n- two",
			contains: []string{"<ul>", "<li>one</li>"},
			excludes: []string{"<p><ul>"},
		},
		{
			name:     "heading body is not wrapped in a paragraph",
			selftext: "## Title",
			contains: []string{"<h2>Title</h2>"},
			excludes: []string{"<p><h2>"},
		},
		{
			name:     "fenced code body is not wrapped in a paragraph",
			selftext: "```\ncode\n```",
			contains: []string{"<pre>"},
			excludes: []string{"<p><pre>"},
		},
		{
			name:     "plain text body still gets its paragraph",
			selftext: "just a sentence",
			contains: []string{"<p>just a sentence</p>"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := renderPostSelftext(redditPostData{Selftext: tt.selftext})
			for _, s := range tt.contains {
				if !strings.Contains(got, s) {
					t.Errorf("rendered output missing %q\nselftext: %s\ngot:      %s", s, tt.selftext, got)
				}
			}
			for _, s := range tt.excludes {
				if strings.Contains(got, s) {
					t.Errorf("rendered output should not contain %q\nselftext: %s\ngot:      %s", s, tt.selftext, got)
				}
			}
		})
	}
}

// TestParseListingStoresBlockMarkdown covers the INGEST path end to end: a post
// body with block markdown arrives in the listing payload and must be stored in
// item.Content as HTML. This is the contract that matters, because the value is
// written once here and read back by the item pane, the Reader API and the
// digest body — a renderer that only works when called directly still leaves the
// reader looking at raw markdown.
func TestParseListingStoresBlockMarkdown(t *testing.T) {
	listingJSON := `{
		"data": { "children": [{
			"kind": "t3",
			"data": {
				"id": "blocks1",
				"title": "Release notes",
				"author": "user1",
				"url": "https://www.reddit.com/r/selfhosted/comments/blocks1/x/",
				"permalink": "/r/selfhosted/comments/blocks1/x/",
				"selftext": "Summary:\n\n## What changed\n\n- first thing\n- second thing\n\n> quoted reply\n\n1. step one\n2. step two",
				"score": 10,
				"num_comments": 5,
				"created_utc": 1693000000.0,
				"is_self": true,
				"over_18": false,
				"stickied": false
			}
		}]}
	}`

	f := &RedditFetcher{}
	items, err := f.parseListing([]byte(listingJSON), "selfhosted")
	if err != nil {
		t.Fatalf("parseListing: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("got %d items, want 1", len(items))
	}
	content := items[0].Content
	for _, want := range []string{
		"<p>Summary:</p>", "<h2>What changed</h2>", "<ul>", "<li>first thing</li>",
		"<blockquote>quoted reply</blockquote>", "<ol>", "<li>step one</li>",
	} {
		if !strings.Contains(content, want) {
			t.Errorf("stored Content missing %q\ngot: %s", want, content)
		}
	}
	// The body opens with plain prose, so it must not be wrapped in a second
	// <p> that would nest the heading and list inside a paragraph.
	if strings.Contains(content, "<p><p>") || strings.Contains(content, "<p><h2>") {
		t.Errorf("stored Content is double-wrapped: %s", content)
	}
}
