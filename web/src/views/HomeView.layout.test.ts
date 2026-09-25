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

function toggleButton(wrapper: ReturnType<typeof mount>) {
  const el = wrapper.find('[data-layout-toggle]')
  return el.exists() ? el : undefined
}

describe('HomeView: stacked desktop layout', () => {
  beforeEach(() => {
    document.body.innerHTML = ''
    setActivePinia(createPinia())
    localStorage.clear()
    vi.clearAllMocks()
    desktop = true
    mqls.length = 0
    vi.mocked(feedApi.list).mockResolvedValue([makeFeed()] as never)
    vi.mocked(feedApi.getItems).mockResolvedValue({
      items: [makeItem(1, 'First item')], total: 1, has_more: false,
    } as never)
    vi.mocked(itemApi.getItem).mockResolvedValue(makeItem(1, 'First item') as never)
  })

  it('offers a layout toggle in the header, enabled on desktop', async () => {
    const wrapper = await mountAt('/feeds/1')
    const btn = toggleButton(wrapper)
    expect(btn, 'the header must carry a layout toggle').toBeTruthy()
    expect(btn!.attributes('aria-label')).toBe('Switch to stacked layout')
    expect(btn!.attributes('disabled')).toBeUndefined()
  })

  it('defaults to the 3-pane layout', async () => {
    const wrapper = await mountAt('/feeds/1')
    expect(wrapper.find('[data-layout="panes"]').exists()).toBe(true)
    expect(wrapper.find('[data-layout="stacked"]').exists()).toBe(false)
  })

  it('switches to the stacked layout: sidebar + list above content', async () => {
    const wrapper = await mountAt('/feeds/1/1')

    await toggleButton(wrapper)!.trigger('click')
    await flushPromises()

    expect(wrapper.find('[data-layout="stacked"]').exists()).toBe(true)
    expect(wrapper.find('[data-layout="panes"]').exists()).toBe(false)
    // The right column is split vertically (list on top, content below).
    const vertical = wrapper.find('[data-orientation="vertical"]')
    expect(vertical.exists(), 'the right column must be a vertical split').toBe(true)
    const stacked = wrapper.find('[data-layout="stacked"]')
    // Two levels of splitting: the outer row (sidebar | right column) and the
    // inner vertical split (list | content) — so 4 panels, 2 handles.
    expect(stacked.findAll('[data-panel]')).toHaveLength(4)
    expect(stacked.findAll('[role="separator"]')).toHaveLength(2)
    expect(wrapper.find('[data-orientation="horizontal"]').exists()).toBe(true)
    expect(wrapper.find('[data-orientation="vertical"]').exists()).toBe(true)
    // Nothing is lost: the sidebar is still rendered in the stacked layout.
    expect(stacked.text()).toContain('Hacker News')
  })

  it('splits the right column into list-above-content, not content-above-list', async () => {
    const wrapper = await mountAt('/feeds/1/1')
    await toggleButton(wrapper)!.trigger('click')
    await flushPromises()

    // Assert DOM order, which is what "on top" means without a layout engine.
    const html = wrapper.find('[data-orientation="vertical"]').html()
    const listAt = html.indexOf('data-swipe-action') >= 0 ? html.indexOf('data-swipe-action') : html.indexOf('role="button"')
    const contentAt = html.indexOf('data-item-content')
    expect(listAt).toBeGreaterThanOrEqual(0)
    expect(contentAt).toBeGreaterThanOrEqual(0)
    expect(listAt, 'the item list must come before the content pane').toBeLessThan(contentAt)
  })

  it('renders compact one-line rows in the stacked layout', async () => {
    const wrapper = await mountAt('/feeds/1/1')
    await toggleButton(wrapper)!.trigger('click')
    await flushPromises()

    const list = wrapper.find('[data-layout="stacked"]')
    const row = list.findAll('[role="button"]').find(r => r.classes().includes('h-7'))
    expect(row, 'stacked rows must use the compact height').toBeTruthy()
    expect(row!.find('[data-item-title]').exists()).toBe(true)
    expect(row!.find('[data-item-excerpt]').exists()).toBe(true)
  })

  it('switches back to the 3-pane layout', async () => {
    const wrapper = await mountAt('/feeds/1')
    await toggleButton(wrapper)!.trigger('click')
    await flushPromises()
    expect(wrapper.find('[data-layout="stacked"]').exists()).toBe(true)

    await wrapper.find('[data-layout-toggle]').trigger('click')
    await flushPromises()

    expect(wrapper.find('[data-layout="panes"]').exists()).toBe(true)
    expect(wrapper.find('[data-layout="stacked"]').exists()).toBe(false)
  })

  it('restores the persisted layout on mount', async () => {
    localStorage.setItem('digesterr-layout', 'stacked')
    const wrapper = await mountAt('/feeds/1')

    expect(wrapper.find('[data-layout="stacked"]').exists()).toBe(true)
    expect(wrapper.find('[data-layout="panes"]').exists()).toBe(false)
    expect(toggleButton(wrapper)!.attributes('aria-label')).toBe('Switch to 3-pane layout')
  })

  it('persists the choice', async () => {
    const wrapper = await mountAt('/feeds/1')
    await toggleButton(wrapper)!.trigger('click')
    await flushPromises()

    expect(localStorage.getItem('digesterr-layout')).toBe('stacked')
  })

  it('disables the toggle below the desktop breakpoint', async () => {
    desktop = false
    const wrapper = await mountAt('/feeds/1')
    const btn = wrapper.find('[data-layout-toggle]')
    expect(btn.exists()).toBe(true)
    expect(btn.attributes('disabled')).toBeDefined()
  })

  it('ignores a stacked preference below the desktop breakpoint', async () => {
    localStorage.setItem('digesterr-layout', 'stacked')
    desktop = false
    const wrapper = await mountAt('/feeds/1')

    // Below lg the drawer/one-pane behaviour is unchanged — stacked is desktop-only.
    expect(wrapper.find('[data-layout="stacked"]').exists()).toBe(false)
    expect(wrapper.find('.h-drawer').exists()).toBe(true)
  })
})

