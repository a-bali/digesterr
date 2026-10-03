// Package linkify turns raw user-authored text (Reddit markdown, HN/Lobsters
// plain text, tweet text) into display HTML with clickable links.
//
// It exists because the same conversion is needed on every surface that shows
// comment text: the web UI item pane, the web UI digest view, and both Google
// Reader API item paths. Each surface used to escape-and-forget, so a URL
// written by a comment author was dead text — including the markdown links
// Reddit authors write as `[label](url)`.
//
// The rules, in order:
//
//  1. Decode HTML entities in the raw text exactly once. Comment sources encode
//     them inconsistently (Reddit stores `&gt;` for a blockquote marker AND
//     `&amp;` for a literal ampersand), so the text is normalized first and
//     re-escaped once at the end. Decoding after escaping instead leaves
//     `&amp;amp;` inside a generated href, which the browser reads as a query
//     parameter literally named `amp;t` — a broken link that looks fine in the
//     source. 266 stored comments carried that artifact before this package.
//  2. Convert Reddit's internal `![alt](giphy|ID)` form to a real giphy URL.
//  3. Markdown images `![alt](url)` become <img>; they must be claimed before
//     markdown links, which match a superset of their syntax.
//  4. Markdown links `[label](url)` become <a>.
//  5. App-only Reddit comment-video links become an inline player. They must be
//     claimed before the bare-URL pass, which would otherwise linkify them into
//     a page Reddit serves as "r/subreddit is banned" (see comment_video.go).
//  6. Bare image-host URLs become <img> (an image is a better rendering than a
//     link to an image).
//  7. Remaining bare URLs (http/https, and `www.` with no scheme) become <a>.
//     This runs on the not-yet-escaped text and BEFORE the generated tags are
//     restored, so a markdown link's own label can never be linkified into a
//     nested anchor.
//  8. Escape everything, then restore the generated tags.
//
// Every href is scheme-checked: no `javascript:`, `data:`, or `vbscript:` URL
// can reach an attribute, whether it arrived as a markdown link or as bare text.
package linkify

import (
	"fmt"
	"html"
	"regexp"
	"strings"
)

var (
	// redditGiphyRe matches Reddit's internal giphy syntax: ![alt](giphy|ID)
	redditGiphyRe = regexp.MustCompile(`!\[([^\]]*)\]\(giphy\|([^)]+)\)`)

	// imgTagRe matches markdown image syntax: ![alt](url)
	imgTagRe = regexp.MustCompile(`!\[([^\]]*)\]\(([^)]+)\)`)

	// mdLinkRe matches markdown link syntax: [label](url). Only http(s) targets
	// are considered, so `[x](foo)` is left as written text.
	mdLinkRe = regexp.MustCompile(`\[([^\]\n]*)\]\((https?://[^)\s]+)\)`)

	// bareImageURLRe matches bare image URLs from common image hosts.
	// Split into giphy (multiple subdomains, share URLs) and other hosts.
	bareImageURLRe = regexp.MustCompile(
		`https?://` +
			`(?:media\d*\.giphy\.com/\S+\.(?:gif|webp|png|jpg|jpeg)(?:\?\S*)?` +
			`|giphy\.com/gifs/\S+` +
			`|[^/\s]*\.?(?:i\.redd\.it|preview\.redd\.it|imgur\.com|i\.imgur\.com|files\.catbox\.moe|cdn\.discordapp\.com)/\S+\.(?:jpg|jpeg|png|gif|webp|svg)(?:\?\S*)?)`)

	// bareURLRe matches a URL with no markup around it. `www.` is included
	// because authors write hostnames the way they read them.
	bareURLRe = regexp.MustCompile(`(?:https?://|www\.)[^\s<>"']+`)
)

// Text renders raw comment text as display HTML: markdown BLOCK structure
// (blockquotes, lists, code fences — see blocks.go) wrapping the inline marks
// rendered by textInline.
//
// The result is a sequence of complete top-level elements, so a caller must NOT
// wrap it in a paragraph.
func Text(s string) string {
	if s == "" {
		return ""
	}

	// Normalize entities ONCE, for the whole body and BEFORE splitting into
	// blocks: sources encode the blockquote marker as &gt;, so splitting first
	// would never see a quote.
	s = html.UnescapeString(s)

	return renderBlocks(s, textInline)
}

