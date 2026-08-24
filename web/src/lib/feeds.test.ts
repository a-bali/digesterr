import { describe, it, expect } from 'vitest'
import { SCORE_FEED_TYPES, feedUrlFor } from './feeds'

describe('SCORE_FEED_TYPES', () => {
  it('contains the score-capable feed types', () => {
    expect([...SCORE_FEED_TYPES].sort()).toEqual(['hn', 'lobsters', 'reddit', 'twitter'])
    expect(SCORE_FEED_TYPES.has('rss')).toBe(false)
  })
})

describe('feedUrlFor', () => {
  it('builds the fixed HN and Lobsters URLs', () => {
    expect(feedUrlFor('hn', '')).toBe('https://news.ycombinator.com/rss')
    expect(feedUrlFor('lobsters', '')).toBe('https://lobste.rs/rss')
  })

  it('builds a subreddit RSS URL, tolerating an .rss suffix', () => {
    expect(feedUrlFor('reddit', 'golang')).toBe('https://www.reddit.com/r/golang.rss')
    expect(feedUrlFor('reddit', 'golang.rss')).toBe('https://www.reddit.com/r/golang.rss')
  })

  it('passes other URLs through unchanged', () => {
    expect(feedUrlFor('rss', 'https://example.com/feed.xml')).toBe('https://example.com/feed.xml')
  })
})
