import { ref, onMounted, onUnmounted, type Ref } from 'vue'

/**
 * Pull-down-to-refresh gesture for a scrollable list element.
 *
 * Activates when a touch starts while the container is scrolled to the top
 * (scrollTop === 0) and drags downward. As the pull passes `threshold`, an
 * indicator drops in; on release past the threshold `onRefresh` fires once
 * and the indicator shows until the returned promise settles.
 *
 * Browser pull-to-refresh conflict: the container must set
 * `overscroll-behavior-y: contain` so Android Chrome/Firefox don't run
 * their native refresh in parallel. Touch listeners are non-passive and
 * call preventDefault() while our gesture is live, which also blocks the
 * native gesture. iOS Safari has no native pull-to-refresh, only harmless
 * rubber-banding.
 */
export function usePullToRefresh(
  container: Ref<HTMLElement | null>,
  onRefresh: () => Promise<unknown> | unknown,
  opts?: {
    /** Min downward travel to trigger (default 70px) */
    threshold?: number
    /** Max x drift allowed before cancelling (default 60px) */
    maxDriftX?: number
    /** Enabled flag (default true) */
    enabled?: Ref<boolean>
  },
) {
  const threshold = opts?.threshold ?? 70
  const maxDriftX = opts?.maxDriftX ?? 60
  const enabled = opts?.enabled

  // Pull distance in px while dragging, 0 when idle. Drives the indicator's
  // translate/opacity directly (style binding), no transition needed live.
  const pull = ref(0)
  // True after release past threshold, until onRefresh's promise resolves.
  const refreshing = ref(false)

  let startY = 0
  let startX = 0
  let tracking = false

  function onTouchStart(e: TouchEvent) {
    if (enabled && !enabled.value) return
    if (refreshing.value) return
    if (!container.value || container.value.scrollTop > 0) return
    // Only claim touches that begin INSIDE this container. Without this, a
    // downward drag starting in another scroller (e.g. the content pane on
    // mobile) gets mistaken for a pull: preventDefault blocks that pane's
    // native scrolling and the indicator flashes.
    if (!(e.target instanceof Node) || !container.value.contains(e.target)) return
    const touch = e.touches[0]
    startY = touch.clientY
    startX = touch.clientX
    tracking = true
    // Not preventing here yet — we only claim the gesture once a clear
    // downward drag develops, so taps/scroll-ups still behave natively.
  }

  function onTouchMove(e: TouchEvent) {
    if (!tracking) return
    const touch = e.touches[0]
    const dy = touch.clientY - startY
    const dx = Math.abs(touch.clientX - startX)
    if (dy <= 0 || dx > maxDriftX) {
      // Upward or horizontal movement: not a pull. Cancel silently without
      // preventDefault so normal scrolling is unaffected.
      if (pull.value > 0) pull.value = 0
      tracking = dy <= 0 ? false : tracking
      return
    }
    if (container.value && container.value.scrollTop > 0) {
      pull.value = 0
      tracking = false
      return
    }
    // Claim the gesture: block native scrolling/pull-to-refresh from here on.
    e.preventDefault()
    // Dampen the pull so the indicator feels physical (drag 2x to move 1x).
    pull.value = Math.min(dy / 2, threshold + 30)
  }

  async function onTouchEnd() {
    if (!tracking) return
    tracking = false
    const triggered = pull.value >= threshold
    pull.value = 0
    if (!triggered) return
    refreshing.value = true
    try {
      await onRefresh()
    } finally {
      refreshing.value = false
    }
  }

  onMounted(() => {
    document.addEventListener('touchstart', onTouchStart, { passive: true })
    document.addEventListener('touchmove', onTouchMove, { passive: false })
    document.addEventListener('touchend', onTouchEnd, { passive: true })
    document.addEventListener('touchcancel', onTouchEnd, { passive: true })
  })

  onUnmounted(() => {
    document.removeEventListener('touchstart', onTouchStart)
    document.removeEventListener('touchmove', onTouchMove)
    document.removeEventListener('touchend', onTouchEnd)
    document.removeEventListener('touchcancel', onTouchEnd)
  })

  return { pull, refreshing }
}