// textInline renders the INLINE marks of one block of comment text. HTML
// entities must already be decoded by the caller.
//
// The rules, in order:
//
//  1. Reddit's internal `![alt](giphy|ID)` form becomes a real giphy URL.
//  2. Markdown images `![alt](url)` become <img>; they must be claimed before
//     markdown links, which match a superset of their syntax.
//  3. Markdown links `[label](url)` become <a>.
//  4. App-only Reddit comment-video links become an inline player. They must be
//     claimed before the bare-URL pass, which would otherwise linkify them into
//     a page Reddit serves as "r/subreddit is banned" (see comment_video.go).
//  5. Bare image-host URLs become <img> (an image is a better rendering than a
//     link to an image).
//  6. Remaining bare URLs (http/https, and `www.` with no scheme) become <a>.
//     This runs on the not-yet-escaped text and BEFORE the generated tags are
//     restored, so a markdown link's own label can never be linkified into a
//     nested anchor.
//  7. Escape everything, then restore the generated tags.
//
// Every href is scheme-checked: no `javascript:`, `data:`, or `vbscript:` URL
// can reach an attribute, whether it arrived as a markdown link or as bare text.
func textInline(s string) string {
	if s == "" {
		return ""
	}

	// Step 1: Reddit's internal giphy syntax → real URL.
	s = redditGiphyRe.ReplaceAllStringFunc(s, func(match string) string {
		parts := redditGiphyRe.FindStringSubmatch(match)
		if len(parts) < 3 {
			return match
		}
		return fmt.Sprintf("![%s](https://media.giphy.com/media/%s/giphy.gif)", parts[1], parts[2])
	})

	tags := make(map[string]string) // placeholder → generated tag
	next := func(tag string) string {
		key := fmt.Sprintf("\x00T%d\x00", len(tags))
		tags[key] = tag
		return key
	}

	// Step 3: markdown images.
	s = imgTagRe.ReplaceAllStringFunc(s, func(match string) string {
		parts := imgTagRe.FindStringSubmatch(match)
		if len(parts) < 3 {
			return match
		}
		src := strings.TrimSpace(parts[2])
		if !SafeHref(src) {
			return match
		}
		return next(imgTag(src, parts[1]))
	})

	// Step 4: markdown links. An empty label falls back to the URL itself, so
	// `[](url)` still renders something clickable.
	s = mdLinkRe.ReplaceAllStringFunc(s, func(match string) string {
		parts := mdLinkRe.FindStringSubmatch(match)
		if len(parts) < 3 {
			return match
		}
		label, target := parts[1], parts[2]
		if !SafeHref(target) {
			return match
		}
		if strings.TrimSpace(label) == "" {
			label = target
		}
		return next(anchorHTML(target, label))
	})

	// Step 5: app-only Reddit comment-video links become an inline player. These
	// must be claimed before the bare-URL pass below, which would otherwise turn
	// them into a link to a page Reddit renders as "r/subreddit is banned".
	s = replaceCommentVideos(s, next)

	// Step 5 (see textInline doc): bare image URLs.
	s = bareImageURLRe.ReplaceAllStringFunc(s, func(match string) string {
		if !SafeHref(match) {
			return match
		}
		return next(imgTag(match, ""))
	})

	// Step 6: remaining bare URLs. Runs before step 7 restores the tags above,
	// so a link's own label is never linkified again.
	s = replaceBareURLs(s, next)

	// Step 7: escape and materialise in a SINGLE pass.
	//
	// This used to escape the whole string and then run one strings.ReplaceAll
	// per generated tag over the whole string. That is O(tags x len) in both
	// time and allocation, and it dominated comment rendering: a 13 KB comment
	// block with ~200 links copied ~2.6 MB, a 52 KB block ~76 MB (measured
	// 9.2 ms and 111 ms respectively on the Celeron in this deployment). A
	// Reader client fetching ~98 items with 10 comments each therefore spent
	// minutes of CPU here alone. Emitting each tag exactly once is O(len+tags).
	return renderEscaped(s, tags)
}

// renderEscaped escapes the literal text, keeps line breaks as <br>, and
// substitutes the NUL-delimited placeholders created by next — in one pass.
//
// Generated tags are emitted verbatim (they are already-safe markup built by
// anchorHTML/imgTag); every other byte is escaped with exactly the same
// substitutions html.EscapeString makes, so output is unchanged. A NUL that is
// not a known placeholder is passed through untouched, keeping the function
// total for arbitrary input.
func renderEscaped(s string, tags map[string]string) string {
	var b strings.Builder
	b.Grow(len(s) + len(s)/4)

	for {
		i := strings.IndexByte(s, 0)
		if i < 0 {
			writeEscaped(&b, s)
			return b.String()
		}
		writeEscaped(&b, s[:i])

		rest := s[i:]
		end := strings.IndexByte(rest[1:], 0)
		if end < 0 {
			writeEscaped(&b, rest)
			return b.String()
		}
		key := rest[:end+2] // both NUL delimiters inclusive
		if tag, ok := tags[key]; ok {
			b.WriteString(tag)
			s = rest[len(key):]
			continue
		}
		// NUL that is not a placeholder: keep it and resume after it.
		writeEscaped(&b, rest[:1])
		s = rest[1:]
	}
}

// writeEscaped appends s to b with the same substitutions html.EscapeString
// applies, plus newline-to-<br>, without allocating an intermediate string.
func writeEscaped(b *strings.Builder, s string) {
	last := 0
	for i := 0; i < len(s); i++ {
		var repl string
		switch s[i] {
		case '&':
			repl = "&amp;"
		case '<':
			repl = "&lt;"
		case '>':
			repl = "&gt;"
		case '"':
			repl = "&#34;"
		case '\'':
			repl = "&#39;"
		case '\n':
			repl = "<br>"
		default:
			continue
		}
		b.WriteString(s[last:i])
		b.WriteString(repl)
		last = i + 1
	}
	b.WriteString(s[last:])
}

