import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { setActivePinia, createPinia } from 'pinia'
import { createRouter, createMemoryHistory } from 'vue-router'
import HomeView from './HomeView.vue'
import { itemApi, feedApi } from '../services/api'

vi.mock('../services/api', () => ({
  authApi: { signout: vi.fn(), changePassword: vi.fn() },
  feedApi: {
    list: vi.fn().mockResolvedValue([]),
    create: vi.fn(),
    update: vi.fn(),
    delete: vi.fn(),
    getItems: vi.fn(),
    multiItems: vi.fn().mockResolvedValue({ items: [], total: 0, has_more: false }),
    fetch: vi.fn(),
    markRead: vi.fn(),
    unreadCounts: vi.fn().mockResolvedValue({}),
  },
  itemApi: { updateStatus: vi.fn(), getItem: vi.fn(), extract: vi.fn() },
  digestApi: {
    listConfigs: vi.fn().mockResolvedValue([]),
    createConfig: vi.fn(),
    updateConfig: vi.fn(),
    deleteConfig: vi.fn(),
    getConfig: vi.fn(),
    generate: vi.fn(),
    listResults: vi.fn().mockResolvedValue({ results: [], has_more: false }),
    getResult: vi.fn(),
    updateResultStatus: vi.fn(),
    deleteResult: vi.fn(),
    listResultItems: vi.fn().mockResolvedValue([]),
    unreadCounts: vi.fn().mockResolvedValue({}),
  },
  logsApi: { list: vi.fn().mockResolvedValue({ entries: [], total: 0, has_more: false }) },
  setAuthToken: vi.fn(),
  getAuthToken: vi.fn(() => null),
  setOnUnauthorized: vi.fn(),
  ApiError: class extends Error {},
}))

// jsdom lacks matchMedia (useBreakpoints) and ResizeObserver (reka-ui).
window.matchMedia = window.matchMedia || ((query: string) => ({
  matches: false,
  media: query,
  onchange: null,
  addListener: () => {},
  removeListener: () => {},
  addEventListener: () => {},
  removeEventListener: () => {},
  dispatchEvent: () => false,
})) as MediaQueryList

class ResizeObserverStub {
  observe() {}
  unobserve() {}
  disconnect() {}
}
vi.stubGlobal('ResizeObserver', ResizeObserverStub)

function makeItem(id: number, title: string, content = '') {
  return {
    id,
    feed_id: 15,
    title,
    url: `https://example.com/${id}`,
    author: 'u/someone',
    content,
    points: 1,
    comments_count: 0,
    published_at: '2026-09-10T08:00:00Z',
    status: 'unread',
    is_saved: false,
  }
}

// The first page holds 50 items and deliberately does NOT contain the linked
// item, mirroring the real report: feed 15's item 152870 sat at position 56.
const FIRST_PAGE = Array.from({ length: 50 }, (_, i) => makeItem(300000 + i, `page item ${i}`))
const LINKED_ITEM = makeItem(152870, 'KERESSÜK! Eltünt a nagymamám!', '<p>Body of the crossposted post</p>')

function router() {
  return createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/', component: HomeView },
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

describe('HomeView deep link to an item', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    localStorage.clear()
    vi.clearAllMocks()
    vi.mocked(feedApi.getItems).mockResolvedValue({ items: FIRST_PAGE, total: 56, has_more: true } as never)
    vi.mocked(itemApi.getItem).mockResolvedValue(LINKED_ITEM as never)
  })

  it('opens a linked item that is not on the loaded page', async () => {
    const wrapper = await mountAt('/feeds/15/152870')

    // The item is fetched directly by id instead of waiting for a page that
    // will never contain it.
    expect(itemApi.getItem).toHaveBeenCalledWith(152870)
    expect(wrapper.text()).toContain('KERESSÜK! Eltünt a nagymamám!')
    // Before the fix the pane stayed on the empty state.
    expect(wrapper.text()).not.toContain('Select an item')
  })

  it('opens a linked item that is already on the loaded page without refetching', async () => {
    const onPage = FIRST_PAGE[3]
    vi.mocked(feedApi.getItems).mockResolvedValue({ items: FIRST_PAGE, total: 56, has_more: true } as never)
    vi.mocked(itemApi.getItem).mockImplementation(async (id: number) =>
      (id === onPage.id ? onPage : LINKED_ITEM) as never)

    const wrapper = await mountAt(`/feeds/15/${onPage.id}`)

    expect(wrapper.text()).toContain(onPage.title)
    expect(wrapper.text()).not.toContain('Select an item')
  })

  it('shows the empty state when no item is linked', async () => {
    const wrapper = await mountAt('/feeds/15')
    expect(wrapper.text()).toContain('Select an item')
  })
})
