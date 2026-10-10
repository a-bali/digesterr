package linkify

import (
	"net/url"
	"strings"

	"golang.org/x/net/html"
)

// twitterMediaHost is the only host whose players need the Referer dropped.
const twitterMediaHost = "video.twimg.com"

// TwitterVideoReferrerFix prepends a no-referrer policy to a body that carries a
// twitter video player, and returns the body unchanged otherwise.
//
// video.twimg.com answers 403 to any request whose Referer is not an X/Twitter
// host: every twitter video URL stored in the dev DB returns 403 with a
// non-X Referer and 200 without one (39/39 each way), while x.com/twitter.com/
// twimg.com referrers pass. The CDN gate is a crude host check, so digesterr's
// own origin fails it.
//
// Both surfaces reach the CDN with a digesterr Referer. The web UI's page origin
// IS the digesterr host and the default strict-origin-when-cross-origin policy
// sends it; Reader clients render article bodies in a web view whose base URL is
// the feed's htmlUrl (a digesterr URL), so their player request carries it too.
// The poster a player shows comes from pbs.twimg.com, which is not gated the same
// way, so the failure presents as a still frame that never plays.
//
// A per-element referrerpolicy attribute cannot fix this: media loads resolve the
// policy from the DOCUMENT, not the element — WebKit's
// HTMLMediaElement::mediaPlayerReferrer reads document().referrerPolicy(). A
// <meta name="referrer"> does apply, and WebKit honours it on insertion without
// requiring head placement, which is what makes it usable here: Reader clients
// render this body as a fragment inside their own template, so there is no head
// to put it in. The web UI is covered by the document-level tag in index.html.
//
// Preferring this over proxying the media keeps the CDN's own bytes and avoids
// buffering multi-hundred-MB clips through the app.
func TwitterVideoReferrerFix(body string) string {
	if !hasTwitterVideoPlayer(body) || strings.HasPrefix(body, twitterNoReferrerMeta) {
		return body
	}
	return twitterNoReferrerMeta + body
}

// UpgradeMediaPlayers applies every read-time media fix to a stored body: the
// Reddit HLS/audio upgrade, then the twitter Referer fix. Both rewrite the same
// call sites in the Reader render path, so they are composed here rather than
// left for each caller to remember.
//
// Reading order matters for the twitter fix only in that it must come after the
// Reddit upgrade: the upgrade rebuilds video blocks, and a body holding both a
// Reddit and a twitter player must still carry the policy for the twitter one.
func UpgradeMediaPlayers(body string) string {
	return TwitterVideoReferrerFix(UpgradeRedditVideos(body))
}

// twitterNoReferrerMeta is emitted as a body prefix. It is a plain string, not a
// template, because nothing in it is derived from the body.
const twitterNoReferrerMeta = `<meta name="referrer" content="no-referrer">`

// hasTwitterVideoPlayer reports whether body contains a video element whose own
// src or one of whose source children points at the twitter media host. Scoping
// to a real player keeps the policy off bodies that merely mention such a URL.
func hasTwitterVideoPlayer(body string) bool {
	if !strings.Contains(body, twitterMediaHost) {
		return false
	}
	z := html.NewTokenizer(strings.NewReader(body))
	inVideo := false
	for {
		tt := z.Next()
		if tt == html.ErrorToken {
			return false
		}
		if tt == html.EndTagToken {
			if name, _ := z.TagName(); inVideo && string(name) == "video" {
				inVideo = false
			}
			continue
		}
		if tt != html.StartTagToken && tt != html.SelfClosingTagToken {
			continue
		}
		name, hasAttr := z.TagName()
		switch string(name) {
		case "video":
			inVideo = true
			if hasAttr && twitterMediaSrc(z) {
				return true
			}
		case "source":
			if inVideo && hasAttr && twitterMediaSrc(z) {
				return true
			}
		}
	}
}

// twitterMediaSrc reports whether the current tag's src attribute is an exact
// HTTPS asset on the twitter media host.
func twitterMediaSrc(z *html.Tokenizer) bool {
	for {
		key, val, more := z.TagAttr()
		if string(key) == "src" && isTwitterMediaURL(string(val)) {
			return true
		}
		if !more {
			return false
		}
	}
}

// isTwitterMediaURL accepts only an exact HTTPS URL on the twitter media host.
func isTwitterMediaURL(raw string) bool {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme != "https" || u.Host != twitterMediaHost {
		return false
	}
	return true
}
