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

function pressA() {
  window.dispatchEvent(new KeyboardEvent('keydown', { key: 'A', bubbles: true }))
}

async function mountAt(path: string) {
  const pinia = createPinia()
  setActivePinia(pinia)
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/', component: HomeView },
      { path: '/digests', component: HomeView },
      { path: '/digests/:configId', component: HomeView },
      { path: '/feeds/:id', component: HomeView },
      { path: '/saved', component: HomeView },
    ],
  })
  router.push(path)
  await router.isReady()
  const wrapper = mount(HomeView, { global: { plugins: [pinia, router] } })
  await flushPromises()
  return { wrapper, router }
}

describe("'u' shortcut in the saved view", () => {
  beforeEach(() => {
    localStorage.clear()
    vi.clearAllMocks()
  })

  it('does nothing, and does not touch the persisted item filter', async () => {
    // Saved always shows every saved item, so there is no view-local filter for
    // 'u' to redirect to (unlike a digest view, which has its own). The risk is
    // not that the keypress is inert, it is that it silently flips the stored
    // item filter — which the user cannot see from here, and which the next
    // feed visit would then honour.
    const { useFeedStore } = await import('../stores/feed')
    const { wrapper } = await mountAt('/saved')
    const feedStore = useFeedStore()
    await flushPromises()

    const before = feedStore.filter
    pressU()
    await flushPromises()

    expect(feedStore.filter).toBe(before)
    expect(feedStore.showingSaved).toBe(true)
    expect(localStorage.getItem('digesterr-filter')).toBeNull()
    wrapper.unmount()
  })
})

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

  it('re-queries the list when the shortcut toggles the filter', async () => {
    // The filter is applied SERVER-side, so the keypress must reach the API.
    //
    // The dropdown did (its computed setter called refetchList); the shortcut
    // assigned resultFilter directly, so the control flipped to "Unread" while
    // the list still held the unfiltered page and the two disagreed. Asserting
    // on the filter VALUE cannot catch that — it is correct either way. Only
    // the request proves the list was actually requeryied.
    const { digestApi } = await import('../services/api')
    const { wrapper } = await mountAt('/digests/7')
    await flushPromises()
    vi.mocked(digestApi.listResults).mockClear()

    pressU()
    await flushPromises()

    // First page of the new filtered query means NO cursor: continuing from the
    // old boundary would page through the previous query's rows.
    const [cfgArg, opts] = vi.mocked(digestApi.listResults).mock.calls.at(-1)!
    expect(cfgArg).toBe(7)
    expect(opts.status).toBe('unread')
    expect(opts.cursor).toBeUndefined()
    expect(opts.offset).toBeUndefined()
    wrapper.unmount()
  })
})

