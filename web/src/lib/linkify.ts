/**
 * Shared comment-text renderer.
 *
 * Lives outside the components because the same conversion is needed on every
 * surface that shows comment text: the item pane and the digest view. It mirrors
 * `internal/linkify` on the Go side so a comment renders the same in the web UI
 * and through the Reader API.
 *
 * The work is split in two, mirroring the Go twin:
 *
 *  - `renderBlocks` splits the text into markdown BLOCKS — blockquotes, lists and
 *    code fences — and emits their wrapping elements. Comments carry markdown
 *    too. Measured over 3,702 real Reddit comments the constructs that occur are
 *    blockquote 2.78%, bullet list 0.46%, ordered list 0.32%, code fence 0.05%;
 *    headings/rules/tables are ~0.05% and are deliberately NOT handled (they keep
 *    rendering as the literal text they render as today, not as a regression).
 *  - `commentInline` renders the INLINE marks inside one block: giphy, markdown
 *    images and links, comment-video links, bare image URLs, bare URLs.
 *
 * The returned HTML is a sequence of complete top-level elements, so a caller
 * must NOT wrap it in a paragraph of its own.
 *
 * Inline order matters, and it is not the obvious one:
 *
 *  1. Decode HTML entities in the raw text exactly once, BEFORE splitting into
 *     blocks: a comment source encodes the blockquote marker as `&gt;`, so
 *     splitting first would never see a quote. Comment sources encode entities
 *     inconsistently (Reddit stores `&gt;` for a blockquote marker AND `&amp;`
 *     for a literal ampersand), so the text is normalized first and re-escaped
 *     once at the end. Decoding after escaping instead leaves `&amp;amp;` inside
 *     a generated href, which the browser reads as a query parameter literally
 *     named `amp;t` — a broken link that looks fine in the source.
 *  2. Reddit's internal `![alt](giphy|ID)` form becomes a real giphy URL.
 *  3. Markdown images `![alt](url)` become <img>; they are claimed before
 *     markdown links, whose syntax is a superset of theirs.
 *  4. Markdown links `[label](url)` become <a>.
 *  5. App-only Reddit comment-video links become an inline player. They must be
 *     claimed before the bare-URL passes, which would otherwise linkify them
 *     into a page Reddit serves as "r/subreddit is banned".
 *  6. Bare image-host URLs become <img> (an image renders better than a link to
 *     an image).
 *  7. Remaining bare URLs become <a>. This runs on the not-yet-escaped text and
 *     BEFORE the generated tags are restored, so a link's own label can never be
 *     linkified into a nested anchor and no href is ever rewritten.
 *  8. Escape once, convert newlines, restore the generated tags.
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
 * Matches Reddit's app-only comment-video deep link:
 *
 *     https://reddit.com/link/<comment_id>/video/<media_id>/player
 *
 * A comment carrying a video has NO media field on the comment object at all —
 * the media id exists only in the URL the author pasted. Reddit's web handler
 * rewrites the path into a comment permalink using the literal string
 * `subreddit` as the subreddit, so an anonymous visitor lands on a page reading
 * "r/subreddit is banned".
 *
 * The scheme and host prefix are optional because authors paste the bare
 * `reddit.com` form. The id class is `[A-Za-z0-9]+` so nothing taken from user
 * text can carry a quote or whitespace into the generated markup.
 */
const COMMENT_VIDEO_RE =
  /(?:https?:\/\/)?(?:www\.|old\.|new\.|m\.)?reddit\.com\/link\/[A-Za-z0-9]+\/video\/([A-Za-z0-9]+)\/player\/?/gi

/** Block-level markers. A marker is only a marker when followed by a space, so `*emphasis*` is not a bullet. */
const QUOTE_RE = /^ {0,3}>[ \t]?(.*)$/
const FENCE_RE = /^ {0,3}(`{3,}|~{3,})[ \t]*(\S*)[ \t]*$/
const BULLET_RE = /^([ \t]*)([-*+])[ \t]+(.*)$/
const ORDERED_RE = /^([ \t]*)(\d{1,9})[.)][ \t]+(.*)$/

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
 * Reports whether the URL at `start` sits inside an existing attribute value
 * (href=…, src=…), where it must not be claimed again. Raw HTML in a comment
 * body is the case that matters; this runs before the escape pass, so the
 * quote is still literal.
 *
 * Mirrors `isAttributeURL` in the Go twin.
 */
function isAttributeUrl(text: string, start: number): boolean {
  if (start === 0) return false
  const prev = text[start - 1]
  if (prev === '>' || prev === '/') return false
  let i = start - 1
  if (prev === '"' || prev === "'") {
    i--
  } else {
    return false
  }
  while (i >= 0 && text[i] === ' ') i--
  if (i < 0 || text[i] !== '=') return false
  i--
  while (i >= 0 && text[i] === ' ') i--
  const head = text.slice(0, i + 1).toLowerCase()
  return head.endsWith('href') || head.endsWith('src')
}

/**
 * The direct address of a comment video's media: the HLS master manifest,
 * which is the only asset Reddit serves carrying BOTH picture and sound. The
 * progressive `CMAF_*.mp4` files are video-only, confirmed by parsing the MP4
 * boxes. This URL answers 200 with no query string.
 */
export function commentVideoMediaUrl(mediaId: string): string {
  return `https://v.redd.it/${mediaId}/HLSPlaylist.m3u8`
}

