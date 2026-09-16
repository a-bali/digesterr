import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { setActivePinia, createPinia } from 'pinia'
import { createRouter, createMemoryHistory } from 'vue-router'
import HomeView from './HomeView.vue'
import { feedApi, itemApi } from '../services/api'

vi.mock('../services/api', () => ({
  authApi: { signout: vi.fn(), changePassword: vi.fn() },
  feedApi: {
    list: vi.fn().mockResolvedValue([]),
    create: vi.fn(), update: vi.fn(), delete: vi.fn(),
    getItems: vi.fn().mockResolvedValue({ items: [], total: 0, has_more: false }),
    multiItems: vi.fn().mockResolvedValue({ items: [], total: 0, has_more: false }),
    fetch: vi.fn(), markRead: vi.fn(),
    markReadAll: vi.fn().mockResolvedValue({ message: 'ok' }),
    unreadCounts: vi.fn().mockResolvedValue({}),
  },
  itemApi: {
    updateStatus: vi.fn(), getItem: vi.fn(), extract: vi.fn(),
    markReadAbove: vi.fn(), markReadBelow: vi.fn(),
  },
  digestApi: {
    listConfigs: vi.fn().mockResolvedValue([]),
    createConfig: vi.fn(), updateConfig: vi.fn(), deleteConfig: vi.fn(), getConfig: vi.fn(),
    generate: vi.fn(),
    listResults: vi.fn().mockResolvedValue({ results: [], has_more: false }),
    getResult: vi.fn(), updateResultStatus: vi.fn(), deleteResult: vi.fn(),
    listResultItems: vi.fn().mockResolvedValue([]),
    unreadCounts: vi.fn().mockResolvedValue({}),
    markAllRead: vi.fn(), markReadAbove: vi.fn(), markReadBelow: vi.fn(),
  },
  logsApi: { list: vi.fn().mockResolvedValue({ entries: [], total: 0, has_more: false }) },
  setAuthToken: vi.fn(),
  getAuthToken: vi.fn(() => null),
  setOnUnauthorized: vi.fn(),
  ApiError: class extends Error {},
}))

// Query-aware matchMedia with LIVE listeners, so a test can drop the desktop
// breakpoint and let useBreakpoints() react — a static stub could not observe
// the "panes come back collapsed" trap.
let desktop = true
const mqls: { query: string; listeners: Set<() => void> }[] = []
function setDesktop(v: boolean) {
  desktop = v
  mqls.forEach(m => m.listeners.forEach(l => l()))
}
Object.defineProperty(window, 'matchMedia', {
  writable: true,
  configurable: true,
  value: (query: string) => {
    const entry = { query, listeners: new Set<() => void>() }
    if (/min-width/.test(query)) mqls.push(entry)
    return {
      get matches() { return desktop && /min-width/.test(query) },
      media: query,
      onchange: null,
      addListener: (l: any) => entry.listeners.add(l),
      removeListener: (l: any) => entry.listeners.delete(l),
      addEventListener: (_: any, l: any) => entry.listeners.add(l),
      removeEventListener: (_: any, l: any) => entry.listeners.delete(l),
      dispatchEvent: () => false,
    }
  },
})

class ResizeObserverStub { observe() {} unobserve() {} disconnect() {} }
vi.stubGlobal('ResizeObserver', ResizeObserverStub)

function makeFeed() {
  return {
    id: 1, user_id: 1, category: 'tech', type: 'hn', name: 'Hacker News',
    url: 'https://news.ycombinator.com/rss', config: '', min_score: 0,
    last_fetched_at: null, status: 'active', extract_content: 0,
    last_error: '', last_error_at: null, last_error_severity: '',
    created_at: '', updated_at: '',
  }
}

function makeItem(id: number, title = `item ${id}`) {
  return {
    id, feed_id: 1, title, url: `https://example.com/${id}`, author: '',
    content: '<p>body</p>', points: 1, comments_count: 0,
    published_at: '2026-09-10T08:00:00Z', status: 'unread',
  }
}

