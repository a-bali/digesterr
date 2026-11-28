package feed

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"digesterr/internal/store"
)

// A poll must record the site the feed document declares, because that address
// is what reader clients are handed as the feed's home page — and they resolve
// the feed's icon from it. A feed served from a different origin than the site
// it describes (mirrors, proxies, aggregators) is the case that matters: without
// this, the client would look for the icon on the mirror.
func TestPollFeedRecordsDeclaredSiteURL(t *testing.T) {
	queries := setupFeedLoggingDB(t)
	user := createFeedLoggingUser(t, queries)

	body := `<?xml version="1.0"?><rss version="2.0"><channel>
<title>Mirrored Feed</title>
<link>https://the-real-site.example/</link>
<item><title>Item</title><link>https://the-real-site.example/1</link>
<guid>1</guid><description>body</description></item>
</channel></rss>`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/rss+xml")
		w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)

	f, err := queries.CreateFeed(t.Context(), store.CreateFeedParams{
		UserID: user.ID, Type: "rss", Name: "Mirrored Feed", Url: srv.URL, Config: "{}", Status: "active",
	})
	if err != nil {
		t.Fatalf("create feed: %v", err)
	}
	if f.SiteUrl != "" {
		t.Fatalf("precondition: site_url should start empty, got %q", f.SiteUrl)
	}

	if err := NewPoller(queries).PollFeed(t.Context(), f); err != nil {
		t.Fatalf("poll: %v", err)
	}

	got, err := queries.GetFeed(t.Context(), store.GetFeedParams{ID: f.ID, UserID: user.ID})
	if err != nil {
		t.Fatalf("get feed: %v", err)
	}
	if got.SiteUrl != "https://the-real-site.example/" {
		t.Fatalf("site_url = %q, want the feed's declared <link>", got.SiteUrl)
	}
}

// A feed that declares no <link> must leave site_url empty rather than storing
// the feed server's origin: an empty value makes the reader fall back to a
// guess, whereas a wrong stored value would look authoritative.
func TestPollFeedLeavesSiteURLEmptyWhenFeedDeclaresNone(t *testing.T) {
	queries := setupFeedLoggingDB(t)
	user := createFeedLoggingUser(t, queries)

	body := `<?xml version="1.0"?><rss version="2.0"><channel>
<title>No Link Feed</title>
<item><title>Item</title><link>https://example.com/1</link>
<guid>1</guid><description>body</description></item>
</channel></rss>`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/rss+xml")
		w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)

	f, err := queries.CreateFeed(t.Context(), store.CreateFeedParams{
		UserID: user.ID, Type: "rss", Name: "No Link Feed", Url: srv.URL, Config: "{}", Status: "active",
	})
	if err != nil {
		t.Fatalf("create feed: %v", err)
	}
	if err := NewPoller(queries).PollFeed(t.Context(), f); err != nil {
		t.Fatalf("poll: %v", err)
	}

	got, err := queries.GetFeed(t.Context(), store.GetFeedParams{ID: f.ID, UserID: user.ID})
	if err != nil {
		t.Fatalf("get feed: %v", err)
	}
	if got.SiteUrl != "" {
		t.Fatalf("site_url = %q, want empty when the feed declares no link", got.SiteUrl)
	}
}
