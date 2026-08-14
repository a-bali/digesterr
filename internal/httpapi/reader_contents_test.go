package httpapi

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// testDigestContent is a handcrafted digest result content JSON used by the
// contents tests. It carries a title, a summary, one highlight (with
// llm_summary and a top comment), and one entry in the all-items list.
const testDigestContent = `{"title":"Go Time","summary":"Today's top stories","highlights":[{"item_id":1,"feed_id":1,"feed_name":"Feed A","title":"Top HN story","url":"https://news.ycombinator.com/item?id=1","comment_url":"https://news.ycombinator.com/item?id=1","points":100,"comments_count":50,"published_at":"2024-01-01T00:00:00Z","llm_summary":"A key insight about the story.","top_comments":[{"author":"alice","text":"Great point","points":10,"url":"https://news.ycombinator.com/item?id=2"}]}],"items":[{"item_id":2,"feed_id":1,"feed_name":"Feed A","title":"Other story","url":"https://example.com/other","comment_url":"","points":0,"comments_count":0,"published_at":"2024-01-01T00:00:00Z"}]}`

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
	if resp.ID == "" {
		t.Fatalf("expected non-empty envelope id (required by NetNewsWire), got %q", resp.ID)
	}
	if resp.Updated == 0 {
		t.Fatalf("expected non-zero envelope updated timestamp, got %d", resp.Updated)
	}
	if len(resp.Items) != 3 {
		t.Fatalf("expected 3 items, got %d: %v", len(resp.Items), contentIDs(resp.Items))
	}

	want := []string{
		readerContentID(seed.aUnreadNewest.ID),
		readerContentID(seed.aDigested.ID),
		readerContentID(seed.drNew.ID + digestRefOffset),
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

	// Digest item: generated title + rendered HTML content + origin.
	digestItem := resp.Items[2]
	if digestItem.Origin.StreamID != digestStreamID(seed.cfg.ID) {
		t.Fatalf("expected digest origin streamId %q, got %q", digestStreamID(seed.cfg.ID), digestItem.Origin.StreamID)
	}
	if digestItem.Title != "Go Time" {
		t.Fatalf("expected digest title %q, got %q", "Go Time", digestItem.Title)
	}
	if !strings.Contains(digestItem.Content.Content, `class="digest-title">Go Time</h1>`) {
		t.Fatalf("expected digest content to render the title heading, got: %s", digestItem.Content.Content)
	}
	if !strings.Contains(digestItem.Content.Content, "Top HN story") {
		t.Fatalf("expected digest content to contain highlight title, got: %s", digestItem.Content.Content)
	}
	// NetNewsWire reads only `summary`, so the full rendered digest must be in
	// both summary and content.
	if digestItem.Summary.Content != digestItem.Content.Content {
		t.Fatalf("expected digest summary to equal content, got summary %q", digestItem.Summary.Content)
	}
	if !strings.Contains(digestItem.Summary.Content, "Top HN story") {
		t.Fatalf("expected digest summary to contain highlight title, got: %s", digestItem.Summary.Content)
	}
	if !strings.Contains(digestItem.Content.Content, "Other story") {
		t.Fatalf("expected digest content to contain all-items entry, got: %s", digestItem.Content.Content)
	}
	// The digest title links to the digest result in digesterr, and the feed
	// link to the digest config.
	wantDigestPath := fmt.Sprintf("/digests/%d/%d", seed.cfg.ID, seed.drNew.ID)
	if len(digestItem.Alternate) != 1 || !strings.HasSuffix(digestItem.Alternate[0].Href, wantDigestPath) {
		t.Fatalf("expected alternate href to end with %q, got %+v", wantDigestPath, digestItem.Alternate)
	}
	if len(digestItem.Canonical) != 1 || !strings.HasSuffix(digestItem.Canonical[0].Href, wantDigestPath) {
		t.Fatalf("expected canonical href to end with %q, got %+v", wantDigestPath, digestItem.Canonical)
	}
	wantConfigPath := fmt.Sprintf("/digests/%d", seed.cfg.ID)
	if !strings.HasSuffix(digestItem.Origin.HTMLURL, wantConfigPath) {
		t.Fatalf("expected digest origin htmlUrl to end with %q, got %q", wantConfigPath, digestItem.Origin.HTMLURL)
	}
}

// TestReaderDigestTitleFallback verifies digest results without a generated
// title (older content) keep the historical "Digest: <config name>" item title.
func TestReaderDigestTitleFallback(t *testing.T) {
	server, queries, db := setupStreamTestServer(t)
	user := createReaderUser(t, queries, "testkey123")
	seed := seedReaderStream(t, queries, db, user.ID)
	setDigestContent(t, db, seed.drNew.ID, `{"summary":"no title here"}`)

	resp := getContents(t, server, "/reader/api/0/stream/items/contents?i="+digestItemStreamID(seed.drNew.ID)+"&output=json")
	if len(resp.Items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(resp.Items))
	}
	if resp.Items[0].Title != "Digest: Daily" {
		t.Fatalf("expected fallback digest title %q, got %q", "Digest: Daily", resp.Items[0].Title)
	}
	if strings.Contains(resp.Items[0].Content.Content, "digest-title") {
		t.Fatalf("content without title should not render a title heading, got: %s", resp.Items[0].Content.Content)
	}
}