function router() {
  return createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/', component: HomeView },
      { path: '/feeds', component: HomeView },
      { path: '/feeds/:feedId/:itemId?', component: HomeView },
      { path: '/feeds/category/:category/:itemId?', component: HomeView },
      { path: '/digests', component: HomeView },
      { path: '/digests/:configId/:resultId?', component: HomeView },
    ],
  })
}

async function mountAt(path: string) {
  const r = router()
  r.push(path)
  await r.isReady()
  const wrapper = mount(HomeView, { global: { plugins: [r] } })
  await flushPromises()
  await flushPromises()
  return wrapper
}

function panels(wrapper: ReturnType<typeof mount>) {
  return wrapper.findAll('[data-layout="panes"] [data-panel]')
}

function collapsedPanelCount(wrapper: ReturnType<typeof mount>) {
  return panels(wrapper).filter(p => (p.attributes('style') || '').includes('flex: 0 1 0px')).length
}

function separators(wrapper: ReturnType<typeof mount>) {
  // Scope to actual splitter handles: Separator components inside the content
  // pane also carry role="separator", so the bare role selector is too loose.
  return wrapper.findAll('[data-layout="panes"] [data-resize-handle]')
}

async function pressX() {
  window.dispatchEvent(new KeyboardEvent('keydown', { key: 'x', bubbles: true }))
  await flushPromises()
}

function backButton(wrapper: ReturnType<typeof mount>) {
  return wrapper.findAll('header button').find(b => (b.text() || '').trim() === 'Back')
}

