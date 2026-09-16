// Feed-type constants shared between the feed store and feed dialogs/sidebar.
// Feed types whose items carry a score (points) that can be filtered/sorted on.
export const SCORE_FEED_TYPES: ReadonlySet<string> = new Set(['hn', 'lobsters', 'reddit', 'twitter'])

// Resolve the final subscription URL for a feed type. `value` is the raw form
// value (full URL for rss/atom, subreddit name for reddit, username for
// twitter, ignored for hn / lobsters which have fixed URLs).
export function feedUrlFor(type: string, value: string): string {
  if (type === 'reddit') return 'https://www.reddit.com/r/' + value.replace(/\.rss$/, '')
  if (type === 'hn') return 'https://news.ycombinator.com/rss'
  if (type === 'lobsters') return 'https://lobste.rs/rss'
  if (type === 'twitter') return 'https://x.com/' + value.replace(/^@/, '')
  return value
}

// Suffixes allowed in an email feed address, mirroring ValidEmailSuffix on the
// server (internal/feed/email_address.go). Kept in sync deliberately: the server
// is the authority and rejects anything invalid with a 400, but validating here
// too means the user finds out before a round trip.
const EMAIL_SUFFIX_RE = /^[a-z0-9](?:[a-z0-9._-]{0,62}[a-z0-9])?$/

export function isValidEmailSuffix(suffix: string): boolean {
  return EMAIL_SUFFIX_RE.test(suffix.trim().toLowerCase())
}

// emailAddressFor composes the full address from the instance's inbox and the
// user's suffix.
//
// The inbox is instance-wide (one AgentMail mailbox serves every email feed), so
// it comes from the server rather than from user input; only the suffix is
// editable. One inbox plus one suffix is why the suffix must be unique across
// all users — see the server's emailAddressTaken.
export function emailAddressFor(inbox: string, suffix: string): string {
  const at = inbox.lastIndexOf('@')
  if (at <= 0) return suffix
  return inbox.slice(0, at) + '+' + suffix.trim().toLowerCase() + inbox.slice(at)
}

// emailPrefixOf / emailDomainOf return the fixed parts of the address that the
// dialog renders as non-editable context around the suffix field.
export function emailPrefixOf(inbox: string): string {
  const at = inbox.lastIndexOf('@')
  return at <= 0 ? '' : inbox.slice(0, at) + '+'
}

export function emailDomainOf(inbox: string): string {
  const at = inbox.lastIndexOf('@')
  return at <= 0 ? '' : inbox.slice(at)
}

// emailSuffixOf is the inverse of emailAddressFor: it extracts the editable
// suffix from a stored feed URL, so editing an existing feed prefills only the
// suffix and cannot corrupt the address on a round trip.
export function emailSuffixOf(url: string): string {
  const at = url.lastIndexOf('@')
  if (at <= 0) return ''
  const local = url.slice(0, at)
  const plus = local.indexOf('+')
  if (plus < 0) return ''
  return local.slice(plus + 1)
}

// emailNameFor derives a display name from a suffix, matching EmailSuffixToName
// on the server so the prefilled name is the same either way.
export function emailNameFor(suffix: string): string {
  return suffix
    .trim()
    .toLowerCase()
    .split(/[-_.]/)
    .filter(Boolean)
    .map(part => part.charAt(0).toUpperCase() + part.slice(1))
    .join(' ')
}
