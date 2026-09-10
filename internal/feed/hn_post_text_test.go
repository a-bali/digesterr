package feed

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"digesterr/internal/store"
)

// hnCommentsLink is the body news.ycombinator.com/rss ships for every item:
// a description holding nothing but a link to the comments page. Ask HN, Show
// HN, and text-only stories are indistinguishable from link stories in that
// feed — none of them carry a post body.
const hnCommentsLink = `<a href="https://news.ycombinator.com/item?id=777">Comments</a>`

// hnRSSItem renders one item in the shape news.ycombinator.com/rss uses.
func hnRSSItem(title, link, commentsURL string) string {
	return `<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0">
  <channel>
    <title>Hacker News</title>
    <link>https://news.ycombinator.com/</link>
    <description>Hacker News</description>
    <item>
      <title>` + title + `</title>
      <link>` + link + `</link>
      <description><![CDATA[<a href="` + commentsURL + `">Comments</a>]]></description>
      <pubDate>Thu, 10 Sep 2026 06:00:00 +0000</pubDate>
      <comments>` + commentsURL + `</comments>
    </item>
  </channel>
</rss>`
}

func TestHNContentIsMissing(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    bool
	}{
		{"empty", "", true},
		{"whitespace only", " \n\t ", true},
		{"comments link", hnCommentsLink, true},
		{"comments link with surrounding whitespace", "\n" + hnCommentsLink + "\n", true},
		{"plain comments word", "Comments", true},
		{"real body", "<p>Announcement body</p>", false},
		{"body followed by comments link", "<p>Body</p>" + hnCommentsLink, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := hnContentIsMissing(tt.content); got != tt.want {
				t.Errorf("hnContentIsMissing(%q) = %v, want %v", tt.content, got, tt.want)
			}
		})
	}
}

func TestApplyHNPostText(t *testing.T) {
	tests := []struct {
		name    string
		content string
		text    string
		want    string
	}{
		{"fills a comments-link body", hnCommentsLink, "<p>Body</p>", "<p>Body</p>"},
		{"fills an empty body", "", "<p>Body</p>", "<p>Body</p>"},
		{"trims the api text", "", "\n<p>Body</p>\n", "<p>Body</p>"},
		{"keeps an existing body", "<p>Existing</p>", "<p>From api</p>", "<p>Existing</p>"},
		{"ignores empty api text", hnCommentsLink, "", hnCommentsLink},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			item := &store.Item{Content: tt.content}
			applyHNPostText(item, tt.text)
			if item.Content != tt.want {
				t.Errorf("content = %q, want %q", item.Content, tt.want)
			}
		})
	}
}

func TestHNEnricherEnrichSetsPostText(t *testing.T) {
	const postText = "<p>DSeek plans to officially release the V4.1 Flash model.</p>"
	body, _ := json.Marshal(map[string]interface{}{
		"id": 777, "score": 42, "descendants": 3, "text": postText,
	})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(body)
	}))
	defer server.Close()

	e := &HNEnricher{client: server.Client(), firebaseURL: server.URL}
	item := &store.Item{
		CommentUrl: "https://news.ycombinator.com/item?id=777",
		Content:    hnCommentsLink,
	}
	if err := e.Enrich(context.Background(), item); err != nil {
		t.Fatalf("Enrich failed: %v", err)
	}
	if item.Content != postText {
		t.Errorf("content = %q, want %q", item.Content, postText)
	}
	if item.Points != 42 || item.CommentsCount != 3 {
		t.Errorf("points/comments = %d/%d, want 42/3", item.Points, item.CommentsCount)
	}
}

func TestHNEnricherBulkEnrichSetsPostText(t *testing.T) {
	const postText = "<p>Announcement body</p>"
	body, _ := json.Marshal(map[string]interface{}{
		"hits": []map[string]interface{}{
			{"objectID": "777", "points": 345, "num_comments": 179, "story_text": postText},
		},
	})
	algolia := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(body)
	}))
	defer algolia.Close()
	firebase := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("null"))
	}))
	defer firebase.Close()

	e := &HNEnricher{client: algolia.Client(), algoliaURL: algolia.URL, firebaseURL: firebase.URL}
	item := &store.Item{
		Hash:       "hn:777",
		CommentUrl: "https://news.ycombinator.com/item?id=777",
		Url:        "https://news.ycombinator.com/item?id=777",
		Content:    hnCommentsLink,
	}
	if err := e.BulkEnrich(context.Background(), []*store.Item{item}); err != nil {
		t.Fatalf("BulkEnrich failed: %v", err)
	}
	if item.Content != postText {
		t.Errorf("content = %q, want %q", item.Content, postText)
	}
}

