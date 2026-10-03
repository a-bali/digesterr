package linkify

import (
	"strings"
	"testing"
)

// TestTextRendersBlocks pins the block-level markdown comment bodies carry.
// Measured over 3,702 real Reddit comments, blockquotes are the common case
// (2.78%) — Reddit encodes the marker as &gt; in the stored body, so the
// entity must be decoded BEFORE the block split or no quote is ever found.
func TestTextRendersBlocks(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		contains []string
		excludes []string
	}{
		{
			name:     "entity-encoded blockquote",
			input:    "&gt; quoted text",
			contains: []string{"<blockquote>", "quoted text", "</blockquote>"},
			excludes: []string{"&gt;"},
		},
		{
			name:     "literal blockquote",
			input:    "> quoted text",
			contains: []string{"<blockquote>", "quoted text"},
			excludes: []string{"&gt;"},
		},
		{
			name:     "multi-line blockquote",
			input:    "> line one\n> line two",
			contains: []string{"<blockquote>", "line one", "line two", "</blockquote>"},
		},
		{
			name:     "bullet list",
			input:    "- first\n- second",
			contains: []string{"<ul>", "<li>first</li>", "<li>second</li>", "</ul>"},
			excludes: []string{"- first<br>"},
		},
		{
			name:     "ordered list",
			input:    "1. first\n2. second",
			contains: []string{"<ol>", "<li>first</li>", "<li>second</li>"},
		},
		{
			name:     "fenced code stays literal",
			input:    "```\n**not bold**\n```",
			contains: []string{"<pre>", "<code>", "**not bold**"},
			excludes: []string{"<strong>"},
		},
		{
			name:     "html in a fence is escaped",
			input:    "```\n<script>alert(1)</script>\n```",
			contains: []string{"&lt;script&gt;"},
			excludes: []string{"<script>"},
		},
		{
			name:     "url inside a blockquote is still linkified",
			input:    "> see https://example.com/a",
			contains: []string{"<blockquote>", `<a href="https://example.com/a"`},
		},
		{
			name:     "markdown link inside a list item is rendered",
			input:    "- [label](https://example.com/a)",
			contains: []string{"<li><a href=\"https://example.com/a\"", ">label</a></li>"},
		},
		{
			name:     "paragraph separated by a blank line",
			input:    "first para\n\nsecond para",
			contains: []string{"<p>first para</p>", "<p>second para</p>"},
		},
		{
			// A numbered sentence is prose, not a list (CommonMark: an ordered
			// list interrupts a paragraph only at 1).
			name:     "numbered sentence in prose keeps its number",
			input:    "Notes follow.\n2024. We shipped it.",
			contains: []string{"2024. We shipped it."},
			excludes: []string{"<li>We shipped"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Text(tt.input)
			for _, s := range tt.contains {
				if !strings.Contains(got, s) {
					t.Errorf("output missing %q\ninput: %s\ngot:   %s", s, tt.input, got)
				}
			}
			for _, s := range tt.excludes {
				if strings.Contains(got, s) {
					t.Errorf("output should not contain %q\ninput: %s\ngot:   %s", s, tt.input, got)
				}
			}
		})
	}
}

// TestTextPlainCommentIsOneParagraph pins the shape an ordinary comment gets.
// Callers render the result inside a <p>, so the block layer must produce a
// complete element rather than bare text (or the comment would lose its
// paragraph entirely).
func TestTextPlainCommentIsOneParagraph(t *testing.T) {
	got := Text("just a normal comment")
	if got != "<p>just a normal comment</p>" {
		t.Errorf("got %q, want <p>just a normal comment</p>", got)
	}
	if got := Text(""); got != "" {
		t.Errorf("empty input: got %q, want empty", got)
	}
}

// TestTextBlockOutputHasNoPlaceholder is the storage guard: a surviving NUL
// placeholder is rendered to the reader as invisible garbage, and on the paths
// that persist this output it is permanent for that row.
func TestTextBlockOutputHasNoPlaceholder(t *testing.T) {
	inputs := []string{
		"> quote with [link](https://example.com/a) and **bold**",
		"- item with `code`\n- item with https://example.com/b",
		"```\nfence\n```",
		"text\n\n&gt; quote\n\n- list",
		"> **bold** and ![alt](https://i.redd.it/a.png)",
	}
	for _, in := range inputs {
		if got := Text(in); strings.ContainsRune(got, 0) {
			t.Errorf("placeholder survived\ninput: %s\ngot:   %q", in, got)
		}
	}
}

// TestTextNestedListNests pins that a child entry is emitted INSIDE its parent
// item, which is what makes the markup valid rather than a sibling list.
func TestTextNestedListNests(t *testing.T) {
	got := Text("- a\n  - b\n- c")
	want := "<ul><li>a<ul><li>b</li></ul></li><li>c</li></ul>"
	if got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

// TestTextBlockquotedReplyShape reproduces the shape that actually occurs in
// Reddit comments: a quoted reply followed by the author's own paragraph.
func TestTextBlockquotedReplyShape(t *testing.T) {
	in := "&gt; They talk about converting as if it is free.\n\nIt is certainly cheaper than humans doing it."
	got := Text(in)
	if !strings.Contains(got, "<blockquote>They talk about converting as if it is free.</blockquote>") {
		t.Errorf("quote not rendered as a blockquote: %s", got)
	}
	if !strings.Contains(got, "<p>It is certainly cheaper than humans doing it.</p>") {
		t.Errorf("reply not rendered as its own paragraph: %s", got)
	}
}
