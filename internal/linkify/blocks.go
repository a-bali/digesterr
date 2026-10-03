package linkify

import (
	"fmt"
	"html"
	"regexp"
	"strconv"
	"strings"
)

// Markdown BLOCK structure for comment text.
//
// Comments carry markdown too, but with a different distribution from post
// bodies. Measured over 3,702 real Reddit comments (14 subreddits, top posts of
// the month) the constructs that actually occur are:
//
//	blockquote    2.78%   the reply-quoting convention
//	bullet list   0.46%
//	ordered list  0.32%
//	code fence    0.05%
//	heading       0.05%
//	thematic brk   ~0
//	table         0.05%
//
// So blockquotes, lists and fences are handled here and the near-absent
// constructs (headings, thematic breaks, tables) are NOT: they keep rendering
// as the literal text they render as today, which is the previous behaviour
// rather than a regression.
//
// The inline renderer (textInline) is unchanged — this only adds the wrapping
// elements around it. Splitting runs on text whose entities are ALREADY decoded,
// because Reddit encodes the blockquote marker as &gt;; splitting first would
// never see a quote.
var (
	quoteLineRe   = regexp.MustCompile(`^ {0,3}>[ \t]?(.*)$`)
	fenceOpenRe   = regexp.MustCompile("^ {0,3}(`{3,}|~{3,})[ \t]*(\\S*)[ \t]*$")
	bulletLineRe  = regexp.MustCompile(`^([ \t]*)([-*+])[ \t]+(.*)$`)
	orderedLineRe = regexp.MustCompile(`^([ \t]*)(\d{1,9})[.)][ \t]+(.*)$`)
)

// blockEntry is one bullet or numbered entry, with the indentation that decides
// nesting.
type blockEntry struct {
	indent  int
	ordered bool
	number  int
	html    string
}

// renderBlocks splits text into markdown blocks and renders each block's inline
// content through inline. The result is a sequence of complete top-level
// elements, so callers must not wrap it.
func renderBlocks(text string, inline func(string) string) string {
	lines := strings.Split(text, "\n")
	var b strings.Builder

	for i := 0; i < len(lines); {
		line := lines[i]

		if strings.TrimSpace(line) == "" {
			i++
			continue
		}

		// Fenced code is claimed first so no inline rule runs over the sample
		// inside it, and its newlines stay significant.
		if m := fenceOpenRe.FindStringSubmatch(line); m != nil {
			fence := m[1]
			var body []string
			i++
			for i < len(lines) {
				if closesFence(lines[i], fence) {
					i++
					break
				}
				body = append(body, lines[i])
				i++
			}
			b.WriteString("<pre><code>" + html.EscapeString(strings.Join(body, "\n")) + "</code></pre>")
			continue
		}

		if quoteLineRe.MatchString(line) {
			var quoted []string
			for i < len(lines) {
				qm := quoteLineRe.FindStringSubmatch(lines[i])
				if qm == nil {
					break
				}
				quoted = append(quoted, qm[1])
				i++
			}
			b.WriteString("<blockquote>" + inline(strings.Join(quoted, "\n")) + "</blockquote>")
			continue
		}

		if isBlockListLine(line) {
			var entries []blockEntry
			for i < len(lines) && isBlockListLine(lines[i]) {
				en, ok := parseBlockListLine(lines[i], inline)
				if !ok {
					break
				}
				entries = append(entries, en)
				i++
			}
			b.WriteString(renderEntries(entries))
			continue
		}

		start := i
		for i < len(lines) && strings.TrimSpace(lines[i]) != "" && !endsBlockParagraph(lines, i) {
			i++
		}
		if i > start {
			b.WriteString("<p>" + inline(strings.Join(lines[start:i], "\n")) + "</p>")
		} else {
			// An unrecognised line must still advance, or the loop spins.
			b.WriteString("<p>" + inline(line) + "</p>")
			i++
		}
	}

	return b.String()
}

