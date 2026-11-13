package httpapi

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"digesterr/internal/store"
)

func TestReaderSummarizeLinkThresholdAndBodyPlacement(t *testing.T) {
	server, queries, _ := setupStreamTestServer(t)
	server.summaryLinkMin = 100
	server.publicBaseURL = "https://digesterr.example"
	user := createReaderUser(t, queries, "testkey123")
	f, err := queries.CreateFeed(t.Context(), store.CreateFeedParams{
		UserID: user.ID, Type: "rss", Name: "Feed", Url: "https://feed.example/rss", Status: "active",
	})
	if err != nil {
		t.Fatalf("create feed: %v", err)
	}

	longBody := "<p>" + strings.Repeat("A sufficiently long article body. ", 8) + "</p>"
	longItem, err := queries.CreateItem(t.Context(), store.CreateItemParams{
		FeedID: f.ID, Hash: "reader-summary-long", Title: "Long article", Url: "https://feed.example/article",
		Content: longBody, Status: "unread",
	})
	if err != nil {
		t.Fatalf("create long item: %v", err)
	}
	shortItem, err := queries.CreateItem(t.Context(), store.CreateItemParams{
		FeedID: f.ID, Hash: "reader-summary-short", Title: "Short article", Url: "https://feed.example/short",
		Content: "<p>Brief.</p>", Status: "unread",
	})
	if err != nil {
		t.Fatalf("create short item: %v", err)
	}

	fetch := func(id int64) readerContentStreamItem {
		t.Helper()
		path := "/reader/api/0/stream/items/contents?i=" + strconv.FormatInt(id, 10)
		w := readerRequest(server, http.MethodGet, path, "", map[string]string{"Authorization": "GoogleLogin auth=testkey123"})
		if w.Code != http.StatusOK {
			t.Fatalf("contents status = %d: %s", w.Code, w.Body.String())
		}
		var resp readerContentsResponse
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("decode contents: %v", err)
		}
		if len(resp.Items) != 1 {
			t.Fatalf("items = %d, want 1", len(resp.Items))
		}
		return resp.Items[0]
	}

	t.Run("long item gets top link in both bodies", func(t *testing.T) {
		item := fetch(longItem.ID)
		body := item.Summary.Content
		wantHref := "https://digesterr.example/items/" + strconv.FormatInt(longItem.ID, 10) + "/summary"
		if !strings.HasPrefix(body, `<p class="digest-meta"><a href="`+wantHref+`" target="_blank" rel="noopener noreferrer">[Summarize]</a></p>`) {
			t.Fatalf("summarize link not at top of body: %q", body[:min(len(body), 240)])
		}
		if item.Content.Content != body {
			t.Fatal("summary.content and content.content differ")
		}
		if strings.Count(body, "Summarize") != 1 {
			t.Fatalf("Summarize link count = %d, want 1", strings.Count(body, "Summarize"))
		}
	})

	t.Run("short item has no link", func(t *testing.T) {
		item := fetch(shortItem.ID)
		if strings.Contains(item.Summary.Content, "/summary") {
			t.Fatalf("short body unexpectedly contains summarize link: %s", item.Summary.Content)
		}
	})

	t.Run("zero disables link", func(t *testing.T) {
		server.summaryLinkMin = 0
		item := fetch(longItem.ID)
		if strings.Contains(item.Summary.Content, "/summary") {
			t.Fatalf("disabled link unexpectedly present: %s", item.Summary.Content)
		}
	})
}

func TestItemVisibleTextLenPrefersCompletedExtraction(t *testing.T) {
	item := store.Item{Content: "<p>short</p>", ExtractContent: "<p>" + strings.Repeat("x", 250) + "</p>", ExtractStatus: "done"}
	if got := itemVisibleTextLen(item); got != 250 {
		t.Fatalf("itemVisibleTextLen() = %d, want 250", got)
	}
	item.ExtractStatus = "pending"
	if got := itemVisibleTextLen(item); got != 5 {
		t.Fatalf("itemVisibleTextLen() with pending extraction = %d, want 5", got)
	}
}
