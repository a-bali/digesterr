package linkify

import (
	"regexp"
	"strings"
)

// Reddit's comment-video deep link.
//
// A comment that carries a video has NO media field on the comment object at all
// — verified against the live API, where the key set is body, body_html,
// permalink, link_id and friends and nothing else. The only place the media id
// exists is the URL the author pasted into the body:
//
//	https://reddit.com/link/<comment_id>/video/<media_id>/player
//
// That path is an app-only construct. Reddit's web handler rewrites it into a
// comment permalink using the literal string "subreddit" as the subreddit, so it
// 301s to
//
//	https://www.reddit.com/r/subreddit/comments/<id>/?new_reddit=true
//
// and r/subreddit happens to be a real, banned subreddit — so an anonymous
// visitor lands on "r/subreddit is banned". Every comment id and every
// User-Agent tried yields the same literal, so the placeholder is hardcoded in
// Reddit's redirect rather than derived from the request.
//
// The media id is all that is needed: https://v.redd.it/<media_id>/ serves the
// asset directly and unsigned (checked with and without the ?f=&v=&a= params
// Reddit's own player uses — both 200, identical manifest).
var (
	// commentVideoURLRe finds the construct anywhere in a body, so a comment
	// that writes prose around the link is handled too.
	commentVideoURLRe = regexp.MustCompile(
		`(?i)(?:https?://)?(?:www\.|old\.|new\.|m\.)?reddit\.com/link/` +
			`[A-Za-z0-9]+/video/([A-Za-z0-9]+)/player/?`)

	// commentVideoAnchoredRe matches the construct and NOTHING else, for callers
	// holding a candidate URL and asking whether it is one.
	commentVideoAnchoredRe = regexp.MustCompile(
		`(?i)^(?:https?://)?(?:www\.|old\.|new\.|m\.)?reddit\.com/link/` +
			`[A-Za-z0-9]+/video/([A-Za-z0-9]+)/player/?$`)
)

// CommentVideoMediaID reports whether raw is an app-only Reddit comment-video
// URL, returning its media id.
//
// Only the media id is taken from raw; every URL built from it comes from a
// fixed template rather than from rewriting the input, so a crafted URL cannot
// steer a generated src at another host.
func CommentVideoMediaID(raw string) (string, bool) {
	m := commentVideoAnchoredRe.FindStringSubmatch(strings.TrimSpace(raw))
	if m == nil {
		return "", false
	}
	return m[1], true
}

// CommentVideoMediaURL is the address of a comment video's media: the HLS master
// manifest, which is the only asset Reddit serves that carries BOTH picture and
// sound. The progressive CMAF_*.mp4 files are video-only — confirmed by parsing
// the MP4 boxes, where every v.redd.it/<id>/CMAF_*.mp4 has a `vide` track and no
// `soun` track, while CMAF_AUDIO_*.mp4 has `soun` alone. This URL answers 200
// with no query string, so no signature is needed to reach it.
func CommentVideoMediaURL(mediaID string) string {
	return "https://v.redd.it/" + mediaID + "/HLSPlaylist.m3u8"
}

// CommentVideoHTML uses HLS first for native readers, followed by progressive
// fallbacks. Keep the watch link outside video so supported players show it.
func CommentVideoHTML(mediaID string) string {
	if !redditMediaIDRe.MatchString(mediaID) {
		return ""
	}
	src := "https://v.redd.it/" + mediaID + "/"
	return `<video controls preload="metadata" style="max-width:100%;border-radius:4px">` +
		`<source src="` + src + `HLSPlaylist.m3u8" type="application/vnd.apple.mpegurl" />` +
		`<source src="` + src + `CMAF_720.mp4" type="video/mp4" />` +
		`<source src="` + src + `CMAF_480.mp4" type="video/mp4" />` +
		`</video><a href="` + src + `HLSPlaylist.m3u8" target="_blank" rel="noopener noreferrer">Watch video ↗</a>`
}

// replaceCommentVideos swaps every app-only comment-video URL in s for an inline
// player, leaving other text untouched.
//
// It runs BEFORE the bare-URL pass, which would otherwise turn the URL into a
// link to a page Reddit renders as "r/subreddit is banned". Like replaceBareURLs
// it takes plain string positions rather than a regex replacement, so it can
// reject a match that starts mid-word (`xhttps://…`) or sits inside a generated
// attribute.
func replaceCommentVideos(s string, next func(string) string) string {
	matches := commentVideoURLRe.FindAllStringIndex(s, -1)
	if len(matches) == 0 {
		return s
	}
	var b strings.Builder
	last := 0
	for _, m := range matches {
		start, end := m[0], m[1]
		// A match overlapping one already consumed is a duplicate find, not a
		// second link.
		if start < last || isAttributeURL(s, start) {
			continue
		}
		// `xreddit.com/link/...` is one word, not a URL.
		if start > 0 && isWordByte(s[start-1]) {
			continue
		}
		mediaID, ok := CommentVideoMediaID(s[start:end])
		if !ok {
			continue
		}
		b.WriteString(s[last:start])
		b.WriteString(next(CommentVideoHTML(mediaID)))
		last = end
	}
	b.WriteString(s[last:])
	return b.String()
}
