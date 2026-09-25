package digest

import (
	"context"
	"database/sql"
	"strconv"
	"testing"
	"time"

	"digesterr/internal/feed"
	"digesterr/internal/store"
)

// TestCatchUpGeneratesMissedOccurrence is the regression guard for the
// production incident where the 05:00 digest silently never existed: the pod
// was evicted at 05:00:03 and its replacement's 1-minute ticker first fired at
// 05:01:06, so no tick ever observed the 05:00 minute. Nothing rechecked past
// minutes, and there was no error to show — just a missing digest.
//
// The scenario is reproduced exactly: a daily config whose occurrence was
// missed, a clock pinned a few seconds into the next minute, and a fresh
// scheduler starting up.
func TestCatchUpGeneratesMissedOccurrence(t *testing.T) {
	db, queries, cleanup := setupDigestTest(t)
	defer cleanup()
	ctx := context.Background()

	user, err := queries.CreateUser(ctx, store.CreateUserParams{
		Email:        "catchup-missed@example.com",
		PasswordHash: "hash",
		ApiKey:       "catchup-missed-key",
	})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	feedObj, err := queries.CreateFeed(ctx, store.CreateFeedParams{
		UserID: user.ID, Type: "rss", Name: "Catch-up Feed",
		Url: "https://example.com/catchup.xml", Config: "{}", Status: "active",
	})
	if err != nil {
		t.Fatalf("create feed: %v", err)
	}
	addItems(t, queries, feedObj.ID, "catchup", 2)

	cfg, err := queries.CreateDigestConfig(ctx, store.CreateDigestConfigParams{
		UserID:   user.ID,
		Name:     "Daily 5am",
		Schedule: "0 5 * * *",
		Config:   `{"feeds":[{"id":` + strconv.FormatInt(feedObj.ID, 10) + `,"highlights":1,"comments":0}]}`,
	})
	if err != nil {
		t.Fatalf("create digest config: %v", err)
	}

	// The config must predate the occurrence under test, or the pre-creation
	// guard skips catch-up for a reason unrelated to what this test asserts
	// (and the test would pass without exercising its guard at all).
	setConfigCreatedAt(t, db, cfg.ID, time.Date(2026, 9, 23, 0, 0, 0, 0, time.UTC))

	gen := NewGenerator(db, queries,
		fakeLLM{resp: `{"title":"CATCHUP","summary":"s","highlights":[]}`},
		feed.NewPoller(queries))

	s := NewScheduler(queries, gen, time.UTC)
	// 05:00:06 — the instant the replacement pod came up, six seconds after
	// the occurrence was missed. The 05:00 minute is already gone.
	s.SetClock(func() time.Time {
		return time.Date(2026, 9, 25, 5, 0, 6, 0, time.UTC)
	})

	s.catchUp(ctx)

	latest, err := queries.GetLatestDigestResult(ctx, store.GetLatestDigestResultParams{
		ConfigID: cfg.ID, UserID: user.ID,
	})
	if err != nil || latest.ID == 0 {
		t.Fatalf("no digest result after catch-up: err=%v id=%d (the missed 05:00 occurrence was lost)", err, latest.ID)
	}
}

// TestCatchUpDoesNotDuplicateRecentDigest is the inverse guard: catch-up must
// never re-generate an occurrence that already produced a digest. Without the
// recency check every restart would fire another LLM call and another digest.
// setDigestResultCreatedAt pins a result's created_at. The column defaults to
// SQLite's real wall clock, but the scheduler tests run on an injected clock —
// so a result generated at a fake 05:00 carries a timestamp from the real
// present, and the recency comparison would read a large negative duration.
// Tests that exercise the recency window must set the timestamp explicitly.
func setDigestResultCreatedAt(t *testing.T, db *sql.DB, resultID int64, at time.Time) {
	t.Helper()
	if _, err := db.ExecContext(t.Context(),
		`UPDATE digest_results SET created_at = ? WHERE id = ?`,
		at.UTC().Format("2006-01-02 15:04:05"), resultID); err != nil {
		t.Fatalf("set created_at: %v", err)
	}
}

