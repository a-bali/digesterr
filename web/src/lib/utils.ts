import { clsx, type ClassValue } from 'clsx'
import { twMerge } from 'tailwind-merge'

export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs))
}

// Per-content memo: stripContent runs once per rendered row per render, and
// a DOMParser per call made long lists jank on scroll. Same eviction policy
// as decodeHTMLEntities (drop rather than grow the unbounded key space).
const STRIP_CACHE_LIMIT = 1000
const stripCache = new Map<string, string>()

// Elements whose contents never render as text, so their subtree must be skipped
// entirely: script/style/iframe hold their content as RAW TEXT (per the HTML spec
// they have no child elements at all), and the browser's own innerText skips them.
// This is what a stored WordPress body starting with a YouTube <iframe> needs —
// without it the iframe's fallback children render as visible markup in the row.
const NEVER_RENDERED = new Set([
  'SCRIPT', 'STYLE', 'IFRAME', 'NOSCRIPT', 'TEMPLATE', 'OBJECT', 'EMBED',
  'HEAD', 'TITLE', 'BASE', 'LINK', 'META', 'PARAM', 'SOURCE', 'TRACK',
  'AUDIO', 'VIDEO', 'CANVAS', 'MAP', 'AREA', 'COL', 'COLGROUP', 'DATALIST',
])

// Elements that start a new line of text. Both boundaries contribute a separator so
// words never glue together across them: without this, <p>one</p><p>two</p> reads
// "onetwo" and <p>a<br>b</p> reads "ab". Real bodies concatenate run-together text
// wherever block markup is adjacent to prose.
//
// Only TAG NAMES belong here — never a regex over the markup. Feed bodies are full
// of code blocks and CLI docs where `<name>`, `<version>` and `<host-uid>` are
// LITERAL TEXT (71 of 74 such matches in a 1043-item prod sample were prose, not
// markup), so an element-shaped pattern would corrupt content that renders fine.
const LINE_BREAK = new Set([
  'ADDRESS', 'ARTICLE', 'ASIDE', 'BLOCKQUOTE', 'BODY', 'CAPTION', 'CENTER', 'DD',
  'DETAILS', 'DIALOG', 'DIR', 'DIV', 'DL', 'DT', 'FIELDSET', 'FIGCAPTION', 'FIGURE',
  'FOOTER', 'FORM', 'H1', 'H2', 'H3', 'H4', 'H5', 'H6', 'HEADER', 'HGROUP', 'HR',
  'HTML', 'LEGEND', 'LI', 'MAIN', 'MENU', 'NAV', 'OL', 'OPTGROUP', 'OPTION', 'P',
  'PRE', 'SECTION', 'SUMMARY', 'TABLE', 'TBODY', 'TD', 'TFOOT', 'TH', 'THEAD', 'TR',
  'UL', 'BR',
])

// The list row renders a single truncated line, so only the first few dozen
// characters are ever displayed. Stop once this much NON-WHITESPACE text has been
// collected — a 5x margin over what fits, so a future layout change stays covered —
// which keeps the pass off the tail of a 400 KB body. Counting whitespace here would
// let a body that opens with whitespace-only nodes stop before any visible text,
// yielding an EMPTY excerpt, so only non-whitespace characters advance the counter.
const EXCERPT_TEXT_BUDGET = 200

const isWhitespace = (ch: number) =>
  ch === 32 || ch === 9 || ch === 10 || ch === 13 || ch === 0xa0 || ch === 0x200b

/**
 * Flatten stored item HTML to the plain text a reader sees, for a one-line excerpt.
 *
 * This is NOT `body.textContent`: that returns every text node including the
 * contents of never-rendered elements and joins everything without separators, so a
 * body whose leading element is a video embed shows the embed's markup as the
 * excerpt instead of the article. It matches the browser's own `innerText` on the
 * characters a truncated row can display, without paying for layout: `innerText`
 * requires an attached, laid-out element (it silently degrades to textContent on a
 * detached node) and costs 3-4x as much.
 *
 * Two known divergences from `innerText` on the FULL string, neither of which a
 * one-line excerpt can show: content inside a collapsed <details> (innerText omits
 * it; the <summary> comes first either way, so the visible text already agrees) and
 * table-cell spacing.
 */
function stripContentUncached(content: string): string {
  if (!content) return ''
  const doc = new DOMParser().parseFromString(content, 'text/html')

  const out: string[] = []
  let textLength = 0
  let done = false

  const walk = (node: Node) => {
    if (done) return
    if (node.nodeType === Node.TEXT_NODE) {
      const value = node.nodeValue || ''
      // A single paragraph can hold tens of KB of text, so respect the budget INSIDE
      // the node: measure it, cut there, and never append past it.
      let take = value.length
      let counted = 0
      for (let i = 0; i < value.length; i++) {
        if (!isWhitespace(value.charCodeAt(i))) counted++
        if (textLength + counted >= EXCERPT_TEXT_BUDGET) {
          take = i + 1
          break
        }
      }
      out.push(take < value.length ? value.slice(0, take) : value)
      textLength += counted
      if (textLength >= EXCERPT_TEXT_BUDGET) done = true
      return
    }
    if (node.nodeType !== Node.ELEMENT_NODE) return
    const el = node as Element
    if (NEVER_RENDERED.has(el.tagName)) return
    // Inline styles the browser would not paint: outerHTML-level truth, and the only
    // hiding form that survives in a stored feed body (class-based hiding cannot be
    // resolved without a stylesheet).
    const style = (el as HTMLElement).style
    if (style && (style.display === 'none' || style.visibility === 'hidden')) return
    const breaks = LINE_BREAK.has(el.tagName)
    if (breaks) out.push(' ')
    for (let child = el.firstChild; child && !done; child = child.nextSibling) walk(child)
    if (breaks) out.push(' ')
  }

  walk(doc.body)

  return out.join('').replace(/\s+/g, ' ').trim()
}

export function stripContent(content: string): string {
  if (!content) return ''
  const cached = stripCache.get(content)
  if (cached !== undefined) return cached
  const out = stripContentUncached(content)
  if (stripCache.size >= STRIP_CACHE_LIMIT) stripCache.clear()
  stripCache.set(content, out)
  return out
}

// Decode HTML entities in a string (e.g. "&amp;" → "&", "&lt;" → "<").
// Used for titles stored with raw HTML entities from feed APIs.
// decodeHTMLEntities is called once per rendered list row and title, so the
// DOM round-trip (createElement + innerHTML parse) was repeated for the same
// handful of strings on every scroll. Results are cached; the key space is
// unbounded across feeds, so the cache is dropped rather than grown.
const ENTITY_CACHE_LIMIT = 1000
const entityCache = new Map<string, string>()

export function decodeHTMLEntities(s: string): string {
  if (!s) return ''
  const cached = entityCache.get(s)
  if (cached !== undefined) return cached
  const el = document.createElement('textarea')
  el.innerHTML = s
  const decoded = el.value
  if (entityCache.size >= ENTITY_CACHE_LIMIT) entityCache.clear()
  entityCache.set(s, decoded)
  return decoded
}
