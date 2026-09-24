package digest

import (
	"context"
	"database/sql"
	"strconv"
	"sync"
	"testing"
	"time"

	"digesterr/internal/feed"
	"digesterr/internal/store"
)

// blockingLLM blocks every Chat call until released, so two generations can be
// held open simultaneously.
type blockingLLM struct {
	mu       sync.Mutex
	started  int
	release  chan struct{}
	response string
}

func (b *blockingLLM) Chat(ctx context.Context, _, _ string) (string, error) {
	b.mu.Lock()
	b.started++
	b.mu.Unlock()
	select {
	case <-b.release:
	case <-ctx.Done():
		return "", ctx.Err()
	}
	return b.response, nil
}

func (b *blockingLLM) Started() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.started
}

// setupLockTest builds a user, feed, items and a digest config.
func setupLockTest(t *testing.T, email string, items int) (*sql.DB, *store.Queries, store.Feed, store.DigestConfig) {
	t.Helper()
	db, queries, cleanup := setupDigestTest(t)
	t.Cleanup(cleanup)
	ctx := context.Background()

	user, err := queries.CreateUser(ctx, store.CreateUserParams{
		Email:        email,
		PasswordHash: "hash",
		ApiKey:       email + "-key",
	})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	feedObj, err := queries.CreateFeed(ctx, store.CreateFeedParams{
		UserID: user.ID, Type: "rss", Name: "Lock Feed",
		Url: "https://example.com/lock.xml", Config: "{}", Status: "active",
	})
	if err != nil {
		t.Fatalf("create feed: %v", err)
	}
	for i := 0; i < items; i++ {
		if _, err := queries.CreateItem(ctx, store.CreateItemParams{
			FeedID: feedObj.ID,
			Hash:   "lock-" + strconv.Itoa(i),
			Title:  "Lock Item " + strconv.Itoa(i),
			Url:    "https://example.com/lock-" + strconv.Itoa(i),
			Status: "unread",
		}); err != nil {
			t.Fatalf("create item: %v", err)
		}
	}
	cfg, err := queries.CreateDigestConfig(ctx, store.CreateDigestConfigParams{
		UserID:   user.ID,
		Name:     "Lock Digest",
		Schedule: "0 8 * * *",
		Config:   `{"feeds":[{"id":` + strconv.FormatInt(feedObj.ID, 10) + `,"highlights":1,"comments":0}]}`,
	})
	if err != nil {
		t.Fatalf("create digest config: %v", err)
	}
	return db, queries, feedObj, cfg
}

// addItems creates n more unread items on a feed, so a second generation has
// something to digest.
func addItems(t *testing.T, queries *store.Queries, feedID int64, prefix string, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		key := prefix + "-" + strconv.Itoa(i)
		if _, err := queries.CreateItem(t.Context(), store.CreateItemParams{
			FeedID: feedID,
			Hash:   key,
			Title:  "Item " + key,
			Url:    "https://example.com/" + key,
			Status: "unread",
		}); err != nil {
			t.Fatalf("create item %s: %v", key, err)
		}
	}
}

// countDigestResults returns how many results exist for a config.
func countDigestResults(t *testing.T, queries *store.Queries, cfg store.DigestConfig) int {
	t.Helper()
	results, err := queries.ListDigestResultsByConfig(t.Context(), store.ListDigestResultsByConfigParams{
		ConfigID: cfg.ID,
		UserID:   cfg.UserID,
	})
	if err != nil {
		t.Fatalf("list digest results: %v", err)
	}
	return len(results)
}

// TestConcurrentGenerationIsSerializedPerConfig: two generations for the SAME
// config must not run at once. Overlapping runs produce duplicate digests and
// pay for the LLM twice.
func TestConcurrentGenerationIsSerializedPerConfig(t *testing.T) {
	db, queries, _, cfg := setupLockTest(t, "lock-concurrent@example.com", 2)
	// ':memory:' is per-connection: without pinning, the second goroutine opens
	// its own empty database, fails before reaching the LLM, and the test would
	// pass even with no lock at all (a regression test that proves nothing).
	db.SetMaxOpenConns(1)

	llm := &blockingLLM{
		release:  make(chan struct{}),
		response: `{"title":"LOCKED","summary":"s","highlights":[]}`,
	}
	poller := feed.NewPoller(queries)
	gen := NewGenerator(db, queries, llm, poller)

	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			_, errs[idx] = gen.Generate(context.Background(), cfg)
		}(i)
	}

	// Give both goroutines time to reach the LLM (or be rejected by the lock).
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if llm.Started() >= 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	// Hold long enough that a second unlocked run would also have started.
	time.Sleep(300 * time.Millisecond)
	started := llm.Started()
	close(llm.release)
	wg.Wait()

	if started != 1 {
		t.Errorf("concurrent Chat calls = %d, want 1 (generations for one config must not overlap; each call is billed)", started)
	}

	got := countDigestResults(t, queries, cfg)
	if got != 1 {
		t.Errorf("digest results = %d, want 1 (an overlapping run created a duplicate)", got)
	}

	// Exactly one run must report that it was already in progress.
	blocked := 0
	for _, err := range errs {
		if err != nil {
			blocked++
		}
	}
	if blocked != 1 {
		t.Errorf("blocked generations = %d, want 1 (errs=%v)", blocked, errs)
	}
}

