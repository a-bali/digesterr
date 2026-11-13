package digest

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"time"

	"digesterr/internal/feed"
	"digesterr/internal/store"
)

// TestGenerateDigestStarvesBacklogUnderInflow is the regression test for the
// HN "old unread items never get digested" bug.
//
// Symptom: the HN feed accumulated unread items forever even though the daily
// digest ran successfully. Digests WERE produced, so nothing looked broken.
//
// Root cause: generator.go asked each feed for only `max_items/len(feeds)`
// unread items ordered `points DESC`, i.e. always the highest-scoring of the
// current backlog. HN ingests more per day than that budget and HN points
// correlate with recency, so every run re-selected the newest items and the
// older, lower-scoring tail was never reached. In production 107 items sat
// unread across 18 days and 57 of them were unreachable at their scores.
//
// The fix has two parts, both asserted here:
//  1. Age-weighted priority, so a neglected item's priority grows with the time
//     it has waited and the tail cannot be starved permanently.
//  2. Redistribution of unused per-feed budget, so an idle feed's share is not
//     wasted while another feed's backlog waits.
func TestGenerateDigestStarvesBacklogUnderInflow(t *testing.T) {
	db, queries, cleanup := setupDigestTest(t)
	defer cleanup()
	ctx := context.Background()

	user, err := queries.CreateUser(ctx, store.CreateUserParams{
		Email:        "inflow@test.com",
		PasswordHash: "hash",
		ApiKey:       "inflow-key",
	})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	hnFeed, err := queries.CreateFeed(ctx, store.CreateFeedParams{
		UserID: user.ID, Category: "Tech", Type: "hn",
		Name: "HN", Url: "https://news.ycombinator.com/rss", Config: "{}", Status: "active",
	})
	if err != nil {
		t.Fatalf("create hn feed: %v", err)
	}
	rssFeed, err := queries.CreateFeed(ctx, store.CreateFeedParams{
		UserID: user.ID, Category: "Tech", Type: "rss",
		Name: "RSS", Url: "https://example.com/rss", Config: "{}", Status: "active",
	})
	if err != nil {
		t.Fatalf("create rss feed: %v", err)
	}

	const (
		maxItems = 100 // per-feed fair share = maxItems / 2 = 50
		perDay   = 60  // HN inflow above the 50 share, as in production
		days     = 6
		perFeed  = maxItems / 2
	)

	cfg, err := queries.CreateDigestConfig(ctx, store.CreateDigestConfigParams{
		UserID:   user.ID,
		Schedule: "0 5 * * *",
		Config: `{"feeds":[{"id":` + strconv.FormatInt(hnFeed.ID, 10) +
			`,"highlights":5,"comments":10},{"id":` + strconv.FormatInt(rssFeed.ID, 10) +
			`,"highlights":5,"comments":10}],"max_items":` + strconv.Itoa(maxItems) + `}`,
	})
	if err != nil {
		t.Fatalf("create digest config: %v", err)
	}

	g := &Generator{
		db:      db,
		queries: queries,
		llm:     fakeLLM{resp: `{"title":"T","summary":"s","highlights":[]}`},
		enrichers: map[string]feed.Enricher{
			"hn": &staticEnricher{},
		},
		commentFetchers: map[string]feed.CommentFetcher{},
	}

	// Items are created with a real created_at spread across days so the age
	// weighting has something to work with; the newest items also score
	// highest, which is what defeated the old points-only ordering.
	base := time.Now().Add(-time.Duration(days) * 24 * time.Hour)
	seq := 0
	for day := 0; day < days; day++ {
		dayStart := base.Add(time.Duration(day) * 24 * time.Hour)
		for i := 0; i < perDay; i++ {
			seq++
			it, err := queries.CreateItem(ctx, store.CreateItemParams{
				FeedID: hnFeed.ID,
				Hash:   "inflow-" + strconv.Itoa(seq),
				Title:  "Day " + strconv.Itoa(day) + " item " + strconv.Itoa(i),
				Url:    "https://example.com/inflow/" + strconv.Itoa(seq),
				Points: int64(10000 + seq),
				Status: "unread",
			})
			if err != nil {
				t.Fatalf("day %d create item %d: %v", day, i, err)
			}
			// Backdate: creation time is what the age weighting reads.
			if _, err := db.ExecContext(ctx,
				`UPDATE items SET created_at = ? WHERE id = ?`,
				dayStart.Format("2006-01-02 15:04:05"), it.ID); err != nil {
				t.Fatalf("backdate item %d: %v", it.ID, err)
			}
		}

		if _, err := g.Generate(ctx, cfg); err != nil && !errors.Is(err, ErrNoUnreadItems) {
			t.Fatalf("day %d: generate: %v", day, err)
		}

		unread, err := queries.ListUnreadDigestItemsByFeed(ctx, store.ListUnreadDigestItemsByFeedParams{
			FeedID: hnFeed.ID,
			Limit:  100000,
		})
		if err != nil {
			t.Fatalf("day %d: count unread: %v", day, err)
		}
		t.Logf("day %d: %d unread after digest (inflow %d/day, fair share %d)",
			day+1, len(unread), perDay, perFeed)
	}

	// The oldest item must have been digested. Under points-only selection it
	// was permanently unreachable, which is the reported bug.
	var oldestID int64
	if err := db.QueryRowContext(ctx,
		`SELECT id FROM items WHERE feed_id = ? ORDER BY created_at ASC, id ASC LIMIT 1`,
		hnFeed.ID).Scan(&oldestID); err != nil {
		t.Fatalf("find oldest item: %v", err)
	}
	oldest, err := queries.GetItem(ctx, oldestID)
	if err != nil {
		t.Fatalf("get oldest item: %v", err)
	}
	if oldest.Status == "unread" {
		t.Fatalf("oldest HN item (id=%d, points=%d) still unread after %d digest runs: "+
			"the backlog tail is starved", oldest.ID, oldest.Points, days)
	}

	// Unread must stay bounded near the accumulated daily surplus rather than
	// growing with total inflow. Surplus per day is perDay-perFeed = 10.
	unread, err := queries.ListUnreadDigestItemsByFeed(ctx, store.ListUnreadDigestItemsByFeedParams{
		FeedID: hnFeed.ID,
		Limit:  100000,
	})
	if err != nil {
		t.Fatalf("count unread: %v", err)
	}
	maxAllowed := days * (perDay - perFeed) * 2
	if len(unread) > maxAllowed {
		t.Fatalf("%d HN items still unread after %d days (allowed <= %d): "+
			"unread is growing instead of draining",
			len(unread), days, maxAllowed)
	}
}

