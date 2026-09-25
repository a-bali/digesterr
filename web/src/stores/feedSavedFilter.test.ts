import { describe, it, expect, beforeEach, vi } from 'vitest'
import { setActivePinia, createPinia } from 'pinia'
import { useFeedStore } from './feed'

vi.mock('../services/api', () => ({
  feedApi: {
    list: vi.fn(),
    create: vi.fn(),
    update: vi.fn(),
    delete: vi.fn(),
    getItems: vi.fn(),
    multiItems: vi.fn(),
    savedItems: vi.fn(),
    fetch: vi.fn(),
    unreadCounts: vi.fn(),
  },
  itemApi: {
    updateStatus: vi.fn(),
    deleteExtracted: vi.fn(),
    getItem: vi.fn(),
  },
  ApiError: class extends Error { constructor(public status: number, body: any) { super(body?.error) } },
}))

beforeEach(() => {
  setActivePinia(createPinia())
  localStorage.clear()
})

// Saving an item is an explicit act of keeping it, so the Saved view must always
// list every saved item. It used to send the persisted read-state filter to the
// server, which meant a global "Unread only" setting — meant for browsing feeds —
// silently hid saved items the user had deliberately kept.
describe('saved view ignores the read-state filter', () => {
  it('sends no status filter even when the stored filter is unread', async () => {
    const { feedApi } = await import('../services/api')
    const store = useFeedStore()
    ;(feedApi.savedItems as any).mockResolvedValue({ items: [], has_more: false, total: 0 })

    store.filter = 'unread'
    await store.fetchSavedItems()

    const arg = (feedApi.savedItems as any).mock.calls[0][0]
    expect(arg.status).toBeUndefined()
    expect(arg).toEqual({ sort: 'date_desc', limit: 50, offset: 0 })
  })

  it('sends no status filter for any of the other filter values either', async () => {
    const { feedApi } = await import('../services/api')
    const store = useFeedStore()
    ;(feedApi.savedItems as any).mockResolvedValue({ items: [], has_more: false, total: 0 })

    for (const f of ['all', 'unread', 'read', 'digested', 'saved'] as const) {
      ;(feedApi.savedItems as any).mockClear()
      store.filter = f
      await store.fetchSavedItems()
      expect((feedApi.savedItems as any).mock.calls[0][0].status, `filter=${f}`).toBeUndefined()
    }
  })

  it('keeps the parsed items regardless of the stored filter', async () => {
    const { feedApi } = await import('../services/api')
    const store = useFeedStore()
    ;(feedApi.savedItems as any).mockResolvedValue({
      items: [{ id: 1, status: 'read' }, { id: 2, status: 'unread' }],
      has_more: false,
      total: 2,
    })

    store.filter = 'unread'
    await store.fetchSavedItems()

    // Both rows are present: a read saved item is still a saved item.
    expect(store.savedItems.map(i => i.id)).toEqual([1, 2])
  })
})

// The feeds list carries last_error/last_error_severity, and the 10s poll is the
// only thing that refreshes it on a timer. Without it, a feed's error mark stayed
// in the sidebar after the condition had cleared on the server.
describe('fetchFeeds silent mode', () => {
  it('does not raise an error toast when silent', async () => {
    const { feedApi } = await import('../services/api')
    const toast = await import('../lib/toast')
    const spy = vi.spyOn(toast, 'toastError').mockImplementation(() => {})
    const store = useFeedStore()
    ;(feedApi.list as any).mockRejectedValue(new Error('network down'))

    await store.fetchFeeds({ silent: true })

    expect(spy).not.toHaveBeenCalled()
    spy.mockRestore()
  })

  it('still raises an error toast when not silent', async () => {
    const { feedApi } = await import('../services/api')
    const toast = await import('../lib/toast')
    const spy = vi.spyOn(toast, 'toastError').mockImplementation(() => {})
    const store = useFeedStore()
    ;(feedApi.list as any).mockRejectedValue(new Error('network down'))

    await store.fetchFeeds()

    expect(spy).toHaveBeenCalled()
    spy.mockRestore()
  })

  it('updates feeds on success so a cleared error reaches the sidebar', async () => {
    const { feedApi } = await import('../services/api')
    const store = useFeedStore()
    ;(feedApi.list as any).mockResolvedValue([
      { id: 1, name: 'Recovered', last_error: '', last_error_severity: '' },
    ])
    ;(feedApi.unreadCounts as any).mockResolvedValue({})

    await store.fetchFeeds({ silent: true })

    expect(store.feeds).toHaveLength(1)
    expect(store.feeds[0].last_error).toBe('')
  })
})
