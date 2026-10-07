package feed

import (
	"strings"
	"unicode/utf8"

	"golang.org/x/net/html"
)

// ExcerptTextBudget bounds how much VISIBLE text an excerpt collects. The list
// row renders a single truncated line, so this only has to cover what can fit
// with a wide margin. Counting stops on NON-whitespace characters: a body that
// opens with whitespace-only nodes would otherwise stop before any visible text
// and yield an empty excerpt.
const ExcerptTextBudget = 200

// ExcerptMaxBody bounds the raw body handed to the parser. A truncated prefix
// parses fine — html.Parse closes unclosed tags — and the tail of a 400 KB body
// cannot reach a 200-char excerpt. The bound is needed because any cut here is a
// character count over RAW HTML: a body opening with inline SVG or a long
// srcset list can spend several KB of markup before its first word of prose.
const ExcerptMaxBody = 8192

// blockTags start a new line of text. Both boundaries contribute a separator so
// words never glue together across them: without it <p>one</p><p>two</p> reads
// "onetwo" and <p>a<br>b</p> reads "ab".
//
// Only TAG NAMES belong here — never a regex over the markup. Feed bodies are
// full of code blocks and CLI docs where `<name>` and `<version>` are LITERAL
// TEXT, so an element-shaped pattern would corrupt content that renders fine.
var blockTags = map[string]bool{
	"address": true, "article": true, "aside": true, "blockquote": true, "body": true,
	"caption": true, "center": true, "dd": true, "details": true, "dialog": true,
	"dir": true, "div": true, "dl": true, "dt": true, "fieldset": true, "figcaption": true,
	"figure": true, "footer": true, "form": true, "h1": true, "h2": true, "h3": true,
	"h4": true, "h5": true, "h6": true, "header": true, "hgroup": true, "hr": true,
	"html": true, "legend": true, "li": true, "main": true, "menu": true, "nav": true,
	"ol": true, "optgroup": true, "option": true, "p": true, "pre": true, "section": true,
	"summary": true, "table": true, "tbody": true, "td": true, "tfoot": true, "th": true,
	"thead": true, "tr": true, "ul": true, "br": true,
}

// neverRendered elements never contribute text, so their whole subtree is
// skipped. script/style/iframe hold their content as RAW TEXT (per the HTML
// spec they have no child elements), so a WordPress body beginning with a
// YouTube <iframe> would otherwise render the embed's fallback markup as the
// excerpt. Note svg is deliberately absent: its <title>/<text> do render.
var neverRendered = map[string]bool{
	"script": true, "style": true, "iframe": true, "noscript": true, "template": true,
	"object": true, "embed": true, "head": true, "title": true, "base": true, "link": true,
	"meta": true, "param": true, "source": true, "track": true, "audio": true, "video": true,
	"canvas": true, "map": true, "area": true, "col": true, "colgroup": true, "datalist": true,
}

// isExcerptSpace mirrors the frontend's isWhitespace: only these characters are
// free, so a nbsp- or zero-width-padded body cannot stop the walk early.
func isExcerptSpace(r rune) bool {
	switch r {
	case ' ', '\t', '\n', '\r', 0xa0, 0x200b:
		return true
	}
	return false
}

type excerptWalk struct {
	parts   []string
	visible int
	done    bool
	budget  int // 0 means unbounded; otherwise stop after this many visible characters.
}

func (w *excerptWalk) walk(n *html.Node) {
	if w.done {
		return
	}
	switch n.Type {
	case html.TextNode:
		w.text(n.Data)
	case html.ElementNode:
		tag := n.Data
		if neverRendered[tag] {
			return
		}
		// An inline style the browser would not paint is the only hiding form
		// that survives in a stored feed body: class-based hiding cannot be
		// resolved without the publisher's stylesheet.
		for _, attr := range n.Attr {
			if attr.Key != "style" {
				continue
			}
			style := strings.ReplaceAll(strings.ToLower(attr.Val), " ", "")
			if strings.Contains(style, "display:none") || strings.Contains(style, "visibility:hidden") {
				return
			}
		}
		breaks := blockTags[tag]
		if breaks {
			w.parts = append(w.parts, " ")
		}
		for c := n.FirstChild; c != nil && !w.done; c = c.NextSibling {
			w.walk(c)
		}
		if breaks {
			w.parts = append(w.parts, " ")
		}
	default:
		// Document and doctype nodes: descend so the tree is actually reached.
		for c := n.FirstChild; c != nil && !w.done; c = c.NextSibling {
			w.walk(c)
		}
	}
}

// text appends the rune prefix of s that stays inside the budget.
//
// The budget bounds the TEXT the row can display, not the plain string: block
// boundaries add separators of their own, so the returned excerpt may exceed
// ExcerptTextBudget by a handful of spaces before the caller collapses them.
func (w *excerptWalk) text(s string) {
	if s == "" {
		return
	}
	if w.budget <= 0 {
		w.parts = append(w.parts, s)
		for _, r := range s {
			if !isExcerptSpace(r) {
				w.visible++
			}
		}
		return
	}
	take := len(s)
	counted := 0
	for i, r := range s {
		if !isExcerptSpace(r) {
			counted++
		}
		if w.visible+counted >= w.budget {
			_, size := utf8.DecodeRuneInString(s[i:])
			take = i + size
			break
		}
	}
	w.parts = append(w.parts, s[:take])
	w.visible += counted
	if w.visible >= w.budget {
		w.done = true
	}
}

// excerptText flattens stored item HTML to the plain text a reader sees, for a
// one-line list excerpt.
//
// This is NOT a tag-stripping regex: attribute content is indistinguishable
// from prose, and naive stripping mangles bodies that legitimately contain
// element-shaped text. It is also not the browser's innerText, which requires
// an attached, laid-out node and costs a reflow per row — far too much for a
// ~40-character preview.
func excerptText(body string) string {
	if body == "" {
		return ""
	}
	if len(body) > ExcerptMaxBody {
		body = body[:ExcerptMaxBody]
	}
	root, err := html.Parse(strings.NewReader(body))
	if err != nil {
		return ""
	}
	w := &excerptWalk{budget: ExcerptTextBudget}
	w.walk(root)
	return strings.Join(strings.Fields(strings.Join(w.parts, "")), " ")
}

// ExcerptForPreview returns the list-row excerpt for a preview body, preferring
// the extracted text when one exists and falling back to the feed's own body
// otherwise.
//
// The order matters: a failed extraction leaves an EMPTY extracted body, so
// keying off extract_status alone would blank the excerpt of every item whose
// extraction failed or was cleared.
func ExcerptForPreview(body, extractBody, extractStatus string) string {
	if extractStatus == "done" {
		if s := excerptText(extractBody); s != "" {
			return s
		}
	}
	return excerptText(body)
}

// ExcerptTextLen returns the number of visible non-whitespace characters in a
// stored item body. It uses the same HTML-aware walker as ExcerptForPreview,
// but does not stop at the preview's 200-character budget.
func ExcerptTextLen(body string) int {
	if body == "" {
		return 0
	}
	// Bound pathological inputs while allowing enough room to measure article
	// bodies well beyond the preview limit.
	if len(body) > ExcerptMaxBody*16 {
		body = body[:ExcerptMaxBody*16]
	}
	root, err := html.Parse(strings.NewReader(body))
	if err != nil {
		return 0
	}
	w := &excerptWalk{}
	w.walk(root)
	return w.visible
}
