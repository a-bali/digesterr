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

// Catch-up runs at startup and asks "has this occurrence been served?" against
// an exact watermark: servedSince compares the newest result's created_at with
// the occurrence minute. A manually generated digest a few seconds BEFORE the
// occurrence therefore does not suppress catch-up, and the user gets a second
// digest for a schedule that had already produced one.
//
// The normal tick does not have this problem — alreadyServed applies a 30-minute
// recency window — so the two paths disagree about what "already served" means.
func TestCatchUpDoesNotDuplicateDigestMadeJustBeforeOccurrence(t *testing.T) {
	db, queries, cleanup := setupDigestTest(t)
	defer cleanup()
	ctx := context.Background()

	_, cfg := catchUpFixture(t, queries, "0 5 * * *")
	gen := NewGenerator(db, queries,
		fakeLLM{resp: `{"title":"DUP","summary":"s","highlights":[]}`},
		feed.NewPoller(queries))

	// A manual digest at 04:59:30, thirty seconds before the 05:00 occurrence.
	// It is one occurrence old, so the user's "last digest" is the same one the
	// schedule would produce.
	manual, err := queries.CreateDigestResult(ctx, store.CreateDigestResultParams{
		UserID:   cfg.UserID,
		ConfigID: cfg.ID,
		Status:   "new",
		Content:  `{"title":"MANUAL","summary":"s","highlights":[]}`,
	})
	if err != nil {
		t.Fatalf("create manual result: %v", err)
	}
	setResultCreatedAt(t, db, manual.ID, time.Date(2026, 9, 25, 4, 59, 30, 0, time.UTC))

	s := NewScheduler(queries, gen, time.UTC)
	s.SetClock(func() time.Time {
		return time.Date(2026, 9, 25, 5, 0, 6, 0, time.UTC)
	})

	s.catchUp(ctx)

	// The occurrence was already covered by the manual digest. A second result
	// inside the same 30-minute window is a duplicate the user did not ask for.
	if n := countDigestResults(t, queries, cfg); n != 1 {
		t.Fatalf("catch-up generated a duplicate: %d results for one occurrence, want 1", n)
	}
}

// An occurrence that fell BEFORE the config existed is not a missed digest: the
// config could not have run then, so generating for it back-fills a digest the
// user never scheduled. A config created at 05:30 with a "0 5 * * *" schedule
// must not immediately produce that morning's 05:00 digest.
func TestCatchUpSkipsOccurrencePredatingConfigCreation(t *testing.T) {
	db, queries, cleanup := setupDigestTest(t)
	defer cleanup()
	ctx := context.Background()

	_, cfg := catchUpFixture(t, queries, "0 5 * * *")
	gen := NewGenerator(db, queries,
		fakeLLM{resp: `{"title":"PRE","summary":"s","highlights":[]}`},
		feed.NewPoller(queries))

	// Created at 05:30, so the 05:00 occurrence is in the past but predates the
	// config.
	setConfigCreatedAt(t, db, cfg.ID, time.Date(2026, 9, 25, 5, 30, 0, 0, time.UTC))

	s := NewScheduler(queries, gen, time.UTC)
	s.SetClock(func() time.Time {
		return time.Date(2026, 9, 25, 5, 45, 0, 0, time.UTC)
	})

	s.catchUp(ctx)

	if n := countDigestResults(t, queries, cfg); n != 0 {
		t.Fatalf("catch-up back-filled an occurrence from before the config existed: %d results, want 0", n)
	}
}

// catchUpFixture builds a user with one digestable feed and a config on the
// given schedule.
func catchUpFixture(t *testing.T, queries *store.Queries, schedule string) (store.User, store.DigestConfig) {
	t.Helper()
	ctx := context.Background()

	suffix := strconv.FormatInt(time.Now().UnixNano(), 36)
	user, err := queries.CreateUser(ctx, store.CreateUserParams{
		Email:        "catchup-fixture-" + suffix + "@example.com",
		PasswordHash: "hash",
		ApiKey:       "ck_" + suffix,
	})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	feedObj, err := queries.CreateFeed(ctx, store.CreateFeedParams{
		UserID: user.ID, Type: "rss", Name: "Catch-up " + suffix,
		Url: "https://example.com/" + suffix + ".xml", Config: "{}", Status: "active",
	})
	if err != nil {
		t.Fatalf("create feed: %v", err)
	}
	addItems(t, queries, feedObj.ID, "catchup-"+suffix, 2)

	cfg, err := queries.CreateDigestConfig(ctx, store.CreateDigestConfigParams{
		UserID:   user.ID,
		Name:     "Catch-up " + suffix,
		Schedule: schedule,
		Config:   `{"feeds":[{"id":` + strconv.FormatInt(feedObj.ID, 10) + `,"highlights":1,"comments":0}]}`,
	})
	if err != nil {
		t.Fatalf("create digest config: %v", err)
	}
	return user, cfg
}

// setResultCreatedAt pins a result's created_at, which the API itself never
// lets a caller choose — the scenario under test is a result created at a
// specific instant, and "now" during a test is not controllable through the
// queries layer.
func setResultCreatedAt(t *testing.T, db *sql.DB, id int64, at time.Time) {
	t.Helper()
	if _, err := db.Exec(
		"UPDATE digest_results SET created_at = ? WHERE id = ?",
		at.UTC().Format("2006-01-02 15:04:05"), id); err != nil {
		t.Fatalf("pin result created_at: %v", err)
	}
}

func setConfigCreatedAt(t *testing.T, db *sql.DB, id int64, at time.Time) {
	t.Helper()
	if _, err := db.Exec(
		"UPDATE digest_configs SET created_at = ? WHERE id = ?",
		at.UTC().Format("2006-01-02 15:04:05"), id); err != nil {
		t.Fatalf("pin config created_at: %v", err)
	}
}
