import { describe, it, expect } from 'vitest'
import {
  SCORE_FEED_TYPES, feedUrlFor,
  isValidEmailSuffix, emailAddressFor, emailPrefixOf, emailDomainOf, emailSuffixOf, emailNameFor,
} from './feeds'

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

  it('builds a subreddit URL, tolerating an .rss suffix', () => {
    expect(feedUrlFor('reddit', 'golang')).toBe('https://www.reddit.com/r/golang')
    expect(feedUrlFor('reddit', 'golang.rss')).toBe('https://www.reddit.com/r/golang')
  })

  it('passes other URLs through unchanged', () => {
    expect(feedUrlFor('rss', 'https://example.com/feed.xml')).toBe('https://example.com/feed.xml')
  })
})

describe('email feed address helpers', () => {
  const inbox = 'name@agentmail.to'

  it('composes the full address from the instance inbox and a suffix', () => {
    expect(emailAddressFor(inbox, 'sport')).toBe('name+sport@agentmail.to')
  })

  it('normalises the suffix so casing cannot create two feeds', () => {
    expect(emailAddressFor(inbox, 'Sport')).toBe('name+sport@agentmail.to')
    expect(emailAddressFor(inbox, '  SPORT  ')).toBe('name+sport@agentmail.to')
  })

  it('splits the address into the fixed prefix and domain', () => {
    expect(emailPrefixOf(inbox)).toBe('name+')
    expect(emailDomainOf(inbox)).toBe('@agentmail.to')
  })

  it('returns empty context rather than a partial string for a malformed inbox', () => {
    expect(emailPrefixOf('no-domain')).toBe('')
    expect(emailDomainOf('no-domain')).toBe('')
    expect(emailAddressFor('no-domain', 'sport')).toBe('sport')
  })

  it('round-trips a stored address back to its editable suffix', () => {
    const url = emailAddressFor(inbox, 'ai-weekly')
    expect(url).toBe('name+ai-weekly@agentmail.to')
    expect(emailSuffixOf(url)).toBe('ai-weekly')
  })

  it('yields an empty suffix for an address without a plus', () => {
    expect(emailSuffixOf('name@agentmail.to')).toBe('')
    expect(emailSuffixOf('not-an-address')).toBe('')
  })

  it('derives a display name from the suffix, matching the server', () => {
    expect(emailNameFor('sport')).toBe('Sport')
    expect(emailNameFor('ai-weekly')).toBe('Ai Weekly')
    expect(emailNameFor('tech.news')).toBe('Tech News')
  })

  describe('isValidEmailSuffix', () => {
    it('accepts the shapes a feed suffix may take', () => {
      for (const ok of ['sport', 'ai-weekly2', 'tech.news', 'my_feed', 'a', '9to5']) {
        expect(isValidEmailSuffix(ok), ok).toBe(true)
      }
    })

    it('rejects spaces, separators at the edges, and address metacharacters', () => {
      for (const bad of ['sp ort', 'sport+extra', 'a@b', 'a/b', 'a?b', 'a#b', '.sport', 'sport.', '-sport', 'sport-', '']) {
        expect(isValidEmailSuffix(bad), bad).toBe(false)
      }
    })

    it('accepts a 64-character suffix and rejects a longer one', () => {
      expect(isValidEmailSuffix('a'.repeat(64))).toBe(true)
      expect(isValidEmailSuffix('a'.repeat(65))).toBe(false)
    })

    it('is case-insensitive', () => {
      expect(isValidEmailSuffix('SPORT')).toBe(true)
      expect(isValidEmailSuffix('  Sport  ')).toBe(true)
    })
  })
})
