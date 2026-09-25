package digest

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"time"

	"digesterr/internal/feed"
	"digesterr/internal/store"
)

// The scheduler used to serve one minute per ticker fire and block on the
// generation inside the tick. Go's ticker holds a single pending fire, so a
// generation that outran the interval silently dropped every occurrence in the
// minutes it occupied: a digest scheduled for 05:03 simply never existed, and
// nothing recorded that it had been skipped.
//
// runTicks is the loop body, tested directly so the behaviour does not depend
// on waiting a real minute for the ticker.

// digestReplyForItem names the single item addItems creates for these tests.
// One highlight is all the config asks for, so the reply is complete and the
// run succeeds — a short reply would be retried and logged as incomplete,
// which is a different behaviour under test elsewhere.
func digestReplyForItem(key string) string {
	return `{"title":"T","summary":"s","highlights":[{"title":"Item ` + key + `-0","summary":"s1"}]}`
}

// digestReplyForItems covers every item addItems created. A generation presents
// one article per requested highlight, and which one it presents varies with
// the pool, so a reply that covers all of them keeps the run complete whichever
// article is chosen. An incomplete reply is retried, which would double the LLM
// call count that the replay test measures.
func digestReplyForItems(key string, n int) string {
	parts := make([]string, 0, n)
	for i := 0; i < n; i++ {
		parts = append(parts, `{"title":"Item `+key+"-"+strconv.Itoa(i)+`","summary":"s1"}`)
	}
	return `{"title":"T","summary":"s","highlights":[` + strings.Join(parts, ",") + `]}`
}

// TestMinutesSinceCoversEveryElapsedMinute pins the watermark arithmetic the
// catch-up depends on: the minutes strictly after `from`, through `to`.
func TestMinutesSinceCoversEveryElapsedMinute(t *testing.T) {
	base := time.Date(2026, 9, 25, 5, 0, 0, 0, time.UTC)

	got := minutesSince(base, base.Add(3*time.Minute))
	want := []time.Time{
		base.Add(time.Minute),
		base.Add(2 * time.Minute),
		base.Add(3 * time.Minute),
	}
	if len(got) != len(want) {
		t.Fatalf("minutesSince over 3 minutes = %d entries, want %d", len(got), len(want))
	}
	for i := range want {
		if !got[i].Equal(want[i]) {
			t.Errorf("minute %d = %v, want %v", i, got[i], want[i])
		}
	}
}

// TestMinutesSinceIgnoresNonPositiveSpan covers the clock standing still
// between two fires and a backwards NTP step: neither may replay a minute.
func TestMinutesSinceIgnoresNonPositiveSpan(t *testing.T) {
	base := time.Date(2026, 9, 25, 5, 0, 0, 0, time.UTC)

	if got := minutesSince(base, base); len(got) != 0 {
		t.Errorf("same instant = %v, want no minutes", got)
	}
	if got := minutesSince(base, base.Add(-time.Minute)); len(got) != 0 {
		t.Errorf("backwards step = %v, want no minutes", got)
	}
	// Sub-minute movement must not fabricate a whole minute either.
	if got := minutesSince(base, base.Add(30*time.Second)); len(got) != 0 {
		t.Errorf("+30s = %v, want no minutes", got)
	}
}

// TestRunTicksServesMinutesSkippedBySlowGeneration is the regression for the
// production failure mode: while a generation ran long, the ticker's pending
// fires were dropped and those minutes were never evaluated. The watermark
// makes each elapsed minute an explicit unit of work.
func TestRunTicksServesMinutesSkippedBySlowGeneration(t *testing.T) {
	db, queries, cleanup := setupDigestTest(t)
	defer cleanup()
	ctx := t.Context()

	user, err := queries.CreateUser(ctx, store.CreateUserParams{
		Email:        "late-tick@example.com",
		PasswordHash: "hash",
		ApiKey:       "late-tick-key",
	})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	feedObj, err := queries.CreateFeed(ctx, store.CreateFeedParams{
		UserID: user.ID, Type: "rss", Name: "Feed",
		Url: "https://example.com/f.xml", Config: "{}", Status: "active",
	})
	if err != nil {
		t.Fatalf("create feed: %v", err)
	}
	// Fires only at minute 3 of the hour.
	cfg, err := queries.CreateDigestConfig(ctx, store.CreateDigestConfigParams{
		UserID:   user.ID,
		Name:     "Late Config",
		Schedule: "3 * * * *",
		Config:   `{"feeds":[{"id":` + strconv.FormatInt(feedObj.ID, 10) + `,"highlights":1,"comments":0}]}`,
	})
	if err != nil {
		t.Fatalf("create digest config: %v", err)
	}
	addItems(t, queries, feedObj.ID, "late-tick", 1)

	// The config must predate the occurrence under test. created_at defaults
	// to the real wall clock, which is LATER than this test's pinned 05:00
	// scheduler clock, so without this the pre-creation guard would skip the
	// 05:03 occurrence for a reason unrelated to what the test asserts.
	setConfigCreatedAt(t, db, cfg.ID, time.Date(2026, 9, 23, 0, 0, 0, 0, time.UTC))

	gen := NewGenerator(db, queries, fakeLLM{resp: digestReplyForItem("late-tick")}, feed.NewPoller(queries))
	s := NewScheduler(queries, gen, time.UTC)

	// The scheduler believes it is 05:00 and last evaluated 05:00. The next
	// time it looks up it is 05:05 — a generation occupied the whole span.
	clock := time.Date(2026, 9, 25, 5, 0, 0, 0, time.UTC)
	s.SetClock(func() time.Time { return clock })
	watermark := clock

	clock = time.Date(2026, 9, 25, 5, 5, 0, 0, time.UTC)
	watermark = s.runTicks(ctx, watermark)

	// 05:03 must have been served even though no tick landed on it.
	after, err := queries.GetDigestConfig(ctx, store.GetDigestConfigParams{ID: cfg.ID, UserID: user.ID})
	if err != nil {
		t.Fatalf("get digest config: %v", err)
	}
	if after.LastError != "" {
		t.Errorf("last_error = %q, want empty: the 05:03 occurrence should have run", after.LastError)
	}
	if got := countDigestResults(t, queries, cfg); got != 1 {
		t.Fatalf("digest results = %d, want exactly 1 for the 05:03 occurrence", got)
	}

	// The watermark advanced to the minute that was just served, so the next
	// tick continues from there rather than replaying the span.
	if !watermark.Equal(clock.Truncate(time.Minute)) {
		t.Errorf("watermark = %v, want %v", watermark, clock.Truncate(time.Minute))
	}
}

