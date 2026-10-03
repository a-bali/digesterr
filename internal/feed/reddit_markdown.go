package feed

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Block-level markdown, the half renderRedditMarkdown used to be missing.
//
// The renderer is split in two:
//
//	renderRedditMarkdown — this file. Splits selftext into BLOCKS (paragraph,
//	                       heading, list, blockquote, fenced code, thematic
//	                       break, table) and emits the wrapping elements.
//	renderRedditInline   — reddit_fetcher.go. Renders the INLINE marks inside a
//	                       block's text (links, images, bare URLs, code spans,
//	                       emphasis, strikethrough) and escapes everything else.
//
// The split is deliberate. Reddit selftext is untrusted input rendered with
// v-html, and the inline side already carries the escaping rules that were
// paid for in production bugs: escape each fragment at stash time (the restore
// pass does not reach inside a placeholder), a scheme allowlist for href/src,
// and an empty link label falling back to the URL. Handing the whole document
// to a general markdown library would have to either re-derive or drop those,
// so block structure is handled here and the inline rules are untouched.
//
// Code is the one place this must NOT apply: inside a fence or a span, markdown
// markers are content, not syntax (`**x**` in a code sample is not bold), and
// any HTML there is escaped rather than dropped — dropping it would silently
// delete text from the post.
var (
	// ATX heading: up to 3 leading spaces, 1-6 hashes, then a SPACE. The space
	// is required, so "#noSpace" stays literal text.
	redditHeadingRe = regexp.MustCompile(`^ {0,3}(#{1,6})[ \t]+(.*?)[ \t]*#*[ \t]*$`)
	// Fenced code: ``` or ~~~, 3+ chars, optional info string (```go).
	redditFenceRe = regexp.MustCompile("^ {0,3}(`{3,}|~{3,})[ \t]*(\\S*)[ \t]*$")
	// Thematic break: 3+ of - * _ (spaces allowed between), nothing else.
	redditHRRe = regexp.MustCompile(`^ {0,3}((?:-[ \t]*){3,}|(?:\*[ \t]*){3,}|(?:_[ \t]*){3,})$`)
	// List items. A marker is only a marker when followed by whitespace, so
	// `*emphasis*` is not a bullet.
	redditBulletRe  = regexp.MustCompile(`^([ \t]*)([-*+])[ \t]+(.*)$`)
	redditOrderedRe = regexp.MustCompile(`^([ \t]*)(\d{1,9})[.)][ \t]+(.*)$`)
	// Blockquote: up to 3 leading spaces, ">", optional space.
	redditQuoteRe = regexp.MustCompile(`^ {0,3}>[ \t]?(.*)$`)
)

// redditListItem is one bullet or numbered entry, with the indentation that
// decides nesting and — for ordered items — the number the author wrote.
type redditListItem struct {
	indent  int
	ordered bool
	number  int
	html    string
}

// renderRedditMarkdown converts Reddit-flavored selftext to HTML.
//
// Every block emits its own wrapper, including paragraphs: the result is a
// complete fragment (a sequence of <p>/<ul>/<h2>/…) rather than bare text, so
// callers must NOT wrap it in another <p> — see renderPostSelftext.
func renderRedditMarkdown(text string) string {
	lines := strings.Split(text, "\n")
	var b strings.Builder

	for i := 0; i < len(lines); {
		line := lines[i]

		if isRedditBlank(line) {
			i++
			continue
		}

		// Fenced code. Extracted before anything else so no markdown rule runs
		// over the sample inside it.
		if m := redditFenceRe.FindStringSubmatch(line); m != nil {
			fence := m[1]
			var body []string
			i++
			for i < len(lines) {
				if redditFenceCloses(lines[i], fence) {
					i++
					break
				}
				body = append(body, lines[i])
				i++
			}
			// Newlines are preserved literally: inside <pre> they are
			// significant, and the inline pass would turn them into <br />.
			b.WriteString("<pre><code>" + redditTextEscape(strings.Join(body, "\n")) + "</code></pre>")
			continue
		}

		if redditHRRe.MatchString(line) {
			b.WriteString("<hr />")
			i++
			continue
		}

		if m := redditHeadingRe.FindStringSubmatch(line); m != nil {
			level := len(m[1])
			fmt.Fprintf(&b, "<h%d>%s</h%d>", level, renderRedditInline(strings.TrimSpace(m[2])), level)
			i++
			continue
		}

		// Table: a run of pipe-bearing lines whose SECOND line is a separator.
		// Checked before the paragraph branch so the run is not split into
		// separate paragraphs.
		if i+1 < len(lines) && strings.Contains(line, "|") && isTableSeparator(lines[i+1]) {
			j := i
			for j < len(lines) && strings.Contains(lines[j], "|") {
				j++
			}
			if j-i >= 3 {
				// Cells are escaped by renderMarkdownTable and deliberately do
				// not get inline marks (unchanged behaviour), so the pass-through
				// placeholder function is what the existing tests pin.
				b.WriteString(renderMarkdownTable(lines[i:j], func(h string) string { return h }))
				i = j
				continue
			}
		}

		if redditQuoteRe.MatchString(line) {
			var quoted []string
			for i < len(lines) {
				qm := redditQuoteRe.FindStringSubmatch(lines[i])
				if qm == nil {
					break
				}
				quoted = append(quoted, qm[1])
				i++
			}
			b.WriteString("<blockquote>" + renderRedditInline(strings.Join(quoted, "\n")) + "</blockquote>")
			continue
		}

		if isRedditListItem(line) {
			var items []redditListItem
			for i < len(lines) && isRedditListItem(lines[i]) {
				it, ok := parseRedditListItem(lines[i])
				if !ok {
					break
				}
				items = append(items, it)
				i++
			}
			b.WriteString(renderRedditList(items))
			continue
		}

		// Paragraph: consume until a blank line or a construct allowed to end it.
		start := i
		for i < len(lines) && !isRedditBlank(lines[i]) && !redditEndsParagraph(lines, i) {
			i++
		}
		if i > start {
			b.WriteString("<p>" + renderRedditInline(strings.Join(lines[start:i], "\n")) + "</p>")
		} else {
			// Defensive: an unrecognised line must still advance, or the loop
			// spins forever on input that no branch claims.
			b.WriteString("<p>" + renderRedditInline(line) + "</p>")
			i++
		}
	}

	return b.String()
}

