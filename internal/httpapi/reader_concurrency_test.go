package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"digesterr/internal/feed"
	"digesterr/internal/store"
)

// countingCommentFetcher is a deterministic stand-in for a real comment fetcher.
// It sleeps a fixed duration per call, counts calls, and optionally tracks the
// peak number running at once — enough to tell serial resolution from parallel
// resolution and to prove the fan-out stays bounded.
type countingCommentFetcher struct {
	delay     time.Duration
	trackPeak bool

	calls    atomic.Int64
	inFlight atomic.Int64
	peak     atomic.Int64
}

func (f *countingCommentFetcher) FetchComments(ctx context.Context, item store.Item, maxComments int) ([]feed.Comment, error) {
	f.calls.Add(1)

	if f.trackPeak {
		cur := f.inFlight.Add(1)
		for {
			old := f.peak.Load()
			if cur <= old || f.peak.CompareAndSwap(old, cur) {
				break
			}
		}
		defer f.inFlight.Add(-1)
	}

	if f.delay > 0 {
		select {
		case <-time.After(f.delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return []feed.Comment{{Author: "tester", Text: "a comment", Points: 1, ValidPoints: true}}, nil
}

func (f *countingCommentFetcher) CommentTargetURL() string { return "https://comments.test" }

// commentableFeedAndItems creates one reddit feed plus n items carrying usable
// post ids, so fetchTopComments reaches the injected fetcher instead of
// returning early (an item whose id cannot be resolved never fetches, which
// would make a test "pass" without exercising the path at all).
func commentableFeedAndItems(t *testing.T, queries *store.Queries, userID int64, n int) []store.Item {
	t.Helper()

	f, err := queries.CreateFeed(t.Context(), store.CreateFeedParams{
		UserID: userID,
		Type:   "reddit",
		Name:   "r/test",
		Url:    "https://www.reddit.com/r/test",
		Status: "active",
	})
	if err != nil {
		t.Fatalf("create feed: %v", err)
	}

	items := make([]store.Item, 0, n)
	for i := range n {
		postID := fmt.Sprintf("aaa%03d", i)
		item, err := queries.CreateItem(t.Context(), store.CreateItemParams{
			FeedID:     f.ID,
			Hash:       postID,
			Title:      fmt.Sprintf("Item %d", i),
			Url:        "https://www.reddit.com/r/test/comments/" + postID + "/x/",
			CommentUrl: "https://www.reddit.com/r/test/comments/" + postID + "/x/",
			Status:     "unread",
		})
		if err != nil {
			t.Fatalf("create item %d: %v", i, err)
		}
		items = append(items, item)
	}
	return items
}

// TestStreamItemsContentsResolvesConcurrently is the regression test for the
// production failure: per-item comment fetching used to run SERIALLY, so a
// 98-item request took 40-400s and blew past NetNewsWire's 60s request timeout —
// which is why the client reported nothing at all.
//
// N items that each block for a fixed delay must finish in roughly one delay,
// not N.
func TestStreamItemsContentsResolvesConcurrently(t *testing.T) {
	server, queries, _ := setupStreamTestServer(t)
	user := createReaderUser(t, queries, "testkey123")

	const n = 16
	items := commentableFeedAndItems(t, queries, user.ID, n)

	fetcher := &countingCommentFetcher{delay: 150 * time.Millisecond}
	server.poller.SetCommentFetcher("reddit", fetcher)
	server.topComments = 5

	start := time.Now()
	rec := serveReaderContents(t, server, streamFormBody(itemIDs(items)))
	elapsed := time.Since(start)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}

	var got readerContentsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(got.Items) != n {
		t.Fatalf("items = %d, want %d", len(got.Items), n)
	}
	if calls := fetcher.calls.Load(); calls != n {
		t.Fatalf("comment fetches = %d, want %d (one per item)", calls, n)
	}

	// Serial would be 16 x 150ms = 2.4s. Fail well before that.
	serial := time.Duration(n) * 150 * time.Millisecond
	if elapsed > serial/2 {
		t.Fatalf("elapsed = %v for %d items; want well under the %v serial bound — resolution is not parallel",
			elapsed, n, serial)
	}
}

// TestStreamItemsContentsKeepsInputOrder guards the ordering half of the
// parallel rewrite. Results go into indexed slots and are reassembled in input
// order, so a client that sends ids in a specific order gets them back in order
// — even though the fetches complete out of order.
func TestStreamItemsContentsKeepsInputOrder(t *testing.T) {
	server, queries, _ := setupStreamTestServer(t)
	user := createReaderUser(t, queries, "testkey123")

	items := commentableFeedAndItems(t, queries, user.ID, 12)
	server.poller.SetCommentFetcher("reddit", &countingCommentFetcher{delay: time.Millisecond})
	server.topComments = 5

	// Reverse the input; a completion-order append would return them reversed.
	reversed := make([]int64, 0, len(items))
	for i := len(items) - 1; i >= 0; i-- {
		reversed = append(reversed, items[i].ID)
	}

	rec := serveReaderContents(t, server, streamFormBody(reversed))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	var got readerContentsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	gotIDs := make([]string, 0, len(got.Items))
	for _, it := range got.Items {
		gotIDs = append(gotIDs, it.ID)
	}
	wantIDs := make([]string, 0, len(reversed))
	for _, id := range reversed {
		wantIDs = append(wantIDs, readerContentID(id))
	}
	if len(gotIDs) != len(wantIDs) {
		t.Fatalf("items = %d, want %d", len(gotIDs), len(wantIDs))
	}
	for i := range wantIDs {
		if gotIDs[i] != wantIDs[i] {
			t.Fatalf("order mismatch at %d: got %s, want %s\n got %v\nwant %v",
				i, gotIDs[i], wantIDs[i], gotIDs, wantIDs)
		}
	}
}

// TestStreamItemsContentsSurvivesClientDisconnect pins the resilience half of
// fix 1: comment fetches run on a context detached from the request, so a client
// that gives up mid-request still populates the cache for its retry. Without
// this, a timed-out sync discards the work it paid for and the next attempt
// starts from zero — which is why the same batch reappeared nightly.
//
// The cancellation is applied MID-FLIGHT, not up front: in production the client
// disconnects while the slow comment fetches are running, well after auth and the
// item query have already succeeded. Cancelling before the request starts would
// instead abort those earlier steps and test nothing about detachment.
func TestStreamItemsContentsSurvivesClientDisconnect(t *testing.T) {
	server, queries, _ := setupStreamTestServer(t)
	user := createReaderUser(t, queries, "testkey123")

	items := commentableFeedAndItems(t, queries, user.ID, 6)
	fetcher := &countingCommentFetcher{delay: 120 * time.Millisecond}
	server.poller.SetCommentFetcher("reddit", fetcher)
	server.topComments = 5

	body := streamFormBody(itemIDs(items))
	req := httptest.NewRequest(http.MethodPost, "/reader/api/0/stream/items/contents", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Authorization", "GoogleLogin auth=testkey123")

	ctx, cancel := context.WithCancel(context.Background())
	req = req.WithContext(ctx)

	done := make(chan struct{})
	go func() {
		defer close(done)
		server.ServeHTTP(httptest.NewRecorder(), req)
	}()

	// Let auth and the item lookups finish, then disconnect while the comment
	// fetches are still running.
	time.Sleep(40 * time.Millisecond)
	cancel()

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("handler did not finish after client disconnect")
	}

	if got := fetcher.calls.Load(); got == 0 {
		t.Fatal("comment fetcher was never called; fetches are not detached from the request context")
	}
	if _, ok := server.commentCache.get(items[0].ID); !ok {
		t.Fatal("comments were not cached after client disconnect; a retry would restart from zero")
	}
}

// TestCommentFetchConcurrencyBoundsFanOut pins the safety half: a large stream
// must not open one upstream request per item simultaneously. The bound is what
// keeps a flood of reader requests from stampeding Reddit.
func TestCommentFetchConcurrencyBoundsFanOut(t *testing.T) {
	server, queries, _ := setupStreamTestServer(t)
	user := createReaderUser(t, queries, "testkey123")

	items := commentableFeedAndItems(t, queries, user.ID, 24)
	fetcher := &countingCommentFetcher{delay: 20 * time.Millisecond, trackPeak: true}
	server.poller.SetCommentFetcher("reddit", fetcher)
	server.topComments = 5

	const bound = 4
	server.commentFetchSem = make(chan struct{}, bound)

	rec := serveReaderContents(t, server, streamFormBody(itemIDs(items)))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	if peak := fetcher.peak.Load(); peak > bound {
		t.Fatalf("peak concurrent comment fetches = %d, want <= %d", peak, bound)
	}
	if peak := fetcher.peak.Load(); peak == 0 {
		t.Fatal("comment fetcher never ran")
	}
	if calls := fetcher.calls.Load(); calls != int64(len(items)) {
		t.Fatalf("comment fetches = %d, want %d — bounded fan-out must still fetch every item",
			calls, len(items))
	}
}

// TestReaderWriteDeadlineScope documents fix 3's scope: Reader routes are
// bounded, while the long-running LLM routes are deliberately not (the server
// sets no WriteTimeout precisely so summarize/generate can run for minutes).
func TestReaderWriteDeadlineScope(t *testing.T) {
	bounded := []string{
		"/reader/api/0/stream/items/contents",
		"/reader/api/0/stream/contents/user/-/state/com.google/reading-list",
		"/reader/api/0/unread-count",
		"/reader/api/0/subscription/list",
	}
	for _, p := range bounded {
		if !wantsWriteDeadline(p) {
			t.Errorf("wantsWriteDeadline(%q) = false, want true", p)
		}
	}

	untouched := []string{
		"/api/items/12/summarize",
		"/api/digest-configs/3/generate",
		"/api/feeds",
		"/api/logs",
		"/healthz",
		"/",
	}
	for _, p := range untouched {
		if wantsWriteDeadline(p) {
			t.Errorf("wantsWriteDeadline(%q) = true, want false — long-running LLM routes must not be bounded", p)
		}
	}
}

// TestReaderWriteDeadlineCancelsHandler proves the deadline is installed and
// observable: a handler waiting on its request context must see a cancellation
// whose cause names the deadline, so callers can tell "too slow" from "client
// went away".
func TestReaderWriteDeadlineCancelsHandler(t *testing.T) {
	_, queries, _ := setupStreamTestServer(t)
	user := createReaderUser(t, queries, "testkey123")

	req := httptest.NewRequest(http.MethodGet, "/reader/api/0/unread-count", nil)
	req.Header.Set("Authorization", "GoogleLogin auth="+user.ApiKey)

	const short = 30 * time.Millisecond
	deadlined, cancel := withWriteDeadline(req, short)
	defer cancel()

	if _, ok := deadlined.Context().Deadline(); !ok {
		t.Fatal("withWriteDeadline did not install a deadline")
	}

	select {
	case <-deadlined.Context().Done():
		cause := context.Cause(deadlined.Context())
		if cause == nil {
			t.Fatal("context cancelled without a cause; callers cannot tell a deadline from a disconnect")
		}
		if !errors.Is(cause, context.DeadlineExceeded) {
			t.Fatalf("cause = %v, want context.DeadlineExceeded", cause)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("deadline never fired")
	}
}

// TestStreamContentsAlsoResolvesConcurrently covers the second entry point:
// stream/contents/{streamID} shares resolveContentObjects, so it must be
// parallel too (it was a separate serial loop before the fix).
func TestStreamContentsAlsoResolvesConcurrently(t *testing.T) {
	server, queries, _ := setupStreamTestServer(t)
	user := createReaderUser(t, queries, "testkey123")

	items := commentableFeedAndItems(t, queries, user.ID, 12)

	fetcher := &countingCommentFetcher{delay: 120 * time.Millisecond}
	server.poller.SetCommentFetcher("reddit", fetcher)
	server.topComments = 5

	start := time.Now()
	rec := readerRequest(server, http.MethodGet,
		"/reader/api/0/stream/contents/user/-/state/com.google/reading-list?n=100&output=json",
		"", map[string]string{"Authorization": "GoogleLogin auth=testkey123"})
	elapsed := time.Since(start)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	if calls := fetcher.calls.Load(); calls != int64(len(items)) {
		t.Fatalf("comment fetches = %d, want %d", calls, len(items))
	}

	serial := time.Duration(len(items)) * 120 * time.Millisecond
	if elapsed > serial/2 {
		t.Fatalf("stream/contents took %v for %d items; want well under the %v serial bound",
			elapsed, len(items), serial)
	}
}

// ---- helpers ----

func itemIDs(items []store.Item) []int64 {
	out := make([]int64, 0, len(items))
	for _, it := range items {
		out = append(out, it.ID)
	}
	return out
}

// streamFormBody builds the form body NetNewsWire posts: output=json plus one
// i=<tag uri> per item id.
func streamFormBody(ids []int64) string {
	var b strings.Builder
	b.WriteString("output=json")
	for _, id := range ids {
		fmt.Fprintf(&b, "&i=%s", readerContentID(id))
	}
	return b.String()
}

// serveReaderContents POSTs a form body to the stream contents endpoint through
// the real ServeHTTP (so middleware, auth and the write deadline all apply).
func serveReaderContents(t *testing.T, server *Server, body string) *httptest.ResponseRecorder {
	t.Helper()
	return readerRequest(server, http.MethodPost, "/reader/api/0/stream/items/contents", body,
		map[string]string{"Authorization": "GoogleLogin auth=testkey123"})
}
