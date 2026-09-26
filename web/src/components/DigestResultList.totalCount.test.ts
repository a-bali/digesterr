import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { setActivePinia, createPinia } from 'pinia'
import DigestResultList from './DigestResultList.vue'
import FilterBar from './FilterBar.vue'
import { digestApi } from '../services/api'
import { useDigestStore } from '../stores/digest'

vi.mock('../services/api', () => ({
  digestApi: {
    listConfigs: vi.fn().mockResolvedValue([]),
    listAllResults: vi.fn(),
    listResults: vi.fn(),
    getResult: vi.fn(),
    listResultItems: vi.fn().mockResolvedValue([]),
    unreadCounts: vi.fn().mockResolvedValue({}),
  },
  feedApi: {}, itemApi: {}, authApi: {}, logsApi: { list: vi.fn() },
  setAuthToken: vi.fn(), getAuthToken: vi.fn(() => null), setOnUnauthorized: vi.fn(),
  ApiError: class extends Error {},
}))

class ResizeObserverStub { observe() {} unobserve() {} disconnect() {} }
vi.stubGlobal('ResizeObserver', ResizeObserverStub)

function rows(from: number, count: number) {
  return Array.from({ length: count }, (_, i) => ({
    id: from + i, config_id: 7, status: 'unread',
    created_at: new Date(Date.UTC(2026, 8, 20, 0, 0, from + i)).toISOString(),
    updated_at: new Date(Date.UTC(2026, 8, 20, 0, 0, from + i)).toISOString(),
    title: `r${from + i}`, item_count: 1, config_name: 'C',
  }))
}

// The header's loaded/total pair must describe two DIFFERENT numbers: what has
// been paged in, and how large the filtered set is server-side. Passing the
// loaded count as both rendered "50/50" on a list the server knew held more, so
// the pair carried no information.
describe('digest list header counts', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    localStorage.clear()
    vi.clearAllMocks()
  })

  it('shows the loaded count against the server total, not loaded/loaded', async () => {
    const store = useDigestStore()
    vi.mocked(digestApi.listResults).mockResolvedValue({
      results: rows(1, 50), has_more: true, total: 120,
    } as never)
    await store.setActiveConfig(7)
    await flushPromises()

    const wrapper = mount(DigestResultList, { props: { showingAll: false } })
    await flushPromises()

    const bar = wrapper.findComponent(FilterBar)
    expect(bar.exists()).toBe(true)
    expect(bar.props('loadedCount')).toBe(50)
    expect(bar.props('totalCount')).toBe(120)
    expect(wrapper.find('[data-filter-count]').text()).toBe('50/120')
  })

  it('never renders a total smaller than the loaded count', async () => {
    const store = useDigestStore()
    // A response with no total (older server) falls back to the loaded length,
    // and a total that lags a page must not read as "120/50".
    vi.mocked(digestApi.listResults).mockResolvedValue({
      results: rows(1, 50), has_more: false, total: 10,
    } as never)
    await store.setActiveConfig(7)
    await flushPromises()

    const wrapper = mount(DigestResultList, { props: { showingAll: false } })
    await flushPromises()

    const bar = wrapper.findComponent(FilterBar)
    expect(bar.props('totalCount')).toBeGreaterThanOrEqual(bar.props('loadedCount') as number)
  })

  it('uses the All-Digests total when the section view is shown', async () => {
    const store = useDigestStore()
    vi.mocked(digestApi.listAllResults).mockResolvedValue({
      results: rows(1, 50), has_more: true, total: 200,
    } as never)
    store.enterAllDigestsMode()
    await flushPromises()
    await flushPromises()

    const wrapper = mount(DigestResultList, { props: { showingAll: true } })
    await flushPromises()

    const bar = wrapper.findComponent(FilterBar)
    expect(bar.props('loadedCount')).toBe(50)
    expect(bar.props('totalCount')).toBe(200)
  })
})