// TestCatchUpDoesNotDuplicateRecentDigest: the occurrence ran normally and a
// restart lands in the same window, so catch-up must not generate again.
func TestCatchUpDoesNotDuplicateRecentDigest(t *testing.T) {
	db, queries, cleanup := setupDigestTest(t)
	defer cleanup()
	ctx := context.Background()

	user, err := queries.CreateUser(ctx, store.CreateUserParams{
		Email:        "catchup-dup@example.com",
		PasswordHash: "hash",
		ApiKey:       "catchup-dup-key",
	})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	feedObj, err := queries.CreateFeed(ctx, store.CreateFeedParams{
		UserID: user.ID, Type: "rss", Name: "Dup Feed",
		Url: "https://example.com/dup.xml", Config: "{}", Status: "active",
	})
	if err != nil {
		t.Fatalf("create feed: %v", err)
	}
	addItems(t, queries, feedObj.ID, "dup", 2)

	cfg, err := queries.CreateDigestConfig(ctx, store.CreateDigestConfigParams{
		UserID:   user.ID,
		Name:     "Daily 5am",
		Schedule: "0 5 * * *",
		Config:   `{"feeds":[{"id":` + strconv.FormatInt(feedObj.ID, 10) + `,"highlights":1,"comments":0}]}`,
	})
	if err != nil {
		t.Fatalf("create digest config: %v", err)
	}

	// The config must predate the occurrence under test, or the pre-creation
	// guard skips catch-up for a reason unrelated to what this test asserts
	// (and the test would pass without exercising its guard at all).
	setConfigCreatedAt(t, db, cfg.ID, time.Date(2026, 9, 23, 0, 0, 0, 0, time.UTC))

	gen := NewGenerator(db, queries,
		fakeLLM{resp: `{"title":"FIRST","summary":"s","highlights":[]}`},
		feed.NewPoller(queries))

	// The occurrence ran normally at 05:00:20, then the pod restarted at
	// 05:00:45 for something unrelated, 25 seconds later.
	run := NewScheduler(queries, gen, time.UTC)
	run.SetClock(func() time.Time {
		return time.Date(2026, 9, 25, 5, 0, 20, 0, time.UTC)
	})
	run.tick(ctx)

	// Pin the first result to the injected timeline so the recency check sees
	// a genuine 25-second gap rather than a negative one.
	first, err := queries.GetLatestDigestResult(ctx, store.GetLatestDigestResultParams{
		ConfigID: cfg.ID, UserID: user.ID,
	})
	if err != nil || first.ID == 0 {
		t.Fatalf("tick did not produce a digest: id=%d err=%v", first.ID, err)
	}
	setDigestResultCreatedAt(t, db, first.ID, time.Date(2026, 9, 25, 5, 0, 20, 0, time.UTC))

	// Fresh unread items, so that if the recency guard were absent catch-up
	// would genuinely be able to produce a second digest. Without them a
	// duplicate is blocked by "no unread items" instead, and the test would
	// pass even with the guard removed.
	addItems(t, queries, feedObj.ID, "dup-extra", 2)

	before := countDigestResults(t, queries, cfg)

	restart := NewScheduler(queries, gen, time.UTC)
	restart.SetClock(func() time.Time {
		return time.Date(2026, 9, 25, 5, 0, 45, 0, time.UTC)
	})
	restart.catchUp(ctx)

	after := countDigestResults(t, queries, cfg)
	if after != before {
		t.Errorf("digest results went %d -> %d after catch-up on an already-generated occurrence; catch-up must not duplicate", before, after)
	}
}

