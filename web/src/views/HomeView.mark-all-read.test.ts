import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { setActivePinia, createPinia } from 'pinia'
import { createRouter, createMemoryHistory } from 'vue-router'
import HomeView from './HomeView.vue'
import { feedApi } from '../services/api'
import type { Feed } from '../types/api'

const markReadAll = vi.fn().mockResolvedValue({ message: 'ok' })
const markRead = vi.fn().mockResolvedValue({ message: 'ok' })

vi.mock('../services/api', () => ({
  authApi: { signout: vi.fn(), changePassword: vi.fn() },
  feedApi: {
    list: vi.fn().mockResolvedValue([]),
    create: vi.fn(),
    update: vi.fn(),
    delete: vi.fn(),
    getItems: vi.fn().mockResolvedValue({ items: [], total: 0, has_more: false }),
    multiItems: vi.fn().mockResolvedValue({ items: [], total: 0, has_more: false }),
    fetch: vi.fn(),
    markRead: (...a: any[]) => markRead(...a),
    markReadAll: (...a: any[]) => markReadAll(...a),
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

window.matchMedia = window.matchMedia || ((query: string) => ({
  matches: true, // desktop layout, so the 3-pane binding is the one under test
  media: query,
  onchange: null,
  addListener: () => {}, removeListener: () => {},
  addEventListener: () => {}, removeEventListener: () => {},
  dispatchEvent: () => false,
})) as MediaQueryList

class ResizeObserverStub { observe() {} unobserve() {} disconnect() {} }
vi.stubGlobal('ResizeObserver', ResizeObserverStub)

function makeFeed(overrides: Partial<Feed> = {}): Feed {
  return {
    id: 1, user_id: 2, category: 'tech', type: 'hn', name: 'Hacker News',
    url: 'https://news.ycombinator.com/rss', config: '', min_score: 0,
    last_fetched_at: null, status: 'active', extract_content: 0,
    last_error: '', last_error_at: null, last_error_severity: '',
    created_at: '', updated_at: '', ...overrides,
  }
}

function makeItem(id: number) {
  return {
    id, feed_id: 1, title: `item ${id}`, url: `https://example.com/${id}`,
    author: '', content: '<p>x</p>', points: 1, comments_count: 0,
    published_at: '2026-09-10T08:00:00Z', status: 'unread',
  }
}

function router() {
  return createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/', component: HomeView },
      { path: '/feeds', component: HomeView },
      { path: '/feeds/:feedId(\\d+)/:itemId(\\d+)?', component: HomeView },
      { path: '/feeds/category/:category/:itemId(\\d+)?', component: HomeView },
      { path: '/digests', component: HomeView },
      { path: '/digests/:configId(\\d+)/:resultId(\\d+)?', component: HomeView },
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

function markAllButton(wrapper: ReturnType<typeof mount>) {
  return wrapper.findAll('button').find(b => (b.text() || '').includes('Mark all as read'))
}

// Every mark-all-read now confirms first (the user reported that 'A' and the
// sidebar controls marked scopes read with no confirmation). These tests drive
// the dialog: click the trigger, then accept the confirmation.
async function confirmMarkAll(wrapper: ReturnType<typeof mount>) {
  await markAllButton(wrapper)!.trigger('click')
  await flushPromises()
  const confirm = document.body.querySelector('[data-mark-all-read-confirm]') as HTMLElement | null
  if (confirm) {
    confirm.click()
    await flushPromises()
  }
}

describe('mid-pane "Mark all as read" in multi-feed views', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    localStorage.clear()
    vi.clearAllMocks()
    vi.mocked(feedApi.list).mockResolvedValue([makeFeed()])
    vi.mocked(feedApi.multiItems).mockResolvedValue({
      items: [makeItem(1), makeItem(2)], total: 2, has_more: false,
    } as never)
    vi.mocked(feedApi.unreadCounts).mockResolvedValue({ 1: 2 })
  })

  it('renders the button in a category view', async () => {
    const wrapper = await mountAt('/feeds/category/tech')
    const { useFeedStore } = await import('../stores/feed')
    const store = useFeedStore()
    store.loadingMore = false
    await flushPromises()

    expect(store.isMultiView).toBe(true)
    expect(markAllButton(wrapper)).toBeTruthy()
  })

  it('marks the whole category read, not a single feed', async () => {
    const wrapper = await mountAt('/feeds/category/tech')
    const { useFeedStore } = await import('../stores/feed')
    const store = useFeedStore()
    store.loadingMore = false
    await flushPromises()

    await confirmMarkAll(wrapper)

    expect(markReadAll).toHaveBeenCalledTimes(1)
    expect(markReadAll).toHaveBeenCalledWith('tech')
    // It must never hit the single-feed endpoint with a null/stale id.
    expect(markRead).not.toHaveBeenCalled()
  })

  it('marks everything read in the "Feeds" section (no category)', async () => {
    const wrapper = await mountAt('/feeds')
    const { useFeedStore } = await import('../stores/feed')
    const store = useFeedStore()
    store.loadingMore = false
    await flushPromises()

    await confirmMarkAll(wrapper)

    expect(markReadAll).toHaveBeenCalledTimes(1)
    expect(markReadAll).toHaveBeenCalledWith(undefined)
    expect(markRead).not.toHaveBeenCalled()
  })

  it('still asks for confirmation for a single feed', async () => {
    const wrapper = await mountAt('/feeds/1')
    const { useFeedStore } = await import('../stores/feed')
    const store = useFeedStore()
    store.loadingMore = false
    await flushPromises()

    const btn = markAllButton(wrapper)
    // Single-feed view routes through the confirmation dialog in the sidebar.
    if (btn) {
      await btn.trigger('click')
      await flushPromises()
    }
    expect(markRead).not.toHaveBeenCalled()
  })
})

describe('mid-pane "Mark all as read" in digest views', () => {
  beforeEach(() => {
    // Portalled dialog nodes survive between tests otherwise, and a query for
    // [role="dialog"] then finds a stale one from an earlier test.
    document.body.innerHTML = ''
    setActivePinia(createPinia())
    localStorage.clear()
    vi.clearAllMocks()
    vi.mocked(feedApi.list).mockResolvedValue([])
  })

  it('marks only the open digest config, not every digest', async () => {
    const { digestApi } = await import('../services/api')
    const markAll = digestApi.markAllRead as ReturnType<typeof vi.fn>
    markAll.mockResolvedValue({ message: 'ok' })
    // The confirmation dialog renders the config's name, which the store derives
    // from the fetched config list — so the list must actually contain it.
    vi.mocked(digestApi.listConfigs).mockResolvedValue([
      { id: 7, user_id: 1, name: 'Morning Brief', schedule: 'daily', config: '{}',
        last_error: '', last_error_at: null, created_at: '', updated_at: '' },
    ])

    const wrapper = await mountAt('/digests/7')
    const { useDigestStore } = await import('../stores/digest')
    const store = useDigestStore()
    store.activeConfigId = 7
    store.results = [{ id: 1, config_id: 7, config_name: 'Morning Brief', status: 'new' } as never]
    await flushPromises()

    const btn = wrapper.findAll('button').find(b => (b.text() || '').includes('Mark all as read'))
    expect(btn, 'the button should render for an unread digest result').toBeTruthy()

    await btn!.trigger('click')
    await flushPromises()

    // A single config confirms first, so nothing is marked yet — only the dialog.
    expect(markAll).not.toHaveBeenCalled()
    const confirm = document.querySelector('[data-mark-all-read-confirm]') as HTMLElement
    expect(confirm, 'the confirm button should exist').not.toBeNull()
    confirm.click()
    await flushPromises()

    // Scoped to the config on screen — not a blanket "every digest" sweep.
    expect(markAll).toHaveBeenCalledWith(7)
  })

  // Regression guard for a silent no-op: the no-unread branch called toastSuccess
  // WITHOUT importing it, so the call threw a ReferenceError inside the handler
  // and the action looked like it did nothing. vue-tsc did not catch it. The
  // <Toaster> lives in App.vue, so HomeView alone cannot render a toast — assert
  // the helper was CALLED instead. Do NOT revert to a bare global here: the import
  // is the thing under test.
  it('tells the user when a scope has nothing unread instead of failing silently', async () => {
    const toast = await import('../lib/toast')
    const spy = vi.spyOn(toast, 'toastSuccess')

    const wrapper = await mountAt('/feeds/category/tech')
    const { useFeedStore } = await import('../stores/feed')
    const store = useFeedStore()
    store.feeds = [makeFeed()]
    store.unreadCounts = { [makeFeed().id]: 0 }
    store.loadingMore = false
    await flushPromises()

    markReadAll.mockClear()
    await markAllButton(wrapper)!.trigger('click')
    await flushPromises()

    // Nothing to mark: no confirmation and no API call...
    expect(markReadAll).not.toHaveBeenCalled()
    expect(document.body.querySelector('[data-mark-all-read-confirm]')).toBeNull()
    // ...but the user is told. A missing import here would throw instead.
    expect(spy, 'the empty-scope path must report something to the user').toHaveBeenCalledWith(
      expect.stringContaining('Nothing unread'),
      expect.stringContaining('tech'),
    )
    spy.mockRestore()
  })

  // The dialog moved here from FeedSidebar; the keyboard flow it had there must
  // survive. Enter is handled explicitly because useKeyboardShortcuts blurs the
  // focused button on keydown, which cancels the native activation.
  it('auto-focuses the confirm button so Enter accepts without a mouse', async () => {
    const { digestApi } = await import('../services/api')
    vi.mocked(digestApi.listConfigs).mockResolvedValue([
      { id: 7, user_id: 1, name: 'Morning Brief', schedule: 'daily', config: '{}',
        last_error: '', last_error_at: null, created_at: '', updated_at: '' },
    ])
    const wrapper = await mountAt('/digests/7')
    const { useDigestStore } = await import('../stores/digest')
    const store = useDigestStore()
    store.activeConfigId = 7
    store.results = [{ id: 1, config_id: 7, config_name: 'Morning Brief', status: 'new' } as never]
    await flushPromises()

    await markAllButton(wrapper)!.trigger('click')
    await flushPromises()
    await new Promise(r => setTimeout(r, 0))

    const confirm = document.body.querySelector('[data-mark-all-read-confirm]') as HTMLElement
    expect(confirm).not.toBeNull()
    expect(document.activeElement, 'the confirm button must take focus').toBe(confirm)
  })

  it('Enter on the focused confirm button runs the action exactly once', async () => {
    const { digestApi } = await import('../services/api')
    const markAll = digestApi.markAllRead as ReturnType<typeof vi.fn>
    markAll.mockResolvedValue({ message: 'ok' })
    vi.mocked(digestApi.listConfigs).mockResolvedValue([
      { id: 7, user_id: 1, name: 'Morning Brief', schedule: 'daily', config: '{}',
        last_error: '', last_error_at: null, created_at: '', updated_at: '' },
    ])
    const wrapper = await mountAt('/digests/7')
    const { useDigestStore } = await import('../stores/digest')
    const store = useDigestStore()
    store.activeConfigId = 7
    store.results = [{ id: 1, config_id: 7, config_name: 'Morning Brief', status: 'new' } as never]
    await flushPromises()

    await markAllButton(wrapper)!.trigger('click')
    await flushPromises()

    const confirm = document.body.querySelector('[data-mark-all-read-confirm]') as HTMLElement
    confirm.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true, cancelable: true }))
    await flushPromises()

    expect(markAll).toHaveBeenCalledTimes(1)
    expect(markAll).toHaveBeenCalledWith(7)
    // reka-ui keeps the node through its exit transition, so closed is
    // data-state='closed', not the absence of the element.
    const dlg = document.body.querySelector('[role="dialog"]') as HTMLElement | null
    expect(dlg === null || dlg.getAttribute('data-state') === 'closed').toBe(true)
  })

  it('cancelling marks nothing read', async () => {
    const { digestApi } = await import('../services/api')
    const markAll = digestApi.markAllRead as ReturnType<typeof vi.fn>
    vi.mocked(digestApi.listConfigs).mockResolvedValue([
      { id: 7, user_id: 1, name: 'Morning Brief', schedule: 'daily', config: '{}',
        last_error: '', last_error_at: null, created_at: '', updated_at: '' },
    ])
    const wrapper = await mountAt('/digests/7')
    const { useDigestStore } = await import('../stores/digest')
    const store = useDigestStore()
    store.activeConfigId = 7
    store.results = [{ id: 1, config_id: 7, config_name: 'Morning Brief', status: 'new' } as never]
    await flushPromises()

    await markAllButton(wrapper)!.trigger('click')
    await flushPromises()

    const cancel = Array.from(document.body.querySelectorAll('button')).find(b => b.textContent?.trim() === 'Cancel')
    cancel!.click()
    await flushPromises()

    expect(markAll).not.toHaveBeenCalled()
    const dlg = document.body.querySelector('[role="dialog"]') as HTMLElement | null
    expect(dlg === null || dlg.getAttribute('data-state') === 'closed').toBe(true)
  })

  it('marks every digest only from the All Digests view', async () => {
    const { digestApi } = await import('../services/api')
    const markAll = digestApi.markAllRead as ReturnType<typeof vi.fn>
    markAll.mockResolvedValue({ message: 'ok' })

    const wrapper = await mountAt('/digests')
    const { useDigestStore } = await import('../stores/digest')
    const store = useDigestStore()
    store.showingAllDigests = true
    store.activeConfigId = null
    store.allResults = [{ id: 1, config_id: 7, config_name: 'Morning Brief', status: 'new' } as never]
    await flushPromises()

    const btn = wrapper.findAll('button').find(b => (b.text() || '').includes('Mark all as read'))
    expect(btn, 'the button should render for an unread digest result').toBeTruthy()

    await btn!.trigger('click')
    await flushPromises()

    // The All Digests section confirms too — it used to mark straight away, which
    // is the inconsistency the user reported for 'A'.
    expect(markAll).not.toHaveBeenCalled()
    const confirm = document.querySelector('[data-mark-all-read-confirm]') as HTMLElement
    expect(confirm, 'the All Digests section must confirm before marking').not.toBeNull()
    confirm.click()
    await flushPromises()

    expect(markAll).toHaveBeenCalledWith(undefined)
  })
})
