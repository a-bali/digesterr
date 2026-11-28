package feed

import (
	"context"
	"fmt"
)

// DiscoverFeedName resolves the name a feed gives itself, so the UI can
// prefill the name field when a user adds a feed from a URL alone. Each feed
// type keeps its name somewhere different:
//
//	rss / atom / hn / lobsters -> the feed's own <title>. hn and lobsters have
//	  no fetcher of their own, so all four are parsed by the RSS fetcher.
//	reddit                     -> the subreddit's display name ("r/golang").
//	twitter                    -> the account's profile display name.
//
// A zero name with a nil error means the source declared no name.
func (p *Poller) DiscoverFeedName(ctx context.Context, feedType, feedURL string) (string, error) {
	switch feedType {
	case "rss", "atom", "hn", "lobsters":
		f, ok := p.fetchers["rss"].(*RSSFetcher)
		if !ok {
			return "", fmt.Errorf("rss fetcher unavailable")
		}
		return f.FeedTitle(ctx, feedURL)
	case "reddit":
		f, ok := p.fetchers["reddit"].(*RedditFetcher)
		if !ok {
			return "", fmt.Errorf("reddit fetcher unavailable")
		}
		return f.SubredditName(ctx, feedURL)
	case "twitter":
		f, ok := p.fetchers["twitter"].(*TwitterFetcher)
		if !ok {
			return "", fmt.Errorf("twitter fetcher unavailable")
		}
		return f.UserDisplayName(ctx, feedURL)
	default:
		return "", fmt.Errorf("unsupported feed type %q", feedType)
	}
}

// DiscoverFeedSiteURL resolves the site a feed describes, so an added feed can
// store it immediately instead of waiting for its first poll. It returns "" for
// types whose payload is not a feed document (and for a feed that declares no
// <link>), which callers treat as "unknown", never as an error.
func (p *Poller) DiscoverFeedSiteURL(ctx context.Context, feedType, feedURL string) (string, error) {
	switch feedType {
	case "rss", "atom", "hn", "lobsters":
		f, ok := p.fetchers["rss"].(*RSSFetcher)
		if !ok {
			return "", fmt.Errorf("rss fetcher unavailable")
		}
		return f.FeedSiteURL(ctx, feedURL)
	default:
		return "", nil
	}
}
