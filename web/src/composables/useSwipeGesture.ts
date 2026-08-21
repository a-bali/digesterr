import { onMounted, onUnmounted, type Ref } from 'vue'

/**
 * Detect a rightward swipe starting from the left edge of the viewport
 * (opens the sidebar) and an optional leftward swipe from the right edge
 * (navigates back from the content pane to the list pane).
 *
 * Constraints (both gestures):
 *  - touchstart.x must be within `edgeWidth` px of the respective edge
 *  - horizontal travel must exceed `threshold` px
 *  - horizontal travel must dominate vertical travel (|dx| > |dy|)
 *  - gesture must complete within `maxDuration` ms
 *
 * When all constraints are met, the matching callback fires once per gesture
 * and receives the gesture's horizontal travel in px.
 *
 * Browser back-gesture suppression: listeners are registered as
 * non-passive, and when a touch starts inside an edge zone we call
 * `preventDefault()` on touchstart/touchmove. This stops mobile browsers
 * (Android Chrome/Firefox) from running their native history-back swipe in
 * parallel with the app gesture. iOS Safari's system edge swipe cannot be
 * suppressed; there the system gesture wins and the sidebar remains
 * reachable via its menu button.
 */
export function useSwipeGesture(
  enabled: Ref<boolean>,
  onSwipe: (distance: number) => void,
  opts?: {
    /** Max x on touchstart to qualify as an edge swipe (default 20) */
    edgeWidth?: number
    /** Min horizontal travel to trigger (default 60) */
    threshold?: number
    /** Max gesture duration in ms (default 400) */
    maxDuration?: number
    /** Max distance from the RIGHT edge for a back-swipe start.
     *  0 disables the right-edge gesture (default: same as edgeWidth). */
    rightEdgeWidth?: number
    /** Fired on a completed leftward right-edge swipe. */
    onBackSwipe?: () => void
  },
) {
  const edgeWidth = opts?.edgeWidth ?? 20
  const threshold = opts?.threshold ?? 60
  const maxDuration = opts?.maxDuration ?? 400
  const rightEdgeWidth = opts?.rightEdgeWidth ?? edgeWidth
  const onBackSwipe = opts?.onBackSwipe

  let startX = 0
  let startY = 0
  let startTime = 0
  let active = false
  // Which edge the live gesture started from: 'left' | 'right' | null.
  let edge: 'left' | 'right' | null = null

  function onTouchStart(e: TouchEvent) {
    if (!enabled.value) return
    const touch = e.touches[0]
    const vw = document.documentElement.clientWidth
    if (touch.clientX <= edgeWidth) {
      edge = 'left'
    } else if (onBackSwipe && rightEdgeWidth > 0 && touch.clientX >= vw - rightEdgeWidth) {
      edge = 'right'
    } else {
      return
    }
    // Suppress the browser's native back-swipe for touches in our zones.
    e.preventDefault()
    startX = touch.clientX
    startY = touch.clientY
    startTime = Date.now()
    active = true
  }

  function onTouchMove(e: TouchEvent) {
    if (!active) return
    const touch = e.touches[0]
    const dx = touch.clientX - startX
    const dy = Math.abs(touch.clientY - startY)
    // Cancel if the gesture moves away from its edge or scrolls vertically
    // more than horizontally.
    if ((edge === 'left' && dx < 0) || (edge === 'right' && dx > 0) || dy > Math.abs(dx)) {
      active = false
      edge = null
      return
    }
    // Keep suppressing the browser gesture while ours is still live.
    e.preventDefault()
  }

  function onTouchEnd(e: TouchEvent) {
    if (!active) return
    active = false
    const startedEdge = edge
    edge = null
    const elapsed = Date.now() - startTime
    if (elapsed > maxDuration) return
    const touch = e.changedTouches[0]
    const dx = touch.clientX - startX
    if (startedEdge === 'left' && dx >= threshold) onSwipe(dx)
    else if (startedEdge === 'right' && -dx >= threshold) onBackSwipe?.()
  }

  onMounted(() => {
    document.addEventListener('touchstart', onTouchStart, { passive: false })
    document.addEventListener('touchmove', onTouchMove, { passive: false })
    document.addEventListener('touchend', onTouchEnd, { passive: true })
  })

  onUnmounted(() => {
    document.removeEventListener('touchstart', onTouchStart)
    document.removeEventListener('touchmove', onTouchMove)
    document.removeEventListener('touchend', onTouchEnd)
  })
}
