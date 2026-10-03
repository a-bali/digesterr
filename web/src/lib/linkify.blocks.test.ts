import { describe, it, expect } from 'vitest'
import { renderCommentHtml } from './linkify'

/**
 * Block-level markdown in comment text. These mirror `internal/linkify`'s
 * blocks_test.go: the two implementations are deliberate twins, so a construct
 * handled in one and not the other is a defect.
 */
describe('renderCommentHtml — blockquotes', () => {
  it('renders an entity-encoded blockquote (the stored form)', () => {
    const got = renderCommentHtml('&gt; quoted text')
    expect(got).toContain('<blockquote>')
    expect(got).toContain('quoted text')
    expect(got).toContain('</blockquote>')
    expect(got).not.toContain('&gt;')
  })

  it('renders a literal blockquote marker', () => {
    const got = renderCommentHtml('> quoted text')
    expect(got).toContain('<blockquote>quoted text</blockquote>')
  })

  it('keeps a multi-line quote in one blockquote', () => {
    const got = renderCommentHtml('> line one\n> line two')
    expect(got).toContain('<blockquote>')
    expect(got).toContain('line one')
    expect(got).toContain('line two')
  })

  it('still linkifies a URL inside a quote', () => {
    const got = renderCommentHtml('> see https://example.com/a')
    expect(got).toContain('<blockquote>')
    expect(got).toContain('<a href="https://example.com/a"')
  })

  it('renders a quoted reply followed by the author paragraph', () => {
    const got = renderCommentHtml('&gt; it is free.\n\nCheaper than humans.')
    expect(got).toContain('<blockquote>it is free.</blockquote>')
    expect(got).toContain('<p>Cheaper than humans.</p>')
  })
})

describe('renderCommentHtml — lists', () => {
  it('renders a bullet list', () => {
    const got = renderCommentHtml('- first\n- second')
    expect(got).toContain('<ul>')
    expect(got).toContain('<li>first</li>')
    expect(got).toContain('<li>second</li>')
    expect(got).not.toContain('- first<br>')
  })

  it('renders an ordered list', () => {
    const got = renderCommentHtml('1. first\n2. second')
    expect(got).toContain('<ol>')
    expect(got).toContain('<li>first</li>')
  })

  it('nests a child entry inside its parent item', () => {
    expect(renderCommentHtml('- a\n  - b\n- c')).toBe(
      '<ul><li>a<ul><li>b</li></ul></li><li>c</li></ul>',
    )
  })

  it('renders a markdown link inside a list item', () => {
    const got = renderCommentHtml('- [label](https://example.com/a)')
    expect(got).toContain('<li><a href="https://example.com/a"')
    expect(got).toContain('>label</a></li>')
  })
})

describe('renderCommentHtml — code fences', () => {
  it('keeps markdown markers literal inside a fence', () => {
    const got = renderCommentHtml('```\n**not bold**\n```')
    expect(got).toContain('<pre>')
    expect(got).toContain('**not bold**')
    expect(got).not.toContain('<strong>')
  })

  it('escapes HTML inside a fence rather than dropping it', () => {
    const got = renderCommentHtml('```\n<script>alert(1)</script>\n```')
    expect(got).toContain('&lt;script&gt;')
    expect(got).not.toContain('<script>')
  })
})

describe('renderCommentHtml — paragraph shape', () => {
  it('wraps a plain comment in a single paragraph', () => {
    expect(renderCommentHtml('just a normal comment')).toBe('<p>just a normal comment</p>')
  })

  it('separates paragraphs on a blank line', () => {
    const got = renderCommentHtml('first para\n\nsecond para')
    expect(got).toContain('<p>first para</p>')
    expect(got).toContain('<p>second para</p>')
  })

  it('keeps a numbered sentence as prose, not a list', () => {
    const got = renderCommentHtml('Notes follow.\n2024. We shipped it.')
    expect(got).toContain('2024. We shipped it.')
    expect(got).not.toContain('<li>We shipped')
  })

  it('a bullet list interrupts a paragraph', () => {
    const got = renderCommentHtml('Reasons:\n- first\n- second')
    expect(got).toContain('<p>Reasons:</p>')
    expect(got).toContain('<ul>')
  })

  it('returns empty for empty input', () => {
    expect(renderCommentHtml('')).toBe('')
  })

  it('leaves no placeholder behind', () => {
    const got = renderCommentHtml('&gt; **bold** and https://example.com/a')
    expect(got.includes('\x00')).toBe(false)
  })
})
