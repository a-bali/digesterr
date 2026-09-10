/**
 * Shared handle for the "scroll to comments" action.
 *
 * The keyboard shortcut (`c`) lives in HomeView while the comments pane lives
 * in ItemContent, so neither can call the other directly. A module-level handle
 * keeps that wiring explicit and type-checked, unlike a globalThis property
 * which is invisible to the type checker and only fails at runtime.
 */

type Scroller = () => void

let scroller: Scroller | null = null

/** Register the mounted comments scroller. Pass null to clear it. */
export function setItemCommentsScroller(fn: Scroller | null): void {
  scroller = fn
}

/**
 * Clear the scroller only if it is still the one `fn` registered.
 *
 * HomeView renders ItemContent in two mutually exclusive layout branches. When
 * a layout switch mounts the new pane before unmounting the old one, an
 * unconditional clear from the outgoing instance would wipe the registration
 * the incoming instance just made — leaving `c` permanently dead. Comparing
 * identity first means a stale unmount cannot unregister a live pane.
 */
export function clearItemCommentsScroller(fn: Scroller): void {
  if (scroller === fn) scroller = null
}

/** Scroll the comments section into view, if a pane is currently mounted. */
export function scrollToItemComments(): void {
  scroller?.()
}