describe('HomeView: x maximizes the content pane (desktop 3-pane)', () => {
  beforeEach(() => {
    document.body.innerHTML = ''
    setActivePinia(createPinia())
    localStorage.clear()
    vi.clearAllMocks()
    desktop = true
    mqls.length = 0
    vi.mocked(feedApi.list).mockResolvedValue([makeFeed()] as never)
    vi.mocked(feedApi.getItems).mockResolvedValue({
      items: [makeItem(1, 'First item'), makeItem(2, 'Second item')], total: 2, has_more: false,
    } as never)
    vi.mocked(itemApi.getItem).mockResolvedValue(makeItem(1, 'First item') as never)
  })

  it('collapses the sidebar and list panels on x, keeping the content pane', async () => {
    const wrapper = await mountAt('/feeds/1/1')
    expect(collapsedPanelCount(wrapper)).toBe(0)

    await pressX()

    expect(panels(wrapper)).toHaveLength(3)
    // The first two panels are collapsed; the content pane is untouched.
    expect(collapsedPanelCount(wrapper)).toBe(2)
    expect((panels(wrapper)[2].attributes('style') || '')).not.toContain('flex: 0 1 0px')
    // The item stays on screen — maximizing must not deselect.
    expect(wrapper.text()).toContain('First item')
  })

  it('hides the resize handles while maximized', async () => {
    const wrapper = await mountAt('/feeds/1/1')
    expect(separators(wrapper)).toHaveLength(2)
    expect(separators(wrapper).every(h => !h.classes().includes('hidden'))).toBe(true)

    await pressX()

    expect(separators(wrapper).every(h => h.classes().includes('hidden'))).toBe(true)
    // NOTE: the computed display:none is deliberately NOT asserted here — vitest
    // loads no stylesheet, so jsdom cannot evaluate the Tailwind `hidden` class.
    // It is verified live in the browser instead (getComputedStyle on the
    // handles), because a hidden-but-still-4px-wide handle would offset the row.
  })

  it('swaps the logo for a Back button in the header', async () => {
    const wrapper = await mountAt('/feeds/1/1')
    expect(backButton(wrapper)).toBeUndefined()
    expect(wrapper.find('[data-app-logo]').exists()).toBe(true)

    await pressX()

    expect(backButton(wrapper), 'header must offer a way back').toBeTruthy()
    expect(wrapper.find('[data-app-logo]').exists()).toBe(false)
  })

  it('restores the 3-pane view when x is pressed again', async () => {
    const wrapper = await mountAt('/feeds/1/1')
    await pressX()
    expect(collapsedPanelCount(wrapper)).toBe(2)

    await pressX()

    expect(collapsedPanelCount(wrapper)).toBe(0)
    expect(separators(wrapper).every(h => !h.classes().includes('hidden'))).toBe(true)
    expect(wrapper.find('[data-app-logo]').exists()).toBe(true)
    expect(backButton(wrapper)).toBeUndefined()
  })

  it('restores the 3-pane view from the Back button WITHOUT dropping the selection', async () => {
    const wrapper = await mountAt('/feeds/1/1')
    await pressX()

    await backButton(wrapper)!.trigger('click')
    await flushPromises()

    expect(collapsedPanelCount(wrapper)).toBe(0)
    expect(wrapper.find('[data-app-logo]').exists()).toBe(true)
    // Back = "restore the 3-pane view", not "leave the content pane": the
    // selected item is still open.
    expect(wrapper.text()).toContain('First item')
    expect(wrapper.text()).not.toContain('Select an item')
  })

  it('is a no-op with nothing selected', async () => {
    const wrapper = await mountAt('/feeds/1')
    expect(wrapper.text()).toContain('Select an item')

    await pressX()

    expect(collapsedPanelCount(wrapper)).toBe(0)
    expect(backButton(wrapper)).toBeUndefined()
  })

  it('is a no-op below the desktop breakpoint', async () => {
    desktop = false
    const wrapper = await mountAt('/feeds/1/1')

    await pressX()

    // No desktop 3-pane group is rendered at all, so nothing can collapse.
    expect(wrapper.find('[data-layout="panes"]').exists()).toBe(false)
  })

  it('auto-restores when the desktop breakpoint is lost while maximized', async () => {
    const wrapper = await mountAt('/feeds/1/1')
    await pressX()
    expect(collapsedPanelCount(wrapper)).toBe(2)

    setDesktop(false)
    await flushPromises()

    // Coming back to desktop must not show a maximized-empty state.
    setDesktop(true)
    await flushPromises()

    expect(collapsedPanelCount(wrapper)).toBe(0)
    expect(backButton(wrapper)).toBeUndefined()
  })

  it('maximizes the digest content pane in a digest view too', async () => {
    const wrapper = await mountAt('/digests/7')
    const { useDigestStore } = await import('../stores/digest')
    const store = useDigestStore()
    store.activeConfigId = 7
    store.results = [{
      id: 1, config_id: 7, config_name: 'Morning Brief', status: 'new',
      created_at: '2026-09-10T08:00:00Z', content: JSON.stringify({ title: 'Briefing' }),
      item_count: 3, title: 'Briefing',
    } as never]
    store.selectedResult = store.results[0] as never
    await flushPromises()

    await pressX()

    expect(collapsedPanelCount(wrapper)).toBe(2)
    expect(backButton(wrapper)).toBeTruthy()
  })

  it('maximizes in a CATEGORY view (multi-feed item, not a single feed)', async () => {
    // A category view has no activeFeedId, so the old gate read "no content"
    // and x did nothing.
    vi.mocked(feedApi.multiItems).mockResolvedValue({
      items: [{ ...makeItem(1, 'Category item'), feed_name: 'Hacker News', feed_type: 'hn' }],
      total: 1, has_more: false,
    } as never)
    const wrapper = await mountAt('/feeds/category/tech/1')
    const { useFeedStore } = await import('../stores/feed')
    const store = useFeedStore()
    // Mirror what the route watcher + selectItem do for a multi-feed view.
    store.activeFeedId = null
    store.activeCategory = 'tech'
    store.multiItems = [{
      ...makeItem(1, 'Category item'), feed_name: 'Hacker News', feed_type: 'hn',
    }] as never
    store.selectedItem = makeItem(1, 'Category item') as never
    await flushPromises()

    expect(backButton(wrapper)).toBeUndefined()

    await pressX()

    expect(collapsedPanelCount(wrapper)).toBe(2)
    expect(backButton(wrapper)).toBeTruthy()
  })

  it('maximizes in the "All Feeds" section view', async () => {
    const wrapper = await mountAt('/feeds')
    const { useFeedStore } = await import('../stores/feed')
    const store = useFeedStore()
    store.activeFeedId = null
    store.activeSection = 'feeds'
    store.multiItems = [{ ...makeItem(1, 'Section item'), feed_name: 'Hacker News', feed_type: 'hn' }] as never
    store.selectedItem = makeItem(1, 'Section item') as never
    await flushPromises()

    await pressX()

    expect(collapsedPanelCount(wrapper)).toBe(2)
  })

  it('maximizes in the STACKED layout by collapsing BOTH the sidebar and the list', async () => {
    localStorage.setItem('digesterr-layout', 'stacked')
    const wrapper = await mountAt('/feeds/1/1')
    expect(wrapper.find('[data-layout="stacked"]').exists()).toBe(true)

    await pressX()

    // Only the content pane is left: the sidebar AND the item list both collapse,
    // so the content fills the right column top to bottom.
    const stacked = wrapper.find('[data-layout="stacked"]')
    const outerPanels = stacked.findAll(':scope > [data-panel]')
    expect(outerPanels).toHaveLength(2)
    // Sidebar collapsed; the right column (which holds the content) stays.
    expect(outerPanels[0].attributes('style') || '').toContain('flex: 0 1 0px')
    expect(outerPanels[1].attributes('style') || '').not.toContain('flex: 0 1 0px')

    const innerPanels = stacked.findAll('[data-orientation="vertical"] > [data-panel]')
    expect(innerPanels).toHaveLength(2)
    // List collapsed, content NOT collapsed.
    expect(innerPanels[0].attributes('style') || '').toContain('flex: 0 1 0px')
    expect(innerPanels[1].attributes('style') || '').not.toContain('flex: 0 1 0px')

    expect(backButton(wrapper)).toBeTruthy()
    expect(stacked.findAll('[data-panel][inert]')).toHaveLength(2)
  })

  it('restores both stacked splits when the maximized state is left', async () => {
    localStorage.setItem('digesterr-layout', 'stacked')
    const wrapper = await mountAt('/feeds/1/1')
    await pressX()

    await backButton(wrapper)!.trigger('click')
    await flushPromises()

    const stacked = wrapper.find('[data-layout="stacked"]')
    // Every panel is back to the regular view.
    stacked.findAll('[data-panel]').forEach(p => {
      expect(p.attributes('style') || '').not.toContain('flex: 0 1 0px')
    })
    expect(stacked.findAll('[data-panel][inert]')).toHaveLength(0)
    expect(wrapper.find('[data-app-logo]').exists()).toBe(true)
  })

  it('drops the maximized state when the layout is switched', async () => {
    const wrapper = await mountAt('/feeds/1/1')
    await pressX()
    expect(collapsedPanelCount(wrapper)).toBe(2)

    // Switch layout while maximized: the new layout must come up whole, with no
    // leftover Back button and nothing collapsed.
    await wrapper.find('[data-layout-toggle]').trigger('click')
    await flushPromises()

    expect(wrapper.find('[data-layout="stacked"]').exists()).toBe(true)
    expect(wrapper.findAll('[data-panel][inert]')).toHaveLength(0)
    wrapper.findAll('[data-panel]').forEach(p => {
      expect(p.attributes('style') || '').not.toContain('flex: 0 1 0px')
    })
    expect(backButton(wrapper)).toBeUndefined()
    expect(wrapper.find('[data-app-logo]').exists()).toBe(true)
  })

  it('drops the maximized state when switching from stacked back to panes', async () => {
    localStorage.setItem('digesterr-layout', 'stacked')
    const wrapper = await mountAt('/feeds/1/1')
    await pressX()
    // Sidebar + the list's half of the vertical split.
    expect(wrapper.findAll('[data-panel][inert]')).toHaveLength(2)

    await wrapper.find('[data-layout-toggle]').trigger('click')
    await flushPromises()

    expect(wrapper.find('[data-layout="panes"]').exists()).toBe(true)
    expect(wrapper.findAll('[data-panel][inert]')).toHaveLength(0)
    expect(backButton(wrapper)).toBeUndefined()
  })
})