// TestSelectFeedItemsRedistributesUnusedBudget covers fix part 2 in isolation:
// a feed whose backlog is smaller than its fair share must not waste those
// slots while another feed still has candidates. Before the fix each feed was
// capped at max_items/len(feeds) and the remainder was dropped, so a digest of
// 100 could contain as few as 50 items with a full backlog waiting.
func TestSelectFeedItemsRedistributesUnusedBudget(t *testing.T) {
	db, queries, cleanup := setupDigestTest(t)
	defer cleanup()
	ctx := context.Background()

	user, err := queries.CreateUser(ctx, store.CreateUserParams{
		Email: "redistribute@test.com", PasswordHash: "hash", ApiKey: "redistribute-key",
	})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	busy, err := queries.CreateFeed(ctx, store.CreateFeedParams{
		UserID: user.ID, Category: "Tech", Type: "hn",
		Name: "Busy", Url: "https://example.com/busy", Config: "{}", Status: "active",
	})
	if err != nil {
		t.Fatalf("create busy feed: %v", err)
	}
	idle, err := queries.CreateFeed(ctx, store.CreateFeedParams{
		UserID: user.ID, Category: "Tech", Type: "rss",
		Name: "Idle", Url: "https://example.com/idle", Config: "{}", Status: "active",
	})
	if err != nil {
		t.Fatalf("create idle feed: %v", err)
	}

	// Busy feed has far more unread than its share; idle feed has only 3.
	for i := 0; i < 200; i++ {
		if _, err := queries.CreateItem(ctx, store.CreateItemParams{
			FeedID: busy.ID, Hash: "busy-" + strconv.Itoa(i),
			Title: "Busy " + strconv.Itoa(i), Url: "https://example.com/b/" + strconv.Itoa(i),
			Points: int64(100 + i), Status: "unread",
		}); err != nil {
			t.Fatalf("create busy item %d: %v", i, err)
		}
	}
	for i := 0; i < 3; i++ {
		if _, err := queries.CreateItem(ctx, store.CreateItemParams{
			FeedID: idle.ID, Hash: "idle-" + strconv.Itoa(i),
			Title: "Idle " + strconv.Itoa(i), Url: "https://example.com/i/" + strconv.Itoa(i),
			Status: "unread",
		}); err != nil {
			t.Fatalf("create idle item %d: %v", i, err)
		}
	}

	g := &Generator{db: db, queries: queries}
	limits := map[int64]feedLimit{
		busy.ID: {Highlights: 5, Comments: 5},
		idle.ID: {Highlights: 5, Comments: 5},
	}

	got, err := g.selectFeedItems(ctx, limits, 100, time.Now(), nil)
	if err != nil {
		t.Fatalf("selectFeedItems: %v", err)
	}

	// max_items is a hard cap.
	if len(got) > 100 {
		t.Fatalf("selected %d items, exceeds max_items 100", len(got))
	}
	// The idle feed's 3 items must be included (it cannot fill its share) and
	// the remaining 97 slots must go to the busy feed.
	byFeed := map[int64]int{}
	for _, it := range got {
		byFeed[it.FeedID]++
	}
	if byFeed[idle.ID] != 3 {
		t.Errorf("idle feed contributed %d items, want all 3", byFeed[idle.ID])
	}
	if byFeed[busy.ID] != 97 {
		t.Errorf("busy feed contributed %d items, want 97 (100 - 3 redistributed); "+
			"unused budget was wasted", byFeed[busy.ID])
	}
}

