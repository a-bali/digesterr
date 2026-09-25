package digest

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"sync"
	"testing"
	"time"

	"digesterr/internal/feed"
	"digesterr/internal/store"
)

// Startup used to pin its elapsed-minute watermark AFTER catch-up returned.
//
// catchUp ends in wg.Wait, so it blocks until every recovery generation has
// finished. The ticker is created before it, so a fire during recovery is
// buffered. The loop then read the clock and set the watermark to THAT time,
// so its first runTicks evaluated minutesSince(postRecoveryNow,
// postRecoveryNow) -- nothing. Every minute the recovery occupied was therefore
// skipped forever, and nothing recorded that it had been skipped: the same
// silent-missing-digest shape the startup catch-up was added to fix, one layer
// out. With a daily schedule the cost is a whole day's digest.
//
// Pinning the watermark BEFORE catch-up makes the first tick replay the minutes
// recovery occupied. Catch-up's own minute is not replayed (minutesSince is
// strictly after the watermark) and alreadyServed guards it regardless.

// TestStartupWatermarkPrecedesCatchUp drives the REAL Start loop, so it fails
// if the watermark is ever moved back after catch-up. A test that called
// startupWatermark() directly would pass regardless of where Start calls it.
//
// The clock advances on every read, standing in for wall-clock time passing
// during recovery. Catch-up is made slow by a config whose occurrence it must
// recover, and the assertions are the two observable consequences: the first
// evaluated minute is the one after startup (not after recovery), and none of
// the minutes recovery occupied were skipped.
func TestStartupWatermarkPrecedesCatchUp(t *testing.T) {
	db, queries, cleanup := setupDigestTest(t)
	defer cleanup()

	// A config that is genuinely due: catch-up will generate for the 05:00
	// occurrence, so recovery takes real time.
	_, _, cfg := schedulerFixture(t, queries, "watermark@example.com",
		"Watermark Feed", "0 5 * * *", "watermark")
	setConfigCreatedAt(t, db, cfg.ID, time.Date(2026, 9, 23, 0, 0, 0, 0, time.UTC))

	gen := NewGenerator(db, queries,
		fakeLLM{resp: `{"title":"W","summary":"s","highlights":[]}`},
		feed.NewPoller(queries))
	s := NewScheduler(queries, gen, time.UTC)
	s.tickInterval = 20 * time.Millisecond

	// Startup at 05:00:06; every clock read advances four minutes, so the
	// post-catch-up instant is far ahead of startup.
	clock := &steppingClock{
		now:  time.Date(2026, 9, 25, 5, 0, 6, 0, time.UTC),
		step: 4 * time.Minute,
	}
	s.SetClock(clock.Now)

	var mu sync.Mutex
	var evaluated []time.Time
	s.onEvaluate = func(m time.Time) {
		mu.Lock()
		evaluated = append(evaluated, m)
		mu.Unlock()
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); s.Start(ctx) }()

	// Wait for the first tick to be served, then stop.
	deadline := time.After(5 * time.Second)
	for {
		mu.Lock()
		n := len(evaluated)
		mu.Unlock()
		if n > 0 {
			break
		}
		select {
		case <-deadline:
			cancel()
			<-done
			t.Fatal("no minute was ever evaluated after startup")
		default:
			time.Sleep(5 * time.Millisecond)
		}
	}
	cancel()
	<-done

	mu.Lock()
	defer mu.Unlock()
	// The watermark must be the startup minute (05:00), so the first evaluated
	// minute is 05:01 -- NOT 05:05, which is where a post-catch-up watermark
	// would put it.
	if got := evaluated[0]; !got.Equal(time.Date(2026, 9, 25, 5, 1, 0, 0, time.UTC)) {
		t.Errorf("first evaluated minute = %v, want 2026-09-25T05:01:00Z. A later minute "+
			"means the watermark was pinned after catch-up, skipping every minute "+
			"recovery occupied", got)
	}
}

