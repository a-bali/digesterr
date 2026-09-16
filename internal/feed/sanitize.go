package feed

import (
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// Newsletter HTML arrives from arbitrary publishers and is rendered with v-html
// in the reader AND re-served through the Reader API to third-party clients, so
// it is untrusted input on a live DOM. This sanitizer is an ALLOWLIST: anything
// not explicitly permitted is dropped.
//
// The allowlist is intentionally structural (what a newsletter legitimately
// uses) rather than a blocklist of known-bad tags, because a blocklist has to
// anticipate every obfuscation and an allowlist only has to be correct.
//
// Rules:
//   - Disallowed elements are dropped WITH their contents (a <script>'s body
//     must not survive as text), except elements whose children are still
//     meaningful, which are unwrapped (unknown inline containers).
//   - Every attribute is dropped unless it is on the per-element list.
//   - In particular all on* handlers and the style attribute go, and href/src
//     go through a scheme allowlist so javascript: and data: cannot reach the
//     reader.
//   - Remote images are kept (a newsletter without images is unreadable) but
//     get loading="lazy" and referrerpolicy="no-referrer" so a read does not
//     report the reader's referrer back to a tracking endpoint.
var (
	// dropWithContents removes the element and everything inside it. These are
	// never useful in a newsletter and several are actively dangerous.
	dropWithContents = map[atom.Atom]bool{
		atom.Script:   true,
		atom.Style:    true,
		atom.Iframe:   true,
		atom.Frame:    true,
		atom.Frameset: true,
		atom.Object:   true,
		atom.Embed:    true,
		atom.Applet:   true,
		atom.Form:     true,
		atom.Input:    true,
		atom.Button:   true,
		atom.Select:   true,
		atom.Option:   true,
		atom.Textarea: true,
		atom.Svg:      true,
		atom.Math:     true,
		atom.Template: true,
		atom.Noscript: true,
		atom.Link:     true,
		atom.Meta:     true,
		atom.Base:     true,
		atom.Title:    true,
		atom.Head:     true,
	}

	// allowedElements renders as-is. Unlisted elements are unwrapped: the tag
	// goes, its children stay, so unknown markup degrades to visible text
	// rather than vanishing.
	allowedElements = map[atom.Atom]bool{
		atom.A: true, atom.Abbr: true, atom.B: true, atom.Blockquote: true,
		atom.Br: true, atom.Caption: true, atom.Center: true, atom.Cite: true,
		atom.Code: true, atom.Col: true, atom.Colgroup: true, atom.Dd: true,
		atom.Del: true, atom.Div: true, atom.Dl: true, atom.Dt: true,
		atom.Em: true, atom.Figcaption: true, atom.Figure: true, atom.H1: true,
		atom.H2: true, atom.H3: true, atom.H4: true, atom.H5: true, atom.H6: true,
		atom.Hr: true, atom.I: true, atom.Img: true, atom.Ins: true, atom.Kbd: true,
		atom.Li: true, atom.Mark: true, atom.Ol: true, atom.P: true, atom.Pre: true,
		atom.Q: true, atom.S: true, atom.Samp: true, atom.Small: true,
		atom.Span: true, atom.Strike: true, atom.Strong: true, atom.Sub: true,
		atom.Sup: true, atom.Table: true, atom.Tbody: true, atom.Td: true,
		atom.Tfoot: true, atom.Th: true, atom.Thead: true, atom.Tr: true,
		atom.U: true, atom.Ul: true, atom.Var: true, atom.Wbr: true,
	}

	// allowedAttrs is per-element. A global "safe attributes" list would allow
	// e.g. href on non-anchors, where it does nothing useful and only widens
	// the surface.
	allowedAttrs = map[atom.Atom]map[string]bool{
		atom.A:          {"href": true, "title": true},
		atom.Img:        {"src": true, "alt": true, "title": true, "width": true, "height": true},
		atom.Td:         {"colspan": true, "rowspan": true, "align": true, "valign": true},
		atom.Th:         {"colspan": true, "rowspan": true, "align": true, "valign": true},
		atom.Col:        {"span": true, "width": true},
		atom.Table:      {"width": true, "border": true, "cellpadding": true, "cellspacing": true},
		atom.Ol:         {"start": true, "type": true},
		atom.Li:         {"value": true},
		atom.Blockquote: {"cite": true},
		atom.Q:          {"cite": true},
		atom.Del:        {"cite": true},
		atom.Ins:        {"cite": true},
	}

	// urlAttrs must pass the scheme allowlist. Everything else is copied
	// verbatim (it is a number, an enumeration, or a colour).
	urlAttrs = map[string]bool{"href": true, "src": true, "cite": true}
)

// SanitizeNewsletterHTML rewrites untrusted newsletter HTML so it is safe to
// insert into a live document.
//
// It never returns an error: a fragment the parser cannot handle is returned as
// escaped plain text rather than dropped, because silently rendering nothing
// looks like a broken feed and is harder to notice than visible text.
func SanitizeNewsletterHTML(raw string) string {
	if strings.TrimSpace(raw) == "" {
		return ""
	}
	node, err := html.ParseFragment(strings.NewReader(raw), &html.Node{
		Type:     html.ElementNode,
		Data:     "div",
		DataAtom: atom.Div,
	})
	if err != nil || node == nil {
		// Unparseable: show it as text rather than losing it or trusting it.
		return escapeNewsletterText(raw)
	}
	var b strings.Builder
	for _, n := range node {
		writeSanitizedNode(&b, n)
	}
	return b.String()
}

// writeSanitizedNode renders one node and its subtree into b, applying the rules.
func writeSanitizedNode(b *strings.Builder, n *html.Node) {
	switch n.Type {
	case html.TextNode:
		b.WriteString(escapeNewsletterText(n.Data))
		return
	case html.CommentNode:
		// Comments are dropped outright: they are invisible in a browser but
		// are a classic conditional-comment/script smuggling path.
		return
	case html.DocumentNode:
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			writeSanitizedNode(b, c)
		}
		return
	case html.ElementNode:
	default:
		return
	}

	tagAtom := n.DataAtom
	if dropWithContents[tagAtom] {
		return
	}
	if !allowedElements[tagAtom] {
		// Unknown element: unwrap it so its text survives.
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			writeSanitizedNode(b, c)
		}
		return
	}

	name := strings.ToLower(n.Data)
	b.WriteString("<" + name)
	// Attributes are emitted in the original order, filtered.
	for _, a := range n.Attr {
		key := strings.ToLower(a.Key)
		// A disallowed attribute is skipped silently. This is the filter that
		// removes every on* handler and the style attribute, since neither
		// appears on any allowlist.
		if !allowedAttrs[tagAtom][key] {
			continue
		}
		val := a.Val
		if urlAttrs[key] {
			val = safeMarkdownURL(val)
			if val == "" {
				// Unsafe scheme (javascript:, data:, ...). Drop the attribute;
				// for an anchor the text stays, so the content is not lost.
				continue
			}
		}
		b.WriteString(" " + key + `="` + redditAttrEscape(val) + `"`)
	}
	// Remote images: keep them (a newsletter is mostly images) but do not leak
	// the reader's referrer to whatever host serves them.
	if tagAtom == atom.Img {
		b.WriteString(` loading="lazy" referrerpolicy="no-referrer"`)
	}

	if isVoidElement(tagAtom) {
		b.WriteString(">")
		return
	}
	b.WriteString(">")
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		writeSanitizedNode(b, c)
	}
	b.WriteString("</" + name + ">")
}

// isVoidElement reports whether an element cannot have children, so writing a
// closing tag for it would be invalid HTML.
func isVoidElement(a atom.Atom) bool {
	switch a {
	case atom.Area, atom.Base, atom.Br, atom.Col, atom.Embed, atom.Hr,
		atom.Img, atom.Input, atom.Link, atom.Meta, atom.Param, atom.Source,
		atom.Track, atom.Wbr:
		return true
	}
	return false
}

// escapeNewsletterText escapes text for an HTML text context.
//
// It is a function of its own rather than a call to html.EscapeString so the
// escaping rules are stated in one place: & first, then the delimiters, so
// already-escaped input is not double-escaped on the ampersand.
func escapeNewsletterText(s string) string {
	if !strings.ContainsAny(s, "&<>") {
		return s
	}
	return strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
	).Replace(s)
}
