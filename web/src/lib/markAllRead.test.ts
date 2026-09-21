import { describe, it, expect } from 'vitest'
import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import {
  scopeLabel, scopeActionLabel, scopeEmptyLabel, scopeCount, scopeUnit,
  markRelativeLabel, markRelativeShortLabel,
  MARK_SUCCESS_TITLE, MARK_FAILURE_TITLE, MARK_EMPTY_TITLE, MARK_DIALOG_TITLE,
  type MarkAllScope,
} from './markAllRead'

// Every scope the feature can act on, so a newly added scope cannot silently skip
// the vocabulary tests below.
const ALL_SCOPES: MarkAllScope[] = [
  { kind: 'feed', id: 1, name: 'Hacker News' },
  { kind: 'category', name: 'Tech' },
  { kind: 'feeds-section' },
  { kind: 'digest-config', id: 7, name: 'Morning Brief' },
  { kind: 'all-digests' },
]

describe('mark-all-read vocabulary', () => {
  it('describes every scope (no accidental fall-through)', () => {
    for (const scope of ALL_SCOPES) {
      expect(scopeLabel(scope), `label for ${scope.kind}`).toBeTruthy()
      expect(scopeActionLabel(scope), `action for ${scope.kind}`).toBeTruthy()
      expect(scopeEmptyLabel(scope), `empty for ${scope.kind}`).toBeTruthy()
      expect(scopeCount(scope, 3), `count for ${scope.kind}`).toMatch(/^3 /)
    }
  })

  // The whole point: the control a user hovers, the confirmation they accept and
  // the toast they get afterwards must name the same thing.
  it('uses the same scope noun in the tooltip and the toast', () => {
    for (const scope of ALL_SCOPES) {
      const noun = scopeLabel(scope)
      expect(scopeCount(scope, 2)).toBeTruthy()
      // The empty-state message embeds the same noun, so the pair cannot drift.
      expect(scopeEmptyLabel(scope)).toContain(noun)
      // A named scope also appears verbatim in its action label.
      if (scope.kind === 'feed' || scope.kind === 'category' || scope.kind === 'digest-config') {
        expect(scopeActionLabel(scope)).toContain(scope.name)
      }
    }
  })

  it('phrases labels as parallel imperatives, not mixed sentence styles', () => {
    // "Mark X as read" / "Nothing unread in X" — no "Mark read" vs "Marked as read"
    // style drift between the enabled and disabled states of one control.
    for (const scope of ALL_SCOPES) {
      expect(scopeActionLabel(scope).startsWith('Mark ')).toBe(true)
      expect(scopeEmptyLabel(scope).startsWith('Nothing unread in ')).toBe(true)
    }
  })

  it('names the entity for named scopes and nothing else for the aggregate ones', () => {
    expect(scopeLabel({ kind: 'feed', id: 1, name: 'Hacker News' })).toBe('Hacker News')
    expect(scopeLabel({ kind: 'category', name: 'Tech' })).toBe('category “Tech”')
    expect(scopeLabel({ kind: 'digest-config', id: 7, name: 'Morning Brief' })).toBe('Morning Brief')
    // A config with no name yet must still read as a phrase, never as an empty string.
    expect(scopeLabel({ kind: 'digest-config', id: 7, name: '' })).toBe('this digest')
    expect(scopeLabel({ kind: 'feeds-section' })).toBe('all feeds')
    expect(scopeLabel({ kind: 'all-digests' })).toBe('all digests')
  })

  // Feeds/categories mark ITEMS; digest scopes mark RESULTS. A count phrase that
  // says "3 items" for a digest is a factual error, not just a wording choice.
  it('counts items for feed scopes and results for digest scopes', () => {
    expect(scopeUnit({ kind: 'feed', id: 1, name: 'F' })).toBe('item')
    expect(scopeUnit({ kind: 'category', name: 'C' })).toBe('item')
    expect(scopeUnit({ kind: 'feeds-section' })).toBe('item')
    expect(scopeUnit({ kind: 'digest-config', id: 1, name: 'D' })).toBe('result')
    expect(scopeUnit({ kind: 'all-digests' })).toBe('result')
  })

  it('pluralises correctly, including one and an unknown count', () => {
    const feed = { kind: 'feed', id: 1, name: 'F' } as const
    expect(scopeCount(feed, 1)).toBe('1 item')
    expect(scopeCount(feed, 0)).toBe('0 items')
    expect(scopeCount(feed, 12)).toBe('12 items')
    // null means "count not loaded yet", which must still read as a count phrase.
    expect(scopeCount(feed, null)).toBe('0 items')
    expect(scopeCount({ kind: 'all-digests' }, 1)).toBe('1 result')
  })

  it('uses ONE phrasing for the relative mark, with a fitted short form', () => {
    for (const dir of ['above', 'below'] as const) {
      expect(markRelativeLabel(dir)).toBe(`Mark everything ${dir} as read, except this item`)
      expect(markRelativeShortLabel(dir)).toBe(`Mark ${dir} read`)
    }
  })

  it('exposes exactly one title per outcome', () => {
    expect(MARK_SUCCESS_TITLE).toBe('Marked as read')
    expect(MARK_FAILURE_TITLE).toBe('Failed to mark as read')
    expect(MARK_EMPTY_TITLE).toBe('Nothing to mark as read')
    expect(MARK_DIALOG_TITLE).toBe('Mark all as read?')
  })
})

