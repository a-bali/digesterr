import { describe, it, expect, vi } from 'vitest'
import { cn, stripContent, decodeHTMLEntities } from './utils'

describe('cn', () => {
  it('merges class names', () => {
    expect(cn('px-4', 'py-2')).toBe('px-4 py-2')
  })

  it('handles conditional classes', () => {
    expect(cn('base', false && 'hidden', 'visible')).toBe('base visible')
  })

  it('resolves Tailwind conflicts (last wins)', () => {
    expect(cn('px-4', 'px-2')).toBe('px-2')
  })

  it('accepts no arguments', () => {
    expect(cn()).toBe('')
  })

  it('handles undefined and null', () => {
    expect(cn('a', undefined, 'b', null)).toBe('a b')
  })

  it('handles arrays', () => {
    expect(cn(['a', 'b'], 'c')).toBe('a b c')
  })
})

describe('stripContent', () => {
  it('returns empty string for empty input', () => {
    expect(stripContent('')).toBe('')
  })

  it('strips HTML tags', () => {
    expect(stripContent('<p>hello</p>')).toBe('hello')
  })

  it('decodes &amp;', () => {
    expect(stripContent('foo &amp; bar')).toBe('foo & bar')
  })

  it('decodes &lt; and &gt;', () => {
    expect(stripContent('a &lt; b &gt; c')).toBe('a < b > c')
  })

  it('decodes &quot;', () => {
    expect(stripContent('say &quot;hi&quot;')).toBe('say "hi"')
  })

  it('decodes numeric entities like &#39;', () => {
    expect(stripContent('it&#39;s nice')).toBe("it's nice")
  })

  it('decodes &#x27; (hex entity)', () => {
    expect(stripContent('it&#x27;s nice')).toBe("it's nice")
  })

  it('decodes &nbsp; and &#32;', () => {
    expect(stripContent('a&nbsp;b&#32;c')).toBe('a b c')
  })

  it('collapses whitespace', () => {
    expect(stripContent('hello    world')).toBe('hello world')
  })

  it('trims surrounding whitespace', () => {
    expect(stripContent('  hello  ')).toBe('hello')
  })
})

describe('decodeHTMLEntities', () => {
  it('decodes named and numeric entities', () => {
    expect(decodeHTMLEntities('Tom &amp; Jerry')).toBe('Tom & Jerry')
    expect(decodeHTMLEntities('&lt;b&gt;bold&lt;/b&gt;')).toBe('<b>bold</b>')
    expect(decodeHTMLEntities('caf&#233;')).toBe('café')
  })

  it('returns an empty string for empty input', () => {
    expect(decodeHTMLEntities('')).toBe('')
  })

  it('does not repeat the DOM round-trip for a title it has already decoded', () => {
    // The list renders one row per item on every scroll, so the decode must not
    // re-run createElement + innerHTML parse for the same title.
    const createElement = vi.spyOn(document, 'createElement')
    const title = 'cached &amp; unique title'

    const first = decodeHTMLEntities(title)
    const callsAfterFirst = createElement.mock.calls.length
    const second = decodeHTMLEntities(title)

    expect(first).toBe('cached & unique title')
    expect(second).toBe(first)
    expect(createElement.mock.calls.length, 'a cached title must not hit the DOM again')
      .toBe(callsAfterFirst)

    createElement.mockRestore()
  })
})