// TestReaderStreamItemsContentsExtractedSummary verifies that NetNewsWire-visible
// summary.content carries the extracted body (NNW ignores the content field).
func TestReaderStreamItemsContentsExtractedSummary(t *testing.T) {
	server, queries, db := setupStreamTestServer(t)
	user := createReaderUser(t, queries, "testkey123")
	seed := seedReaderStream(t, queries, db, user.ID)

	if _, err := db.Exec("UPDATE items SET extract_content = ?, extract_status = 'done' WHERE id = ?",
		"<p>full extracted article</p>", seed.aUnreadNewest.ID); err != nil {
		t.Fatalf("failed to set extracted content: %v", err)
	}

	resp := getContents(t, server, "/reader/api/0/stream/items/contents?i="+itemStreamID(seed.aUnreadNewest.ID)+"&output=json")
	if len(resp.Items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(resp.Items))
	}
	it := resp.Items[0]
	if it.Summary.Content != "<p>full extracted article</p>" {
		t.Fatalf("expected summary to carry extracted body, got %q", it.Summary.Content)
	}
	if it.Content.Content != "<p>full extracted article</p>" {
		t.Fatalf("expected content to carry extracted body, got %q", it.Content.Content)
	}
}

func TestReaderStreamItemsContentsSavedAndUnknown(t *testing.T) {
	server, queries, db := setupStreamTestServer(t)
	user := createReaderUser(t, queries, "testkey123")
	seed := seedReaderStream(t, queries, db, user.ID)

	path := "/reader/api/0/stream/items/contents" +
		"?i=" + itemStreamID(seed.aSaved.ID) +
		"&i=999999" +
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

// TestReaderStreamItemsContentsPostTagURIs mirrors NetNewsWire's content
// fetch: a form-encoded POST whose `i` params are long-form tag: URIs.
func TestReaderStreamItemsContentsPostTagURIs(t *testing.T) {
	server, queries, db := setupStreamTestServer(t)
	user := createReaderUser(t, queries, "testkey123")
	seed := seedReaderStream(t, queries, db, user.ID)

	form := url.Values{}
	form.Add("T", "testkey123")
	form.Add("output", "json")
	form.Add("i", readerContentID(seed.aUnreadNewest.ID))
	form.Add("i", readerContentID(seed.drNew.ID+digestRefOffset))

	w := readerRequest(server, http.MethodPost, "/reader/api/0/stream/items/contents", form.Encode(), map[string]string{
		"Authorization": "GoogleLogin auth=testkey123",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp readerContentsResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	want := []string{
		readerContentID(seed.aUnreadNewest.ID),
		readerContentID(seed.drNew.ID + digestRefOffset),
	}
	if got := contentIDs(resp.Items); !equalStrings(got, want) {
		t.Fatalf("post contents ids mismatch:\n got %v\nwant %v", got, want)
	}
}

func TestParseReaderItemRef(t *testing.T) {
	cases := []struct {
		in   string
		want int64
		ok   bool
	}{
		{"tag:google.com,2005:reader/item/0000000000001744", 5956, true}, // NetNewsWire, padded hex
		{"tag:google.com,2005:reader/item/1744", 5956, true},            // unpadded hex
		{"0000000000001744", 5956, true},                                 // Reeder bare hex
		{"5956", 5956, true},                                             // Liferea decimal
		{"item/5956", 0, false},                                          // legacy custom form
		{"digest-item/9", 0, false},                                      // legacy custom form
		{"", 0, false},
		{"garbage", 0, false},
	}
	for _, tc := range cases {
		got, ok := parseReaderItemRef(tc.in)
		if ok != tc.ok || (ok && got != tc.want) {
			t.Errorf("parseReaderItemRef(%q) = (%d, %v), want (%d, %v)", tc.in, got, ok, tc.want, tc.ok)
		}
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
		readerContentID(seed.aUnreadNewest.ID),
		readerContentID(seed.drNew.ID + digestRefOffset),
		readerContentID(seed.aUnreadOldest.ID),
		readerContentID(seed.bUnread.ID),
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
		readerContentID(seed.aSaved.ID),
		readerContentID(seed.aUnreadNewest.ID),
		readerContentID(seed.aRead.ID),
		readerContentID(seed.aUnreadOldest.ID),
		readerContentID(seed.aDigested.ID),
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
