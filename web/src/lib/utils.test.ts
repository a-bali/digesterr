import { describe, it, expect } from 'vitest'
import { cn, stripContent } from './utils'

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