// On mobile the list pane is not unmounted when content opens: it slides
// off-screen (translate + zero width) so its scroll position survives. The
// wrapper carried `pointer-events-none`, which stops the MOUSE only — the
// off-screen list stayed in the tab order and the accessibility tree, so
// tabbing out of the content pane walked into invisible rows. The drawer got
// `inert` for exactly this; the list behind the content did not.
describe('HomeView: off-screen list on mobile is inert', () => {
  beforeEach(() => {
    document.body.innerHTML = ''
    setActivePinia(createPinia())
    localStorage.clear()
    vi.clearAllMocks()
    desktop = false
    mqls.length = 0
    vi.mocked(feedApi.list).mockResolvedValue([makeFeed()] as never)
    vi.mocked(feedApi.getItems).mockResolvedValue({
      items: [makeItem(1, 'First item')], total: 1, has_more: false,
    } as never)
    vi.mocked(itemApi.getItem).mockResolvedValue(makeItem(1, 'First item') as never)
  })

  it('removes the off-screen list from interaction once content is open', async () => {
    // Route straight to an item so the content pane is open on mount.
    const wrapper = await mountAt('/feeds/1/1')
    await flushPromises()

    // Two elements make up the off-screen list: the outer wrapper that leaves
    // the flex flow (and carries inert) and the inner one that slides out. The
    // inert binding is on the OUTER wrapper, so that is what to assert on.
    const inner = wrapper.findAll('div').find(d => d.classes().includes('-translate-x-full'))
    expect(inner, 'the list must slide off-screen when content opens').toBeTruthy()

    const outer = inner!.element.parentElement as HTMLElement
    expect(outer, 'the sliding list must have an outer wrapper').toBeTruthy()
    // inert is what actually takes it out of the tab order; the transform alone
    // leaves every row focusable, which is the defect.
    //
    // Assert the VALUE, not mere presence. inert is a BOOLEAN attribute, so
    // presence means inert -- which makes an `[inert]` selector a tautology
    // here: Vue writes inert="false" for a false binding (jsdom has no `inert`
    // property, so every binding becomes an attribute) and that ALSO reads as
    // inert. Only the literal value distinguishes the two.
    expect(outer.getAttribute('inert')).toBe('true')
    expect(outer.getAttribute('aria-hidden')).toBe('true')
  })

  it('leaves the list interactive while it is the visible pane', async () => {
    // No item selected: the list is on screen, so its wrapper must NOT be inert
    // -- otherwise the fix disables the very pane the user is looking at.
    //
    // Scoped to the list wrapper on purpose: the closed sidebar drawer is
    // legitimately inert in this state, so a blanket "nothing is inert"
    // assertion would fail on correct code.
    const wrapper = await mountAt('/feeds/1')
    await flushPromises()

    const inner = wrapper.findAll('div').find(d => d.classes().includes('-translate-x-full'))
    expect(inner, 'the list must be on screen, not slid away').toBeUndefined()

    const listWrapper = wrapper.findAll('div').find(d =>
      d.classes().includes('flex-1') && d.classes().includes('min-w-0'),
    )
    expect(listWrapper, 'the visible list wrapper must exist').toBeTruthy()
    expect(listWrapper!.attributes('inert')).toBeUndefined()
  })
})
