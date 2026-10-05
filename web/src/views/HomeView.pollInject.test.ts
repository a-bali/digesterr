import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { setActivePinia, createPinia } from 'pinia'
import { createRouter, createMemoryHistory } from 'vue-router'
import HomeView from './HomeView.vue'
import { feedApi } from '../services/api'

// The 10-second poll must refresh the OPEN item list, not only the sidebar
// counts: with a feed open, a newly arrived item raised the badge while the
// middle pane stayed frozen until the feed was reselected. And the tab-return
// handler must MERGE into the list rather than clean-replace it (a replace is
// what used to make read rows vanish from a filtered view), so a refocus while a
// feed is open must not hit the Saved fetcher.
const savedCalls = { n: 0 }

vi.mock('../services/api', () => ({
  authApi: { signout: vi.fn(), changePassword: vi.fn() },
  feedApi: {
    list: vi.fn().mockResolvedValue([]),
    create: vi.fn(), update: vi.fn(), delete: vi.fn(),
    getItems: vi.fn().mockResolvedValue({ items: [], total: 0, has_more: false }),
    multiItems: vi.fn().mockResolvedValue({ items: [], total: 0, has_more: false }),
    fetch: vi.fn(),
    markRead: vi.fn().mockResolvedValue({ message: 'ok' }),
    markReadAll: vi.fn().mockResolvedValue({ message: 'ok' }),
    unreadCounts: vi.fn().mockResolvedValue({}),
    savedCount: vi.fn().mockResolvedValue({ count: 0 }),
    savedItems: vi.fn(() => { savedCalls.n++; return Promise.resolve({ items: [], total: 0, has_more: false }) }),
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
  setAuthToken: vi.fn(), getAuthToken: vi.fn(() => null), setOnUnauthorized: vi.fn(),
  ApiError: class extends Error {},
}))

if (!window.matchMedia) {
  Object.defineProperty(window, 'matchMedia', {
    writable: true, configurable: true,
    value: (query: string) => ({
      matches: true, media: query, onchange: null,
      addListener: () => {}, removeListener: () => {},
      addEventListener: () => {}, removeEventListener: () => {},
      dispatchEvent: () => false,
    }),
  })
}

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

function makeItem(id: number, title: string) {
  return {
    id, feed_id: 1, title, url: `https://example.com/${id}`, author: '',
    content: '<p>body</p>', points: 1, comments_count: 0,
    published_at: '2026-09-10T08:00:00Z', status: 'unread',
    is_saved: 0, extract_content: '', extract_status: '', comment_url: '',
  }
}

async function mountAtFeed() {
  const pinia = createPinia()
  setActivePinia(pinia)
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [{ path: '/feeds/:feedId/:itemId?', component: HomeView }],
  })
  router.push('/feeds/1')
  await router.isReady()
  const wrapper = mount(HomeView, { global: { plugins: [pinia, router] } })
  await flushPromises()
  await flushPromises()
  return wrapper
}

describe('HomeView: the poll injects new items into the open feed', () => {
  beforeEach(() => {
    savedCalls.n = 0
    vi.clearAllMocks()
    vi.mocked(feedApi.list).mockResolvedValue([makeFeed()] as never)
    // First load: one item. Every later call (the poll) reports a NEW arrival at
    // the top.
    vi.mocked(feedApi.getItems).mockResolvedValueOnce({
      items: [makeItem(1, 'Existing item')], total: 1, has_more: false,
    } as never)
    vi.mocked(feedApi.getItems).mockResolvedValue({
      items: [makeItem(2, 'New arrival'), makeItem(1, 'Existing item')], total: 2, has_more: false,
    } as never)
  })
  afterEach(() => {
    document.body.innerHTML = ''
    vi.useRealTimers()
  })

  it('shows a newly arrived item after one poll tick', async () => {
    vi.useFakeTimers() // before mount: the interval is created in onMounted
    const wrapper = await mountAtFeed()
    expect(wrapper.text()).toContain('Existing item')
    expect(wrapper.text()).not.toContain('New arrival')

    await vi.advanceTimersByTimeAsync(10_000)
    await flushPromises()

    // The poll refetched the open feed's items and injected the arrival.
    expect(wrapper.text()).toContain('New arrival')
    expect(vi.mocked(feedApi.getItems).mock.calls.length).toBeGreaterThan(1)
    wrapper.unmount()
  })

  it('merges on tab return instead of clean-replacing (no Saved refetch)', async () => {
    const wrapper = await mountAtFeed()
    savedCalls.n = 0
    const itemsBefore = vi.mocked(feedApi.getItems).mock.calls.length

    Object.defineProperty(document, 'visibilityState', { value: 'visible', configurable: true })
    document.dispatchEvent(new Event('visibilitychange'))
    await flushPromises()

    // The feed list refreshed on return...
    expect(vi.mocked(feedApi.getItems).mock.calls.length).toBeGreaterThan(itemsBefore)
    // ...and the Saved fetcher (a clean replace) was NOT used while a feed is open.
    expect(savedCalls.n).toBe(0)
    wrapper.unmount()
  })
})
