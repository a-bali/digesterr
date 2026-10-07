package linkify

import (
	stdhtml "html"
	"io"
	"net/url"
	"regexp"
	"strings"

	"golang.org/x/net/html"
)

var redditMediaIDRe = regexp.MustCompile(`^[A-Za-z0-9]+$`)
var redditAssetRe = regexp.MustCompile(`^/([A-Za-z0-9]+)/(?:HLSPlaylist\.m3u8|(?:CMAF|DASH)_[0-9]+\.mp4)$`)

// RedditVideoMediaID accepts only exact HTTPS CDN assets (or a bare post URL).
// Never derive a media URL from a substring host match, credentials or escaped path.
func RedditVideoMediaID(raw string) (string, bool) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host != "v.redd.it" || u.User != nil || u.RawPath != "" || u.Fragment != "" {
		return "", false
	}
	if m := redditAssetRe.FindStringSubmatch(u.Path); m != nil {
		return m[1], true
	}
	id := strings.TrimPrefix(u.Path, "/")
	if redditMediaIDRe.MatchString(id) {
		return id, true
	}
	return "", false
}

// UpgradeRedditVideos upgrades stored players at read time without changing data.
// Token raw bytes are retained everywhere outside targeted video blocks, avoiding
// whole-document parse/serialization changes. Incomplete or mixed-host players
// and looping GIF players are left alone. This is not an HTML sanitizer.
func UpgradeRedditVideos(input string) string {
	z := html.NewTokenizer(strings.NewReader(input))
	var out, block strings.Builder
	active, eligible := false, true
	id := ""
	var fallbacks []string
	for {
		tt := z.Next()
		raw := string(z.Raw())
		if tt == html.ErrorToken {
			if z.Err() == io.EOF {
				out.WriteString(block.String())
			}
			return out.String()
		}
		t := z.Token()
		if !active {
			if tt != html.StartTagToken || t.Data != "video" {
				out.WriteString(raw)
				continue
			}
			active, eligible, id = true, true, ""
			block.Reset()
			fallbacks = nil
		}
		block.WriteString(raw)
		if tt == html.StartTagToken || tt == html.SelfClosingTagToken {
			if t.Data == "video" || t.Data == "source" {
				for _, a := range t.Attr {
					if t.Data == "video" && (a.Key == "loop" || a.Key == "data-reddit-gif") {
						eligible = false
					}
					if a.Key == "src" {
						candidate, ok := RedditVideoMediaID(a.Val)
						// Bare CDN paths redirect to HTML and are not legacy playable assets.
						if !ok || !strings.Contains(a.Val, ".mp4") && !strings.Contains(a.Val, ".m3u8") || id != "" && candidate != id {
							eligible = false
						} else {
							id = candidate
							if strings.Contains(a.Val, ".mp4") {
								fallbacks = append(fallbacks, a.Val)
							}
						}
					}
				}
			}
		}
		if tt == html.EndTagToken && t.Data == "video" {
			upgraded := CommentVideoHTML(id)
			for _, fallback := range fallbacks {
				src := `src="` + stdhtml.EscapeString(fallback) + `"`
				if !strings.Contains(upgraded, src) {
					upgraded = strings.Replace(upgraded, `</video>`, `<source `+src+` type="video/mp4" /></video>`, 1)
				}
			}
			player := strings.Split(upgraded, "</video>")[0] + "</video>"
			if eligible && id != "" && block.String() != player {
				out.WriteString(upgraded)
			} else {
				out.WriteString(block.String())
			}
			active = false
			block.Reset()
		}
	}
}