// TestAgeWeightGrowsAndIsCapped pins the ranking function's shape: the age
// bonus grows with waiting time, is bounded, and is enough to lift a neglected
// item of comparable worth above a fresh one — but not enough to make a
// trivial old item beat a genuinely important fresh one.
func TestAgeWeightGrowsAndIsCapped(t *testing.T) {
	now := time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)

	if w := ageWeight(now, now); w != 1 {
		t.Errorf("fresh item weight = %v, want 1", w)
	}
	if w := ageWeight(now.Add(-ageHalfLife), now); w != 2 {
		t.Errorf("one-half-life-old weight = %v, want 2", w)
	}
	if w := ageWeight(now.Add(-1000*24*time.Hour), now); w != maxAgeWeight {
		t.Errorf("ancient item weight = %v, want cap %v", w, maxAgeWeight)
	}
	// Monotonic in age.
	prev := 0.0
	for _, age := range []time.Duration{0, time.Hour, 24 * time.Hour, 72 * time.Hour, 30 * 24 * time.Hour} {
		w := ageWeight(now.Add(-age), now)
		if w < prev {
			t.Errorf("weight decreased with age at %v: %v < %v", age, w, prev)
		}
		prev = w
	}

	// Equal score: the older item wins. This is the property that drains a
	// backlog, since a stuck item's score does not change.
	oldSame := store.Item{Points: 100, CreatedAt: now.Add(-7 * 24 * time.Hour)}
	freshSame := store.Item{Points: 100, CreatedAt: now}
	if selectionPriority(oldSame, now) <= selectionPriority(freshSame, now) {
		t.Errorf("an old 100-point item should outrank a fresh 100-point item; got old=%v fresh=%v",
			selectionPriority(oldSame, now), selectionPriority(freshSame, now))
	}

	// Comparable worth: a low-scoring item that has waited long enough does
	// overtake a moderately higher-scoring fresh one. Without this the
	// production tail (55-75 pts) could never beat fresh items.
	oldLow := store.Item{Points: 55, CreatedAt: now.Add(-17 * 24 * time.Hour)}
	freshMid := store.Item{Points: 300, CreatedAt: now}
	if selectionPriority(oldLow, now) <= selectionPriority(freshMid, now) {
		t.Errorf("a 17-day-old 55-point item should outrank a fresh 300-point item; got old=%v fresh=%v",
			selectionPriority(oldLow, now), selectionPriority(freshMid, now))
	}

	// But the bonus must not invert the product: a trivial old item still
	// loses to a very important fresh one.
	trivialOld := store.Item{Points: 1, CreatedAt: now.Add(-30 * 24 * time.Hour)}
	importantFresh := store.Item{Points: 5000, CreatedAt: now}
	if selectionPriority(trivialOld, now) >= selectionPriority(importantFresh, now) {
		t.Errorf("a trivial old item must not outrank a very important fresh item; got old=%v fresh=%v",
			selectionPriority(trivialOld, now), selectionPriority(importantFresh, now))
	}

	// A zero-score (RSS) item must stay selectable.
	zero := store.Item{Points: 0, CreatedAt: now}
	if selectionPriority(zero, now) <= 0 {
		t.Errorf("zero-score item priority = %v, want > 0", selectionPriority(zero, now))
	}
}

