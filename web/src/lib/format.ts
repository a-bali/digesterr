// Shared date/time formatting helpers.
// Accepts the Go `time.Time` JSON shape ({ Time, Valid }) as well as raw
// ISO strings, so callers can pass API fields directly.

function unwrap(val: any): any {
  if (val && typeof val === 'object' && !Array.isArray(val)) {
    if (val.Valid === false) return null
    if (val.Time !== undefined) return val.Time
  }
  return val
}

export function parseDate(val: any): Date | null {
  if (!val) return null
  const v = unwrap(val)
  if (typeof v !== 'string') return null
  const d = new Date(v)
  return isNaN(d.getTime()) ? null : d
}

export function relativeTime(val: any): string {
  const d = parseDate(val)
  if (!d) return ''
  const diff = Date.now() - d.getTime()
  const sec = Math.floor(diff / 1000)
  if (sec < 60) return 'now'
  const min = Math.floor(sec / 60)
  if (min < 60) return `${min}m`
  const h = Math.floor(min / 60)
  if (h < 24) return `${h}h`
  const d2 = Math.floor(h / 24)
  if (d2 < 30) return `${d2}d`
  const m = Math.floor(d2 / 30)
  if (m < 12) return `${m}mo`
  return `${Math.floor(m / 12)}y`
}

export function absoluteTime(val: any): string {
  const d = parseDate(val)
  if (!d) return ''
  return d.toLocaleString(undefined, { year: 'numeric', month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' })
}

// Feed favicon URL; returns '' when none can be derived.
export function feedIcon(feed: { type: string; url: string }): string {
  if (feed.type === 'hn') return 'https://news.ycombinator.com/favicon.ico'
  if (feed.type === 'lobsters') return 'https://lobste.rs/favicon.ico'
  try {
    const domain = new URL(feed.url).hostname
    return `https://icons.duckduckgo.com/ip3/${domain}.ico`
  } catch {
    return ''
  }
}
