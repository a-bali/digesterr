import { ref, type Ref } from 'vue'

/**
 * Swipe-left-to-reveal for list rows.
 *
 * Item rows keep their mark-above/below chevrons behind `group-hover`, which
 * touch devices have no reliable equivalent for, so the action is unreachable
 * on a phone. This reveals the same pair in the same place via a horizontal
 * swipe instead of inventing a second affordance.
 *
 * Gesture rules:
 *  - **Leftward only.** The chevrons sit at the row's right edge, so dragging
 *    the row leftward is the platform-conventional reveal. It also cannot
 *    collide with the rightward edge-swipe that opens the sidebar
 *    (`useSwipeGesture`) or with pull-to-refresh (`usePullToRefresh`), which
 *    cancels itself once horizontal drift passes `maxDriftX`.
 *  - **Horizontal must dominate.** The gesture is claimed only when horizontal
 *    travel exceeds `dominance` times the vertical travel, so vertical
 *    scrolling through the list is never hijacked.
 *  - **Reveal happens mid-drag**, as soon as the threshold is crossed, rather
 *    than on release — the row answers the finger.
 *
 * Rows should carry `touch-action: pan-y pinch-zoom` so the browser keeps
 * handling vertical panning natively (and pinch-zoom still works) while
 * horizontal drags reach these handlers.
 *
 * Only one row is revealed at a time. Revealing does NOT select the row: the
 * browser still fires a synthetic click after a drag, so `shouldSuppressClick`
 * guards the row's own click handler for a short grace window.
 */
export function useRowSwipeReveal(opts?: {
  /** Horizontal travel required to reveal (default 40) */
  threshold?: number
  /** How far horizontal must beat vertical (default 2) */
  dominance?: number
  /** Window after a reveal in which the row's click is ignored (default 400) */
  clickGrace?: number
}) {
  const threshold = opts?.threshold ?? 40
  const dominance = opts?.dominance ?? 2
  const clickGrace = opts?.clickGrace ?? 400

  /** Id of the row currently revealed, or null. */
  const revealedId: Ref<number | null> = ref(null)

  let startX = 0
  let startY = 0
  let candidateId: number | null = null
  let claimed = false
  let suppressClickUntil = 0

  function onTouchStart(e: TouchEvent, id: number) {
    const touch = e.touches[0]
    if (!touch) return
    startX = touch.clientX
    startY = touch.clientY
    candidateId = id
    claimed = false
  }

  function onTouchMove(e: TouchEvent) {
    if (candidateId === null || claimed) return
    const touch = e.touches[0]
    if (!touch) return
    const dx = touch.clientX - startX
    const dy = touch.clientY - startY

    // Leftward and clearly horizontal, past the threshold. Anything else is
    // left to the browser (vertical scroll, sidebar edge-swipe, taps).
    if (dx > -threshold) return
    if (Math.abs(dx) <= Math.abs(dy) * dominance) return

    claimed = true
    suppressClickUntil = Date.now() + clickGrace
    // Swiping the already-revealed row puts it away again.
    revealedId.value = revealedId.value === candidateId ? null : candidateId
  }

  function onTouchEnd() {
    candidateId = null
    claimed = false
  }

  function reveal(id: number) { revealedId.value = id }
  function dismiss() { revealedId.value = null }
  function toggle(id: number) { revealedId.value = revealedId.value === id ? null : id }
  function isRevealed(id: number) { return revealedId.value === id }
  /** True briefly after a reveal, so the drag's synthetic click is ignored. */
  function shouldSuppressClick() { return Date.now() < suppressClickUntil }

  return {
    revealedId,
    onTouchStart,
    onTouchMove,
    onTouchEnd,
    reveal,
    dismiss,
    toggle,
    isRevealed,
    shouldSuppressClick,
  }
}
