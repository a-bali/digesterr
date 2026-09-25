package feed

import (
	"context"
	"testing"

	"digesterr/internal/store"
)

// stubFetcher returns a fixed item list, so a poll can be driven without a
// network fetch.
type stubFetcher struct {
	items []Item
}

func (s *stubFetcher) Fetch(_ context.Context, _ string) ([]Item, error) {
	return s.items, nil
}

// CreateItemIfNotExists is INSERT OR IGNORE ... RETURNING id, and SQLite returns
// ZERO ROWS for an ignored insert (the RETURNING clause yields an empty result
// set, so the Go scan surfaces sql.ErrNoRows — not id 0). The poller's twitter
// title refresh sat on an `else if` branch keyed on id == 0, which is
// unreachable: a re-poll of an existing tweet hit the ErrNoRows path and
// `continue`d before the refresh.
//
// Consequence: a tweet's title carries volatile state (a 📌 pinned badge, RT
// provenance) and the item hash deliberately excludes the title, so a
// re-polled tweet whose title changed upstream never got updated.
func TestRepolledTweetRefreshesVolatileTitle(t *testing.T) {
	queries := setupFeedLoggingDB(t)
	user := createFeedLoggingUser(t, queries)

	feedObj, err := queries.CreateFeed(t.Context(), store.CreateFeedParams{
		UserID: user.ID,
		Type:   "twitter",
		Name:   "Twitter Feed",
		Url:    "https://x.com/exampleuser",
		Config: "{}",
		Status: "active",
	})
	if err != nil {
		t.Fatalf("create feed: %v", err)
	}

	const guid = "twitter:555"
	// The tweet is stored as first seen — not pinned.
	stored, err := queries.CreateItem(t.Context(), store.CreateItemParams{
		FeedID: feedObj.ID,
		Hash:   itemHash(guid, "", "", ""),
		Title:  "hello world",
		Url:    "https://x.com/exampleuser/status/555",
		Status: "unread",
	})
	if err != nil {
		t.Fatalf("create stored tweet: %v", err)
	}

	// Upstream now reports the tweet as pinned. The hash is GUID-anchored and
	// excludes the title, so this is the SAME item — the insert is ignored.
	const pinnedTitle = "📌 hello world"
	poller := NewPoller(queries)
	poller.fetchers["twitter"] = &stubFetcher{items: []Item{{
		GUID:  guid,
		Title: pinnedTitle,
		URL:   "https://x.com/exampleuser/status/555",
	}}}

	if err := poller.PollFeed(context.Background(), feedObj); err != nil {
		t.Fatalf("PollFeed failed: %v", err)
	}

	after, err := queries.GetItem(t.Context(), stored.ID)
	if err != nil {
		t.Fatalf("get item: %v", err)
	}
	if after.Title != pinnedTitle {
		t.Errorf("stored title = %q, want %q: re-polling an existing tweet must refresh its volatile title",
			after.Title, pinnedTitle)
	}
	// The refresh must not have created a second row for the same tweet.
	items, err := queries.ListItemsByFeed(t.Context(), feedObj.ID)
	if err != nil {
		t.Fatalf("list items: %v", err)
	}
	if len(items) != 1 {
		t.Errorf("items in feed = %d, want 1: a re-poll must not duplicate the tweet", len(items))
	}
}

// The refresh must be feed-scoped: another feed holding the same GUID is a
// different copy and must keep its own title.
func TestRepolledTweetTitleRefreshIsScopedToTheFeed(t *testing.T) {
	queries := setupFeedLoggingDB(t)
	user := createFeedLoggingUser(t, queries)

	mkFeed := func(name, url string) store.Feed {
		f, err := queries.CreateFeed(t.Context(), store.CreateFeedParams{
			UserID: user.ID, Type: "twitter", Name: name, Url: url,
			Config: "{}", Status: "active",
		})
		if err != nil {
			t.Fatalf("create feed %s: %v", name, err)
		}
		return f
	}
	feedA := mkFeed("A", "https://x.com/usera")
	feedB := mkFeed("B", "https://x.com/userb")

	const guid = "twitter:777"
	mkItem := func(f store.Feed, title string) store.Item {
		it, err := queries.CreateItem(t.Context(), store.CreateItemParams{
			FeedID: f.ID, Hash: itemHash(guid, "", "", ""), Title: title,
			Url: "https://x.com/x/status/777", Status: "unread",
		})
		if err != nil {
			t.Fatalf("create item: %v", err)
		}
		return it
	}
	itemA := mkItem(feedA, "a-title")
	itemB := mkItem(feedB, "b-title")

	poller := NewPoller(queries)
	poller.fetchers["twitter"] = &stubFetcher{items: []Item{{
		GUID:  guid,
		Title: "a-updated",
		URL:   "https://x.com/x/status/777",
	}}}

	if err := poller.PollFeed(context.Background(), feedA); err != nil {
		t.Fatalf("PollFeed failed: %v", err)
	}

	gotA, err := queries.GetItem(t.Context(), itemA.ID)
	if err != nil {
		t.Fatalf("get item A: %v", err)
	}
	if gotA.Title != "a-updated" {
		t.Errorf("feed A title = %q, want %q", gotA.Title, "a-updated")
	}
	gotB, err := queries.GetItem(t.Context(), itemB.ID)
	if err != nil {
		t.Fatalf("get item B: %v", err)
	}
	if gotB.Title != "b-title" {
		t.Errorf("feed B title = %q, want %q: the refresh must not reach another feed's copy",
			gotB.Title, "b-title")
	}
}
