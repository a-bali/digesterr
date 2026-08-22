import { describe, it, expect, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { defineComponent, h } from 'vue'
import { useKeyboardShortcuts } from './useKeyboardShortcuts'

function makeCtx(overrides: Partial<Parameters<typeof useKeyboardShortcuts>[0]> = {}) {
  return {
    items: () => [
      { id: 1, url: 'https://example.com/1' },
      { id: 2, url: 'https://example.com/2' },
      { id: 3, url: 'https://example.com/3' },
    ],
    selectedItem: () => null as any,
    feeds: () => [{ id: 10 }, { id: 20 }, { id: 30 }],
    activeFeedId: () => 10 as number | null,
    selectItem: vi.fn(),
    selectFeed: vi.fn(),
    toggleRead: vi.fn(),
    toggleSaved: vi.fn(),
    markAllRead: vi.fn(),
    toggleShowRead: vi.fn(),
    refreshCounts: vi.fn(),
    toggleTheme: vi.fn(),
    openHelp: vi.fn(),
    ...overrides,
  }
}

// Mount the composable inside a throwaway component so onMounted registers
// the window listener, then drive keydown events through window. The ctx is
// made stateful: selectItem updates the "current" item so consecutive
// presses advance through the list like the real UI.
function mountHarness(base: ReturnType<typeof makeCtx>) {
  const current = { item: base.selectedItem() as any, feedId: base.activeFeedId() }
  const ctx = {
    ...base,
    selectedItem: () => current.item,
    activeFeedId: () => current.feedId,
    selectItem: (item: any) => { current.item = item; base.selectItem(item) },
    selectFeed: (id: number) => { current.feedId = id; base.selectFeed(id) },
  }
  const wrapper = mount(defineComponent({
    setup() {
      useKeyboardShortcuts(ctx as Parameters<typeof useKeyboardShortcuts>[0])
      return () => h('div')
    },
  }))
  const press = (init: Partial<KeyboardEventInit> & { key: string }, target?: EventTarget) => {
    const ev = new KeyboardEvent('keydown', { cancelable: true, ...init })
    const t = target ?? window
    // jsdom's KeyboardEvent has a readonly `target` set at dispatch; define
    // it beforehand so the handler sees the simulated target.
    try { Object.defineProperty(ev, 'target', { value: t, configurable: true, writable: true }) } catch { /* ignore */ }
    t.dispatchEvent(ev)
    return ev
  }
  return { wrapper, press }
}

describe('useKeyboardShortcuts', () => {
  it('j/n/ArrowRight selects the next item', () => {
    const ctx = makeCtx({ selectedItem: () => ({ id: 1 }) })
    const { press, wrapper } = mountHarness(ctx)
    press({ key: 'j' })
    press({ key: 'n' })
    press({ key: 'ArrowRight' })
    expect(ctx.selectItem).toHaveBeenCalledTimes(3)
    expect(ctx.selectItem).toHaveBeenNthCalledWith(1, expect.objectContaining({ id: 2 }))
    expect(ctx.selectItem).toHaveBeenNthCalledWith(3, expect.objectContaining({ id: 3 }))
    wrapper.unmount()
  })

  it('k/p/ArrowLeft selects the previous item', () => {
    const ctx = makeCtx({ selectedItem: () => ({ id: 3 }) })
    const { press, wrapper } = mountHarness(ctx)
    press({ key: 'k' })
    expect(ctx.selectItem).toHaveBeenCalledWith(expect.objectContaining({ id: 2 }))
    press({ key: 'ArrowLeft' })
    expect(ctx.selectItem).toHaveBeenLastCalledWith(expect.objectContaining({ id: 1 }))
    wrapper.unmount()
  })

  it('clamps navigation at list boundaries', () => {
    const ctx = makeCtx({ selectedItem: () => ({ id: 1 }) })
    const { press, wrapper } = mountHarness(ctx)
    press({ key: 'k' }) // already first
    expect(ctx.selectItem).toHaveBeenCalledWith(expect.objectContaining({ id: 1 }))
    const ctx2 = makeCtx({ selectedItem: () => ({ id: 3 }) })
    const h2 = mountHarness(ctx2)
    h2.press({ key: 'End' })
    expect(ctx2.selectItem).toHaveBeenCalledWith(expect.objectContaining({ id: 3 }))
    h2.wrapper.unmount()
    wrapper.unmount()
  })

  it('Home and End jump to first/last item', () => {
    const ctx = makeCtx()
    const { press, wrapper } = mountHarness(ctx)
    press({ key: 'Home' })
    expect(ctx.selectItem).toHaveBeenCalledWith(expect.objectContaining({ id: 1 }))
    press({ key: 'End' })
    expect(ctx.selectItem).toHaveBeenLastCalledWith(expect.objectContaining({ id: 3 }))
    wrapper.unmount()
  })

  it('m and Space toggle read on the selected item', () => {
    const sel = { id: 2 }
    const ctx = makeCtx({ selectedItem: () => sel })
    const { press, wrapper } = mountHarness(ctx)
    press({ key: 'm' })
    press({ key: ' ' })
    expect(ctx.toggleRead).toHaveBeenCalledTimes(2)
    expect(ctx.toggleRead).toHaveBeenCalledWith(sel)
    wrapper.unmount()
  })

  it('s toggles saved on the selected item', () => {
    const sel = { id: 2 }
    const ctx = makeCtx({ selectedItem: () => sel })
    const { press, wrapper } = mountHarness(ctx)
    press({ key: 's' })
    expect(ctx.toggleSaved).toHaveBeenCalledWith(sel)
    wrapper.unmount()
  })

  it('v opens the item URL', () => {
    const openSpy = vi.spyOn(window, 'open').mockImplementation(() => null)
    const ctx = makeCtx({ selectedItem: () => ({ id: 1, url: 'https://example.com/x' }) })
    const { press, wrapper } = mountHarness(ctx)
    press({ key: 'v' })
    expect(openSpy).toHaveBeenCalledWith('https://example.com/x', '_blank')
    openSpy.mockRestore()
    wrapper.unmount()
  })

  it('ArrowUp/ArrowDown switch feeds', () => {
    const ctx = makeCtx()
    const { press, wrapper } = mountHarness(ctx)
    press({ key: 'ArrowDown' })
    expect(ctx.selectFeed).toHaveBeenCalledWith(20)
    press({ key: 'ArrowUp' })
    expect(ctx.selectFeed).toHaveBeenLastCalledWith(10)
    wrapper.unmount()
  })

  it('u toggles the show-read filter', () => {
    const ctx = makeCtx()
    const { press, wrapper } = mountHarness(ctx)
    press({ key: 'u' })
    expect(ctx.toggleShowRead).toHaveBeenCalledTimes(1)
    wrapper.unmount()
  })

  it('Shift+A marks all read', () => {
    const ctx = makeCtx()
    const { press, wrapper } = mountHarness(ctx)
    press({ key: 'A', shiftKey: true })
    expect(ctx.markAllRead).toHaveBeenCalledTimes(1)
    wrapper.unmount()
  })

  it('r refreshes counts', () => {
    const ctx = makeCtx()
    const { press, wrapper } = mountHarness(ctx)
    press({ key: 'r' })
    expect(ctx.refreshCounts).toHaveBeenCalledTimes(1)
    wrapper.unmount()
  })

  it('Shift+D toggles the theme', () => {
    const ctx = makeCtx()
    const { press, wrapper } = mountHarness(ctx)
    press({ key: 'D', shiftKey: true })
    expect(ctx.toggleTheme).toHaveBeenCalledTimes(1)
    wrapper.unmount()
  })

  it('? opens help', () => {
    const ctx = makeCtx()
    const { press, wrapper } = mountHarness(ctx)
    press({ key: '?' })
    expect(ctx.openHelp).toHaveBeenCalledTimes(1)
    wrapper.unmount()
  })

  it('h opens help only when no item is selected', () => {
    const ctx = makeCtx()
    const { press, wrapper } = mountHarness(ctx)
    press({ key: 'h' })
    expect(ctx.openHelp).toHaveBeenCalledTimes(1)
    const ctx2 = makeCtx({ selectedItem: () => ({ id: 1 }) })
    const h2 = mountHarness(ctx2)
    h2.press({ key: 'h' })
    expect(ctx2.openHelp).not.toHaveBeenCalled()
    h2.wrapper.unmount()
    wrapper.unmount()
  })

  it('ignores keys typed in inputs, textareas, selects and contentEditable', () => {
    const ctx = makeCtx()
    const { press, wrapper } = mountHarness(ctx)
    const input = document.createElement('input')
    document.body.appendChild(input)
    input.focus()
    press({ key: 'j' }, input)
    expect(ctx.selectItem).not.toHaveBeenCalled()
    const editable = document.createElement('div')
    editable.contentEditable = 'true'
    document.body.appendChild(editable)
    editable.focus()
    press({ key: 'j' }, editable)
    expect(ctx.selectItem).not.toHaveBeenCalled()
    input.remove()
    editable.remove()
    wrapper.unmount()
  })

  it('ignores ctrl/meta/alt combos', () => {
    const ctx = makeCtx({ selectedItem: () => ({ id: 1 }) })
    const { press, wrapper } = mountHarness(ctx)
    press({ key: 'j', ctrlKey: true })
    press({ key: 's', metaKey: true })
    press({ key: 'v', altKey: true })
    expect(ctx.selectItem).not.toHaveBeenCalled()
    expect(ctx.toggleSaved).not.toHaveBeenCalled()
    wrapper.unmount()
  })

  it('does nothing with an empty item list', () => {
    const ctx = makeCtx({ items: () => [] })
    const { press, wrapper } = mountHarness(ctx)
    press({ key: 'j' })
    press({ key: 'Home' })
    expect(ctx.selectItem).not.toHaveBeenCalled()
    wrapper.unmount()
  })

  it('Space with no selected item does not toggle read or preventDefault', () => {
    const ctx = makeCtx()
    const { press, wrapper } = mountHarness(ctx)
    // dispatchEvent returns synchronously, so defaultPrevented is final here.
    const ev = press({ key: ' ' })
    expect(ctx.toggleRead).not.toHaveBeenCalled()
    expect(ev.defaultPrevented).toBe(false)
    wrapper.unmount()
  })

  it('Space with a selected item toggles read and preventDefaults', () => {
    const sel = { id: 1 }
    const ctx = makeCtx({ selectedItem: () => sel })
    const { press, wrapper } = mountHarness(ctx)
    const ev = press({ key: ' ' })
    expect(ctx.toggleRead).toHaveBeenCalledWith(sel)
    expect(ev.defaultPrevented).toBe(true)
    wrapper.unmount()
  })

  it('skips contentEditable targets', () => {
    const ctx = makeCtx({ selectedItem: () => ({ id: 1 }) })
    const { press, wrapper } = mountHarness(ctx)
    const editable = document.createElement('div')
    editable.contentEditable = 'true'
    document.body.appendChild(editable)
    editable.focus()
    press({ key: ' ' }, editable)
    press({ key: 'm' }, editable)
    expect(ctx.toggleRead).not.toHaveBeenCalled()
    editable.remove()
    wrapper.unmount()
  })
})
