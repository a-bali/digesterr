package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"digesterr/internal/store"
)

// The subscription list must tell a reader client where each feed's CONTENT
// lives, not where digesterr lives.
//
// NetNewsWire resolves a feed's icon from the home page it is handed: the
// page's apple-touch-icon, then og:image, then its rel=icon, then
// /favicon.ico. digesterr used to send its own /feeds/<id> page here, so every
// subscription resolved to digesterr's logo — and before the web UI had a
// favicon, to an HTML body the client cached as "no icon", which is why feeds
// appeared to have no favicons at all. This pins the three states htmlUrl can
// be in.
func TestReaderSubscriptionListHTMLURLIsTheFeedSite(t *testing.T) {
	server, queries := setupTestServer(t)
	user := createReaderUser(t, queries, "testkey123")

	cases := []struct {
		name     string
		feedType string
		url      string
		siteURL  string
		want     string
	}{
		{
			name:     "recorded site wins over the feed URL's origin",
			feedType: "rss",
			url:      "https://feed-proxy.example.net/simonwillison.xml",
			siteURL:  "https://simonwillison.net/",
			want:     "https://simonwillison.net/",
		},
		{
			// A twitter feed's URL is its profile page, whose og:image is that
			// account's avatar. Collapsing it to the origin would give every
			// account the same x.com logo.
			name:     "twitter profile page, not its origin",
			feedType: "twitter",
			url:      "https://x.com/opencode",
			siteURL:  "",
			want:     "https://x.com/opencode",
		},
		{
			// An rss feed's URL is a feed DOCUMENT, not a page: pointing a
			// client at it yields XML, so only the origin is meaningful.
			name:     "rss feed document collapses to its origin",
			feedType: "rss",
			url:      "https://krebsonsecurity.com/feed/",
			siteURL:  "",
			want:     "https://krebsonsecurity.com",
		},
		{
			// An email feed's "url" is an address, not a URL: no origin can be
			// derived, and inventing one would point the client at nonsense.
			name:     "email address yields no home page",
			feedType: "email",
			url:      "newsletters+tag@agentmail.to",
			siteURL:  "",
			want:     "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			created, err := queries.CreateFeed(t.Context(), store.CreateFeedParams{
				UserID: user.ID,
				Type:   tc.feedType,
				Name:   tc.name,
				Url:    tc.url,
				Config: "{}",
				Status: "active",
			})
			if err != nil {
				t.Fatalf("create feed: %v", err)
			}
			if tc.siteURL != "" {
				if err := queries.UpdateFeedSiteURL(t.Context(), store.UpdateFeedSiteURLParams{
					ID:      created.ID,
					SiteUrl: tc.siteURL,
				}); err != nil {
					t.Fatalf("set site url: %v", err)
				}
			}

			subs := readerSubscriptions(t, server)
			var found *readerSubscription
			for i := range subs {
				if subs[i].ID == feedStreamID(created.ID) {
					found = &subs[i]
					break
				}
			}
			if found == nil {
				t.Fatalf("subscription for feed/%d missing", created.ID)
			}
			if found.HTMLURL != tc.want {
				t.Fatalf("htmlUrl = %q, want %q", found.HTMLURL, tc.want)
			}
			if strings.Contains(found.HTMLURL, "/feeds/") {
				t.Fatalf("htmlUrl points at digesterr's own feed page: %q", found.HTMLURL)
			}
		})
	}
}

// An empty site_url must not erase what a poll already learned: the update is
// conditional, so a feed document that stops declaring a <link> keeps the last
// known site instead of regressing to the feed URL's origin.
func TestUpdateFeedSiteURLEmptyValueIsANoOp(t *testing.T) {
	_, queries := setupTestServer(t)
	user := createReaderUser(t, queries, "testkey123")

	created, err := queries.CreateFeed(t.Context(), store.CreateFeedParams{
		UserID: user.ID, Type: "rss", Name: "Feed", Url: "https://example.com/feed", Config: "{}", Status: "active",
	})
	if err != nil {
		t.Fatalf("create feed: %v", err)
	}
	if err := queries.UpdateFeedSiteURL(t.Context(), store.UpdateFeedSiteURLParams{
		ID: created.ID, SiteUrl: "https://example.com/",
	}); err != nil {
		t.Fatalf("set site url: %v", err)
	}
	if err := queries.UpdateFeedSiteURL(t.Context(), store.UpdateFeedSiteURLParams{
		ID: created.ID, SiteUrl: "",
	}); err != nil {
		t.Fatalf("clear site url: %v", err)
	}

	got, err := queries.GetFeed(t.Context(), store.GetFeedParams{ID: created.ID, UserID: user.ID})
	if err != nil {
		t.Fatalf("get feed: %v", err)
	}
	if got.SiteUrl != "https://example.com/" {
		t.Fatalf("site_url = %q, want the previously recorded value", got.SiteUrl)
	}
}