// TestTickDoesNotDuplicateAManualGeneration: if a digest was produced shortly
// before a scheduled minute — a manual "Generate", or the repeated wall-clock
// minute of a DST fall-back — the tick must not immediately produce a second
// one. This pins the recency window on the tick path, which catch-up no longer
// exercises because it is anchored on the occurrence instead.
func TestTickDoesNotDuplicateAManualGeneration(t *testing.T) {
	db, queries, cleanup := setupDigestTest(t)
	defer cleanup()
	ctx := context.Background()

	user, err := queries.CreateUser(ctx, store.CreateUserParams{
		Email:        "tick-window@example.com",
		PasswordHash: "hash",
		ApiKey:       "tick-window-key",
	})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	feedObj, err := queries.CreateFeed(ctx, store.CreateFeedParams{
		UserID: user.ID, Type: "rss", Name: "Tick Feed",
		Url: "https://example.com/tick.xml", Config: "{}", Status: "active",
	})
	if err != nil {
		t.Fatalf("create feed: %v", err)
	}
	addItems(t, queries, feedObj.ID, "tick", 2)

	cfg, err := queries.CreateDigestConfig(ctx, store.CreateDigestConfigParams{
		UserID:   user.ID,
		Name:     "Daily 5am",
		Schedule: "0 5 * * *",
		Config:   `{"feeds":[{"id":` + strconv.FormatInt(feedObj.ID, 10) + `,"highlights":1,"comments":0}]}`,
	})
	if err != nil {
		t.Fatalf("create digest config: %v", err)
	}

	// The config must predate the occurrence under test, or the pre-creation
	// guard skips catch-up for a reason unrelated to what this test asserts
	// (and the test would pass without exercising its guard at all).
	setConfigCreatedAt(t, db, cfg.ID, time.Date(2026, 9, 23, 0, 0, 0, 0, time.UTC))

	gen := NewGenerator(db, queries,
		fakeLLM{resp: `{"title":"TICK","summary":"s","highlights":[]}`},
		feed.NewPoller(queries))

	// A digest produced by a manual "Generate" at 04:50. Generate directly:
	// tick only fires when the cron matches, so it cannot stand in for a
	// manual generation outside the scheduled minute.
	if _, err := gen.Generate(ctx, cfg); err != nil {
		t.Fatalf("manual generation: %v", err)
	}

	res, err := queries.GetLatestDigestResult(ctx, store.GetLatestDigestResultParams{
		ConfigID: cfg.ID, UserID: user.ID,
	})
	if err != nil || res.ID == 0 {
		t.Fatalf("setup generation produced no digest: id=%d err=%v", res.ID, err)
	}
	setDigestResultCreatedAt(t, db, res.ID, time.Date(2026, 9, 25, 4, 50, 0, 0, time.UTC))

	// Fresh items, so only the recency window can prevent a second digest.
	addItems(t, queries, feedObj.ID, "tick-extra", 2)
	before := countDigestResults(t, queries, cfg)

	// The scheduled minute arrives 10 minutes later, inside the window.
	scheduled := NewScheduler(queries, gen, time.UTC)
	scheduled.SetClock(func() time.Time {
		return time.Date(2026, 9, 25, 5, 0, 0, 0, time.UTC)
	})
	scheduled.tick(ctx)

	if after := countDigestResults(t, queries, cfg); after != before {
		t.Errorf("digest results went %d -> %d: a scheduled tick duplicated a digest generated 10 minutes earlier", before, after)
	}
}