// TestBacklogTailDrainsWithProductionShape reproduces the exact production
// score/age shape that was starved: a 17-day-old tail at 55-75 points against
// fresh items at 100-3723 points. Points-only ordering never selected the tail;
// with age weighting it must be consumed within a few runs.
func TestBacklogTailDrainsWithProductionShape(t *testing.T) {
	db, queries, cleanup := setupDigestTest(t)
	defer cleanup()
	ctx := context.Background()

	user, err := queries.CreateUser(ctx, store.CreateUserParams{
		Email: "prodshape@test.com", PasswordHash: "hash", ApiKey: "prodshape-key",
	})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	hnFeed, err := queries.CreateFeed(ctx, store.CreateFeedParams{
		UserID: user.ID, Category: "Tech", Type: "hn",
		Name: "HN", Url: "https://example.com/hn", Config: "{}", Status: "active",
	})
	if err != nil {
		t.Fatalf("create hn feed: %v", err)
	}

	now := time.Now()

	// The stuck tail: 57 items, 55-75 points, spread over 2-17 days old.
	var tailIDs []int64
	for i := 0; i < 57; i++ {
		it, err := queries.CreateItem(ctx, store.CreateItemParams{
			FeedID: hnFeed.ID, Hash: "tail-" + strconv.Itoa(i),
			Title: "Tail " + strconv.Itoa(i), Url: "https://example.com/t/" + strconv.Itoa(i),
			Points: int64(55 + i%21), Status: "unread",
		})
		if err != nil {
			t.Fatalf("create tail item %d: %v", i, err)
		}
		daysOld := 2 + i%16
		if _, err := db.ExecContext(ctx, `UPDATE items SET created_at = ? WHERE id = ?`,
			now.Add(-time.Duration(daysOld)*24*time.Hour).Format("2006-01-02 15:04:05"), it.ID); err != nil {
			t.Fatalf("backdate tail %d: %v", i, err)
		}
		tailIDs = append(tailIDs, it.ID)
	}

	// Fresh competition: 50 items today at 100-3723 points, like a real HN day.
	for i := 0; i < 50; i++ {
		if _, err := queries.CreateItem(ctx, store.CreateItemParams{
			FeedID: hnFeed.ID, Hash: "fresh-" + strconv.Itoa(i),
			Title: "Fresh " + strconv.Itoa(i), Url: "https://example.com/f/" + strconv.Itoa(i),
			Points: int64(100 + i*70), Status: "unread",
		}); err != nil {
			t.Fatalf("create fresh item %d: %v", i, err)
		}
	}

	g := &Generator{db: db, queries: queries}
	limits := map[int64]feedLimit{hnFeed.ID: {Highlights: 5, Comments: 10}}

	// Run the selection repeatedly (as successive daily digests would) and
	// check the tail is progressively consumed. Selection alone is exercised
	// here; the items are flipped to digested to simulate persistence.
	for run := 0; run < 4; run++ {
		got, err := g.selectFeedItems(ctx, limits, 100, now, nil)
		if err != nil {
			t.Fatalf("run %d: selectFeedItems: %v", run, err)
		}
		for _, it := range got {
			if _, err := db.ExecContext(ctx, `UPDATE items SET status='digested' WHERE id=?`, it.ID); err != nil {
				t.Fatalf("mark digested: %v", err)
			}
		}
	}

	stillStuck := 0
	for _, id := range tailIDs {
		it, err := queries.GetItem(ctx, id)
		if err != nil {
			t.Fatalf("get tail item %d: %v", id, err)
		}
		if it.Status == "unread" {
			stillStuck++
		}
	}
	if stillStuck > 0 {
		t.Fatalf("%d of 57 production-shaped tail items still unread after 4 runs; "+
			"age weighting is not rescuing the backlog", stillStuck)
	}
}

// staticEnricher leaves points untouched so selection order is deterministic.
type staticEnricher struct{}

func (s *staticEnricher) Enrich(_ context.Context, _ *store.Item) error { return nil }

func (s *staticEnricher) BulkEnrich(_ context.Context, _ []*store.Item) error { return nil }
