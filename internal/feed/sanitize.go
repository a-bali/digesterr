package feed

import (
	"io"
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
//
// SanitizeFeedHTML is the same allowlist under the historical feed-content
// name: RSS/Atom bodies are untrusted publisher HTML on the same live DOM as
// newsletters, so they get the same treatment.
func SanitizeFeedHTML(raw string) string {
	return SanitizeNewsletterHTML(raw)
}

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
		b.WriteString(escapeNewsletterText(collapseFillerRuns(n.Data)))
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
	if tagAtom == atom.Img && isTrackingPixel(n) {
		// A 1x1 pixel exists only to report a read. It renders as a broken
		// image box in a client whose image loader is blocked or offline, and
		// it carries no content, so dropping it loses nothing. Matching is
		// deliberately narrow: an explicit 1x1 with no alt text. Anything the
		// author could have meant as content stays.
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
		writeImgExtraAttrs(b, n)
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

// imgConstraintStyle keeps a remote image inside whatever width the reader
// gives the article.
//
// Without it an image whose natural size exceeds the view (1000-2000px wide
// newsletter artwork) renders at full size and forces the article horizontally
// scrollable: the text then CONTINUES past the right edge instead of wrapping,
// which is what a "not word-wrapped any more" report actually looks like.
//
// A `width` attribute is not enough. Newsletters set width="100%" on a
// scaffolding table cell, so the percentage resolves against a table that is
// itself sized by its content, and an unsized image contributes nothing to
// shrink. An inline max-width beats both cases, and it is the only lever that
// travels with the HTML: a third-party Reader client inserts this body into its
// own article view and applies no stylesheet of ours. Measured in bare Chromium
// over four real bodies: 297-1372px of horizontal overrun before, 0px after.
//
// height:auto is not decoration — a width/height pair encoded in the attributes
// would otherwise be honoured literally, squashing the aspect ratio once
// max-width kicks in.
//
// This is a CONSTANT rather than publisher CSS: the sanitizer still drops the
// style attribute, so nothing a feed writes can reach this slot.
const imgConstraintStyle = ` style="max-width:100%;height:auto"`

// UpgradeStoredBody repairs a body that was stored before the image and filler
// rules existed, so existing items render correctly without a re-poll.
//
// The rules themselves live in the sanitizer's emit path, but that path only
// runs on INGEST: a body already in the database was written by an older
// version and keeps its old markup forever, because the poller only processes
// never-seen items. That is exactly why the regression showed up on items
// arriving after a publisher changed its template and not on older ones.
//
// This is deliberately a TOKEN pass, not SanitizeNewsletterHTML over the stored
// body. Stored bodies are not all sanitizer output: the Reddit and Twitter paths
// build <video> players and linkify builds markup the sanitizer's allowlist does
// not contain, so re-sanitizing would delete 400+ real players from history.
// Rewriting only the img tags and the text runs through the tokenizer leaves
// every other byte of the body exactly as it was.
//
// It is idempotent: an image that already carries the attributes is emitted with
// its existing ones left alone, and a collapsed run has no run left to collapse.
func UpgradeStoredBody(body string) string {
	if strings.TrimSpace(body) == "" {
		return body
	}
	// Cheap pre-check: a body with no image and no filler run cannot be changed
	// by this pass. The Reader contents path renders up to 100 bodies per
	// request, so the common case must not pay for a tokenizer run. Case
	// insensitive because HTML tag names are, and a body written by another
	// generator may well use <IMG>.
	if !strings.Contains(strings.ToLower(body), "<img") && !strings.ContainsFunc(body, isFillerRune) {
		return body
	}

	var b strings.Builder
	b.Grow(len(body) + len(body)/64)
	z := html.NewTokenizer(strings.NewReader(body))
	for {
		tt := z.Next()
		if tt == html.ErrorToken {
			if z.Err() == io.EOF {
				break
			}
			// Unparseable tail: keep the remaining bytes rather than losing them.
			b.Write(z.Raw())
			break
		}
		switch tt {
		case html.TextToken:
			b.WriteString(escapeNewsletterText(collapseFillerRuns(string(z.Text()))))
		case html.StartTagToken, html.SelfClosingTagToken:
			name, _ := z.TagName()
			if string(name) != "img" {
				b.Write(z.Raw())
				continue
			}
			writeUpgradedImgTag(&b, z, tt == html.SelfClosingTagToken)
		default:
			b.Write(z.Raw())
		}
	}
	return b.String()
}

// writeUpgradedImgTag emits an img element with the attributes the read path
// requires, keeping every attribute the body already had.
//
// The existing attributes are re-read from the tokenizer and copied in order, so
// a body that already carries width/alt/srcset keeps them; only the missing
// pieces are appended. That is what makes the transform idempotent and
// non-destructive.
func writeUpgradedImgTag(b *strings.Builder, z *html.Tokenizer, selfClosing bool) {
	type kv struct{ k, v string }
	var attrs []kv
	seen := map[string]bool{}
	hasAlt, constrainsWidth := false, false
	styleIdx := -1
	for {
		k, v, more := z.TagAttr()
		if k == nil && !more {
			break
		}
		key := string(k)
		lower := strings.ToLower(key)
		seen[lower] = true
		switch lower {
		case "alt":
			hasAlt = true
		case "style":
			styleIdx = len(attrs)
			// The body may already carry a width constraint of its own (the
			// linkify image builder emits max-width:100% with a border radius),
			// in which case there is nothing to add.
			if strings.Contains(strings.ToLower(string(v)), "max-width") {
				constrainsWidth = true
			}
		}
		attrs = append(attrs, kv{key, string(v)})
		if !more {
			break
		}
	}

	// A tag may only carry ONE style attribute, so a body that already has one
	// gets the constraint appended to it rather than a second attribute (which
	// browsers resolve by keeping the first, silently discarding ours).
	if !constrainsWidth && styleIdx >= 0 {
		attrs[styleIdx].v = strings.TrimRight(attrs[styleIdx].v, "; ") + ";max-width:100%;height:auto"
		constrainsWidth = true
	}

	b.WriteString("<img")
	for _, a := range attrs {
		b.WriteString(" " + a.k + `="` + redditAttrEscape(a.v) + `"`)
	}
	if !seen["loading"] {
		b.WriteString(` loading="lazy"`)
	}
	if !seen["referrerpolicy"] {
		b.WriteString(` referrerpolicy="no-referrer"`)
	}
	if !constrainsWidth {
		b.WriteString(imgConstraintStyle)
	}
	if !hasAlt {
		b.WriteString(` alt=""`)
	}
	if selfClosing {
		b.WriteString(" />")
		return
	}
	b.WriteString(">")
}

// isTrackingPixel reports whether an img node is a read receipt: explicitly
// 1x1 and carrying no alt text.
func isTrackingPixel(n *html.Node) bool {
	var w, h, alt string
	var hasW, hasH bool
	for _, a := range n.Attr {
		switch strings.ToLower(a.Key) {
		case "width":
			w, hasW = strings.TrimSpace(a.Val), true
		case "height":
			h, hasH = strings.TrimSpace(a.Val), true
		case "alt":
			alt = strings.TrimSpace(a.Val)
		}
	}
	return hasW && hasH && w == "1" && h == "1" && alt == ""
}

// writeImgExtraAttrs emits the attributes every stored image gets, whether it
// arrives through the sanitizer (ingest) or is repaired in place (a body stored
// before these rules existed).
//
// Shared rather than duplicated because the Reader view is what the rules exist
// for, and the two paths must produce identical markup: a body that was
// sanitized at ingest and one that was upgraded later must not render
// differently in a client.
func writeImgExtraAttrs(b *strings.Builder, n *html.Node) {
	// Keep the image inside whatever width the reader gives the article.
	b.WriteString(` loading="lazy" referrerpolicy="no-referrer"` + imgConstraintStyle)
	// Every image must carry an alt attribute, empty when the publisher did not
	// supply one. This is not an accessibility afterthought: an <img> with NO
	// alt that fails to load renders a 16x16 broken-image box, while the same
	// element with alt="" renders nothing. Ad beacons are shipped exactly that
	// way — no alt, no dimensions — and a body here carried three of them,
	// pointing at a tracker that redirects to an ad exchange and serves no image
	// bytes. Measured in bare Chromium: a failed image with no alt occupies
	// 16x16; adding alt="" takes it to 0x0, at any configured width.
	//
	// So the rule is deliberately about the ELEMENT, not the URL: no host list,
	// no path matching, nothing that needs a feed-specific exception.
	// Populating a missing alt is also the one change that cannot hide content,
	// because the attribute is empty by definition.
	if !hasAltAttr(n) {
		b.WriteString(` alt=""`)
	}
}

// hasAltAttr reports whether an img node carries an alt attribute at all,
// including an explicitly empty one.
//
// Presence is what matters, not content: HTML renders alt="" as nothing when the
// image fails, while a missing alt renders the browser's broken-image glyph.
func hasAltAttr(n *html.Node) bool {
	for _, a := range n.Attr {
		if strings.EqualFold(a.Key, "alt") {
			return true
		}
	}
	return false
}

// collapseFillerRuns replaces runs of invisible filler characters with a single
// space, and removes runs that are entirely zero-width.
//
// Newsletters pad their preheader with a run like "&nbsp;\u200c\u200c…" hundreds
// of characters long so the mail client's inbox preview fills with the real
// summary. That run is invisible, but it is still TEXT: it has no break
// opportunity, so it sets a minimum content width for every block that contains
// it. In a narrow pane the paragraph cannot wrap below that width, so the whole
// article inherits a huge scroll width and every line appears clipped at the
// right edge. Measured on the affected body: a 500-character run forced 1372px
// of overrun in a 720px pane; collapsing it to one space took that to 0.
//
// The collapse is per text node, which is where the run physically lives.
// Nothing visible is removed: a run of only zero-width characters renders as
// nothing at all, so it becomes empty rather than becoming a space.
//
// A run of exactly ONE character is left alone. A lone &nbsp; is ordinary
// typography — it keeps "10 kg" and "5 km/h" from breaking — and rewriting it
// to an ordinary space would change how the text wraps. Only longer runs are the
// padding this targets.
func collapseFillerRuns(s string) string {
	if !strings.ContainsFunc(s, isFillerRune) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	var run strings.Builder // the exact bytes seen in the current filler run
	runLen := 0             // run length in CHARACTERS (nbsp and zero-width are multi-byte)
	flush := func() {
		if runLen == 0 {
			return
		}
		switch {
		case runLen == 1:
			// Ordinary typography: preserve the character verbatim.
			b.WriteString(run.String())
		case strings.Contains(run.String(), "\u00a0"):
			// At least one non-breaking space: the run occupied visible
			// width, so a single space preserves the author's separation.
			b.WriteByte(' ')
		default:
			// Purely zero-width: it rendered as nothing, so it leaves nothing.
		}
		run.Reset()
		runLen = 0
	}
	for _, r := range s {
		if isFillerRune(r) {
			run.WriteRune(r)
			runLen++
			continue
		}
		// An ordinary space already provides a break opportunity, so it ends the
		// run: collapsing across it would merge two separately-spaced words.
		flush()
		b.WriteRune(r)
	}
	flush()
	return b.String()
}

// isFillerRune reports whether r participates in a filler run: the invisible
// characters newsletters pad with, plus the spaces that can separate them.
//
// A run has to be longer than one character to be filler — a single &nbsp; is
// ordinary typography (it keeps "10 kg" together) and is left alone.
func isFillerRune(r rune) bool {
	switch r {
	case '\u00a0', '\u200b', '\u200c', '\u200d', '\u2060', '\ufeff':
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