// The vocabulary only works if the components actually USE it. These checks fail if
// a surface goes back to a hand-written string, which is exactly how the four
// phrasings for one action appeared in the first place.
describe('mark-all-read surfaces derive their wording', () => {
  const read = (rel: string) => readFileSync(resolve(process.cwd(), 'src', rel), 'utf8')

  it('has no hand-written mark-read strings left in the sidebar', () => {
    const src = read('components/FeedSidebar.vue')
    expect(src).toMatch(/scopeActionLabel\(/)
    expect(src).toMatch(/scopeEmptyLabel\(/)
    // A bare literal tooltip is the drift we are preventing.
    expect(src).not.toMatch(/title="Mark \w+ as read"/)
    expect(src).not.toMatch(/title="Nothing unread/)
  })

  it('has no hand-written relative-mark strings left in either list', () => {
    for (const file of ['components/ItemList.vue', 'components/DigestResultList.vue']) {
      const src = read(file)
      expect(src, file).toMatch(/markRelativeLabel\(/)
      expect(src, file).not.toMatch(/Mark everything (above|below) as read/)
      expect(src, file).not.toMatch(/Mark all (items )?(above|below) as read/)
      expect(src, file).not.toMatch(/Mark all read (above|below)/)
    }
  })

  it('routes the per-feed row through the shared vocabulary', () => {
    const src = read('components/FeedRow.vue')
    expect(src).toMatch(/scopeActionLabel\(/)
    expect(src).not.toMatch(/title="Mark read"/)
  })

  it('uses the shared titles in both stores', () => {
    for (const file of ['stores/feed.ts', 'stores/digest.ts']) {
      const src = read(file)
      expect(src, file).toMatch(/MARK_SUCCESS_TITLE/)
      expect(src, file).toMatch(/MARK_FAILURE_TITLE/)
      // The old divergent titles must not come back.
      expect(src, file).not.toMatch(/'Feed marked read'/)
      expect(src, file).not.toMatch(/'Digest marked read'/)
      expect(src, file).not.toMatch(/'All digests marked read'/)
      expect(src, file).not.toMatch(/'Marked above as read'/)
      expect(src, file).not.toMatch(/'Marked below as read'/)
    }
  })

  it('builds the confirmation copy from the scope, in one place', () => {
    const src = read('views/HomeView.vue')
    expect(src).toMatch(/MARK_DIALOG_TITLE/)
    expect(src).toMatch(/scopeCount\(/)
    expect(src).toMatch(/scopeLabel\(/)
    // One builder, not one per entry point.
    expect(src.match(/function targetForScope\(/g)?.length).toBe(1)
    expect(src).not.toMatch(/function resolveMarkAllTarget/)
  })
})
