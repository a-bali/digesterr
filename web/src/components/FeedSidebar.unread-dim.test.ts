import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { setActivePinia, createPinia } from 'pinia'
import { createRouter, createMemoryHistory } from 'vue-router'
import FeedSidebar from './FeedSidebar.vue'
import type { Feed, DigestConfig } from '../types/api'

vi.mock('../services/api', () => ({
  feedApi: {
    list: vi.fn().mockResolvedValue([]),
    create: vi.fn(), update: vi.fn(), delete: vi.fn(),
    getItems: vi.fn().mockResolvedValue({ items: [] }),
    fetch: vi.fn(), markRead: vi.fn(),
    unreadCounts: vi.fn().mockImplementation(() => Promise.resolve({})),
  },
  digestApi: {
    listConfigs: vi.fn().mockResolvedValue([]),
    createConfig: vi.fn(), updateConfig: vi.fn(), deleteConfig: vi.fn(), getConfig: vi.fn(),
    generate: vi.fn(),
    listResults: vi.fn().mockResolvedValue({ results: [], has_more: false }),
    getResult: vi.fn(), updateResultStatus: vi.fn(), deleteResult: vi.fn(),
    listResultItems: vi.fn().mockResolvedValue([]),
    unreadCounts: vi.fn().mockImplementation(() => Promise.resolve({})),
  },
  itemApi: { updateStatus: vi.fn(), getItem: vi.fn(), extract: vi.fn() },
  logsApi: { list: vi.fn().mockResolvedValue({ entries: [], total: 0, has_more: false }) },
  setAuthToken: vi.fn(), getAuthToken: vi.fn(() => null), setOnUnauthorized: vi.fn(),
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

class ResizeObserverStub { observe() {} unobserve() {} disconnect() {} }
vi.stubGlobal('ResizeObserver', ResizeObserverStub)

const feed: Feed = {
  id: 1, user_id: 1, category: '', type: 'rss', name: 'Example Feed', url: 'https://example.com/rss',
  config: '', min_score: 0, last_fetched_at: null, status: 'ok', extract_content: 0,
  last_error: '', last_error_at: null, last_error_severity: '', created_at: '', updated_at: '',
}

const digestConfig: DigestConfig = {
  id: 7, user_id: 1, name: 'Morning Brief', schedule: '0 8 * * *', config: '{}',
  last_error: '', last_error_at: null, created_at: '', updated_at: '',
}

// Pass a fresh Pinia in explicitly so each mount gets its own store instances.
// Also note the API mocks above use mockImplementation and NOT
// mockResolvedValue({}) — the latter hands out ONE shared object reference on
// every call, so a test writing into it (store.unreadCounts[7] = 4) poisons every
// later test that receives the same fixture, and clearAllMocks() does not undo it.
async function mountSidebar() {
  const pinia = createPinia()
  setActivePinia(pinia)
  const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/', component: FeedSidebar }] })
  const { digestApi } = await import('../services/api')
  vi.mocked(digestApi.listConfigs).mockResolvedValue([digestConfig])
  const wrapper = mount(FeedSidebar, { global: { plugins: [pinia, router] } })
  await flushPromises()
  // FeedSidebar does not load feeds itself (HomeView calls fetchFeeds), so seed
  // the store directly, as the other sidebar tests do.
  const { useFeedStore } = await import('../stores/feed')
  useFeedStore(pinia).feeds.push(feed)
  await flushPromises()
  return { wrapper, pinia }
}

// The digest row's name span; its label is `cfg.name || cfg.schedule`.
function digestNameClasses(wrapper: ReturnType<typeof mount>): string[] {
  const spans = wrapper.findAll('span.truncate').filter(s => s.text() === 'Morning Brief')
  expect(spans.length).toBe(1)
  return spans[0].classes()
}

describe('FeedSidebar unread-aware dimming', () => {
  beforeEach(() => {
    document.body.innerHTML = ''
    vi.clearAllMocks()
  })

  it('dims a digest config with no unread results', async () => {
    const { wrapper, pinia } = await mountSidebar()
    const { useDigestStore } = await import('../stores/digest')
    const store = useDigestStore(pinia)
    store.unreadCounts[digestConfig.id] = 0
    await flushPromises()

    expect(digestNameClasses(wrapper)).toContain('opacity-60')
    wrapper.unmount()
  })

  it('does not dim a digest config with unread results', async () => {
    const { wrapper, pinia } = await mountSidebar()
    const { useDigestStore } = await import('../stores/digest')
    const store = useDigestStore(pinia)
    store.unreadCounts[digestConfig.id] = 4
    await flushPromises()

    expect(digestNameClasses(wrapper)).not.toContain('opacity-60')
    wrapper.unmount()
  })

  it('does not dim digest rows while counts are still loading', async () => {
    // A missing key means "not loaded yet", not zero: /digest-unread-counts is a
    // separate request, and a failed one must not render as "all read".
    const { wrapper, pinia } = await mountSidebar()
    const { useDigestStore } = await import('../stores/digest')
    const store = useDigestStore(pinia)
    expect(store.unreadCounts[digestConfig.id]).toBeUndefined()

    expect(digestNameClasses(wrapper)).not.toContain('opacity-60')
    wrapper.unmount()
  })

  it('dims feed rows in the sidebar list by the same rule', async () => {
    const { wrapper, pinia } = await mountSidebar()
    const { useFeedStore } = await import('../stores/feed')
    const store = useFeedStore(pinia)
    store.unreadCounts[feed.id] = 0
    await flushPromises()

    const row = wrapper.findAll('span.truncate').filter(s => s.text() === 'Example Feed')
    expect(row.length).toBe(1)
    expect(row[0].classes()).toContain('opacity-60')
    wrapper.unmount()
  })

  it('does not dim feed rows while counts are still loading', async () => {
    const { wrapper, pinia } = await mountSidebar()
    const { useFeedStore } = await import('../stores/feed')
    const store = useFeedStore(pinia)
    expect(store.unreadCounts[feed.id]).toBeUndefined()

    const row = wrapper.findAll('span.truncate').filter(s => s.text() === 'Example Feed')
    expect(row.length).toBe(1)
    expect(row[0].classes()).not.toContain('opacity-60')
    wrapper.unmount()
  })
})