// TestReplayedTickSkipsOccurrenceBeforeConfigCreation covers the second half of
// the finding. The startup catch-up refuses an occurrence older than the config,
// but the replayed-tick path used by runTicks had no such guard, so a minute
// replayed after a slow recovery could generate for an occurrence that predates
// the config -- a digest the user never scheduled, which is precisely what the
// catch-up guard exists to prevent.
func TestReplayedTickSkipsOccurrenceBeforeConfigCreation(t *testing.T) {
	db, queries, cleanup := setupDigestTest(t)
	defer cleanup()
	ctx := context.Background()

	user, _, cfg := schedulerFixture(t, queries, "precreation@example.com",
		"Pre-creation Feed", "0 5 * * *", "precreation")

	// Created at 05:30, so the 05:00 occurrence predates it by 30 minutes.
	setConfigCreatedAt(t, db, cfg.ID, time.Date(2026, 9, 25, 5, 30, 0, 0, time.UTC))

	gen := NewGenerator(db, queries,
		fakeLLM{resp: `{"title":"P","summary":"s","highlights":[]}`},
		feed.NewPoller(queries))
	s := NewScheduler(queries, gen, time.UTC)

	// Replay the 05:00 minute -- exactly what runTicks hands tickAt after a
	// slow startup recovery.
	s.tickAt(ctx, time.Date(2026, 9, 25, 5, 0, 0, 0, time.UTC))

	latest, err := queries.GetLatestDigestResult(ctx, store.GetLatestDigestResultParams{
		ConfigID: cfg.ID, UserID: user.ID,
	})
	// No rows is the PASS case here: it means no digest was generated.
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("get latest: %v", err)
	}
	if latest.ID != 0 {
		t.Errorf("a replayed tick generated a digest for an occurrence 30 minutes "+
			"before the config existed (result id %d): the pre-creation guard must "+
			"apply to replayed minutes, not only to startup catch-up", latest.ID)
	}
}

// TestReplayedTickStillGeneratesForCurrentOccurrence is the inverse guard: the
// pre-creation check must not reject a legitimate occurrence. A replayed minute
// that the config DOES predate must still generate, or the fix trades one silent
// miss for another.
func TestReplayedTickStillGeneratesForCurrentOccurrence(t *testing.T) {
	db, queries, cleanup := setupDigestTest(t)
	defer cleanup()
	ctx := context.Background()

	user, _, cfg := schedulerFixture(t, queries, "legit@example.com",
		"Legit Feed", "0 5 * * *", "legit")

	// The config predates the occurrence comfortably.
	setConfigCreatedAt(t, db, cfg.ID, time.Date(2026, 9, 25, 1, 0, 0, 0, time.UTC))

	gen := NewGenerator(db, queries,
		fakeLLM{resp: `{"title":"L","summary":"s","highlights":[]}`},
		feed.NewPoller(queries))
	s := NewScheduler(queries, gen, time.UTC)

	s.tickAt(ctx, time.Date(2026, 9, 25, 5, 0, 0, 0, time.UTC))

	latest, err := queries.GetLatestDigestResult(ctx, store.GetLatestDigestResultParams{
		ConfigID: cfg.ID, UserID: user.ID,
	})
	if err != nil {
		t.Fatalf("get latest: %v", err)
	}
	if latest.ID == 0 {
		t.Error("no digest generated for an occurrence the config predates by 4 hours: " +
			"the pre-creation guard is too wide and now suppresses legitimate runs")
	}
}

// --- helpers ---------------------------------------------------------------

// steppingClock is a mutable clock that advances by step on every read, so a
// test can make wall-clock time pass during an operation without sleeping.
type steppingClock struct {
	mu   sync.Mutex
	now  time.Time
	step time.Duration
}

func (c *steppingClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	cur := c.now
	c.now = c.now.Add(c.step)
	return cur
}

// schedulerFixture creates the user, feed-with-items and digest config that
// every scheduler test needs, returning all three.
func schedulerFixture(t *testing.T, queries *store.Queries, email, feedName, schedule, key string) (store.User, store.Feed, store.DigestConfig) {
	t.Helper()
	ctx := context.Background()

	user, err := queries.CreateUser(ctx, store.CreateUserParams{
		Email:        email,
		PasswordHash: "hash",
		ApiKey:       "key-" + key,
	})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	feedObj, err := queries.CreateFeed(ctx, store.CreateFeedParams{
		UserID: user.ID, Type: "rss", Name: feedName,
		Url: "https://example.com/" + key + ".xml", Config: "{}", Status: "active",
	})
	if err != nil {
		t.Fatalf("create feed: %v", err)
	}
	addItems(t, queries, feedObj.ID, key, 2)

	cfg, err := queries.CreateDigestConfig(ctx, store.CreateDigestConfigParams{
		UserID:   user.ID,
		Name:     feedName + " digest",
		Schedule: schedule,
		Config:   `{"feeds":[{"id":` + strconv.FormatInt(feedObj.ID, 10) + `,"highlights":1,"comments":0}]}`,
	})
	if err != nil {
		t.Fatalf("create digest config: %v", err)
	}
	return user, feedObj, cfg
}