// TestGenerationLockIsReleasedAfterRun: the lock must be released when the run
// finishes, or every later generation for that config is refused forever.
func TestGenerationLockIsReleasedAfterRun(t *testing.T) {
	db, queries, feedObj, cfg := setupLockTest(t, "lock-release@example.com", 1)

	poller := feed.NewPoller(queries)
	gen := NewGenerator(db, queries, fakeLLM{resp: `{"title":"ONE","summary":"s","highlights":[]}`}, poller)

	if _, err := gen.Generate(context.Background(), cfg); err != nil {
		t.Fatalf("first Generate: %v", err)
	}
	// The first run consumed the feed's unread items (they became 'digested'),
	// so give the second run something to work with. The 30-minute recency
	// guard does not apply to manual runs, so it must be allowed to proceed.
	addItems(t, queries, feedObj.ID, "release-second", 1)
	if _, err := gen.Generate(context.Background(), cfg); err != nil {
		t.Fatalf("second Generate after the first completed: %v (lock was not released)", err)
	}
	if got := countDigestResults(t, queries, cfg); got != 2 {
		t.Errorf("digest results = %d, want 2", got)
	}
}

// TestGenerationLockIsReleasedOnFailure: a failed run must not wedge the config.
func TestGenerationLockIsReleasedOnFailure(t *testing.T) {
	db, queries, _, cfg := setupLockTest(t, "lock-failure@example.com", 1)

	poller := feed.NewPoller(queries)
	gen := NewGenerator(db, queries, errorLLM{}, poller)

	if _, err := gen.Generate(context.Background(), cfg); err == nil {
		t.Fatal("expected the failing LLM to surface an error")
	}
	// Swap in a working client: the config must be runnable again.
	gen.llm = fakeLLM{resp: `{"title":"AFTER-FAILURE","summary":"s","highlights":[]}`}
	if _, err := gen.Generate(context.Background(), cfg); err != nil {
		t.Fatalf("Generate after a failed run: %v (lock leaked on the error path)", err)
	}
}

// TestConcurrentGenerationDifferentConfigsRunInParallel: the lock is per
// config. Two different configs must still generate at the same time, or one
// slow digest would serialize the whole instance.
func TestConcurrentGenerationDifferentConfigsRunInParallel(t *testing.T) {
	db, queries, _, cfgA := setupLockTest(t, "lock-parallel@example.com", 1)
	// ':memory:' gives every connection its OWN database, so the second
	// goroutine would open a fresh empty one ("no such table: items"). One
	// connection keeps both runs on the same database; the LLM calls still
	// overlap because neither holds a connection while blocked in Chat.
	db.SetMaxOpenConns(1)

	// Config B gets its OWN feed with its own unread item. Sharing feed A would
	// make the test measure item contention instead of the lock: whichever run
	// went second would find everything already 'digested' and exit before the
	// LLM, which looks identical to being blocked by a global lock.
	feedB, err := queries.CreateFeed(t.Context(), store.CreateFeedParams{
		UserID: cfgA.UserID, Type: "rss", Name: "Lock Feed B",
		Url: "https://example.com/lock-b.xml", Config: "{}", Status: "active",
	})
	if err != nil {
		t.Fatalf("create second feed: %v", err)
	}
	addItems(t, queries, feedB.ID, "parallel-b", 1)
	cfgB, err := queries.CreateDigestConfig(t.Context(), store.CreateDigestConfigParams{
		UserID:   cfgA.UserID,
		Name:     "Lock Digest B",
		Schedule: "0 9 * * *",
		Config:   `{"feeds":[{"id":` + strconv.FormatInt(feedB.ID, 10) + `,"highlights":1,"comments":0}]}`,
	})
	if err != nil {
		t.Fatalf("create second config: %v", err)
	}

	llm := &blockingLLM{
		release:  make(chan struct{}),
		response: `{"title":"PARALLEL","summary":"s","highlights":[]}`,
	}
	poller := feed.NewPoller(queries)
	gen := NewGenerator(db, queries, llm, poller)

	var wg sync.WaitGroup
	for _, cfg := range []store.DigestConfig{cfgA, cfgB} {
		wg.Add(1)
		go func(c store.DigestConfig) {
			defer wg.Done()
			_, _ = gen.Generate(context.Background(), c)
		}(cfg)
	}

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if llm.Started() >= 2 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	started := llm.Started()
	close(llm.release)
	wg.Wait()

	if started < 2 {
		t.Errorf("concurrent Chat calls = %d, want 2 (the lock must be per config, not global)", started)
	}
}

// errorLLM always fails, to exercise the error path.
type errorLLM struct{}

func (errorLLM) Chat(context.Context, string, string) (string, error) {
	return "", context.DeadlineExceeded
}
