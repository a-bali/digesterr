package feed

import (
	"encoding/xml"
	"testing"
)

// The site a feed declares is what reader clients are pointed at to find the
// feed's icon, so extraction must be exact and must refuse anything that is not
// an absolute http(s) address.
func TestFeedSiteURL(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string
	}{
		{
			name: "rss channel link",
			body: `<?xml version="1.0"?><rss version="2.0"><channel><title>t</title>
<link>https://krebsonsecurity.com</link><item><title>i</title></item></channel></rss>`,
			want: "https://krebsonsecurity.com",
		},
		{
			name: "rss link is trimmed of surrounding whitespace",
			body: `<?xml version="1.0"?><rss version="2.0"><channel><title>t</title>
<link>
   https://example.com/blog/
</link></channel></rss>`,
			want: "https://example.com/blog/",
		},
		{
			// A feed served from a different origin than the site it describes
			// is the whole reason this value is worth storing.
			name: "atom alternate link wins over the feed's own origin",
			body: `<?xml version="1.0"?><feed xmlns="http://www.w3.org/2005/Atom"><title>t</title>
<id>urn:uuid:1</id>
<link rel="self" href="https://feedproxy.example.net/atom"/>
<link rel="alternate" type="text/html" href="https://simonwillison.net/"/>
</feed>`,
			want: "https://simonwillison.net/",
		},
		{
			name: "atom link with no rel is an alternate",
			body: `<?xml version="1.0"?><feed xmlns="http://www.w3.org/2005/Atom"><title>t</title>
<id>urn:uuid:1</id><link href="https://example.org/"/></feed>`,
			want: "https://example.org/",
		},
		{
			name: "feed declaring no link yields empty",
			body: `<?xml version="1.0"?><rss version="2.0"><channel><title>t</title></channel></rss>`,
			want: "",
		},
		{
			// javascript: and data: would be executed or rendered if a client
			// treated them as a page to visit.
			name: "javascript link is refused",
			body: `<?xml version="1.0"?><rss version="2.0"><channel><title>t</title>
<link>javascript:alert(1)</link></channel></rss>`,
			want: "",
		},
		{
			name: "relative link is refused",
			body: `<?xml version="1.0"?><rss version="2.0"><channel><title>t</title>
<link>/blog</link></channel></rss>`,
			want: "",
		},
		{
			name: "non-feed payload yields empty",
			body: `{"not":"a feed"}`,
			want: "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := feedSiteURL([]byte(tc.body)); got != tc.want {
				t.Fatalf("feedSiteURL() = %q, want %q", got, tc.want)
			}
		})
	}
}

// A parsed RSS document must expose its channel link through the same parser
// path that extracts it, so the value can never disagree with what a full parse
// sees.
func TestFeedSiteURLMatchesParsedRSSLink(t *testing.T) {
	var doc struct {
		Channel struct {
			Link string `xml:"link"`
		} `xml:"channel"`
	}
	if err := xml.Unmarshal([]byte(testRSS), &doc); err != nil {
		t.Fatalf("unmarshal fixture: %v", err)
	}
	if doc.Channel.Link == "" {
		t.Fatal("fixture has no channel link")
	}
	if got := feedSiteURL([]byte(testRSS)); got != doc.Channel.Link {
		t.Fatalf("feedSiteURL() = %q, want %q (the channel <link>)", got, doc.Channel.Link)
	}
	if got := feedSiteURL([]byte(testAtom)); got != "https://example.com" {
		t.Fatalf("atom feedSiteURL() = %q, want %q", got, "https://example.com")
	}
}