// TestCatchUpDoesNotDuplicateAcrossHourlyRestart is the production cadence: the
// descheduler restarts this pod hourly, so the 06:00 restart runs catch-up 59
// minutes after the 05:00 digest. A recency-window dedup calls that unserved
// and generates a duplicate the user already has; the occurrence-anchored check
// must not.
func TestCatchUpDoesNotDuplicateAcrossHourlyRestart(t *testing.T) {
	db, queries, cleanup := setupDigestTest(t)
	defer cleanup()
	ctx := context.Background()

	user, err := queries.CreateUser(ctx, store.CreateUserParams{
		Email:        "catchup-hourly@example.com",
		PasswordHash: "hash",
		ApiKey:       "catchup-hourly-key",
	})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	feedObj, err := queries.CreateFeed(ctx, store.CreateFeedParams{
		UserID: user.ID, Type: "rss", Name: "Hourly Feed",
		Url: "https://example.com/hourly.xml", Config: "{}", Status: "active",
	})
	if err != nil {
		t.Fatalf("create feed: %v", err)
	}
	addItems(t, queries, feedObj.ID, "hourly", 2)

	cfg, err := queries.CreateDigestConfig(ctx, store.CreateDigestConfigParams{
		UserID:   user.ID,
		Name:     "Daily 5am",
		Schedule: "0 5 * * *",
		Config:   `{"feeds":[{"id":` + strconv.FormatInt(feedObj.ID, 10) + `,"highlights":1,"comments":0}]}`,
	})
	if err != nil {
		t.Fatalf("create digest config: %v", err)
	}

	// The config must predate the occurrence under test, or the pre-creation
	// guard skips catch-up for a reason unrelated to what this test asserts
	// (and the test would pass without exercising its guard at all).
	setConfigCreatedAt(t, db, cfg.ID, time.Date(2026, 9, 23, 0, 0, 0, 0, time.UTC))

	gen := NewGenerator(db, queries,
		fakeLLM{resp: `{"title":"HOURLY","summary":"s","highlights":[]}`},
		feed.NewPoller(queries))

	// 05:00:53 — the real timestamp of the last successful production digest.
	run := NewScheduler(queries, gen, time.UTC)
	run.SetClock(func() time.Time {
		return time.Date(2026, 9, 25, 5, 0, 53, 0, time.UTC)
	})
	run.tick(ctx)

	first, err := queries.GetLatestDigestResult(ctx, store.GetLatestDigestResultParams{
		ConfigID: cfg.ID, UserID: user.ID,
	})
	if err != nil || first.ID == 0 {
		t.Fatalf("tick did not produce a digest: id=%d err=%v", first.ID, err)
	}
	setDigestResultCreatedAt(t, db, first.ID, time.Date(2026, 9, 25, 5, 0, 53, 0, time.UTC))

	// Fresh items so a duplicate would genuinely be possible if the guard failed.
	addItems(t, queries, feedObj.ID, "hourly-extra", 2)
	before := countDigestResults(t, queries, cfg)

	// 06:00:06 — the routine hourly eviction/restart, 59m13s after the digest,
	// far outside the 30-minute recency window but still inside the 6h grace.
	restart := NewScheduler(queries, gen, time.UTC)
	restart.SetClock(func() time.Time {
		return time.Date(2026, 9, 25, 6, 0, 6, 0, time.UTC)
	})
	restart.catchUp(ctx)

	if after := countDigestResults(t, queries, cfg); after != before {
		t.Errorf("digest results went %d -> %d: the hourly restart regenerated a digest that had already been produced for the 05:00 occurrence", before, after)
	}
}

// TestCatchUpGeneratesAfterRecencyWindowCloses is the mirror of the duplicate
// guard: once the previous result is older than the recency window, catch-up
// must generate again. Without this, a guard that skipped unconditionally
// would look correct while silently resurrecting the lost-digest bug.
func TestCatchUpGeneratesAfterRecencyWindowCloses(t *testing.T) {
	db, queries, cleanup := setupDigestTest(t)
	defer cleanup()
	ctx := context.Background()

	user, err := queries.CreateUser(ctx, store.CreateUserParams{
		Email:        "catchup-window@example.com",
		PasswordHash: "hash",
		ApiKey:       "catchup-window-key",
	})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	feedObj, err := queries.CreateFeed(ctx, store.CreateFeedParams{
		UserID: user.ID, Type: "rss", Name: "Window Feed",
		Url: "https://example.com/window.xml", Config: "{}", Status: "active",
	})
	if err != nil {
		t.Fatalf("create feed: %v", err)
	}
	addItems(t, queries, feedObj.ID, "window", 2)

	cfg, err := queries.CreateDigestConfig(ctx, store.CreateDigestConfigParams{
		UserID:   user.ID,
		Name:     "Daily 5am",
		Schedule: "0 5 * * *",
		Config:   `{"feeds":[{"id":` + strconv.FormatInt(feedObj.ID, 10) + `,"highlights":1,"comments":0}]}`,
	})
	if err != nil {
		t.Fatalf("create digest config: %v", err)
	}

	// The config must predate the occurrence under test, or the pre-creation
	// guard skips catch-up for a reason unrelated to what this test asserts
	// (and the test would pass without exercising its guard at all).
	setConfigCreatedAt(t, db, cfg.ID, time.Date(2026, 9, 23, 0, 0, 0, 0, time.UTC))

	gen := NewGenerator(db, queries,
		fakeLLM{resp: `{"title":"WINDOW","summary":"s","highlights":[]}`},
		feed.NewPoller(queries))

	// Yesterday's digest exists, well outside the recency window.
	run := NewScheduler(queries, gen, time.UTC)
	run.SetClock(func() time.Time {
		return time.Date(2026, 9, 24, 5, 0, 10, 0, time.UTC)
	})
	run.tick(ctx)

	first, err := queries.GetLatestDigestResult(ctx, store.GetLatestDigestResultParams{
		ConfigID: cfg.ID, UserID: user.ID,
	})
	if err != nil || first.ID == 0 {
		t.Fatalf("tick did not produce a digest: id=%d err=%v", first.ID, err)
	}
	// 23h55m before the catch-up instant: far outside the 30-minute window.
	setDigestResultCreatedAt(t, db, first.ID, time.Date(2026, 9, 24, 5, 0, 10, 0, time.UTC))
	before := countDigestResults(t, queries, cfg)

	// The first run consumed the unread items; today's missed digest needs its
	// own material, otherwise generation fails for a reason unrelated to the
	// recency guard and the assertion would pass or fail by accident.
	addItems(t, queries, feedObj.ID, "window-day2", 2)

	// Today's 05:00 was missed; the pod comes up at 05:05 and must recover it.
	restart := NewScheduler(queries, gen, time.UTC)
	restart.SetClock(func() time.Time {
		return time.Date(2026, 9, 25, 5, 5, 0, 0, time.UTC)
	})
	restart.catchUp(ctx)

	if after := countDigestResults(t, queries, cfg); after != before+1 {
		t.Errorf("digest results went %d -> %d; a previous result outside the recency window must not block recovery", before, after)
	}
}

