import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { setActivePinia, createPinia } from 'pinia'
import { createRouter, createMemoryHistory } from 'vue-router'
import HomeView from './HomeView.vue'
import { digestApi } from '../services/api'

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

function makeResult(id: number, title: string, created = '2026-08-13T08:00:00Z') {
  return {
    id,
    user_id: 1,
    config_id: 7,
    config_snapshot: '{}',
    content: JSON.stringify({ title, summary: 's', items: [] }),
    status: 'unread',
    created_at: created,
    updated_at: created,
    title,
    item_count: 2,
  }
}

// Page one holds results that deliberately do NOT include the linked one, which
// mirrors the report: an older result deep link resolved to nothing because the
// route handler searched only the loaded page.
const PAGE_ONE = [makeResult(900, 'page one result')]
const LINKED = makeResult(42, 'an older digest from page three', '2026-07-01T06:00:00Z')

function router() {
  return createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/', component: HomeView },
      { path: '/digests', component: HomeView },
      { path: '/digests/:configId/:resultId?', component: HomeView },
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

describe('HomeView deep link to a digest result', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    localStorage.clear()
    vi.clearAllMocks()
    vi.mocked(digestApi.listResults).mockResolvedValue({ results: PAGE_ONE, has_more: true } as never)
    vi.mocked(digestApi.listResultItems).mockResolvedValue([] as never)
    vi.mocked(digestApi.getResult).mockResolvedValue(LINKED as never)
  })

  it('opens a linked result that is not on the loaded page', async () => {
    const wrapper = await mountAt('/digests/7/42')

    // Resolved by id instead of waiting for a page that will never hold it.
    // Before the fix the content pane stayed empty.
    expect(digestApi.getResult).toHaveBeenCalledWith(42)
    await flushPromises()
    expect(wrapper.text()).toContain('an older digest from page three')
  })

  it('does not fetch by id when the linked result is already loaded', async () => {
    const loaded = PAGE_ONE[0]
    const wrapper = await mountAt(`/digests/7/${loaded.id}`)

    // The loaded row is passed to selectResult, which then fetches the full
    // content exactly once. The bug this guards against is a SECOND fetch: an
    // earlier shape of selectResultById fetched by id AND let selectResult
    // fetch the same row again.
    expect(digestApi.getResult).toHaveBeenCalledTimes(1)
    expect(digestApi.getResult).toHaveBeenCalledWith(loaded.id)
    await flushPromises()
    expect(wrapper.text()).toContain('page one result')
  })

  it('leaves the pane empty for a result id the server will not serve', async () => {
    vi.mocked(digestApi.getResult).mockRejectedValue(new Error('404') as never)
    const wrapper = await mountAt('/digests/7/999999')
    await flushPromises()

    // A stale or foreign id must not throw or leave a half-selected result.
    expect(wrapper.text()).not.toContain('an older digest from page three')
  })
})