// replaceBareURLs wraps bare URLs in anchors, leaving text inside an attribute
// untouched. It is used only by Text, on text whose generated tags have not
// been restored yet.
func replaceBareURLs(s string, next func(string) string) string {
	matches := bareURLRe.FindAllStringIndex(s, -1)
	if len(matches) == 0 {
		return s
	}
	var b strings.Builder
	last := 0
	for _, m := range matches {
		start, end := m[0], m[1]
		if start < last || isAttributeURL(s, start) {
			continue
		}
		// `awww.example.com` is one hostname, not a URL; a URL never starts in
		// the middle of a word.
		if start > 0 && isWordByte(s[start-1]) {
			continue
		}
		raw := trimURLTail(s[start:end])
		if raw == "" {
			continue
		}
		href := raw
		if strings.HasPrefix(strings.ToLower(raw), "www.") {
			href = "https://" + raw
		}
		if !SafeHref(href) {
			continue
		}
		b.WriteString(s[last:start])
		b.WriteString(next(anchorHTML(href, raw)))
		// Trailing punctuation trimmed off the URL stays in the text.
		last = start + len(raw)
	}
	b.WriteString(s[last:])
	return b.String()
}

// trimURLTail removes trailing punctuation and quote-entity fragments that the
// URL regex absorbs because they are not whitespace. `see https://x.com/a.`
// must link the URL, not the sentence's full stop; a balanced `)` belongs to
// the URL (`.../Foo_(bar)`) while an unmatched one closes the surrounding text.
func trimURLTail(raw string) string {
	s := raw
	for {
		changed := false
		for _, suffix := range []string{"&#39;", "&#34;", "&quot;", "&gt;", "&lt;", "&amp;"} {
			if strings.HasSuffix(s, suffix) {
				s = strings.TrimSuffix(s, suffix)
				changed = true
			}
		}
		if !changed {
			if trimmed := strings.TrimRight(s, ".,;:!?"); trimmed != s {
				s = trimmed
				changed = true
			}
		}
		if !changed && strings.HasSuffix(s, ")") && strings.Count(s, ")") > strings.Count(s, "(") {
			s = s[:len(s)-1]
			changed = true
		}
		if !changed {
			return s
		}
	}
}

// isWordByte reports whether b can be part of a word, used to reject a URL
// match that starts in the middle of one.
func isWordByte(b byte) bool {
	switch {
	case b >= 'a' && b <= 'z', b >= 'A' && b <= 'Z', b >= '0' && b <= '9':
		return true
	case b == '_' || b == '-' || b == '.':
		return true
	case b == '@':
		return true
	}
	return false
}

// isAttributeURL reports whether the URL at start sits inside an existing
// attribute value (href=…, src=…), where it must not be linkified again. Raw
// HTML in a comment body is the case that matters; escaped quotes are checked
// because this runs both before and after escaping.
func isAttributeURL(s string, start int) bool {
	if start == 0 {
		return false
	}
	if c := s[start-1]; c == '>' || c == '/' {
		return false
	}
	i := start - 1
	switch {
	case s[i] == '"' || s[i] == '\'':
		i--
	case strings.HasSuffix(s[:start], "&#34;") || strings.HasSuffix(s[:start], "&#39;"):
		i = start - len("&#34;")
	default:
		return false
	}
	for i >= 0 && s[i] == ' ' {
		i--
	}
	if i < 0 || s[i] != '=' {
		return false
	}
	i--
	for i >= 0 && s[i] == ' ' {
		i--
	}
	head := strings.ToLower(s[:i+1])
	return strings.HasSuffix(head, "href") || strings.HasSuffix(head, "src")
}

// anchorHTML builds a link with the attributes every surface needs: a new tab
// and no referrer/opener leak to the linked site.
func anchorHTML(href, label string) string {
	return `<a href="` + html.EscapeString(href) + `" target="_blank" rel="noopener noreferrer">` +
		html.EscapeString(label) + `</a>`
}

// imgTag builds the image tag used for markdown and bare image URLs.
func imgTag(src, alt string) string {
	return fmt.Sprintf(
		`<img src="%s" alt="%s" style="max-width:400px;max-height:300px;width:auto;height:auto;border-radius:4px;margin:4px 0" loading="lazy" />`,
		html.EscapeString(src), html.EscapeString(alt),
	)
}

// SafeHref reports whether raw may be used as an href. Only http(s) is allowed,
// after trimming whitespace; anything that could break out of the attribute
// (quotes, angle brackets, whitespace) is rejected too. This is the single gate
// for both markdown links and bare-URL linkification — `javascript:` in a
// comment must never become a clickable link.
func SafeHref(raw string) bool {
	s := strings.TrimSpace(raw)
	if s == "" {
		return false
	}
	lower := strings.ToLower(s)
	if !strings.HasPrefix(lower, "http://") && !strings.HasPrefix(lower, "https://") {
		return false
	}
	return !strings.ContainsAny(s, " \t\n\r\"'<>")
}
