import { onMounted, onUnmounted, type Ref } from 'vue'

/**
 * Detect a rightward swipe starting from the left edge of the viewport.
 *
 * Constraints:
 *  - touchstart.x must be within `edgeWidth` px of the left edge
 *  - horizontal travel must exceed `threshold` px
 *  - horizontal travel must dominate vertical travel (|dx| > |dy|)
 *  - gesture must complete within `maxDuration` ms
 *
 * When all constraints are met, `onSwipe` fires once per gesture.
 * The callback receives the gesture's horizontal travel in px.
 *
 * Browser back-gesture suppression: listeners are registered as
 * non-passive, and when a touch starts inside the edge zone we call
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
  },
) {
  const edgeWidth = opts?.edgeWidth ?? 20
  const threshold = opts?.threshold ?? 60
  const maxDuration = opts?.maxDuration ?? 400

  let startX = 0
  let startY = 0
  let startTime = 0
  let active = false

  function onTouchStart(e: TouchEvent) {
    if (!enabled.value) return
    const touch = e.touches[0]
    if (touch.clientX > edgeWidth) return
    // Suppress the browser's native back-swipe for touches in our zone.
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
    // Cancel if the gesture goes left or scrolls vertically more than horizontally.
    if (dx < 0 || dy > dx) {
      active = false
      return
    }
    // Keep suppressing the browser gesture while ours is still live.
    e.preventDefault()
  }

  function onTouchEnd(e: TouchEvent) {
    if (!active) return
    active = false
    const elapsed = Date.now() - startTime
    if (elapsed > maxDuration) return
    const touch = e.changedTouches[0]
    const dx = touch.clientX - startX
    if (dx >= threshold) onSwipe(dx)
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
