import { describe, it, expect, vi, afterEach } from 'vitest'
import { parseDate, relativeTime, absoluteTime, feedIcon } from './format'

afterEach(() => {
  vi.useRealTimers()
})

describe('parseDate', () => {
  it('parses ISO strings', () => {
    const d = parseDate('2026-01-15T10:00:00Z')
    expect(d).toBeInstanceOf(Date)
    expect(d!.getUTCFullYear()).toBe(2026)
  })

  it('unwraps Go time.Time object shape', () => {
    const d = parseDate({ Time: '2026-01-15T10:00:00Z', Valid: true })
    expect(d!.getUTCFullYear()).toBe(2026)
  })

  it('returns null for invalid/null', () => {
    expect(parseDate(null)).toBeNull()
    expect(parseDate(undefined)).toBeNull()
    expect(parseDate({ Valid: false })).toBeNull()
    expect(parseDate('not-a-date')).toBeNull()
  })
})

describe('relativeTime', () => {
  it('returns now for < 60s', () => {
    vi.useFakeTimers()
    vi.setSystemTime(new Date('2026-01-15T10:00:00Z'))
    expect(relativeTime('2026-01-15T09:59:30Z')).toBe('now')
  })

  it('formats minutes/hours/days', () => {
    vi.useFakeTimers()
    vi.setSystemTime(new Date('2026-01-15T10:00:00Z'))
    expect(relativeTime('2026-01-15T09:00:00Z')).toBe('1h')
    expect(relativeTime('2026-01-14T10:00:00Z')).toBe('1d')
    expect(relativeTime('2026-01-15T09:58:00Z')).toBe('2m')
  })

  it('returns empty for invalid', () => {
    expect(relativeTime(null)).toBe('')
  })
})

describe('absoluteTime', () => {
  it('formats full date', () => {
    const s = absoluteTime('2026-01-15T10:00:00Z')
    expect(s).toContain('2026')
    expect(s).toContain('Jan')
  })

  it('returns empty for invalid', () => {
    expect(absoluteTime('')).toBe('')
  })
})

describe('feedIcon', () => {
  it('maps hn and lobsters', () => {
    expect(feedIcon({ type: 'hn', url: 'x' })).toContain('ycombinator')
    expect(feedIcon({ type: 'lobsters', url: 'x' })).toContain('lobste.rs')
  })

  it('derives domain favicon', () => {
    expect(feedIcon({ type: 'rss', url: 'https://example.com/feed' })).toContain('example.com')
  })

  it('returns empty for bad url', () => {
    expect(feedIcon({ type: 'rss', url: 'not a url' })).toBe('')
  })
})
