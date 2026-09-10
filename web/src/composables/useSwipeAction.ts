import { ref, type Ref } from 'vue'

/** Which set of items a swipe action would mark: the ones after the row
 *  ("below") or the ones before it ("above"), in the pane's current order. */
export type SwipeActionDirection = 'above' | 'below'

/**
 * Swipe-to-open confirm panel for list rows.
 *
 * Item rows keep their mark-above/below chevrons behind `group-hover`, which
 * touch devices have no equivalent for, so on a phone the action is reached by
 * swiping the row instead:
 *
 *  - **Swipe right-to-left** (finger travels left) opens a panel that slides in
 *    from the row's right edge reading "Mark all read below".
 *  - **Swipe left-to-right** opens the mirror panel from the left edge reading
 *    "Mark all read above".
 *
 * The panel then STAYS open until the user taps it — the two-step shape the
 * user chose, so a gesture that catches hundreds of items is never committed by
 * an accidental swipe. Swiping the same row the same way again, or swiping the
 * other way, or scrolling, or tapping the row, all put it away.
 *
 * Gesture rules:
 *  - **Horizontal must dominate** (`|dx| > dominance * |dy|`), so vertical
 *    scrolling through the list is never hijacked.
 *  - **Rightward swipes starting in the left edge zone are ignored**, because
 *    that is the sidebar's edge-swipe (`useSwipeGesture` claims x <= 12px).
 *    iOS's own back-swipe also owns that band, so it is not worth competing for.
 *    Leftward swipes from that band are unaffected — only the rightward
 *    direction collides.
 *  - Leftward swipes are safe from both: the sidebar gesture is rightward-only
 *    and `usePullToRefresh` cancels itself past its horizontal drift limit.
 */
export function useSwipeAction(opts?: {
  /** Horizontal travel required to open the panel (default 40) */
  threshold?: number
  /** How far horizontal must beat vertical (default 2) */
  dominance?: number
  /** Touch starts at or left of this x belong to the sidebar edge-swipe (default 16) */
  edgeZone?: number
  /** Window after a swipe in which the row's synthetic click is ignored (default 400) */
  clickGrace?: number
}) {
  const threshold = opts?.threshold ?? 40
  const dominance = opts?.dominance ?? 2
  const edgeZone = opts?.edgeZone ?? 16
  const clickGrace = opts?.clickGrace ?? 400

  /** Row whose panel is open, or null. Only ever one row at a time. */
  const openId: Ref<number | null> = ref(null)
  /** Which panel that row shows. */
  const direction: Ref<SwipeActionDirection | null> = ref(null)

  let startX = 0
  let startY = 0
  let candidateId: number | null = null
  let claimed = false
  let suppressClick = false
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

    if (Math.abs(dx) < threshold) return
    if (Math.abs(dx) <= Math.abs(dy) * dominance) return

    const next: SwipeActionDirection = dx < 0 ? 'below' : 'above'
    // Rightward swipes that began in the screen's left edge zone belong to the
    // sidebar gesture (and to iOS's own back-swipe), so leave those alone. A
    // leftward swipe from the same band is harmless and still ours.
    if (next === 'above' && startX <= edgeZone) return

    claimed = true
    suppressClick = true
    suppressClickUntil = Date.now() + clickGrace

    // Same row, same direction: the swipe acts as a toggle, so the panel can
    // be dismissed the same way it was opened.
    if (openId.value === candidateId && direction.value === next) {
      openId.value = null
      direction.value = null
      return
    }
    openId.value = candidateId
    direction.value = next
  }

  function onTouchEnd() {
    candidateId = null
    claimed = false
  }

  /** Which panel (if any) this row is showing. */
  function actionFor(id: number): SwipeActionDirection | null {
    return openId.value === id ? direction.value : null
  }

  function close() {
    openId.value = null
    direction.value = null
  }

  /**
   * True for exactly the ONE click the browser fires when a swipe drag ends,
   * and false for anything after it.
   *
   * Consuming a single click rather than ignoring every click for a fixed
   * window matters: the panel is dismissed by tapping the row, and a window
   * long enough to be safe (~400ms) also swallowed a genuine tap made straight
   * after the swipe, leaving the panel stuck open. The window remains only as a
   * backstop, so a drag that produces no click at all cannot leave the flag set
   * to eat a later tap.
   */
  function shouldSuppressClick() {
    if (!suppressClick) return false
    suppressClick = false
    return Date.now() < suppressClickUntil
  }

  return {
    openId,
    direction,
    onTouchStart,
    onTouchMove,
    onTouchEnd,
    actionFor,
    close,
    shouldSuppressClick,
  }
}