// renderRedditList emits nested <ul>/<ol> from a flat run of items. Nesting
// follows the indentation: an item more indented than the previous one opens a
// list INSIDE the previous item, so the result is valid HTML
// (<li>a<ul><li>b</li></ul></li>) rather than a list rendered as a sibling of
// the item it belongs to.
func renderRedditList(items []redditListItem) string {
	type frame struct {
		ordered bool
		indent  int
		// openLI is set when an item was left unclosed because the next item is
		// its child. The </li> is written when the level is left.
		openLI bool
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

	for idx, it := range items {
		closeDeeperThan(it.indent)

		if len(stack) > 0 && stack[len(stack)-1].indent == it.indent {
			top := &stack[len(stack)-1]
			switch {
			case top.ordered != it.ordered:
				// Same level, different marker: end this list, start another.
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
				// Coming back up to a level whose item was left open for its
				// children; that item ends here.
				b.WriteString("</li>")
				top.openLI = false
			}
		}

		if len(stack) == 0 || stack[len(stack)-1].indent < it.indent {
			if it.ordered {
				// Preserve the author's start number: a list written 3,4,5 must
				// not renumber to 1,2,3.
				if it.number > 1 {
					fmt.Fprintf(&b, `<ol start="%d">`, it.number)
				} else {
					b.WriteString("<ol>")
				}
			} else {
				b.WriteString("<ul>")
			}
			stack = append(stack, frame{ordered: it.ordered, indent: it.indent})
		}

		b.WriteString("<li>" + it.html)
		if idx+1 < len(items) && items[idx+1].indent > it.indent {
			// The next item is this one's child: keep the <li> open.
			stack[len(stack)-1].openLI = true
		} else {
			b.WriteString("</li>")
		}
	}

	closeDeeperThan(-1)
	return b.String()
}

// isRedditBlank reports whether a line carries nothing but whitespace.
func isRedditBlank(line string) bool {
	return strings.TrimSpace(line) == ""
}

// redditFenceCloses reports whether line closes a fence opened with open.
// The closing run must use the same character and be at least as long, and
// carry nothing else — so a longer fence can quote a shorter one.
func redditFenceCloses(line, open string) bool {
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

// redditEndsParagraph reports whether lines[i] is a construct that ends the
// paragraph currently being collected. A construct handled here but missing from
// this list would be swallowed into the preceding paragraph; one listed but not
// handled by the main loop would end the paragraph and then be re-read as body
// text.
//
// The distinction from a block START matters for lists. CommonMark lets a bullet
// list interrupt a paragraph but an ORDERED list only when it begins at 1 —
// "2024. We shipped X" is prose, not a list item, and treating it as one
// silently deletes the number. So `1. foo` right after a line of prose is a
// list item, while `2. foo` there is a continuation of the paragraph.
func redditEndsParagraph(lines []string, i int) bool {
	l := lines[i]
	if redditFenceRe.MatchString(l) || redditHRRe.MatchString(l) ||
		redditHeadingRe.MatchString(l) || redditQuoteRe.MatchString(l) {
		return true
	}
	if m := redditBulletRe.FindStringSubmatch(l); m != nil {
		return true
	}
	if m := redditOrderedRe.FindStringSubmatch(l); m != nil {
		n, _ := strconv.Atoi(m[2])
		return n == 1
	}
	if i+1 < len(lines) && strings.Contains(l, "|") && isTableSeparator(lines[i+1]) {
		return true
	}
	return false
}

// isRedditListItem reports whether a line is a bullet or numbered list item.
func isRedditListItem(line string) bool {
	_, ok := parseRedditListItem(line)
	return ok
}

// parseRedditListItem splits a list line into its indentation, kind and
// rendered content. Content is rendered here (not by the caller) so that inline
// marks inside an item behave exactly as they do in a paragraph.
func parseRedditListItem(line string) (redditListItem, bool) {
	if m := redditBulletRe.FindStringSubmatch(line); m != nil {
		return redditListItem{
			indent:  redditIndentWidth(m[1]),
			ordered: false,
			html:    renderRedditInline(strings.TrimSpace(m[3])),
		}, true
	}
	if m := redditOrderedRe.FindStringSubmatch(line); m != nil {
		n, _ := strconv.Atoi(m[2])
		return redditListItem{
			indent:  redditIndentWidth(m[1]),
			ordered: true,
			number:  n,
			html:    renderRedditInline(strings.TrimSpace(m[3])),
		}, true
	}
	return redditListItem{}, false
}

// redditIndentWidth measures leading whitespace, counting a tab as four columns
// so a tab-indented child nests under a space-indented parent.
func redditIndentWidth(ws string) int {
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
