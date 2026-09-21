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

  // The excerpt is the only text a list row shows, so markup must never reach it.
  // Regression: a WordPress-exported body opens with a YouTube <iframe> whose
  // fallback children are editor-bookmark <span>s. Iframe/script/style hold their
  // content as RAW TEXT (no child elements to walk), so reading the concatenated
  // text of every node surfaced those spans as literal text in the row instead of
  // the article's opening words. Seen on 9 of 10 items in one production feed.
  it('drops the fallback content of a video iframe (prod shape)', () => {
    const body =
      '<p><iframe src="https://www.youtube.com/embed/X">' +
      '<span data-mce-type="bookmark" class="mce_SELRES_start">&#65279;</span>' +
      '</iframe></p><p>The Fenix 9&#8217;s arrival brings changes</p>'
    const out = stripContent(body)
    expect(out).toBe('The Fenix 9\u2019s arrival brings changes')
    expect(out).not.toContain('<')
    expect(out).not.toContain('mce_SELRES')
  })

  it('drops script and style contents', () => {
    expect(stripContent('<p>hi</p><script>var a = 1;</script><style>.a{color:red}</style>')).toBe('hi')
  })

  // textContent inserts no separator, so adjacent blocks ran together. This is the
  // common case, not an edge case: it affects any body with block markup.
  it('separates words across block boundaries instead of gluing them', () => {
    expect(stripContent('<p>one</p><p>two</p>')).toBe('one two')
    expect(stripContent('<ul><li>alpha</li><li>beta</li></ul>')).toBe('alpha beta')
  })

  it('separates words across a <br>', () => {
    expect(stripContent('<p>line one<br>line two</p>')).toBe('line one line two')
  })

  it('separates a caption from the body that follows it', () => {
    expect(stripContent('<div class="wp-caption"><p>Caption here</p></div><p>body</p>')).toBe('Caption here body')
  })

  it('still joins inline elements that belong to one word run', () => {
    expect(stripContent('<p>The <b>bold</b> and <i>ital</i> words</p>')).toBe('The bold and ital words')
    expect(stripContent('<p>one<span>two</span></p>')).toBe('onetwo')
  })

  it('skips content hidden with an inline style', () => {
    expect(stripContent('<p>a</p><p style="display:none">gone</p><p>b</p>')).toBe('a b')
    expect(stripContent('<p>a</p><p style="visibility:hidden">gone</p><p>b</p>')).toBe('a b')
    expect(stripContent('<p>a</p><div style="display:none"><p>deep</p><span>more</span></div><p>b</p>')).toBe('a b')
  })

  // Both engines render these as text, and the drop list is tag-name based, so the
  // excerpt must keep them. They also prove the excerpt never contains markup.
  it('keeps literal angle-bracket prose from code samples', () => {
    expect(stripContent('<p>Run <code>docker run --user &lt;host-uid&gt;:&lt;host-gid&gt;</code> instead</p>'))
      .toBe('Run docker run --user <host-uid>:<host-gid> instead')
  })

  // A DROPPED element's markup must never reach the excerpt — the invariant the row
  // depends on. (Literal `<version>` from &lt;version&gt; prose IS text the browser
  // renders, so the assertion is about the source's own markup, not any `<`.)
  it('never leaks the source markup of a dropped element', () => {
    const samples = [
      '<p>hi</p><script>var a = 1;</script>',
      '<p>hi</p><style>.a{color:red}</style>',
      '<p>a</p><iframe src="x"><span class="mce_SELRES_start">fallback</span></iframe><p>b</p>',
      '<p>a</p><noscript><span>fallback</span></noscript><p>b</p>',
      '<p>a</p><p style="display:none">hidden</p><p>b</p>',
    ]
    for (const s of samples) {
      const out = stripContent(s)
      expect(out).not.toMatch(/<script|<style|<iframe|<noscript|<span|style=/)
      expect(out).not.toContain('&lt;')
    }
  })

  // The row renders ONE truncated line, so the walk stops after a budget of visible
  // text rather than scanning a multi-hundred-KB body. An exhausted budget must not
  // change what a row would display.
  it('bounds the output at the text budget while keeping the visible prefix intact', () => {
    const opening = 'The opening sentence that a row would display'
    const filler = '<p>' + 'lorem ipsum dolor sit amet '.repeat(500) + '</p>'
    const out = stripContent('<p>' + opening + '</p>' + filler)
    expect(out.startsWith(opening)).toBe(true)
    expect(out.length).toBeLessThan(400)
  })

  // One long text node (a 13 KB paragraph is common) must be cut at the budget too,
  // not appended wholesale before the counter is consulted — otherwise the output is
  // bounded by the node, not the budget.
  it('bounds the output even when a single text node is longer than the budget', () => {
    const out = stripContent('<p>' + 'word '.repeat(5000) + '</p>')
    expect(out.length).toBeLessThan(400)
    expect(out.startsWith('word word word')).toBe(true)
  })

  // A budget counted in whitespace would stop on a leading whitespace-only node and
  // return an EMPTY excerpt for a body that has text — the failure this guards.
  it('counts only non-whitespace toward the budget, so leading whitespace cannot empty the excerpt', () => {
    const body = '\n  \t  \n' + '<p>Real text follows the whitespace</p>'
    expect(stripContent(body)).toBe('Real text follows the whitespace')
    expect(stripContent(body)).not.toBe('')
  })

  it('finds text that sits behind a leading iframe and image', () => {
    const body = '<p><iframe src="x"><span>fallback</span></iframe></p><p><img src="y"></p><p>Article starts here</p>'
    expect(stripContent(body)).toBe('Article starts here')
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