// TestPollFeedFillsHNPostTextOnInsert is the reported bug end to end: a fresh
// HN item is polled, the feed ships no body, and the post text from the HN API
// must be stored so the reader has something to show.
func TestPollFeedFillsHNPostTextOnInsert(t *testing.T) {
	queries := setupFeedLoggingDB(t)
	user := createFeedLoggingUser(t, queries)

	const postText = "<p>DSeek plans to officially release the V4.1 Flash model.</p>"
	fb := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp, _ := json.Marshal(map[string]interface{}{
			"id": 777, "score": 345, "descendants": 179, "text": postText,
		})
		w.Write(resp)
	}))
	defer fb.Close()
	// Algolia unavailable, so the Firebase fallback supplies the body.
	broken := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer broken.Close()

	rssURL, setBody := mutableRSSBodyServer(t)
	itemURL := "https://news.ycombinator.com/item?id=777"
	setBody(hnRSSItem("DeepSeek launching v4.1 flash cheaper and more capable than v4 pro", itemURL, itemURL))

	feedObj, err := queries.CreateFeed(t.Context(), store.CreateFeedParams{
		UserID: user.ID,
		Type:   "hn",
		Name:   "Hacker News",
		Url:    rssURL,
		Config: "{}",
		Status: "active",
	})
	if err != nil {
		t.Fatalf("create feed: %v", err)
	}

	poller := NewPoller(queries)
	enricher := NewHNEnricher()
	enricher.firebaseURL = fb.URL
	enricher.algoliaURL = broken.URL
	orig := poller.enrichers["hn"]
	poller.enrichers["hn"] = enricher
	defer func() { poller.enrichers["hn"] = orig }()

	if err := poller.PollFeed(context.Background(), feedObj); err != nil {
		t.Fatalf("PollFeed: %v", err)
	}

	items, err := queries.ListItemsByFeed(t.Context(), feedObj.ID)
	if err != nil {
		t.Fatalf("list items: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	if items[0].Content != postText {
		t.Errorf("stored content = %q, want the post body %q", items[0].Content, postText)
	}
}

// TestEnrichmentFillsHNPostTextForExistingItem covers items inserted before the
// body was available. The API scores match the stored ones exactly, so nothing
// but the body changes — the update must still be persisted.
func TestEnrichmentFillsHNPostTextForExistingItem(t *testing.T) {
	queries := setupFeedLoggingDB(t)
	user := createFeedLoggingUser(t, queries)

	const postText = "<p>Body from the HN API.</p>"
	itemURL := "https://news.ycombinator.com/item?id=777"
	body, _ := json.Marshal(map[string]interface{}{
		"hits": []map[string]interface{}{
			{"objectID": "777", "points": 345, "num_comments": 179, "story_text": postText},
		},
	})
	algolia := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(body)
	}))
	defer algolia.Close()
	firebase := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("null"))
	}))
	defer firebase.Close()

	feedObj, err := queries.CreateFeed(t.Context(), store.CreateFeedParams{
		UserID: user.ID,
		Type:   "hn",
		Name:   "Hacker News",
		Url:    "https://example.com/hn",
		Config: "{}",
		Status: "active",
	})
	if err != nil {
		t.Fatalf("create feed: %v", err)
	}

	item, err := queries.CreateItem(t.Context(), store.CreateItemParams{
		FeedID:        feedObj.ID,
		Hash:          "hn:777",
		Title:         "DeepSeek launching v4.1 flash",
		Url:           itemURL,
		Content:       hnCommentsLink,
		CommentUrl:    itemURL,
		Points:        345,
		CommentsCount: 179,
		Status:        "unread",
	})
	if err != nil {
		t.Fatalf("create item: %v", err)
	}

	poller := NewPoller(queries)
	enricher := &HNEnricher{client: algolia.Client(), algoliaURL: algolia.URL, firebaseURL: firebase.URL}
	orig := poller.enrichers["hn"]
	poller.enrichers["hn"] = enricher
	defer func() { poller.enrichers["hn"] = orig }()

	poller.enrichForPoll(context.Background(), feedObj, nil, slog.Default())

	got, err := queries.GetItem(t.Context(), item.ID)
	if err != nil {
		t.Fatalf("get item: %v", err)
	}
	if got.Content != postText {
		t.Errorf("stored content = %q, want %q", got.Content, postText)
	}
	if got.Points != 345 || got.CommentsCount != 179 {
		t.Errorf("points/comments = %d/%d, want 345/179 (unchanged)", got.Points, got.CommentsCount)
	}
}
