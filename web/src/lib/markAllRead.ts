/**
 * One vocabulary for the mark-all-read feature.
 *
 * This lives in its own module because the words for a single action had drifted
 * across four files: the sidebar tooltip said "Mark all digests as read" while the
 * toast for that same action said "All digests marked as read" with the scope in
 * the title, the empty state appeared as "Nothing unread", "Nothing unread here"
 * and "Nothing unread in this category", and the feed path put the scope in the
 * toast SUBTITLE while the digest path put it in the TITLE. Every one of those was
 * a separate string literal.
 *
 * The rule here: a scope is described in exactly one place, and every surface that
 * names it — tooltip, dialog title, dialog body, toast, empty-state message —
 * derives from that description.
 */

/** A scope a mark-all-read can act on. */
export type MarkAllScope =
  | { kind: 'feed'; id: number; name: string }
  | { kind: 'category'; name: string }
  | { kind: 'feeds-section' }
  | { kind: 'digest-config'; id: number; name: string }
  | { kind: 'all-digests' }

/**
 * The scope as a noun phrase for a sentence: "Hacker News", "category “Tech”",
 * "all digests". Used as the toast subtitle and inside dialog copy, so a toast
 * always names what it acted on (a bare "All digests marked as read" does not scale
 * to several configs).
 */
export function scopeLabel(scope: MarkAllScope): string {
  switch (scope.kind) {
    case 'feed': return scope.name
    case 'category': return `category “${scope.name}”`
    case 'feeds-section': return 'all feeds'
    case 'digest-config': return scope.name || 'this digest'
    case 'all-digests': return 'all digests'
  }
}

/**
 * The scope as an imperative control label: the tooltip on the sidebar/row button
 * and the accessible name of the action. Shares scopeLabel's vocabulary so the
 * control a user hovers describes the same thing as the toast they get afterwards.
 */
export function scopeActionLabel(scope: MarkAllScope): string {
  switch (scope.kind) {
    case 'feed': return `Mark “${scope.name}” as read`
    case 'category': return `Mark category “${scope.name}” as read`
    case 'feeds-section': return 'Mark all feeds as read'
    case 'digest-config': return `Mark “${scope.name}” as read`
    case 'all-digests': return 'Mark all digests as read'
  }
}

/**
 * The imperative label when the scope has nothing unread. Same construction as
 * scopeActionLabel (parallel wording, so the pair reads as one control changing
 * state) rather than a third phrasing like "Nothing unread here".
 */
export function scopeEmptyLabel(scope: MarkAllScope): string {
  return `Nothing unread in ${scopeLabel(scope)}`
}

/** Count of the thing a scope marks. Feeds/categories count items; digests count results. */
export function scopeUnit(scope: MarkAllScope): 'item' | 'result' {
  return scope.kind === 'feed' || scope.kind === 'category' || scope.kind === 'feeds-section'
    ? 'item'
    : 'result'
}

/** Pluralised count phrase: "3 items", "1 result". Count may be unknown (null). */
export function scopeCount(scope: MarkAllScope, n: number | null): string {
  const count = n ?? 0
  const unit = scopeUnit(scope)
  return `${count} ${unit}${count === 1 ? '' : 's'}`
}

// Fixed toast titles. Both entry points and every scope use these exact strings, so
// the only variation a user sees between actions is the scope subtitle.
export const MARK_SUCCESS_TITLE = 'Marked as read'
export const MARK_FAILURE_TITLE = 'Failed to mark as read'
export const MARK_EMPTY_TITLE = 'Nothing to mark as read'

/** Dialog title — one string for every scope. The body carries the specifics. */
export const MARK_DIALOG_TITLE = 'Mark all as read?'

/**
 * The relative mark ("above"/"below" a row) is ONE action with one boundary rule:
 * everything on that side of the anchoring row, excluding the row itself. It had
 * four different phrasings before — "Mark everything above as read, except this
 * item" on the desktop chevron, "Mark all read below" in the swipe panel, "Mark all
 * items below as read" as the panel's aria-label, and "Mark feed as read" in the
 * shortcuts dialog.
 *
 * One phrasing, used everywhere. "Everything … except this item" is the accurate
 * one: the row under the pointer is deliberately skipped.
 */
export function markRelativeLabel(direction: 'above' | 'below'): string {
  return `Mark everything ${direction} as read, except this item`
}

/** The short form a narrow swipe panel shows. Same sentence, trimmed to fit. */
export function markRelativeShortLabel(direction: 'above' | 'below'): string {
  return `Mark ${direction} read`
}