/** HLS first for native readers; progressive fallbacks for other engines. */
export function commentVideoHtml(mediaId: string): string {
  if (!/^[A-Za-z0-9]+$/.test(mediaId)) return ''
  const src = `https://v.redd.it/${mediaId}/`
  return (
    '<video controls preload="metadata" style="max-width:100%;border-radius:4px">' +
    `<source src="${src}HLSPlaylist.m3u8" type="application/vnd.apple.mpegurl" />` +
    `<source src="${src}CMAF_720.mp4" type="video/mp4" />` +
    `<source src="${src}CMAF_480.mp4" type="video/mp4" />` +
    '</video>' +
    `<a href="${src}HLSPlaylist.m3u8" target="_blank" rel="noopener noreferrer">Watch video ↗</a>`
  )
}

/**
 * Replaces app-only comment-video links with an inline player, before the
 * bare-URL pass would otherwise linkify them into a banned-subreddit page.
 *
 * Like `replaceBareUrls` it takes plain string positions, so it can reject a
 * match that starts mid-word (`xreddit.com/…`) or sits inside an existing
 * attribute value.
 */
function replaceCommentVideos(text: string, placeholders: string[]): string {
  COMMENT_VIDEO_RE.lastIndex = 0
  const matches: { start: number; end: number; mediaId: string }[] = []
  let m: RegExpExecArray | null
  while ((m = COMMENT_VIDEO_RE.exec(text)) !== null) {
    matches.push({ start: m.index, end: m.index + m[0].length, mediaId: m[1] })
  }
  if (matches.length === 0) return text

  let out = ''
  let last = 0
  for (const cand of matches) {
    const { start, end, mediaId } = cand
    // A match overlapping one already consumed is a duplicate find.
    if (start < last || isAttributeUrl(text, start)) continue
    // `xreddit.com/link/...` is one word, not a URL.
    if (start > 0 && /[\w.\-@]/.test(text[start - 1])) continue
    out += text.slice(last, start)
    placeholders.push(commentVideoHtml(mediaId))
    out += `\x00T${placeholders.length - 1}\x00`
    last = end
  }
  out += text.slice(last)
  return out
}

/**
 * Renders raw comment text as display HTML. Output is safe to insert with
 * `v-html`.
 */
export function renderCommentHtml(text: string): string {
  if (!text) return ''

  // Normalize entities once, for the whole body and BEFORE splitting into
  // blocks: the blockquote marker is stored as &gt;, so splitting first would
  // never see a quote.
  return renderBlocks(decodeEntitiesOnce(text), commentInline)
}

/** One bullet or numbered entry, with the indentation that decides nesting. */
interface BlockEntry {
  indent: number
  ordered: boolean
  number: number
  content: string
}

/**
 * Splits text into markdown blocks and renders each block's inline content
 * through `inline`. Blocks produced: fenced code, blockquote, bullet/ordered
 * lists, and paragraphs.
 */
function renderBlocks(text: string, inline: (s: string) => string): string {
  const lines = text.split('\n')
  let out = ''
  let i = 0

  while (i < lines.length) {
    const line = lines[i]

    if (line.trim() === '') {
      i++
      continue
    }

    // Fenced code is claimed first so no inline rule runs over the sample inside
    // it, and its newlines stay significant inside <pre>.
    const fence = FENCE_RE.exec(line)
    if (fence) {
      const marker = fence[1]
      const body: string[] = []
      i++
      while (i < lines.length) {
        if (closesFence(lines[i], marker)) {
          i++
          break
        }
        body.push(lines[i])
        i++
      }
      out += '<pre><code>' + escapeAttr(body.join('\n')) + '</code></pre>'
      continue
    }

    if (QUOTE_RE.test(line)) {
      const quoted: string[] = []
      while (i < lines.length) {
        const m = QUOTE_RE.exec(lines[i])
        if (!m) break
        quoted.push(m[1])
        i++
      }
      out += '<blockquote>' + inline(quoted.join('\n')) + '</blockquote>'
      continue
    }

    if (parseEntry(line) !== null) {
      const entries: BlockEntry[] = []
      while (i < lines.length) {
        const e = parseEntry(lines[i])
        if (!e) break
        entries.push(e)
        i++
      }
      out += renderEntries(entries, inline)
      continue
    }

    const start = i
    while (i < lines.length && lines[i].trim() !== '' && !endsParagraph(lines, i)) {
      i++
    }
    if (i > start) {
      out += '<p>' + inline(lines.slice(start, i).join('\n')) + '</p>'
    } else {
      // An unrecognised line must still advance, or the loop spins.
      out += '<p>' + inline(line) + '</p>'
      i++
    }
  }

  return out
}

