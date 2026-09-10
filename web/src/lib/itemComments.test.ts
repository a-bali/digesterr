import { describe, it, expect, vi, beforeEach } from 'vitest'
import {
  setItemCommentsScroller,
  clearItemCommentsScroller,
  scrollToItemComments,
} from './itemComments'

describe('itemComments scroll handle', () => {
  beforeEach(() => setItemCommentsScroller(null))

  it('invokes the registered scroller', () => {
    const scroller = vi.fn()
    setItemCommentsScroller(scroller)
    scrollToItemComments()
    expect(scroller).toHaveBeenCalledTimes(1)
  })

  it('does nothing when no pane is mounted', () => {
    expect(() => scrollToItemComments()).not.toThrow()
  })

  // Regression: HomeView renders ItemContent in two mutually exclusive layout
  // branches, and a layout switch mounts the replacement before unmounting the
  // outgoing pane. An unconditional `setItemCommentsScroller(null)` in the
  // outgoing instance's onUnmounted wiped the incoming registration, which
  // silently killed the `c` shortcut for the rest of the session.
  it('keeps the live scroller when a stale instance unregisters', () => {
    const stale = vi.fn()
    const live = vi.fn()
    setItemCommentsScroller(stale)
    setItemCommentsScroller(live)

    clearItemCommentsScroller(stale)

    scrollToItemComments()
    expect(live).toHaveBeenCalledTimes(1)
    expect(stale).not.toHaveBeenCalled()
  })

  it('unregisters when the owning instance clears itself', () => {
    const scroller = vi.fn()
    setItemCommentsScroller(scroller)
    clearItemCommentsScroller(scroller)
    scrollToItemComments()
    expect(scroller).not.toHaveBeenCalled()
  })
})
