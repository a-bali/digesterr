package feed

import (
	"strings"
	"testing"
)

// TestRedditBackslashEscapes pins CommonMark backslash-escape handling in the
// inline renderer. Reddit stores the escape verbatim in selftext, so before
// this fix an author who wrote `warm\_compaction` saw the literal `\_`.
//
// Each case asserts the escaped character is present and the backslash is gone,
// and — for a marker an escape is meant to SUPPRESS — that the corresponding
// element was NOT produced. That second half is the whole point of escaping:
// unescaping to a bare `*` and then letting the italic pass honour it would
// leave the backslash gone but the intent broken.
func TestRedditBackslashEscapes(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		contains []string
		excludes []string
	}{
		{
			// The reported r/hermesagent case, in the shape it arrived.
			name:     "escaped underscore",
			input:    `warm\_compaction`,
			contains: []string{"warm_compaction"},
			excludes: []string{`\`, `\_`},
		},
		{
			name:     "escaped underscore in a list item",
			input:    "- warm\\_compaction: 16.8 s",
			contains: []string{"<li>", "warm_compaction: 16.8 s"},
			excludes: []string{`\`},
		},
		{
			name:     "escaped underscore in a heading",
			input:    "## hermes\\_warm\\_compaction",
			contains: []string{"<h2>hermes_warm_compaction</h2>"},
			excludes: []string{`\`},
		},
		{
			// Escaping a marker must suppress the mark, not just drop the slash.
			name:     "escaped asterisks are not emphasis",
			input:    `\*not emphasis\*`,
			contains: []string{"*not emphasis*"},
			excludes: []string{"<em>", `\`},
		},
		{
			name:     "escaped underscores are not emphasis",
			input:    `\_not emphasis\_`,
			contains: []string{"_not emphasis_"},
			excludes: []string{"<em>", `\`},
		},
		{
			name:     "escaped brackets are not a link",
			input:    `\[not a link\](x)`,
			contains: []string{"[not a link](x)"},
			excludes: []string{"<a ", `\`},
		},
		{
			name:     "escaped backtick is not code",
			input:    "\\`not code\\`",
			contains: []string{"`not code`"},
			excludes: []string{"<code>", `\`},
		},
		{
			// A backslash before a non-punctuation character is literal.
			name:     "backslash before a letter is literal",
			input:    `C:\path	o`,
			contains: []string{`C:\path	o`},
		},
		{
			// Real upstream shape (r/stocks): a doubled backslash before a dash
			// is an escaped backslash plus a literal dash, so the line must NOT
			// become a bullet. Reddit's own renderer emits exactly `\- CVX`.
			name:     "doubled backslash before a dash is not a bullet",
			input:    `\\- CVX plans to invest`,
			contains: []string{`\- CVX plans to invest`},
			excludes: []string{"<ul>", "<li>"},
		},
		{
			// Inside a code span the escape is content, not syntax (CommonMark).
			name:     "escape inside a code span is preserved",
			input:    "`warm\\_compaction`",
			contains: []string{"<code>warm\\_compaction</code>"},
		},
		{
			// A URL with escaped underscores must render as a working link.
			name:     "escaped underscores inside a link",
			input:    `[thread](https://www.reddit.com/r/hu/comments/oc\_aki\_szerette/?tl=hu)`,
			contains: []string{`href="https://www.reddit.com/r/hu/comments/oc_aki_szerette/?tl=hu"`},
			excludes: []string{`\`},
		},
		{
			// The literal text must still be HTML-escaped after resolving.
			name:     "escaped angle brackets stay escaped",
			input:    `\<script\>`,
			contains: []string{"&lt;script&gt;"},
			excludes: []string{"<script>", `\`},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			out := renderRedditMarkdown(tc.input)
			for _, want := range tc.contains {
				if !strings.Contains(out, want) {
					t.Errorf("output missing %q: %q", want, out)
				}
			}
			for _, bad := range tc.excludes {
				if strings.Contains(out, bad) {
					t.Errorf("output must not contain %q: %q", bad, out)
				}
			}
		})
	}
}

// TestRedditBackslashEscapeReportedBody is the end-to-end repro for the
// r/hermesagent warm_compaction report: the whole pasted body must render with
// no stray backslash anywhere.
func TestRedditBackslashEscapeReportedBody(t *testing.T) {
	in := `The idea
At compaction time, warm\_compaction sends the last main-model request again.

Results (synthetic 100K+ token sessions, 10 cases each, median)

Compaction time:
- warm\_compaction: 16.8 s
- hermes-lcm: 43.5 s

Fact in the middle of a long message:
- warm\_compaction: 10/10

Rollback: set context.engine back to compressor.

Install
hermes plugins install Elevatormusic/hermes-warm-compaction#warm\_compaction --enable`

	out := renderRedditMarkdown(in)
	if strings.Contains(out, `\`) {
		t.Errorf("rendered body still carries a backslash: %q", out)
	}
	if !strings.Contains(out, "warm_compaction: 16.8 s") {
		t.Errorf("expected the unescaped term in the output: %q", out)
	}
	if strings.Contains(out, "<em>") {
		t.Errorf("no emphasis should be produced: %q", out)
	}
}