/**
 * Emits nested <ul>/<ol> from a flat run of entries. An entry more indented than
 * the previous one opens a list INSIDE it, so the markup nests
 * (`<li>a<ul><li>b</li></ul></li>`) rather than emitting the child as a sibling.
 */
function renderEntries(entries: BlockEntry[], inline: (s: string) => string): string {
  interface Frame {
    ordered: boolean
    indent: number
    openLI: boolean
  }
  const stack: Frame[] = []
  let out = ''

  const closeDeeperThan = (indent: number) => {
    while (stack.length > 0 && stack[stack.length - 1].indent > indent) {
      const f = stack.pop() as Frame
      if (f.openLI) out += '</li>'
      out += f.ordered ? '</ol>' : '</ul>'
    }
  }

  entries.forEach((en, idx) => {
    closeDeeperThan(en.indent)

    if (stack.length > 0 && stack[stack.length - 1].indent === en.indent) {
      const top = stack[stack.length - 1]
      if (top.ordered !== en.ordered) {
        // Same level, different marker: end this list, start another.
        if (top.openLI) {
          out += '</li>'
          top.openLI = false
        }
        out += top.ordered ? '</ol>' : '</ul>'
        stack.pop()
      } else if (top.openLI) {
        // Coming back up to a level whose item was left open for its children.
        out += '</li>'
        top.openLI = false
      }
    }

    if (stack.length === 0 || stack[stack.length - 1].indent < en.indent) {
      if (en.ordered) {
        // Preserve the author's start number: 3,4,5 must not renumber to 1,2,3.
        out += en.number > 1 ? `<ol start="${en.number}">` : '<ol>'
      } else {
        out += '<ul>'
      }
      stack.push({ ordered: en.ordered, indent: en.indent, openLI: false })
    }

    out += '<li>' + inline(en.content)
    if (idx + 1 < entries.length && entries[idx + 1].indent > en.indent) {
      stack[stack.length - 1].openLI = true
    } else {
      out += '</li>'
    }
  })

  closeDeeperThan(-1)
  return out
}

/**
 * `true` when line closes a fence opened with `open`: the same character, at
 * least as long, and carrying nothing else.
 */
function closesFence(line: string, open: string): boolean {
  const t = line.replace(/^[ \t]+/, '')
  const ch = open[0]
  let n = 0
  while (n < t.length && t[n] === ch) n++
  if (n < open.length) return false
  return t.slice(n).trim() === ''
}

/**
 * `true` when lines[i] ends the paragraph being collected. Mirrors the branches
 * of renderBlocks, with CommonMark's rule that an ordered list interrupts a
 * paragraph only when it starts at 1 — so "2024. We shipped X" stays prose and
 * keeps its number instead of losing it to a list marker.
 */
function endsParagraph(lines: string[], i: number): boolean {
  const l = lines[i]
  if (FENCE_RE.test(l) || QUOTE_RE.test(l) || BULLET_RE.test(l)) return true
  const m = ORDERED_RE.exec(l)
  if (m) return parseInt(m[2], 10) === 1
  return false
}

/** Splits a list line into its indentation, kind and content. */
function parseEntry(line: string): BlockEntry | null {
  const bullet = BULLET_RE.exec(line)
  if (bullet) {
    return { indent: indentWidth(bullet[1]), ordered: false, number: 0, content: bullet[3].trim() }
  }
  const ordered = ORDERED_RE.exec(line)
  if (ordered) {
    return {
      indent: indentWidth(ordered[1]),
      ordered: true,
      number: parseInt(ordered[2], 10),
      content: ordered[3].trim(),
    }
  }
  return null
}

/**
 * Counts leading whitespace, treating a tab as four columns so a tab-indented
 * child nests under a space-indented parent.
 */
function indentWidth(ws: string): number {
  let w = 0
  for (const ch of ws) w += ch === '\t' ? 4 : 1
  return w
}

/**
 * Renders the INLINE marks of one block of comment text. HTML entities must
 * already be decoded by the caller.
 */
function commentInline(work: string): string {
  if (!work) return ''

  const placeholders: string[] = []

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

  // Step 5: app-only Reddit comment-video links become an inline player. These
  // must be claimed before the bare-URL passes below, which would otherwise
  // turn them into a link to a page Reddit renders as "r/subreddit is banned".
  work = replaceCommentVideos(work, placeholders)

  // Step 6: bare image URLs.
  work = work.replace(BARE_IMAGE_RE, (m: string) => {
    const safe = safeUrl(m)
    if (!safe) return m
    placeholders.push(imgHtml(safe, ''))
    return `\x00T${placeholders.length - 1}\x00`
  })

  // Step 7: remaining bare URLs.
  work = replaceBareUrls(work, placeholders)

  // Step 8: escape once, keep line breaks, restore generated tags.
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