// TestCatchUpSkipsStaleOccurrence bounds the recovery window: a digest missed
// long enough ago must not be generated, because it would be presented as
// current while the next occurrence is nearly due.
func TestCatchUpSkipsStaleOccurrence(t *testing.T) {
	db, queries, cleanup := setupDigestTest(t)
	defer cleanup()
	ctx := context.Background()

	user, err := queries.CreateUser(ctx, store.CreateUserParams{
		Email:        "catchup-stale@example.com",
		PasswordHash: "hash",
		ApiKey:       "catchup-stale-key",
	})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	feedObj, err := queries.CreateFeed(ctx, store.CreateFeedParams{
		UserID: user.ID, Type: "rss", Name: "Stale Feed",
		Url: "https://example.com/stale.xml", Config: "{}", Status: "active",
	})
	if err != nil {
		t.Fatalf("create feed: %v", err)
	}
	addItems(t, queries, feedObj.ID, "stale", 2)

	cfg, err := queries.CreateDigestConfig(ctx, store.CreateDigestConfigParams{
		UserID:   user.ID,
		Name:     "Daily 5am",
		Schedule: "0 5 * * *",
		Config:   `{"feeds":[{"id":` + strconv.FormatInt(feedObj.ID, 10) + `,"highlights":1,"comments":0}]}`,
	})
	if err != nil {
		t.Fatalf("create digest config: %v", err)
	}

	// The config must predate the occurrence under test, or the pre-creation
	// guard skips catch-up for a reason unrelated to what this test asserts
	// (and the test would pass without exercising its guard at all).
	setConfigCreatedAt(t, db, cfg.ID, time.Date(2026, 9, 23, 0, 0, 0, 0, time.UTC))

	gen := NewGenerator(db, queries,
		fakeLLM{resp: `{"title":"STALE","summary":"s","highlights":[]}`},
		feed.NewPoller(queries))

	s := NewScheduler(queries, gen, time.UTC)
	// 13:00 — eight hours after the missed 05:00 occurrence, beyond the
	// catch-up grace window. There is no code path that can generate here.
	s.SetClock(func() time.Time {
		return time.Date(2026, 9, 25, 13, 0, 0, 0, time.UTC)
	})
	s.catchUp(ctx)

	latest, err := queries.GetLatestDigestResult(ctx, store.GetLatestDigestResultParams{
		ConfigID: cfg.ID, UserID: user.ID,
	})
	if err == nil && latest.ID != 0 {
		t.Errorf("stale occurrence was generated anyway (result id %d); the grace window must bound catch-up", latest.ID)
	}
}

