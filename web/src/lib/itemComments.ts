/**
 * Shared handle for the "scroll to comments" action.
 *
 * The keyboard shortcut (`c`) lives in HomeView while the comments pane lives
 * in ItemContent, so neither can call the other directly. A module-level handle
 * keeps that wiring explicit and type-checked, unlike a globalThis property
 * which is invisible to the type checker and only fails at runtime.
 */
let scroller: (() => void) | null = null

/** Register the mounted comments scroller. Pass null to clear it on unmount. */
export function setItemCommentsScroller(fn: (() => void) | null): void {
  scroller = fn
}

/** Scroll the comments section into view, if a pane is currently mounted. */
export function scrollToItemComments(): void {
  scroller?.()
}
