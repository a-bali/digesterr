import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { setActivePinia, createPinia } from 'pinia'
import { createRouter, createMemoryHistory } from 'vue-router'
import HomeView from './HomeView.vue'

// 'u' must toggle unread-only for whichever list is ON SCREEN.
//
// The bug: DigestResultList kept its status filter in a local ref, and the
// shortcut only reached feedStore.filter. So in a digest view 'u' did nothing
// visible AND silently overwrote the persisted item filter — the next visit to
// a feed would come back filtered to Unread for no reason the user could see.
vi.mock('../services/api', () => ({
  authApi: { signout: vi.fn(), changePassword: vi.fn() },
  feedApi: {
    list: vi.fn().mockResolvedValue([]),
    create: vi.fn(), update: vi.fn(), delete: vi.fn(),
    getItems: vi.fn().mockResolvedValue({ items: [], total: 0, has_more: false }),
    multiItems: vi.fn().mockResolvedValue({ items: [], total: 0, has_more: false }),
    fetch: vi.fn(), markRead: vi.fn(), markReadAll: vi.fn(),
    unreadCounts: vi.fn().mockResolvedValue({}),
    savedCount: vi.fn().mockResolvedValue({ count: 0 }),
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
    listAllResults: vi.fn().mockResolvedValue({ results: [], has_more: false }),
  },
  logsApi: { list: vi.fn().mockResolvedValue({ entries: [], total: 0, has_more: false }) },
  setAuthToken: vi.fn(),
  getAuthToken: vi.fn(() => null),
  setOnUnauthorized: vi.fn(),
  ApiError: class extends Error {},
}))

window.matchMedia = window.matchMedia || ((query: string) => ({
  matches: true,
  media: query,
  onchange: null,
  addListener: () => {}, removeListener: () => {},
  addEventListener: () => {}, removeEventListener: () => {},
  dispatchEvent: () => false,
})) as MediaQueryList

class ResizeObserverStub { observe() {} unobserve() {} disconnect() {} }
vi.stubGlobal('ResizeObserver', ResizeObserverStub)

function pressU() {
  window.dispatchEvent(new KeyboardEvent('keydown', { key: 'u', bubbles: true }))
}

async function mountAt(path: string) {
  const pinia = createPinia()
  setActivePinia(pinia)
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/', component: HomeView },
      { path: '/digests/:configId', component: HomeView },
      { path: '/feeds/:id', component: HomeView },
    ],
  })
  router.push(path)
  await router.isReady()
  const wrapper = mount(HomeView, { global: { plugins: [pinia, router] } })
  await flushPromises()
  return { wrapper, router }
}

describe("'u' shortcut in digest view", () => {
  beforeEach(() => {
    localStorage.clear()
    vi.clearAllMocks()
  })

  it('toggles the DIGEST filter when a digest config is on screen', async () => {
    const { useDigestStore } = await import('../stores/digest')
    const { wrapper } = await mountAt('/digests/7')
    const digestStore = useDigestStore()
    await flushPromises()

    expect(digestStore.resultFilter).toBe('all')
    pressU()
    await flushPromises()
    expect(digestStore.resultFilter).toBe('unread')
    pressU()
    await flushPromises()
    expect(digestStore.resultFilter).toBe('all')
    wrapper.unmount()
  })

  it('does NOT touch the item filter while in a digest view', async () => {
    // This is the regression that made the shortcut feel dead: the item filter
    // changed underneath, invisibly, and was persisted to localStorage.
    const { useFeedStore } = await import('../stores/feed')
    const { wrapper } = await mountAt('/digests/7')
    const feedStore = useFeedStore()
    await flushPromises()

    const before = feedStore.filter
    pressU()
    await flushPromises()
    expect(feedStore.filter).toBe(before)
    // And nothing was persisted for the next feed visit.
    expect(localStorage.getItem('digesterr-filter')).toBeNull()
    wrapper.unmount()
  })

  it('still toggles the item filter in an item view', async () => {
    const { useFeedStore } = await import('../stores/feed')
    const { wrapper } = await mountAt('/feeds/38')
    const feedStore = useFeedStore()
    await flushPromises()

    expect(feedStore.filter).toBe('all')
    pressU()
    await flushPromises()
    expect(feedStore.filter).toBe('unread')
    wrapper.unmount()
  })

  it('leaves the digest filter untouched in an item view', async () => {
    const { useDigestStore } = await import('../stores/digest')
    const { wrapper } = await mountAt('/feeds/38')
    const digestStore = useDigestStore()
    await flushPromises()

    digestStore.resultFilter = 'all'
    pressU()
    await flushPromises()
    expect(digestStore.resultFilter).toBe('all')
    wrapper.unmount()
  })

  it('persists the shared digest filter back into the list', async () => {
    // The shortcut writes to the store; the list must read from the same place
    // or the toggle would be invisible even though state changed.
    const { useDigestStore } = await import('../stores/digest')
    const { wrapper } = await mountAt('/digests/7')
    const digestStore = useDigestStore()
    await flushPromises()

    digestStore.resultFilter = 'unread'
    await flushPromises()
    // The FilterBar renders the active status label.
    expect(wrapper.text()).toContain('Unread')
    wrapper.unmount()
  })
})
