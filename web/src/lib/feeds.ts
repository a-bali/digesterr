// Feed-type constants shared between the feed store and feed dialogs/sidebar.

// Feed types whose items carry a score (points) that can be filtered/sorted on.
export const SCORE_FEED_TYPES: ReadonlySet<string> = new Set(['hn', 'lobsters', 'reddit'])

// Resolve the final subscription URL for a feed type. `value` is the raw form
// value (full URL for rss/atom, subreddit name for reddit, ignored for hn /
// lobsters which have fixed URLs).
export function feedUrlFor(type: string, value: string): string {
  if (type === 'reddit') return 'https://www.reddit.com/r/' + value.replace(/\.rss$/, '') + '.rss'
  if (type === 'hn') return 'https://news.ycombinator.com/rss'
  if (type === 'lobsters') return 'https://lobste.rs/rss'
  return value
}
