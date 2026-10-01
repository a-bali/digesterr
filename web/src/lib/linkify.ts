/**
 * Shared comment-text renderer.
 *
 * Lives outside the components because the same conversion is needed on every
 * surface that shows comment text: the item pane and the digest view. It mirrors
 * `internal/linkify` on the Go side so a comment renders the same in the web UI
 * and through the Reader API.
 *
 * Order matters, and it is not the obvious one:
 *
 *  1. Decode HTML entities in the raw text exactly once. Comment sources encode
 *     them inconsistently (Reddit stores `&gt;` for a blockquote marker AND
 *     `&amp;` for a literal ampersand), so the text is normalized first and
 *     re-escaped once at the end. Decoding after escaping instead leaves
 *     `&amp;amp;` inside a generated href, which the browser reads as a query
 *     parameter literally named `amp;t` — a broken link that looks fine in the
 *     source.
 *  2. Reddit's internal `![alt](giphy|ID)` form becomes a real giphy URL.
 *  3. Markdown images `![alt](url)` become <img>; they are claimed before
 *     markdown links, whose syntax is a superset of theirs.
 *  4. Markdown links `[label](url)` become <a>.
 *  5. Bare image-host URLs become <img> (an image renders better than a link to
 *     an image).
 *  6. Remaining bare URLs become <a>. This runs on the not-yet-escaped text and
 *     BEFORE the generated tags are restored, so a link's own label can never be
 *     linkified into a nested anchor and no href is ever rewritten.
 *  7. Escape once, convert newlines, restore the generated tags.
 *
 * Every href goes through `safeUrl`: no `javascript:`/`data:` URL can reach an
 * attribute, whether it arrived as a markdown link or as bare text.
 */

const IMG_STYLE = 'max-width:400px;max-height:300px;width:auto;height:auto;border-radius:4px;margin:4px 0;display:block'

/** Escapes a value for use inside an attribute or as element content. */
function escapeAttr(s: string): string {
  return s
    .replace(/&/g, '&amp;')
    .replace(/"/g, '&quot;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
}

/**
 * Returns `raw` if it is safe to put in an href, otherwise null.
 *
 * Only http(s) is accepted: `javascript:` and `data:` are the schemes that turn
 * a link into script execution. Whitespace, quotes and angle brackets are
 * rejected too, because they would break out of the attribute.
 */
export function safeUrl(raw: string): string | null {
  const s = raw.trim()
  if (!/^https?:\/\//i.test(s)) return null
  if (/[\s"'<>]/.test(s)) return null
  return s
}

/** Matches a markdown image: ![alt](url) */
const IMG_RE = /!\[([^\]]*)\]\(([^)\s]+)\)/g
/** Matches a markdown link: [label](url), http(s) targets only. */
const MD_LINK_RE = /\[([^\]\n]*)\]\((https?:\/\/[^)\s]+)\)/g
/** Matches a bare image URL from a common image host. */
const BARE_IMAGE_RE =
  /https?:\/\/(?:media\d*\.giphy\.com\/\S+\.(?:gif|webp|png|jpg|jpeg)(?:\?\S*)?|giphy\.com\/gifs\/\S+|[^/\s]*\.?(?:i\.redd\.it|preview\.redd\.it|imgur\.com|i\.imgur\.com|files\.catbox\.moe|cdn\.discordapp\.com)\/\S+\.(?:jpg|jpeg|png|gif|webp|svg)(?:\?\S*)?)/g
