import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { ref, nextTick, createApp, defineComponent, h } from 'vue'
import { useSwipeGesture } from './useSwipeGesture'

// jsdom may lack Touch constructor; polyfill if missing.
if (typeof Touch === 'undefined') {
  // eslint-disable-next-line @typescript-eslint/no-extraneous-class
  globalThis.Touch = class Touch { constructor(init: any) { Object.assign(this, init) } } as any
}

// Helper: mount the composable inside a minimal component so onMounted/onUnmounted fire.
function mountSwipe(opts?: { enabled?: boolean; onSwipe?: (d: number) => void; edgeWidth?: number; threshold?: number; maxDuration?: number }) {
  const enabled = ref(opts?.enabled ?? true)
  const onSwipe = opts?.onSwipe ?? vi.fn()
  const app = createApp(defineComponent({
    setup() {
      useSwipeGesture(enabled, onSwipe, opts)
      return () => h('div')
    },
  }))
  const el = document.createElement('div')
  document.body.appendChild(el)
  app.mount(el)
  return { enabled, onSwipe, el, app }
}

function touchStart(x: number, y: number) {
  const e = new TouchEvent('touchstart', {
    touches: [new Touch({ identifier: 0, target: document.body, clientX: x, clientY: y })],
    bubbles: true,
    cancelable: true,
  })
  vi.spyOn(e, 'preventDefault')
  document.dispatchEvent(e)
  return e
}

function touchMove(x: number, y: number) {
  document.dispatchEvent(new TouchEvent('touchmove', {
    touches: [new Touch({ identifier: 0, target: document.body, clientX: x, clientY: y })],
    bubbles: true,
  }))
}

function touchEnd(x: number, y: number) {
  document.dispatchEvent(new TouchEvent('touchend', {
    changedTouches: [new Touch({ identifier: 0, target: document.body, clientX: x, clientY: y })],
    bubbles: true,
  }))
}

let current: ReturnType<typeof mountSwipe>

afterEach(() => {
  current?.app.unmount()
  document.body.innerHTML = ''
})

describe('useSwipeGesture', () => {
  beforeEach(() => {
    current = mountSwipe()
  })

  it('fires onSwipe when left-edge swipe exceeds threshold', async () => {
    touchStart(5, 100)
    touchMove(80, 102)
    touchEnd(100, 104)
    await nextTick()
    expect(current.onSwipe).toHaveBeenCalledOnce()
    expect(current.onSwipe).toHaveBeenCalledWith(95)
  })

  it('does not fire when touch starts away from left edge', async () => {
    touchStart(50, 100)
    touchMove(120, 102)
    touchEnd(130, 104)
    await nextTick()
    expect(current.onSwipe).not.toHaveBeenCalled()
  })

  it('does not fire when vertical movement exceeds horizontal', async () => {
    touchStart(5, 100)
    touchMove(30, 200)
    touchEnd(30, 200)
    await nextTick()
    expect(current.onSwipe).not.toHaveBeenCalled()
  })

  it('does not fire when disabled', async () => {
    current.enabled.value = false
    await nextTick()
    touchStart(5, 100)
    touchMove(80, 102)
    touchEnd(100, 104)
    await nextTick()
    expect(current.onSwipe).not.toHaveBeenCalled()
  })

  it('does not fire when swipe is too short', async () => {
    touchStart(5, 100)
    touchMove(40, 102)
    touchEnd(45, 102)
    await nextTick()
    expect(current.onSwipe).not.toHaveBeenCalled()
  })

  it('respects custom edgeWidth and threshold', async () => {
    // Unmount the beforeEach instance first, otherwise its still-attached
    // enabled listener leaks into this and later tests.
    current.app.unmount()
    const onSwipe = vi.fn()
    current = mountSwipe({ edgeWidth: 40, threshold: 100, onSwipe })
    touchStart(35, 100)
    touchEnd(145, 102)
    await nextTick()
    expect(onSwipe).toHaveBeenCalledOnce()
    expect(onSwipe).toHaveBeenCalledWith(110)
  })

  it('preventDefaults touchstart in the edge zone to suppress browser back-swipe', async () => {
    const e = touchStart(5, 100)
    expect(e.preventDefault).toHaveBeenCalled()
  })

  it('does not preventDefault touchstart outside the edge zone', async () => {
    const e = touchStart(50, 100)
    expect(e.preventDefault).not.toHaveBeenCalled()
  })

  it('does not preventDefault when disabled', async () => {
    // Remount fresh so no still-mounted enabled listener from a prior test
    // also receives this event.
    current.app.unmount()
    current = mountSwipe({ enabled: false })
    await nextTick()
    const e = touchStart(5, 100)
    expect(e.preventDefault).not.toHaveBeenCalled()
  })
})
