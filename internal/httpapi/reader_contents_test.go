package httpapi

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// testDigestContent is a handcrafted digest result content JSON used by the
// contents tests. It carries a summary, one highlight (with llm_summary and a
// top comment), and one entry in the all-items list.
const testDigestContent = `{"summary":"Today's top stories","highlights":[{"item_id":1,"feed_id":1,"feed_name":"Feed A","title":"Top HN story","url":"https://news.ycombinator.com/item?id=1","comment_url":"https://news.ycombinator.com/item?id=1","points":100,"comments_count":50,"published_at":"2024-01-01T00:00:00Z","llm_summary":"A key insight about the story.","top_comments":[{"author":"alice","text":"Great point","points":10,"url":"https://news.ycombinator.com/item?id=2"}]}],"items":[{"item_id":2,"feed_id":1,"feed_name":"Feed A","title":"Other story","url":"https://example.com/other","comment_url":"","points":0,"comments_count":0,"published_at":"2024-01-01T00:00:00Z"}]}`

func setDigestContent(t *testing.T, db *sql.DB, id int64, content string) {
	t.Helper()
	if _, err := db.Exec("UPDATE digest_results SET content = ? WHERE id = ?", content, id); err != nil {
		t.Fatalf("failed to set digest content: %v", err)
	}
}

