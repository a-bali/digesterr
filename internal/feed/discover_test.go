package feed

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// rssFeedDoc is a minimal RSS 2.0 document.
func rssFeedDoc(title string) string {
	return `<?xml version="1.0"?><rss version="2.0"><channel><title>` + title + `</title>` +
		`<link>https://example.com/</link><item><title>Post</title><link>https://example.com/1</link></item></channel></rss>`
}

// atomFeedDoc is a minimal Atom document.
func atomFeedDoc(title string) string {
	return `<?xml version="1.0"?><feed xmlns="http://www.w3.org/2005/Atom"><title>` + title + `</title>` +
		`<entry><title>Post</title><id>https://example.com/1</id></entry></feed>`
}

func TestDiscoverFeedNameRSS(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/rss+xml")
		_, _ = w.Write([]byte(rssFeedDoc("Herald &amp; Times Feed")))
	}))
	defer srv.Close()

	p := NewPoller(nil)
	name, err := p.DiscoverFeedName(context.Background(), "rss", srv.URL)
	if err != nil {
		t.Fatalf("DiscoverFeedName: %v", err)
	}
	if name != "Herald & Times Feed" {
		t.Errorf("name = %q, want %q", name, "Herald & Times Feed")
	}
}

func TestDiscoverFeedNameAtom(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(atomFeedDoc("Atom Example")))
	}))
	defer srv.Close()

	p := NewPoller(nil)
	name, err := p.DiscoverFeedName(context.Background(), "atom", srv.URL)
	if err != nil {
		t.Fatalf("DiscoverFeedName: %v", err)
	}
	if name != "Atom Example" {
		t.Errorf("name = %q, want %q", name, "Atom Example")
	}
}

// hn and lobsters have an enricher but no fetcher: their URL is an RSS feed
// parsed by the RSS fetcher, so discovery must route them there too.
func TestDiscoverFeedNameHNAndLobstersUseRSSFetcher(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(rssFeedDoc("Hacker News")))
	}))
	defer srv.Close()

	p := NewPoller(nil)
	for _, feedType := range []string{"hn", "lobsters"} {
		name, err := p.DiscoverFeedName(context.Background(), feedType, srv.URL)
		if err != nil {
			t.Fatalf("%s: DiscoverFeedName: %v", feedType, err)
		}
		if name != "Hacker News" {
			t.Errorf("%s: name = %q, want %q", feedType, name, "Hacker News")
		}
	}
}

func TestDiscoverFeedNameReddit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/v1/access_token":
			_, _ = w.Write([]byte(`{"access_token":"tok","token_type":"bearer","expires_in":3600}`))
		case strings.HasPrefix(r.URL.Path, "/r/golang/about"):
			if got := r.Header.Get("Authorization"); got != "Bearer tok" {
				t.Errorf("Authorization = %q, want %q", got, "Bearer tok")
			}
			// Reddit's own about payload carries the prefixed display name.
			_, _ = w.Write([]byte(`{"data":{"display_name":"golang","display_name_prefixed":"r/golang","title":"The Go Programming Language"}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	p := NewPoller(nil)
	rf, ok := p.fetchers["reddit"].(*RedditFetcher)
	if !ok {
		t.Fatal("reddit fetcher missing")
	}
	rf.baseURL = srv.URL
	rf.apiBaseURL = srv.URL
	rf.SetCredentials("id", "secret")

	name, err := p.DiscoverFeedName(context.Background(), "reddit", "https://www.reddit.com/r/golang")
	if err != nil {
		t.Fatalf("DiscoverFeedName: %v", err)
	}
	if name != "r/golang" {
		t.Errorf("name = %q, want %q", name, "r/golang")
	}
}

// A payload without display_name_prefixed still yields r/<display_name>.
func TestDiscoverFeedNameRedditFallsBackToDisplayName(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/access_token" {
			_, _ = w.Write([]byte(`{"access_token":"tok","token_type":"bearer","expires_in":3600}`))
			return
		}
		_, _ = w.Write([]byte(`{"data":{"display_name":"golang"}}`))
	}))
	defer srv.Close()

	p := NewPoller(nil)
	rf := p.fetchers["reddit"].(*RedditFetcher)
	rf.baseURL = srv.URL
	rf.apiBaseURL = srv.URL
	rf.SetCredentials("id", "secret")

	name, err := p.DiscoverFeedName(context.Background(), "reddit", "https://www.reddit.com/r/golang")
	if err != nil {
		t.Fatalf("DiscoverFeedName: %v", err)
	}
	if name != "r/golang" {
		t.Errorf("name = %q, want %q", name, "r/golang")
	}
}

func TestDiscoverFeedNameTwitter(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "UserByScreenName") {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"user": map[string]any{
			"result": map[string]any{
				"rest_id": "44196397",
				"legacy":  map[string]any{"name": "Elon Musk", "screen_name": "elonmusk"},
			},
		}}})
	}))
	defer srv.Close()

	p := NewPoller(nil)
	tf := p.fetchers["twitter"].(*TwitterFetcher)
	tf.baseURL = srv.URL
	tf.SetCredentials("ct0-test", "auth-test")

	name, err := p.DiscoverFeedName(context.Background(), "twitter", "https://x.com/elonmusk")
	if err != nil {
		t.Fatalf("DiscoverFeedName: %v", err)
	}
	if name != "Elon Musk" {
		t.Errorf("name = %q, want %q", name, "Elon Musk")
	}
}

// A profile without a display name falls back to the handle rather than
// leaving the field empty.
func TestDiscoverFeedNameTwitterFallsBackToHandle(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"user": map[string]any{
			"result": map[string]any{
				"rest_id": "113419064",
				"legacy":  map[string]any{"name": "", "screen_name": "golang"},
			},
		}}})
	}))
	defer srv.Close()

	p := NewPoller(nil)
	tf := p.fetchers["twitter"].(*TwitterFetcher)
	tf.baseURL = srv.URL
	tf.SetCredentials("ct0-test", "auth-test")

	name, err := p.DiscoverFeedName(context.Background(), "twitter", "https://x.com/golang")
	if err != nil {
		t.Fatalf("DiscoverFeedName: %v", err)
	}
	if name != "golang" {
		t.Errorf("name = %q, want %q", name, "golang")
	}
}

func TestDiscoverFeedNameErrors(t *testing.T) {
	p := NewPoller(nil)

	if _, err := p.DiscoverFeedName(context.Background(), "bogus", "https://example.com/feed"); err == nil {
		t.Error("unsupported feed type should error")
	}

	// Unreachable host: error, never a panic or a hang.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url := srv.URL
	srv.Close()
	if _, err := p.DiscoverFeedName(context.Background(), "rss", url); err == nil {
		t.Error("unreachable feed should error")
	}

	// Non-feed content: no title to find.
	html := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<html><head><title>Not a feed</title></head></html>`))
	}))
	defer html.Close()
	if _, err := p.DiscoverFeedName(context.Background(), "rss", html.URL); err == nil {
		t.Error("non-feed content should error")
	}
}
