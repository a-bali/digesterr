import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { setActivePinia, createPinia } from 'pinia'
import { createRouter, createMemoryHistory } from 'vue-router'
import HomeView from './HomeView.vue'
import { feedApi, itemApi } from '../services/api'

const SAVED = [
  { id: 11, feed_id: 1, title: 'saved one', status: 'read', is_saved: 1, url: 'https://e/11', content: '<p>one</p>', created_at: '2026-09-01T00:00:00Z', published_at: '2026-09-01T00:00:00Z', feed_name: 'F', feed_type: 'rss' },
  { id: 12, feed_id: 2, title: 'saved two', status: 'read', is_saved: 1, url: 'https://e/12', content: '<p>two</p>', created_at: '2026-08-01T00:00:00Z', published_at: '2026-08-01T00:00:00Z', feed_name: 'G', feed_type: 'rss' },
]

vi.mock('../services/api', () => ({
  authApi: { signout: vi.fn(), changePassword: vi.fn() },
  feedApi: {
    list: vi.fn().mockResolvedValue([]),
    unreadCounts: vi.fn().mockResolvedValue({}),
    savedCount: vi.fn().mockResolvedValue({ count: 2 }),
    savedItems: vi.fn(),
    getItems: vi.fn().mockResolvedValue({ items: [], total: 0, has_more: false }),
    multiItems: vi.fn().mockResolvedValue({ items: [], total: 0, has_more: false }),
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
      { path: '/feeds', component: HomeView },
      { path: '/feeds/item/:itemId(\\d+)', component: HomeView },
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

// A saved item must be nameable by URL. Saved deliberately spans feeds and
// categories, so the bare /saved route could not represent WHICH item was open:
// the pane updated while the route stayed at /saved, so the item was unlinkable
// and a reload lost it.
describe('Saved view: item in the URL', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    localStorage.clear()
    vi.clearAllMocks()
    vi.mocked(feedApi.savedItems).mockResolvedValue({ items: SAVED, has_more: false, total: 2 } as never)
    vi.mocked(itemApi.getItem).mockImplementation(async (id: number) =>
      SAVED.find(i => i.id === id) as never)
  })

  it('puts the selected saved item in the URL', async () => {
    const { wrapper, r } = await mountAt('/saved')

    const rows = wrapper.findAll('[role="button"]')
    expect(rows.length).toBeGreaterThanOrEqual(2)
    await rows[0].trigger('click')
    await flushPromises()

    expect(r.currentRoute.value.path).toBe('/saved/11')
    expect(r.currentRoute.value.params.itemId).toBe('11')
  })

  it('restores the open item from a deep link on a cold load', async () => {
    const { wrapper, r } = await mountAt('/saved/12')
    await flushPromises()

    expect(r.currentRoute.value.params.itemId).toBe('12')
    // The linked row is in view, so it is resolved without a second fetch.
    expect(wrapper.text()).toContain('saved two')
  })

  it('fetches a saved item that is not on the loaded page', async () => {
    // Page one holds only the first row; the deep link points at the other.
    vi.mocked(feedApi.savedItems).mockResolvedValue({ items: [SAVED[0]], has_more: true, total: 2 } as never)
    const { wrapper } = await mountAt('/saved/12')
    await flushPromises()

    expect(itemApi.getItem).toHaveBeenCalledWith(12)
    await flushPromises()
    expect(wrapper.text()).toContain('saved two')
  })

  it('returns to the bare /saved path when the list is shown again', async () => {
    const { wrapper, r } = await mountAt('/saved/11')
    await flushPromises()
    expect(r.currentRoute.value.path).toBe('/saved/11')

    // The mobile/back affordance clears the selection; the route must follow, or
    // the item would reopen.
    const back = wrapper.find('[data-back-to-list]')
    if (back.exists()) {
      await back.trigger('click')
      await flushPromises()
      expect(r.currentRoute.value.path).toBe('/saved')
    }
  })
})