/** Matches a bare URL, with or without a scheme. `www.` is included because authors write hostnames the way they read them. */
const BARE_URL_RE = /(?:https?:\/\/|www\.)[^\s<>"']+/g

/**
 * Removes trailing punctuation a URL match absorbs because it is not
 * whitespace. `see https://x.com/a.` must link the URL, not the full stop; a
 * balanced `)` belongs to the URL (`.../Foo_(bar)`) while an unmatched one
 * closes the surrounding text.
 */
function trimUrlTail(raw: string): string {
  let s = raw
  for (;;) {
    const before = s
    s = s.replace(/(?:&#39;|&#34;|&quot;|&gt;|&lt;|&amp;)+$/, '')
    s = s.replace(/[.,;:!?]+$/, '')
    if (s.endsWith(')') && (s.match(/\)/g)?.length ?? 0) > (s.match(/\(/g)?.length ?? 0)) {
      s = s.slice(0, -1)
    }
    if (s === before) return s
  }
}

function anchorHtml(href: string, label: string): string {
  return `<a href="${escapeAttr(href)}" target="_blank" rel="noopener noreferrer">${escapeAttr(label)}</a>`
}

function imgHtml(src: string, alt: string): string {
  return `<img src="${escapeAttr(src)}" alt="${escapeAttr(alt)}" style="${IMG_STYLE}" loading="lazy" />`
}

/**
 * Replaces bare URLs with placeholders, pushing the generated anchors onto
 * `placeholders`. Runs on text whose generated tags have not been restored yet,
 * so nothing it produces can be picked up by a later phase.
 */
function replaceBareUrls(text: string, placeholders: string[]): string {
  return text.replace(BARE_URL_RE, (match: string, offset: number) => {
    // The regex has no lookbehind, so reject a match starting mid-word:
    // `awww.example.com` is one hostname, not a URL.
    if (offset > 0 && /[\w.\-@]/.test(text[offset - 1])) return match
    const raw = trimUrlTail(match)
    if (!raw) return match
    const href = /^https?:\/\//i.test(raw) ? raw : `https://${raw}`
    if (!safeUrl(href)) return match
    placeholders.push(anchorHtml(href, raw))
    // Whatever was trimmed off the URL tail (a full stop, a closing paren that
    // belongs to the sentence) stays in the text after the link.
    return `\x00T${placeholders.length - 1}\x00${match.slice(raw.length)}`
  })
}

/** Restores the generated tags that were swapped out for placeholders. */
function restore(s: string, placeholders: string[]): string {
  return s.replace(/\x00T(\d+)\x00/g, (_, i: string) => placeholders[parseInt(i, 10)])
}

/**
 * Renders raw comment text as display HTML. Output is safe to insert with
 * `v-html`.
 */
export function renderCommentHtml(text: string): string {
  if (!text) return ''

  const placeholders: string[] = []

  // Step 1: normalize entities once, on the raw text.
  let work = decodeEntitiesOnce(text)

  // Step 2: Reddit's internal giphy syntax → real URL. The ID is restricted to
  // URL-safe characters so it cannot smuggle `"`, whitespace or a scheme break
  // into the generated img tag.
  work = work.replace(/!\[([^\]]*)\]\(giphy\|([^)]+)\)/g, (m: string, alt: string, id: string) => {
    if (!/^[A-Za-z0-9_-]+$/.test(id)) return m
    return `![${alt}](https://media.giphy.com/media/${id}/giphy.gif)`
  })

  // Step 3: markdown images.
  work = work.replace(IMG_RE, (m: string, alt: string, src: string) => {
    const safe = safeUrl(src)
    if (!safe) return m
    placeholders.push(imgHtml(safe, alt))
    return `\x00T${placeholders.length - 1}\x00`
  })

  // Step 4: markdown links. An empty label falls back to the URL itself, so
  // `[](url)` still renders something clickable.
  work = work.replace(MD_LINK_RE, (m: string, label: string, target: string) => {
    const safe = safeUrl(target)
    if (!safe) return m
    const text = label.trim() === '' ? safe : label
    placeholders.push(anchorHtml(safe, text))
    return `\x00T${placeholders.length - 1}\x00`
  })

  // Step 5: bare image URLs.
  work = work.replace(BARE_IMAGE_RE, (m: string) => {
    const safe = safeUrl(m)
    if (!safe) return m
    placeholders.push(imgHtml(safe, ''))
    return `\x00T${placeholders.length - 1}\x00`
  })

  // Step 6: remaining bare URLs.
  work = replaceBareUrls(work, placeholders)

  // Step 7: escape once, keep line breaks, restore generated tags.
  const escaped = work
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')

  return restore(escaped.replace(/\n/g, '<br>'), placeholders)
}

/**
 * Decodes the HTML entities a comment source encoded, once.
 *
 * A single pass over one regex, so an input that literally reads `&amp;lt;`
 * decodes to `&lt;` — not to `<`, which a second decode of the same entry would
 * have produced.
 */
function decodeEntitiesOnce(s: string): string {
  const named: Record<string, string> = {
    '&lt;': '<',
    '&gt;': '>',
    '&quot;': '"',
    '&#39;': "'",
    '&apos;': "'",
    '&nbsp;': '\u00a0',
  }
  return s.replace(/&(?:lt|gt|quot|#39|apos|nbsp|amp);/g, (m) =>
    m === '&amp;' ? '&' : (named[m] ?? m),
  )
}
