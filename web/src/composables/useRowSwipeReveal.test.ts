import { describe, it, expect, vi } from 'vitest'
import { useRowSwipeReveal } from './useRowSwipeReveal'

function touch(x: number, y: number, id = 0) {
  return { clientX: x, clientY: y, identifier: id }
}

function start(x: number, y: number) {
  return { touches: [touch(x, y)] } as unknown as TouchEvent
}

function move(x: number, y: number) {
  return { touches: [touch(x, y)] } as unknown as TouchEvent
}

describe('useRowSwipeReveal', () => {
  it('reveals a row when the drag goes left past the threshold', () => {
    const r = useRowSwipeReveal({ threshold: 40 })
    r.onTouchStart(start(300, 100), 7)
    r.onTouchMove(move(240, 104))

    expect(r.revealedId.value).toBe(7)
    expect(r.isRevealed(7)).toBe(true)
  })

  it('does not reveal on a drag shorter than the threshold', () => {
    const r = useRowSwipeReveal({ threshold: 40 })
    r.onTouchStart(start(300, 100), 7)
    r.onTouchMove(move(280, 102))

    expect(r.revealedId.value).toBeNull()
  })

  it('does not reveal on a vertically dominant drag, so list scrolling is never hijacked', () => {
    const r = useRowSwipeReveal({ threshold: 40, dominance: 2 })
    r.onTouchStart(start(300, 100), 7)
    // 60px left but 50px down: horizontal does not beat vertical by 2x.
    r.onTouchMove(move(240, 150))

    expect(r.revealedId.value).toBeNull()
  })

  it('does not reveal on a rightward drag (that is the sidebar edge-swipe)', () => {
    const r = useRowSwipeReveal({ threshold: 40 })
    r.onTouchStart(start(200, 100), 7)
    r.onTouchMove(move(300, 100))

    expect(r.revealedId.value).toBeNull()
  })

  it('reveals mid-drag, as soon as the threshold is crossed', () => {
    const r = useRowSwipeReveal({ threshold: 40 })
    r.onTouchStart(start(300, 100), 7)
    r.onTouchMove(move(230, 103))
    expect(r.revealedId.value).toBe(7)

    // A further move in the same gesture must not toggle it back off.
    r.onTouchMove(move(200, 103))
    expect(r.revealedId.value).toBe(7)
  })

  it('puts the row away when the revealed row is swiped left again', () => {
    const r = useRowSwipeReveal({ threshold: 40 })
    r.onTouchStart(start(300, 100), 7)
    r.onTouchMove(move(240, 100))
    expect(r.revealedId.value).toBe(7)
    r.onTouchEnd()

    r.onTouchStart(start(300, 100), 7)
    r.onTouchMove(move(240, 100))
    expect(r.revealedId.value).toBeNull()
  })

  it('moves the reveal to another row instead of showing two at once', () => {
    const r = useRowSwipeReveal({ threshold: 40 })
    r.onTouchStart(start(300, 100), 7)
    r.onTouchMove(move(240, 100))
    r.onTouchEnd()

    r.onTouchStart(start(300, 100), 8)
    r.onTouchMove(move(240, 100))
    r.onTouchEnd()

    expect(r.revealedId.value).toBe(8)
    expect(r.isRevealed(7)).toBe(false)
  })

  it('starts a fresh gesture after touchEnd, so a later swipe still works', () => {
    const r = useRowSwipeReveal({ threshold: 40 })
    r.onTouchStart(start(300, 100), 7)
    r.onTouchMove(move(240, 100))
    r.onTouchEnd()

    // Reveal a different row, then reveal the original again: it must re-reveal.
    r.onTouchStart(start(300, 100), 8)
    r.onTouchMove(move(240, 100))
    r.onTouchEnd()

    r.onTouchStart(start(300, 100), 7)
    r.onTouchMove(move(240, 100))
    expect(r.revealedId.value).toBe(7)
  })

  it('suppresses the drag\'s synthetic click so revealing does not also open the item', () => {
    const r = useRowSwipeReveal({ threshold: 40, clickGrace: 400 })
    expect(r.shouldSuppressClick()).toBe(false)

    r.onTouchStart(start(300, 100), 7)
    r.onTouchMove(move(240, 100))
    expect(r.shouldSuppressClick()).toBe(true)
  })

  it('does not suppress clicks when no swipe happened', () => {
    const r = useRowSwipeReveal({ threshold: 40, clickGrace: 0 })
    r.onTouchStart(start(300, 100), 7)
    r.onTouchMove(move(300, 100))
    r.onTouchEnd()

    expect(r.shouldSuppressClick()).toBe(false)
  })

  it('stops suppressing once the grace window has passed', () => {
    vi.useFakeTimers()
    try {
      const r = useRowSwipeReveal({ threshold: 40, clickGrace: 400 })
      r.onTouchStart(start(300, 100), 7)
      r.onTouchMove(move(240, 100))
      expect(r.shouldSuppressClick()).toBe(true)

      vi.advanceTimersByTime(500)
      expect(r.shouldSuppressClick()).toBe(false)
    } finally {
      vi.useRealTimers()
    }
  })

  it('dismiss and reveal are exposed for non-gesture callers', () => {
    const r = useRowSwipeReveal()
    r.reveal(3)
    expect(r.revealedId.value).toBe(3)
    r.dismiss()
    expect(r.revealedId.value).toBeNull()
    r.toggle(4)
    expect(r.revealedId.value).toBe(4)
    r.toggle(4)
    expect(r.revealedId.value).toBeNull()
  })

  it('ignores a touchMove with no active touchStart', () => {
    const r = useRowSwipeReveal({ threshold: 40 })
    r.onTouchMove(move(100, 100))
    expect(r.revealedId.value).toBeNull()
  })
})