// TestCatchUpOwnsTheCurrentMinute: when Start happens inside the scheduled
// minute, the ticker cannot observe that minute — an anchored ticker's first
// fire is in the NEXT minute — so catch-up must treat the current minute as
// its own business. This is the production shape: the pod came up at 05:00:06
// for a 05:00 schedule.
func TestCatchUpOwnsTheCurrentMinute(t *testing.T) {
	db, queries, cleanup := setupDigestTest(t)
	defer cleanup()
	ctx := context.Background()

	user, err := queries.CreateUser(ctx, store.CreateUserParams{
		Email:        "catchup-current@example.com",
		PasswordHash: "hash",
		ApiKey:       "catchup-current-key",
	})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	feedObj, err := queries.CreateFeed(ctx, store.CreateFeedParams{
		UserID: user.ID, Type: "rss", Name: "Current Feed",
		Url: "https://example.com/current.xml", Config: "{}", Status: "active",
	})
	if err != nil {
		t.Fatalf("create feed: %v", err)
	}
	addItems(t, queries, feedObj.ID, "current", 2)

	cfg, err := queries.CreateDigestConfig(ctx, store.CreateDigestConfigParams{
		UserID:   user.ID,
		Name:     "Daily 5am",
		Schedule: "0 5 * * *",
		Config:   `{"feeds":[{"id":` + strconv.FormatInt(feedObj.ID, 10) + `,"highlights":1,"comments":0}]}`,
	})
	if err != nil {
		t.Fatalf("create digest config: %v", err)
	}

	// The config must predate the occurrence under test, or the pre-creation
	// guard skips catch-up for a reason unrelated to what this test asserts
	// (and the test would pass without exercising its guard at all).
	setConfigCreatedAt(t, db, cfg.ID, time.Date(2026, 9, 23, 0, 0, 0, 0, time.UTC))

	gen := NewGenerator(db, queries,
		fakeLLM{resp: `{"title":"CURRENT","summary":"s","highlights":[]}`},
		feed.NewPoller(queries))

	s := NewScheduler(queries, gen, time.UTC)
	// 05:00:30 — inside the scheduled minute, which no tick will ever cover.
	s.SetClock(func() time.Time {
		return time.Date(2026, 9, 25, 5, 0, 30, 0, time.UTC)
	})
	s.catchUp(ctx)

	latest, err := queries.GetLatestDigestResult(ctx, store.GetLatestDigestResultParams{
		ConfigID: cfg.ID, UserID: user.ID,
	})
	if err != nil || latest.ID == 0 {
		t.Fatal("catch-up ignored the occurrence in the current minute; no ticker tick can ever cover it, so the digest would be lost")
	}

	// A second catch-up in the same window must not duplicate it.
	before := countDigestResults(t, queries, cfg)
	s.catchUp(ctx)
	if after := countDigestResults(t, queries, cfg); after != before {
		t.Errorf("digest results went %d -> %d on a repeated catch-up", before, after)
	}
}

// TestStartRunsCatchUpBeforeServingTicks is the wiring guard: the recovery
// above is worthless unless Start actually invokes it. Every other test here
// calls catchUp directly, so only this one fails if the call is dropped from
// Start — which is exactly the regression that would silently reinstate the
// lost-digest bug.
func TestStartRunsCatchUpBeforeServingTicks(t *testing.T) {
	db, queries, cleanup := setupDigestTest(t)
	defer cleanup()
	ctx := context.Background()

	user, err := queries.CreateUser(ctx, store.CreateUserParams{
		Email:        "catchup-start@example.com",
		PasswordHash: "hash",
		ApiKey:       "catchup-start-key",
	})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	feedObj, err := queries.CreateFeed(ctx, store.CreateFeedParams{
		UserID: user.ID, Type: "rss", Name: "Start Feed",
		Url: "https://example.com/start.xml", Config: "{}", Status: "active",
	})
	if err != nil {
		t.Fatalf("create feed: %v", err)
	}
	addItems(t, queries, feedObj.ID, "start", 2)

	cfg, err := queries.CreateDigestConfig(ctx, store.CreateDigestConfigParams{
		UserID:   user.ID,
		Name:     "Daily 5am",
		Schedule: "0 5 * * *",
		Config:   `{"feeds":[{"id":` + strconv.FormatInt(feedObj.ID, 10) + `,"highlights":1,"comments":0}]}`,
	})
	if err != nil {
		t.Fatalf("create digest config: %v", err)
	}

	// The config must predate the occurrence under test, or the pre-creation
	// guard skips catch-up for a reason unrelated to what this test asserts
	// (and the test would pass without exercising its guard at all).
	setConfigCreatedAt(t, db, cfg.ID, time.Date(2026, 9, 23, 0, 0, 0, 0, time.UTC))

	gen := NewGenerator(db, queries,
		fakeLLM{resp: `{"title":"START","summary":"s","highlights":[]}`},
		feed.NewPoller(queries))

	s := NewScheduler(queries, gen, time.UTC)
	// 05:00:06 on the day of the missed 05:00 occurrence.
	s.SetClock(func() time.Time {
		return time.Date(2026, 9, 25, 5, 0, 6, 0, time.UTC)
	})

	// Start runs in its own goroutine below, so the pool would otherwise open
	// a second connection — and every connection to ":memory: sqlite" gets its
	// own empty database, so the poll would watch a DB that never sees the
	// digest. Production pins this to 1 (cmd/digesterr/main.go); mirror it.
	db.SetMaxOpenConns(1)

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		s.Start(runCtx)
	}()

	// The ticker's first fire is a minute away, so anything that appears now
	// came from the catch-up path in Start.
	var got int64
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		latest, err := queries.GetLatestDigestResult(ctx, store.GetLatestDigestResultParams{
			ConfigID: cfg.ID, UserID: user.ID,
		})
		if err == nil && latest.ID != 0 {
			got = latest.ID
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	cancel()
	<-done

	if got == 0 {
		t.Fatal("Start did not generate the missed occurrence: catch-up is not wired into Start")
	}
}