func getContents(t *testing.T, server *Server, path string) readerContentsResponse {
	t.Helper()
	w := readerRequest(server, http.MethodGet, path, "", map[string]string{
		"Authorization": "GoogleLogin auth=testkey123",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp readerContentsResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	return resp
}

func contentIDs(items []readerContentStreamItem) []string {
	ids := make([]string, len(items))
	for i, it := range items {
		ids[i] = it.ID
	}
	return ids
}

func hasCategory(categories []string, want string) bool {
	for _, c := range categories {
		if c == want {
			return true
		}
	}
	return false
}

func TestReaderStreamItemsContents(t *testing.T) {
	server, queries, db := setupStreamTestServer(t)
	user := createReaderUser(t, queries, "testkey123")
	seed := seedReaderStream(t, queries, db, user.ID)
	setDigestContent(t, db, seed.drNew.ID, testDigestContent)

	path := "/reader/api/0/stream/items/contents" +
		"?i=" + itemStreamID(seed.aUnreadNewest.ID) +
		"&i=" + itemStreamID(seed.aDigested.ID) +
		"&i=" + digestItemStreamID(seed.drNew.ID) +
		"&output=json"
	resp := getContents(t, server, path)

	if resp.Direction != "ltr" {
		t.Fatalf("expected direction %q, got %q", "ltr", resp.Direction)
	}
	if len(resp.Items) != 3 {
		t.Fatalf("expected 3 items, got %d: %v", len(resp.Items), contentIDs(resp.Items))
	}

	want := []string{
		itemStreamID(seed.aUnreadNewest.ID),
		itemStreamID(seed.aDigested.ID),
		digestItemStreamID(seed.drNew.ID),
	}
	if got := contentIDs(resp.Items); !equalStrings(got, want) {
		t.Fatalf("ids mismatch:\n got %v\nwant %v", got, want)
	}

	// Regular item: unread item maps to reading-list category.
	unreadItem := resp.Items[0]
	if !hasCategory(unreadItem.Categories, readerStreamReadingList) {
		t.Fatalf("expected unread item categories to contain %q, got %v", readerStreamReadingList, unreadItem.Categories)
	}
	if unreadItem.Origin.StreamID != feedStreamID(seed.feedA.ID) {
		t.Fatalf("expected origin streamId %q, got %q", feedStreamID(seed.feedA.ID), unreadItem.Origin.StreamID)
	}
	if unreadItem.Origin.Title != "Feed A" {
		t.Fatalf("expected origin title %q, got %q", "Feed A", unreadItem.Origin.Title)
	}

	// Digested item maps to read.
	digestedItem := resp.Items[1]
	if !hasCategory(digestedItem.Categories, readerStreamRead) {
		t.Fatalf("expected digested item categories to contain %q, got %v", readerStreamRead, digestedItem.Categories)
	}
	if hasCategory(digestedItem.Categories, readerStreamReadingList) {
		t.Fatalf("digested item should not be in reading-list, got %v", digestedItem.Categories)
	}

	// Digest item: rendered HTML content + origin.
	digestItem := resp.Items[2]
	if digestItem.Origin.StreamID != digestStreamID(seed.cfg.ID) {
		t.Fatalf("expected digest origin streamId %q, got %q", digestStreamID(seed.cfg.ID), digestItem.Origin.StreamID)
	}
	if digestItem.Title != "Digest: Daily" {
		t.Fatalf("expected digest title %q, got %q", "Digest: Daily", digestItem.Title)
	}
	if !strings.Contains(digestItem.Content.Content, "Top HN story") {
		t.Fatalf("expected digest content to contain highlight title, got: %s", digestItem.Content.Content)
	}
	if digestItem.Summary.Content != "Today's top stories" {
		t.Fatalf("expected digest summary %q, got %q", "Today's top stories", digestItem.Summary.Content)
	}
	if !strings.Contains(digestItem.Content.Content, "top stories") {
		t.Fatalf("expected digest content to contain summary, got: %s", digestItem.Content.Content)
	}
	if !strings.Contains(digestItem.Content.Content, "Other story") {
		t.Fatalf("expected digest content to contain all-items entry, got: %s", digestItem.Content.Content)
	}
}

func TestReaderStreamItemsContentsSavedAndUnknown(t *testing.T) {
	server, queries, db := setupStreamTestServer(t)
	user := createReaderUser(t, queries, "testkey123")
	seed := seedReaderStream(t, queries, db, user.ID)

	path := "/reader/api/0/stream/items/contents" +
		"?i=" + itemStreamID(seed.aSaved.ID) +
		"&i=item/999999" +
		"&output=json"
	resp := getContents(t, server, path)

	// Unknown id is silently skipped.
	if len(resp.Items) != 1 {
		t.Fatalf("expected 1 item (unknown skipped), got %d: %v", len(resp.Items), contentIDs(resp.Items))
	}

	saved := resp.Items[0]
	if !hasCategory(saved.Categories, readerStreamRead) {
		t.Fatalf("expected saved item to contain read category, got %v", saved.Categories)
	}
	if !hasCategory(saved.Categories, readerStreamStarred) {
		t.Fatalf("expected saved item to contain starred category, got %v", saved.Categories)
	}
}

func TestReaderStreamItemsContentsEmpty(t *testing.T) {
	server, queries, db := setupStreamTestServer(t)
	user := createReaderUser(t, queries, "testkey123")
	seedReaderStream(t, queries, db, user.ID)

	resp := getContents(t, server, "/reader/api/0/stream/items/contents?output=json")
	if len(resp.Items) != 0 {
		t.Fatalf("expected empty items without i params, got %d", len(resp.Items))
	}
}

func TestReaderStreamContentsReadingList(t *testing.T) {
	server, queries, db := setupStreamTestServer(t)
	user := createReaderUser(t, queries, "testkey123")
	seed := seedReaderStream(t, queries, db, user.ID)
	setDigestContent(t, db, seed.drNew.ID, testDigestContent)

	resp := getContents(t, server, "/reader/api/0/stream/contents/user/-/state/com.google/reading-list?output=json")

	// Unread items + new digest results, newest first.
	want := []string{
		itemStreamID(seed.aUnreadNewest.ID),
		digestItemStreamID(seed.drNew.ID),
		itemStreamID(seed.aUnreadOldest.ID),
		itemStreamID(seed.bUnread.ID),
	}
	if got := contentIDs(resp.Items); !equalStrings(got, want) {
		t.Fatalf("reading-list contents ids mismatch:\n got %v\nwant %v", got, want)
	}

	// Full content objects: the unread item carries a title and origin.
	first := resp.Items[0]
	if first.Title == "" {
		t.Fatalf("expected non-empty title for unread item")
	}
	if first.Origin.StreamID != feedStreamID(seed.feedA.ID) {
		t.Fatalf("expected origin streamId %q, got %q", feedStreamID(seed.feedA.ID), first.Origin.StreamID)
	}
}

func TestReaderStreamContentsFeed(t *testing.T) {
	server, queries, db := setupStreamTestServer(t)
	user := createReaderUser(t, queries, "testkey123")
	seed := seedReaderStream(t, queries, db, user.ID)

	resp := getContents(t, server, "/reader/api/0/stream/contents/"+feedStreamID(seed.feedA.ID)+"?output=json")

	want := []string{
		itemStreamID(seed.aSaved.ID),
		itemStreamID(seed.aUnreadNewest.ID),
		itemStreamID(seed.aRead.ID),
		itemStreamID(seed.aUnreadOldest.ID),
		itemStreamID(seed.aDigested.ID),
	}
	if got := contentIDs(resp.Items); !equalStrings(got, want) {
		t.Fatalf("feed contents ids mismatch:\n got %v\nwant %v", got, want)
	}

	for _, it := range resp.Items {
		if it.Origin.StreamID != feedStreamID(seed.feedA.ID) {
			t.Fatalf("expected all items to have origin %q, got %q", feedStreamID(seed.feedA.ID), it.Origin.StreamID)
		}
	}
}

func TestReaderStreamContentsLimit(t *testing.T) {
	server, queries, db := setupStreamTestServer(t)
	user := createReaderUser(t, queries, "testkey123")
	seed := seedReaderStream(t, queries, db, user.ID)

	resp := getContents(t, server, "/reader/api/0/stream/contents/"+feedStreamID(seed.feedA.ID)+"?n=2&output=json")
	if len(resp.Items) != 2 {
		t.Fatalf("expected 2 items with n=2, got %d", len(resp.Items))
	}
}

func TestReaderStreamContentsUnauthorized(t *testing.T) {
	server, queries, db := setupStreamTestServer(t)
	user := createReaderUser(t, queries, "testkey123")
	seedReaderStream(t, queries, db, user.ID)

	for _, path := range []string{
		"/reader/api/0/stream/items/contents?i=item/1",
		"/reader/api/0/stream/contents/user/-/state/com.google/reading-list",
	} {
		w := readerRequest(server, http.MethodGet, path, "", nil)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("%s without key: expected 401, got %d", path, w.Code)
		}
	}
}
