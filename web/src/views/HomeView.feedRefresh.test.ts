import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { setActivePinia, createPinia } from 'pinia'
import { createRouter, createMemoryHistory } from 'vue-router'
import HomeView from './HomeView.vue'
import { feedApi } from '../services/api'

// The sidebar's feed rows carry last_error/last_error_severity. The server clears
// a feed's error as soon as it recovers, but the list was only refetched on mount
// and on unrelated feed actions — so a cleared error mark stayed on screen. The
// 10s poll must refresh the feed list, the same way it already refreshed digest
// configs (which is why digest errors disappeared and feed errors did not).
const listCalls = { n: 0 }

vi.mock('../services/api', () => ({
  authApi: { signout: vi.fn(), changePassword: vi.fn() },
  feedApi: {
    list: vi.fn(() => { listCalls.n++; return Promise.resolve([]) }),
    create: vi.fn(), update: vi.fn(), delete: vi.fn(),
    getItems: vi.fn().mockResolvedValue({ items: [], total: 0, has_more: false }),
    multiItems: vi.fn().mockResolvedValue({ items: [], total: 0, has_more: false }),
    fetch: vi.fn(),
    markRead: vi.fn().mockResolvedValue({ message: 'ok' }),
    markReadAll: vi.fn().mockResolvedValue({ message: 'ok' }),
    unreadCounts: vi.fn().mockResolvedValue({}),
    savedCount: vi.fn().mockResolvedValue({ count: 0 }),
    savedItems: vi.fn().mockResolvedValue({ items: [], total: 0, has_more: false }),
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

async function mountHome() {
  const pinia = createPinia()
  setActivePinia(pinia)
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [{ path: '/', component: { template: '<div />' } }],
  })
  const wrapper = mount(HomeView, {
    attachTo: document.body,
    global: { plugins: [pinia, router] },
  })
  await flushPromises()
  return wrapper
}

describe('sidebar feed-error refresh', () => {
  beforeEach(() => { listCalls.n = 0 })
  afterEach(() => {
    document.body.innerHTML = ''
    vi.useRealTimers()
  })

  it('refetches the feed list on every poll tick, so a cleared error disappears', async () => {
    // Fake timers must be installed BEFORE mount: the poll interval is created
    // during onMounted, and an interval registered against the real clock is not
    // driven by advanceTimersByTime.
    vi.useFakeTimers()
    const wrapper = await mountHome()
    const afterMount = listCalls.n
    expect(afterMount).toBeGreaterThan(0)

    await vi.advanceTimersByTimeAsync(30_000)
    await flushPromises()

    // Three ticks, three refetches — the rows carrying last_error are reloaded.
    expect(listCalls.n).toBeGreaterThan(afterMount + 2)
    wrapper.unmount()
  })

  it('does not refetch feeds while the tab is hidden', async () => {
    const wrapper = await mountHome()
    vi.useFakeTimers()

    Object.defineProperty(document, 'hidden', { value: true, configurable: true })
    const before = listCalls.n
    await vi.advanceTimersByTimeAsync(30_000)
    await flushPromises()

    expect(listCalls.n).toBe(before)
    Object.defineProperty(document, 'hidden', { value: false, configurable: true })
    wrapper.unmount()
  })
})