// renderEntries emits nested <ul>/<ol> from a flat run of entries. An entry more
// indented than the previous one opens a list INSIDE it, so the markup nests
// (<li>a<ul><li>b</li></ul></li>) instead of emitting the child as a sibling.
func renderEntries(entries []blockEntry) string {
	type frame struct {
		ordered bool
		indent  int
		openLI  bool
	}
	var stack []frame
	var b strings.Builder

	closeDeeperThan := func(indent int) {
		for len(stack) > 0 && stack[len(stack)-1].indent > indent {
			f := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			if f.openLI {
				b.WriteString("</li>")
			}
			if f.ordered {
				b.WriteString("</ol>")
			} else {
				b.WriteString("</ul>")
			}
		}
	}

	for idx, en := range entries {
		closeDeeperThan(en.indent)

		if len(stack) > 0 && stack[len(stack)-1].indent == en.indent {
			top := &stack[len(stack)-1]
			switch {
			case top.ordered != en.ordered:
				if top.openLI {
					b.WriteString("</li>")
					top.openLI = false
				}
				if top.ordered {
					b.WriteString("</ol>")
				} else {
					b.WriteString("</ul>")
				}
				stack = stack[:len(stack)-1]
			case top.openLI:
				b.WriteString("</li>")
				top.openLI = false
			}
		}

		if len(stack) == 0 || stack[len(stack)-1].indent < en.indent {
			if en.ordered {
				if en.number > 1 {
					fmt.Fprintf(&b, `<ol start="%d">`, en.number)
				} else {
					b.WriteString("<ol>")
				}
			} else {
				b.WriteString("<ul>")
			}
			stack = append(stack, frame{ordered: en.ordered, indent: en.indent})
		}

		b.WriteString("<li>" + en.html)
		if idx+1 < len(entries) && entries[idx+1].indent > en.indent {
			stack[len(stack)-1].openLI = true
		} else {
			b.WriteString("</li>")
		}
	}

	closeDeeperThan(-1)
	return b.String()
}

// closesFence reports whether line closes a fence opened with open: the same
// character, at least as long, and carrying nothing else.
func closesFence(line, open string) bool {
	t := strings.TrimLeft(line, " \t")
	ch := open[0]
	n := 0
	for n < len(t) && t[n] == ch {
		n++
	}
	if n < len(open) {
		return false
	}
	return strings.TrimSpace(t[n:]) == ""
}

// endsBlockParagraph reports whether lines[i] ends the paragraph being
// collected. Mirrors the branches of renderBlocks, with CommonMark's rule that
// an ordered list interrupts a paragraph only when it starts at 1 — so
// "2024. We shipped X" stays prose and keeps its number.
func endsBlockParagraph(lines []string, i int) bool {
	l := lines[i]
	if fenceOpenRe.MatchString(l) || quoteLineRe.MatchString(l) {
		return true
	}
	if bulletLineRe.MatchString(l) {
		return true
	}
	if m := orderedLineRe.FindStringSubmatch(l); m != nil {
		n, _ := strconv.Atoi(m[2])
		return n == 1
	}
	return false
}

func isBlockListLine(line string) bool {
	_, ok := parseBlockListLine(line, nil)
	return ok
}

// parseBlockListLine splits a list line into its indentation, kind and rendered
// content. The content is rendered through inline, so marks inside an item
// behave as they do in a paragraph.
func parseBlockListLine(line string, inline func(string) string) (blockEntry, bool) {
	render := func(s string) string {
		if inline == nil {
			return ""
		}
		return inline(s)
	}
	if m := bulletLineRe.FindStringSubmatch(line); m != nil {
		return blockEntry{indent: blockIndentWidth(m[1]), html: render(strings.TrimSpace(m[3]))}, true
	}
	if m := orderedLineRe.FindStringSubmatch(line); m != nil {
		n, _ := strconv.Atoi(m[2])
		return blockEntry{indent: blockIndentWidth(m[1]), ordered: true, number: n, html: render(strings.TrimSpace(m[3]))}, true
	}
	return blockEntry{}, false
}

// blockIndentWidth counts a tab as four columns so a tab-indented child nests
// under a space-indented parent.
func blockIndentWidth(ws string) int {
	w := 0
	for _, r := range ws {
		if r == '\t' {
			w += 4
		} else {
			w++
		}
	}
	return w
}