// TestPreviousOccurrenceFindsLastMatchingMinute pins the backward scan,
// including schedules whose matches are far apart.
func TestPreviousOccurrenceFindsLastMatchingMinute(t *testing.T) {
	now := time.Date(2026, 9, 25, 5, 0, 6, 0, time.UTC)

	prev, ok := previousOccurrence("0 5 * * *", now)
	if !ok {
		t.Fatal("no previous occurrence found for a daily 05:00 schedule")
	}
	// Today's 05:00 is at-or-before 05:00:06, so it is the occurrence the
	// scheduler must consider — this is the missed one in production.
	if want := time.Date(2026, 9, 25, 5, 0, 0, 0, time.UTC); !prev.Equal(want) {
		t.Errorf("previous occurrence = %s, want %s", prev.Format(time.RFC3339), want.Format(time.RFC3339))
	}

	// A 5-minute schedule: the previous match is 05:00, one minute before now.
	prev, ok = previousOccurrence("*/5 * * * *", now)
	if !ok {
		t.Fatal("no previous occurrence found for a */5 schedule")
	}
	if want := time.Date(2026, 9, 25, 5, 0, 0, 0, time.UTC); !prev.Equal(want) {
		t.Errorf("previous occurrence = %s, want %s", prev.Format(time.RFC3339), want.Format(time.RFC3339))
	}

	// A weekly Monday 05:00 schedule from a Friday: the previous match is days
	// back and outside the search horizon, so the scan must report no
	// occurrence rather than spinning or returning a stale one.
	nowFriday := time.Date(2026, 9, 25, 5, 0, 6, 0, time.UTC) // a Friday
	if _, ok := previousOccurrence("0 5 * * 1", nowFriday); ok {
		t.Error("previousOccurrence returned a weekly occurrence older than the search horizon; it must give up so the grace window can reject it")
	}

	// The same schedule seen on a Monday finds that morning's occurrence.
	nowMonday := time.Date(2026, 9, 28, 5, 0, 6, 0, time.UTC) // the next Monday
	prev, ok = previousOccurrence("0 5 * * 1", nowMonday)
	if !ok {
		t.Fatal("no previous occurrence found for a weekly Monday schedule on that Monday")
	}
	if want := time.Date(2026, 9, 28, 5, 0, 0, 0, time.UTC); !prev.Equal(want) {
		t.Errorf("previous occurrence = %s, want %s", prev.Format(time.RFC3339), want.Format(time.RFC3339))
	}
}

// TestPreviousOccurrenceGivesUpBeyondTheSearchWindow guards the bound on the
// backward scan: an unreachable schedule must terminate, not spin.
func TestPreviousOccurrenceGivesUpBeyondTheSearchWindow(t *testing.T) {
	now := time.Date(2026, 9, 25, 5, 0, 6, 0, time.UTC)
	// Feb 30 never exists; the scan must stop at its step budget.
	if _, ok := previousOccurrence("0 5 30 2 *", now); ok {
		t.Error("previousOccurrence found a match for an impossible schedule")
	}
}
