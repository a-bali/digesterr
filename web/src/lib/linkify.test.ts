import { describe, it, expect } from 'vitest'
import { renderCommentHtml, safeUrl } from './linkify'

/**
 * The rendered text a reader sees: entities in the source are decoded by the
 * browser, so asserting the source spelling would pass for either encoding.
 */
function rendered(html: string): string {
  return html
    .replace(/<br>/g, '\n')
    .replace(/&amp;/g, '&')
    .replace(/&lt;/g, '<')
    .replace(/&gt;/g, '>')
    .replace(/&quot;/g, '"')
    .replace(/&#39;/g, "'")
}

describe('renderCommentHtml — bare URLs', () => {
  it('links a bare URL', () => {
    const got = renderCommentHtml('No, it doesn\'t.\nhttps://colo.to/exercise.pdf\nThey exercised it.')
    expect(got).toContain(
      '<a href="https://colo.to/exercise.pdf" target="_blank" rel="noopener noreferrer">https://colo.to/exercise.pdf</a>',
    )
    expect(rendered(got)).toContain('They exercised it.')
  })

  it('links every URL on a line', () => {
    const got = renderCommentHtml('first https://example.com/a then https://example.com/b end')
    expect(got.match(/<a href="https:\/\/example\.com\//g)).toHaveLength(2)
  })

  it('links a shortener and a wikipedia URL', () => {
    expect(renderCommentHtml('@OpenRouter @stripe why you lie https://t.co/zktIrzIbyR')).toContain(
      'href="https://t.co/zktIrzIbyR"',
    )
    expect(renderCommentHtml('https://en.wikipedia.org/wiki/Cambridge_Analytica')).toContain(
      'href="https://en.wikipedia.org/wiki/Cambridge_Analytica"',
    )
  })

  it('links a www host with no scheme, adding https', () => {
    expect(renderCommentHtml('go to www.example.com/x?y=1 for details')).toContain(
      '<a href="https://www.example.com/x?y=1" target="_blank" rel="noopener noreferrer">www.example.com/x?y=1</a>',
    )
  })

  it('does not linkify a www inside a word', () => {
    expect(renderCommentHtml('awww.example.com is not a host')).not.toContain('<a href=')
  })

  it('excludes trailing punctuation from the URL but keeps it as text', () => {
    const got = renderCommentHtml('see https://example.com/page. Then continue')
    expect(got).toContain('href="https://example.com/page"')
    expect(got).toContain('</a>. Then continue')
  })

  it('keeps balanced parentheses inside the URL', () => {
    expect(renderCommentHtml('(see https://en.wikipedia.org/wiki/Foo_(bar) for more)')).toContain(
      'href="https://en.wikipedia.org/wiki/Foo_(bar)"',
    )
  })
})

describe('renderCommentHtml — markdown links', () => {
  it('renders a markdown link with its label', () => {
    const got = renderCommentHtml('[Itt](https://www.youtube.com/live/BU6rXYv726I?si=U50DG_vDKNazLq92) is the video')
    expect(got).toContain(
      '<a href="https://www.youtube.com/live/BU6rXYv726I?si=U50DG_vDKNazLq92" target="_blank" rel="noopener noreferrer">Itt</a>',
    )
  })

  // The regression this guards: the raw comment body stores `&amp;` for a
  // literal ampersand, so a URL query separator arrives as `&amp;`. Escaping it
  // a second time produced `&amp;amp;`, which the browser decodes to a parameter
  // literally named `amp;t` — a broken link that looks fine in the source.
  it('escapes the query separator exactly once', () => {
    const got = renderCommentHtml('[Itt](https://www.youtube.com/live/BU6rXYv726I?si=U50DG_vDKNazLq92&amp;t=30918)')
    expect(got).toContain('href="https://www.youtube.com/live/BU6rXYv726I?si=U50DG_vDKNazLq92&amp;t=30918"')
    expect(got).not.toContain('&amp;amp;')
    expect(rendered(got)).toContain('https://www.youtube.com/live/BU6rXYv726I?si=U50DG_vDKNazLq92&t=30918')
  })

  it('escapes a plain ampersand in a markdown link', () => {
    expect(renderCommentHtml('[kifli](https://kifli.hu/kereses?q=a&b=2)')).toContain(
      'href="https://kifli.hu/kereses?q=a&amp;b=2"',
    )
  })

  it('never links a non-http scheme', () => {
    for (const raw of ['javascript:alert(1)', 'data:text/html;base64,PHNjcmlwdD4=', 'vbscript:msgbox(1)']) {
      const got = renderCommentHtml(`click [here](${raw}) and ${raw}`)
      expect(got, raw).not.toContain('<a href=')
    }
  })

  it('falls back to the URL when the label is empty', () => {
    expect(renderCommentHtml('[](https://example.com/x)')).toContain(
      '<a href="https://example.com/x" target="_blank" rel="noopener noreferrer">https://example.com/x</a>',
    )
  })
})

describe('renderCommentHtml — images', () => {
  it('renders a markdown image and does not also link it', () => {
    const got = renderCommentHtml('![screenshot](https://i.redd.it/abc123.jpg) shows the issue.')
    expect(got).toContain('<img src="https://i.redd.it/abc123.jpg"')
    expect(got).toContain('alt="screenshot"')
    expect(got).not.toContain('<a href=')
  })

  it('renders a bare image-host URL as an image, not a link', () => {
    const got = renderCommentHtml('look at this https://i.imgur.com/abc123.png pretty cool')
    expect(got).toContain('<img src="https://i.imgur.com/abc123.png"')
    expect(got).not.toContain('<a href="https://i.imgur.com/abc123.png"')
    expect(got).toContain('pretty cool')
  })

  it('converts Reddit internal giphy syntax and rejects an unsafe id', () => {
    expect(renderCommentHtml('![gif](giphy|whhCAcuxtAXcrQhHTk)')).toContain(
      '<img src="https://media.giphy.com/media/whhCAcuxtAXcrQhHTk/giphy.gif"',
    )
    // A crafted id must never reach an attribute: the syntax is left as text.
    const got = renderCommentHtml('![gif](giphy|x" onerror="alert(1))')
    expect(got).not.toContain('<img')
    expect(got).toContain('&quot;')
  })
})

describe('renderCommentHtml — escaping', () => {
  it('escapes raw markup', () => {
    const got = renderCommentHtml('Nice <script>alert(1)</script> comment & more')
    expect(got).not.toContain('<script>')
    expect(rendered(got)).toContain('Nice <script>alert(1)</script> comment & more')
  })

  it('decodes source entities exactly once', () => {
    const got = renderCommentHtml('&gt;quoted text&lt; with &amp; ampersand &quot;and&quot; &#39;quotes&#39;')
    expect(rendered(got)).toContain('>quoted text< with & ampersand "and" \'quotes\'')
    expect(got).not.toContain('&amp;amp;')
  })

  it('converts newlines to breaks', () => {
    expect(renderCommentHtml('line one\nline two')).toContain('line one<br>line two')
  })
})

describe('safeUrl', () => {
  it('accepts http and https with no attribute-breaking characters', () => {
    expect(safeUrl('https://example.com')).toBe('https://example.com')
    expect(safeUrl(' http://example.com/a?b=1 ')).toBe('http://example.com/a?b=1')
  })

  it('rejects other schemes and attribute-breaking input', () => {
    for (const raw of ['javascript:alert(1)', 'data:text/html,x', '//example.com', 'https://e.com/a b', 'https://e.com/a"b']) {
      expect(safeUrl(raw), raw).toBeNull()
    }
  })
})