// Shift+A must mark read for whichever list is ON SCREEN, exactly like the
// pane's own button. The bug: HomeView wired the shortcut straight to
// markAllReadInCurrentView, which only inspects FEED state — and a digest view
// has cleared it (activeFeedId/activeCategory are null), so every branch fell
// through and the key was silently a no-op in both digest views.
describe("'A' shortcut in digest views", () => {
  beforeEach(() => {
    localStorage.clear()
    vi.clearAllMocks()
  })

  it('marks the active digest config read from the keyboard', async () => {
    const { digestApi } = await import('../services/api')
    vi.mocked(digestApi.listConfigs).mockResolvedValue([
      { id: 7, user_id: 1, name: 'Morning Brief', schedule: 'daily', config: '{}',
        last_error: '', last_error_at: null, created_at: '', updated_at: '' },
    ])
    const { wrapper } = await mountAt('/digests/7')
    await flushPromises()

    pressA()
    await flushPromises()
    // A single config confirms first, exactly like a single feed — the shortcut
    // must NOT mark anything read until the dialog is confirmed.
    expect(vi.mocked(digestApi.markAllRead)).not.toHaveBeenCalled()
    const dlg = document.querySelector('[role="dialog"]')
    expect(dlg, 'a confirmation dialog should open').not.toBeNull()
    expect(dlg!.textContent).toContain('Mark all as read?')
    wrapper.unmount()
  })

  it('only marks read once the confirmation is accepted', async () => {
    const { digestApi } = await import('../services/api')
    vi.mocked(digestApi.listConfigs).mockResolvedValue([
      { id: 7, user_id: 1, name: 'Morning Brief', schedule: 'daily', config: '{}',
        last_error: '', last_error_at: null, created_at: '', updated_at: '' },
    ])
    const { wrapper } = await mountAt('/digests/7')
    await flushPromises()

    pressA()
    await flushPromises()
    expect(vi.mocked(digestApi.markAllRead)).not.toHaveBeenCalled()

    const confirm = document.querySelector('[data-mark-all-read-confirm]') as HTMLElement
    expect(confirm, 'the confirm button should exist').not.toBeNull()
    confirm.click()
    await flushPromises()
    expect(vi.mocked(digestApi.markAllRead)).toHaveBeenCalledWith(7)
    wrapper.unmount()
  })

  it('cancelling marks nothing read', async () => {
    const { digestApi } = await import('../services/api')
    vi.mocked(digestApi.listConfigs).mockResolvedValue([
      { id: 7, user_id: 1, name: 'Morning Brief', schedule: 'daily', config: '{}',
        last_error: '', last_error_at: null, created_at: '', updated_at: '' },
    ])
    const { wrapper } = await mountAt('/digests/7')
    await flushPromises()

    pressA()
    await flushPromises()
    const cancel = [...document.querySelectorAll('[role="dialog"] button')]
      .find(b => /Cancel/.test(b.textContent || '')) as HTMLElement
    expect(cancel).not.toBeUndefined()
    cancel.click()
    await flushPromises()
    expect(vi.mocked(digestApi.markAllRead)).not.toHaveBeenCalled()
    wrapper.unmount()
  })

  it('marks every digest read from the keyboard in the All Digests section', async () => {
    const { digestApi } = await import('../services/api')
    const { wrapper } = await mountAt('/digests')
    await flushPromises()

    pressA()
    await flushPromises()

    // The All Digests section now confirms as well. It used to mark every digest
    // read straight from the keyboard, which is the inconsistency reported for
    // 'A' in a digest view: the key destroyed the section with no undo.
    expect(vi.mocked(digestApi.markAllRead), 'nothing is marked before confirming').not.toHaveBeenCalled()
    const confirm = document.querySelector('[data-mark-all-read-confirm]') as HTMLElement
    expect(confirm, 'All Digests must confirm before marking').not.toBeNull()
    confirm.click()
    await flushPromises()

    expect(vi.mocked(digestApi.markAllRead)).toHaveBeenCalledWith(undefined)
    wrapper.unmount()
  })

  it('does nothing when neither a feed nor a digest is on screen', async () => {
    // A no-op is the correct outcome here — the point is only that it must not
    // reach for a feed that is not selected.
    const { digestApi, feedApi } = await import('../services/api')
    const { wrapper } = await mountAt('/')
    await flushPromises()

    pressA()
    await flushPromises()
    expect(vi.mocked(digestApi.markAllRead)).not.toHaveBeenCalled()
    expect(vi.mocked(feedApi.markReadAll)).not.toHaveBeenCalled()
    wrapper.unmount()
  })
})

// The saved view clears every feed/category/digest selection, so the page title
// had no name to render — and formatTitle() discards the item title when the
// name is empty, which is why BOTH halves went missing.
describe('page title in the saved view', () => {
  beforeEach(() => {
    localStorage.clear()
    vi.clearAllMocks()
    document.title = ''
  })

  it('names the view and shows the saved total when nothing is selected', async () => {
    const { useFeedStore } = await import('../stores/feed')
    const { wrapper } = await mountAt('/saved')
    const feedStore = useFeedStore()
    feedStore.savedCount = 4
    await flushPromises()

    expect(document.title).toBe('[0] 📰 (Saved: 4)')
    wrapper.unmount()
  })

  it('includes the selected article title', async () => {
    const { useFeedStore } = await import('../stores/feed')
    const { wrapper } = await mountAt('/saved')
    const feedStore = useFeedStore()
    feedStore.savedCount = 4
    feedStore.selectedItem = { id: 9, title: 'A saved article' } as any
    await flushPromises()

    expect(document.title).toBe('[0] 📰 (Saved: 4) A saved article')
    wrapper.unmount()
  })
})