// iconUrl is what non-NetNewsWire Reader clients read. It must never point at
// a feed type that has no favicon (email), and must be present for the rest.
func TestReaderSubscriptionListIconURL(t *testing.T) {
	server, queries := setupTestServer(t)
	user := createReaderUser(t, queries, "testkey123")

	hn, err := queries.CreateFeed(t.Context(), store.CreateFeedParams{
		UserID: user.ID, Type: "hn", Name: "HN", Url: "https://news.ycombinator.com/rss", Config: "{}", Status: "active",
	})
	if err != nil {
		t.Fatalf("create hn feed: %v", err)
	}
	blog, err := queries.CreateFeed(t.Context(), store.CreateFeedParams{
		UserID: user.ID, Type: "rss", Name: "Blog", Url: "https://krebsonsecurity.com/feed/", Config: "{}", Status: "active",
	})
	if err != nil {
		t.Fatalf("create rss feed: %v", err)
	}
	mail, err := queries.CreateFeed(t.Context(), store.CreateFeedParams{
		UserID: user.ID, Type: "email", Name: "Mail", Url: "newsletters+tag@agentmail.to", Config: "{}", Status: "active",
	})
	if err != nil {
		t.Fatalf("create email feed: %v", err)
	}

	want := map[string]string{
		feedStreamID(hn.ID):   "https://news.ycombinator.com/favicon.ico",
		feedStreamID(blog.ID): "https://krebsonsecurity.com/favicon.ico",
		feedStreamID(mail.ID): "",
	}

	subs := readerSubscriptions(t, server)
	seen := 0
	for _, sub := range subs {
		w, ok := want[sub.ID]
		if !ok {
			continue
		}
		seen++
		if sub.IconURL != w {
			t.Errorf("%s iconUrl = %q, want %q", sub.ID, sub.IconURL, w)
		}
	}
	if seen != len(want) {
		t.Fatalf("matched %d of %d subscriptions", seen, len(want))
	}
}

// Digests have no site of their own, so their htmlUrl stays on digesterr where
// the rendered digest is readable, and they carry no icon.
func TestReaderSubscriptionListDigestHasNoIconURL(t *testing.T) {
	server, queries := setupTestServer(t)
	user := createReaderUser(t, queries, "testkey123")
	cfg, err := queries.CreateDigestConfig(t.Context(), store.CreateDigestConfigParams{
		UserID: user.ID, Name: "Daily", Schedule: "daily", Config: "{}",
	})
	if err != nil {
		t.Fatalf("create digest config: %v", err)
	}

	subs := readerSubscriptions(t, server)
	for _, sub := range subs {
		if sub.ID != digestStreamID(cfg.ID) {
			continue
		}
		if sub.IconURL != "" {
			t.Errorf("digest iconUrl = %q, want empty", sub.IconURL)
		}
		if !strings.HasSuffix(sub.HTMLURL, fmt.Sprintf("/digests/%d", cfg.ID)) {
			t.Errorf("digest htmlUrl = %q, want digesterr's digest page", sub.HTMLURL)
		}
		return
	}
	t.Fatalf("subscription for digest/%d missing", cfg.ID)
}

// readerSubscriptions fetches and decodes the subscription list for the seeded
// API key.
func readerSubscriptions(t *testing.T, server *Server) []readerSubscription {
	t.Helper()
	w := readerRequest(server, http.MethodGet, "/reader/api/0/subscription/list?output=json", "", map[string]string{
		"Authorization": "GoogleLogin auth=testkey123",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp readerSubscriptionList
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	return resp.Subscriptions
}
