import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { ref } from 'vue'
import { mount } from '@vue/test-utils'
import { defineComponent, h, type Ref } from 'vue'
import { usePullToRefresh } from './usePullToRefresh'

// Minimal TouchEvent harness: jsdom has no TouchEvent, so dispatch plain
// Events with a `touches` array attached. Bubbling is required — the
// composable listens at document level.
function touch(el: Element, type: string, x: number, y: number) {
  const ev = new Event(type, { cancelable: true, bubbles: true }) as any
  ev.touches = [{ clientX: x, clientY: y }]
  el.dispatchEvent(ev)
  return ev
}

describe('usePullToRefresh', () => {
  let outer: HTMLElement
  let container: HTMLElement
  let child: HTMLElement
  let otherPane: HTMLElement

  beforeEach(() => {
    document.body.innerHTML = ''
    // Structure mirroring HomeView: two sibling scrollers inside one app root.
    outer = document.createElement('div')
    container = document.createElement('div')
    child = document.createElement('p')
    otherPane = document.createElement('div')
    child.textContent = 'item row'
    container.appendChild(child)
    outer.appendChild(container)
    outer.appendChild(otherPane)
    document.body.appendChild(outer)

    // jsdom has no layout: stub scrollTop as always 0 (list at top).
    Object.defineProperty(container, 'scrollTop', { value: 0, configurable: true })
  })

  afterEach(() => {
    document.body.innerHTML = ''
  })

  // Mount a throwaway component so the composable's onMounted listeners bind.
  function mounted() {
    const onRefresh = vi.fn().mockResolvedValue(undefined)
    let api!: ReturnType<typeof usePullToRefresh>
    mount(defineComponent({
      setup() {
        api = usePullToRefresh(ref(container) as Ref<HTMLElement | null>, onRefresh)
        return () => h('div')
      },
    }))
    return { onRefresh, api }
  }

  it('triggers refresh after a downward drag past threshold', async () => {
    const { onRefresh } = mounted()

    touch(child, 'touchstart', 100, 200)
    touch(document.body, 'touchmove', 100, 360) // dy=160 → pull=70 ≥ threshold
    touch(document.body, 'touchend')
    await Promise.resolve()

    expect(onRefresh).toHaveBeenCalledTimes(1)
  })

  it('ignores upward drags (normal scroll)', async () => {
    const { onRefresh } = mounted()

    touch(child, 'touchstart', 100, 300)
    touch(document.body, 'touchmove', 100, 250)
    touch(document.body, 'touchend')

    expect(onRefresh).not.toHaveBeenCalled()
  })

  it('does NOT claim touches starting outside the container (content-pane scroll regression)', async () => {
    const { onRefresh, api } = mounted()

    // Drag down inside the OTHER pane (e.g. article content view). This must
    // scroll natively, not trigger our pull — previously preventDefault was
    // called and the indicator flashed.
    touch(otherPane, 'touchstart', 100, 200)
    const mv = touch(document.body, 'touchmove', 100, 320)
    touch(document.body, 'touchend')

    expect(onRefresh).not.toHaveBeenCalled()
    expect(api.pull.value).toBe(0)
    // Native scrolling not blocked for the foreign gesture.
    expect(mv.defaultPrevented).toBe(false)
  })

  it('cancels when the list scrolls away from top mid-gesture', async () => {
    const { onRefresh } = mounted()

    touch(child, 'touchstart', 100, 200)
    touch(document.body, 'touchmove', 100, 300)
    // User's finger also scrolled the list: no longer at top.
    Object.defineProperty(container, 'scrollTop', { value: 40, configurable: true })
    touch(document.body, 'touchmove', 100, 400)
    touch(document.body, 'touchend')

    expect(onRefresh).not.toHaveBeenCalled()
  })
})
