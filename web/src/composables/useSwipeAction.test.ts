import { describe, it, expect, vi } from 'vitest'
import { useSwipeAction } from './useSwipeAction'

function ev(type: 'start' | 'move', x: number, y: number) {
  return { touches: [{ clientX: x, clientY: y, identifier: 0 }] } as unknown as TouchEvent
}

/** One full swipe gesture: start, move, end. */
function swipe(
  s: ReturnType<typeof useSwipeAction>,
  id: number,
  from: number,
  to: number,
  dy = 0,
) {
  s.onTouchStart(ev('start', from, 200), id)
  s.onTouchMove(ev('move', to, 200 + dy))
  s.onTouchEnd()
}

describe('useSwipeAction', () => {
  it('opens the "below" panel on a right-to-left swipe', () => {
    const s = useSwipeAction()
    swipe(s, 7, 300, 240)

    expect(s.actionFor(7)).toBe('below')
    expect(s.openId.value).toBe(7)
  })

  it('opens the "above" panel on a left-to-right swipe', () => {
    const s = useSwipeAction()
    swipe(s, 7, 120, 180)

    expect(s.actionFor(7)).toBe('above')
  })

  it('leaves the panel open after the gesture ends, so it can be confirmed by tapping', () => {
    const s = useSwipeAction()
    swipe(s, 7, 300, 240)

    // The whole point of the two-step shape: the swipe alone must not commit.
    expect(s.actionFor(7)).toBe('below')
  })

  it('does not open on a short swipe', () => {
    const s = useSwipeAction({ threshold: 40 })
    swipe(s, 7, 300, 285)
    expect(s.actionFor(7)).toBeNull()
  })

  it('does not open on a vertically dominant drag, so list scrolling is never hijacked', () => {
    const s = useSwipeAction({ threshold: 40, dominance: 2 })
    // 60px left but 50px down.
    swipe(s, 7, 300, 240, 50)
    expect(s.actionFor(7)).toBeNull()
  })

  it('ignores rightward swipes that start in the screen\'s left edge zone (the sidebar gesture owns it)', () => {
    const s = useSwipeAction({ edgeZone: 16 })
    swipe(s, 7, 8, 90)
    expect(s.actionFor(7)).toBeNull()
  })

  it('still allows a leftward swipe that starts near the left edge', () => {
    const s = useSwipeAction({ edgeZone: 16 })
    swipe(s, 7, 14, 14 - 60)
    expect(s.actionFor(7)).toBe('below')
  })

  it('toggles the panel away when the same row is swiped the same way again', () => {
    const s = useSwipeAction()
    swipe(s, 7, 300, 240)
    expect(s.actionFor(7)).toBe('below')

    swipe(s, 7, 300, 240)
    expect(s.actionFor(7)).toBeNull()
  })

  it('switches direction when the row is swiped the other way', () => {
    const s = useSwipeAction()
    swipe(s, 7, 300, 240)
    expect(s.actionFor(7)).toBe('below')

    swipe(s, 7, 120, 180)
    expect(s.actionFor(7)).toBe('above')
  })

  it('keeps only one row open at a time', () => {
    const s = useSwipeAction()
    swipe(s, 7, 300, 240)
    swipe(s, 8, 300, 240)

    expect(s.actionFor(8)).toBe('below')
    expect(s.actionFor(7)).toBeNull()
    expect(s.openId.value).toBe(8)
  })

  it('opens at most once per gesture, even as the drag continues', () => {
    const s = useSwipeAction()
    s.onTouchStart(ev('start', 300, 200), 7)
    s.onTouchMove(ev('move', 250, 200))
    expect(s.actionFor(7)).toBe('below')

    // Further movement in the same gesture must not toggle it back off.
    s.onTouchMove(ev('move', 200, 200))
    expect(s.actionFor(7)).toBe('below')
  })

  it('close() puts the panel away', () => {
    const s = useSwipeAction()
    swipe(s, 7, 300, 240)
    s.close()
    expect(s.actionFor(7)).toBeNull()
    expect(s.openId.value).toBeNull()
  })

  it('suppresses the drag\'s synthetic click so the panel stays open for the confirm tap', () => {
    const s = useSwipeAction({ clickGrace: 400 })
    expect(s.shouldSuppressClick()).toBe(false)

    swipe(s, 7, 300, 240)
    expect(s.shouldSuppressClick()).toBe(true)
  })

  it('suppresses only the first click after a swipe, so a real tap still dismisses', () => {
    const s = useSwipeAction({ clickGrace: 400 })
    swipe(s, 7, 300, 240)

    // The click the browser fires as the drag ends.
    expect(s.shouldSuppressClick()).toBe(true)
    // Any later tap is genuine and must reach the row.
    expect(s.shouldSuppressClick()).toBe(false)
    expect(s.shouldSuppressClick()).toBe(false)
  })

  it('does not suppress clicks after a tap that never swiped', () => {
    const s = useSwipeAction({ clickGrace: 400 })
    s.onTouchStart(ev('start', 300, 200), 7)
    s.onTouchMove(ev('move', 296, 200))
    s.onTouchEnd()

    expect(s.shouldSuppressClick()).toBe(false)
  })

  it('stops suppressing once the grace window has elapsed', () => {
    vi.useFakeTimers()
    try {
      const s = useSwipeAction({ clickGrace: 400 })
      swipe(s, 7, 300, 240)
      expect(s.shouldSuppressClick()).toBe(true)

      vi.advanceTimersByTime(500)
      expect(s.shouldSuppressClick()).toBe(false)
    } finally {
      vi.useRealTimers()
    }
  })

  it('ignores a touchMove with no active touchStart', () => {
    const s = useSwipeAction()
    s.onTouchMove(ev('move', 100, 200))
    expect(s.openId.value).toBeNull()
  })

  it('starts a fresh gesture after touchEnd, so a later swipe still opens', () => {
    const s = useSwipeAction()
    swipe(s, 7, 300, 240)
    swipe(s, 8, 300, 240)

    swipe(s, 7, 300, 240)
    expect(s.actionFor(7)).toBe('below')
  })
})