// TestRunTicksDoesNotReplayServedMinutes is the other half: a repeat pass at an
// instant already served must evaluate nothing, or every slow generation would
// duplicate the digest it was late for.
//
// The fake clock is deliberately in the FUTURE. created_at is written from the
// real wall clock, so a past-dated scheduler clock makes every existing result
// look newer than "now - recentGenerationWindow" and the recency guard would
// mask whatever this test is trying to observe. With the clock ahead, recency
// stays out of the way and the watermark is the only thing that can dedupe.
//
// The schedule fires at :05 and :45 so the two matches inside the span are more
// than recentGenerationWindow apart, again keeping the recency guard out of it.
func TestRunTicksDoesNotReplayServedMinutes(t *testing.T) {
	db, queries, cleanup := setupDigestTest(t)
	defer cleanup()
	ctx := t.Context()

	user, err := queries.CreateUser(ctx, store.CreateUserParams{
		Email:        "noreplay@example.com",
		PasswordHash: "hash",
		ApiKey:       "noreplay-key",
	})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	feedObj, err := queries.CreateFeed(ctx, store.CreateFeedParams{
		UserID: user.ID, Type: "rss", Name: "Feed",
		Url: "https://example.com/f.xml", Config: "{}", Status: "active",
	})
	if err != nil {
		t.Fatalf("create feed: %v", err)
	}
	addItems(t, queries, feedObj.ID, "noreplay", 2)
	_, err = queries.CreateDigestConfig(ctx, store.CreateDigestConfigParams{
		UserID:   user.ID,
		Name:     "Twice an Hour",
		Schedule: "1,2 * * * *",
		Config:   `{"feeds":[{"id":` + strconv.FormatInt(feedObj.ID, 10) + `,"highlights":1,"comments":0}]}`,
	})
	if err != nil {
		t.Fatalf("create digest config: %v", err)
	}

	gen := NewGenerator(db, queries, fakeLLM{resp: digestReplyForItems("noreplay", 2)}, feed.NewPoller(queries))
	s := NewScheduler(queries, gen, time.UTC)
	var evaluated []time.Time
	s.onEvaluate = func(m time.Time) { evaluated = append(evaluated, m) }

	clock := time.Date(2030, 1, 1, 5, 0, 0, 0, time.UTC)
	s.SetClock(func() time.Time { return clock })
	watermark := clock

	// Two minutes elapse, both of which the schedule matches, so both are owed
	// an evaluation.
	clock = time.Date(2030, 1, 1, 5, 2, 0, 0, time.UTC)
	watermark = s.runTicks(ctx, watermark)

	// Evaluations are the instrument, not results or LLM calls: the first run
	// marks the items digested, so the second minute legitimately finds an
	// empty pool and produces neither. Only the evaluated minutes show the
	// span was walked at all.
	if len(evaluated) != 2 {
		t.Fatalf("minutes evaluated = %d (%v), want 2 (05:01 and 05:02)", len(evaluated), evaluated)
	}
	if got := evaluated[0].Format("15:04"); got != "05:01" {
		t.Errorf("first minute evaluated = %s, want 05:01", got)
	}
	if got := evaluated[1].Format("15:04"); got != "05:02" {
		t.Errorf("second minute evaluated = %s, want 05:02", got)
	}

	// A repeat pass at an instant already served owes nothing: the watermark is
	// what stops the span being re-walked on every subsequent tick. Without it,
	// each late tick would re-evaluate the whole span and re-generate.
	watermark = s.runTicks(ctx, watermark)
	if len(evaluated) != 2 {
		t.Errorf("minutes evaluated after a repeat pass = %d (%v), want 2: served minutes must not replay",
			len(evaluated), evaluated)
	}
}


// TestRunTicksWatermarkNeverGoesBackwards guards the watermark against a clock
// that steps backwards, which would otherwise replay minutes on the next tick.
func TestRunTicksWatermarkNeverGoesBackwards(t *testing.T) {
	s := NewScheduler(nil, nil, time.UTC)
	later := time.Date(2026, 9, 25, 5, 10, 0, 0, time.UTC)
	s.SetClock(func() time.Time { return time.Date(2026, 9, 25, 5, 5, 0, 0, time.UTC) })

	if got := s.runTicks(context.Background(), later); !got.Equal(later) {
		t.Errorf("watermark after a backwards clock = %v, want %v (unchanged)", got, later)
	}
}
