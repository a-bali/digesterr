import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { setActivePinia, createPinia } from 'pinia'
import { createRouter, createMemoryHistory } from 'vue-router'
import HomeView from './HomeView.vue'
import { feedApi, itemApi } from '../services/api'

function item(id: number, status = 'read', extra: Record<string, unknown> = {}) {
  return {
    id, feed_id: 5, title: `t${id}`, status, is_saved: 0,
    created_at: '2026-09-01T00:00:00Z', published_at: '2026-09-01T00:00:00Z',
    feed_name: 'F', feed_type: 'rss', ...extra,
  }
}

vi.mock('../services/api', () => ({
  authApi: { signout: vi.fn(), changePassword: vi.fn() },
  feedApi: {
    list: vi.fn(),
    unreadCounts: vi.fn(),
    savedCount: vi.fn().mockResolvedValue({ count: 0 }),
    savedItems: vi.fn().mockResolvedValue({ items: [], has_more: false, total: 0 }),
    getItems: vi.fn(),
    multiItems: vi.fn(),
    markRead: vi.fn().mockResolvedValue({}),
    markAllRead: vi.fn().mockResolvedValue({}),
  },
  itemApi: { getItem: vi.fn(), updateStatus: vi.fn().mockResolvedValue({}), extract: vi.fn() },
  digestApi: {
    listConfigs: vi.fn().mockResolvedValue([]),
    unreadCounts: vi.fn().mockResolvedValue({}),
    listResults: vi.fn().mockResolvedValue({ results: [], has_more: false, total: 0 }),
    listAllResults: vi.fn().mockResolvedValue({ results: [], has_more: false, total: 0 }),
  },
  logsApi: { list: vi.fn().mockResolvedValue({ entries: [], total: 0, has_more: false }) },
  setAuthToken: vi.fn(), getAuthToken: vi.fn(() => null), setOnUnauthorized: vi.fn(),
  ApiError: class extends Error {},
}))

window.matchMedia = window.matchMedia || ((query: string) => ({
  matches: false, media: query, onchange: null,
  addListener: () => {}, removeListener: () => {},
  addEventListener: () => {}, removeEventListener: () => {}, dispatchEvent: () => false,
})) as MediaQueryList
class ResizeObserverStub { observe() {} unobserve() {} disconnect() {} }
vi.stubGlobal('ResizeObserver', ResizeObserverStub)

function router() {
  return createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/', component: HomeView },
      { path: '/saved/:itemId(\\d+)?', component: HomeView },
      { path: '/feeds/:feedId(\\d+)/:itemId(\\d+)?', component: HomeView },
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
  return { wrapper, r }
}

// The mark-all control must not answer a question about the WHOLE scope using
// only the rows it happens to hold. The action covers the feed or category, and
// the paginated window can be entirely read while unread items sit on a later
// page -- so gating on loaded rows disabled the button and labelled it "nothing
// unread" while the sidebar showed a non-zero badge for the same scope.
describe('mark-all-read control scope gating', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    localStorage.clear()
    vi.clearAllMocks()
    vi.mocked(feedApi.list).mockResolvedValue([
      { id: 5, name: 'F', url: 'https://f/feed', type: 'rss', category: null },
    ] as never)
  })

  it('stays enabled when unread items exist but only beyond the loaded page', async () => {
    // The loaded page is entirely read; the server counts 5 unread elsewhere.
    vi.mocked(feedApi.getItems).mockResolvedValue({
      items: [item(21), item(22)], total: 7, has_more: true,
    } as never)
    vi.mocked(feedApi.unreadCounts).mockResolvedValue({ 5: 5 } as never)

    const { wrapper } = await mountAt('/feeds/5')
    await flushPromises()

    const list = wrapper.findComponent({ name: 'ItemList' })
    expect(list.exists()).toBe(true)
    const markAll = list.find('[data-mark-all-read]')
    expect(markAll.exists()).toBe(true)
    // Enabled, because the scope is not provably empty.
    expect(markAll.attributes('disabled')).toBeUndefined()
    // And it offers the action, not a false claim of emptiness.
    expect(markAll.attributes('title')).not.toContain('Nothing unread')
  })

  it('says nothing is unread only when the server count is actually zero', async () => {
    vi.mocked(feedApi.getItems).mockResolvedValue({
      items: [item(21), item(22)], total: 2, has_more: false,
    } as never)
    vi.mocked(feedApi.unreadCounts).mockResolvedValue({ 5: 0 } as never)

    const { wrapper } = await mountAt('/feeds/5')
    await flushPromises()

    const list = wrapper.findComponent({ name: 'ItemList' })
    const markAll = list.find('[data-mark-all-read]')
    expect(markAll.exists()).toBe(true)
    // Still clickable, so the action can answer "nothing to mark as read",
    // but the label now matches the server's count.
    expect(markAll.attributes('disabled')).toBeUndefined()
    expect(markAll.attributes('title')).toContain('Nothing unread')
  })

  it('does not claim emptiness when the counts have not loaded yet', async () => {
    vi.mocked(feedApi.getItems).mockResolvedValue({
      items: [item(21)], total: 1, has_more: false,
    } as never)
    // No counts at all -- unknown, which must not read as zero.
    vi.mocked(feedApi.unreadCounts).mockResolvedValue({} as never)

    const { wrapper } = await mountAt('/feeds/5')
    await flushPromises()

    const list = wrapper.findComponent({ name: 'ItemList' })
    const markAll = list.find('[data-mark-all-read]')
    if (markAll.exists()) {
      expect(markAll.attributes('title')).not.toContain('Nothing unread')
    }
  })
})

// The Saved view deliberately ignores the persisted read-state filter: saving is
// an explicit act of keeping something, so a global "Unread only" setting must
// not hide it. The empty message was derived from that unused filter, so it could
// say "match this filter" on a view that applies no filter.
describe('Saved empty message', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    localStorage.clear()
    vi.clearAllMocks()
    vi.mocked(feedApi.savedItems).mockResolvedValue({ items: [], has_more: false, total: 0 } as never)
    vi.mocked(feedApi.unreadCounts).mockResolvedValue({} as never)
    vi.mocked(feedApi.getItems).mockResolvedValue({ items: [], total: 0, has_more: false } as never)
  })

  it('does not mention a filter, because Saved applies none', async () => {
    localStorage.setItem('digesterr.filter', 'unread')
    const { wrapper } = await mountAt('/saved')
    await flushPromises()

    const empty = wrapper.find('[data-saved-empty]')
    expect(empty.exists()).toBe(true)
    expect(empty.text()).toBe('No saved items')
    expect(empty.text()).not.toContain('filter')
  })
})
