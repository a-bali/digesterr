import { describe, it, expect, vi, beforeEach } from 'vitest'
import { setActivePinia, createPinia } from 'pinia'
import { useFeedStore } from './feed'
import { feedApi } from '../services/api'

vi.mock('../services/api', () => ({
  feedApi: {
    list: vi.fn().mockResolvedValue([]),
    getItems: vi.fn(),
    multiItems: vi.fn(),
    unreadCounts: vi.fn().mockResolvedValue({}),
    savedItems: vi.fn(),
    savedCount: vi.fn().mockResolvedValue({ count: 0 }),
  },
  itemApi: { getItem: vi.fn(), updateStatus: vi.fn() },
  digestApi: {}, authApi: {}, logsApi: { list: vi.fn() },
  setAuthToken: vi.fn(), getAuthToken: vi.fn(() => null), setOnUnauthorized: vi.fn(),
  ApiError: class extends Error {},
}))

function deferred<T>() {
  let resolve!: (v: T) => void
  let reject!: (e: unknown) => void
  const promise = new Promise<T>((res, rej) => { resolve = res; reject = rej })
  return { promise, resolve, reject }
}

const ITEM = (id: number) => ({
  id, feed_id: 5, title: `t${id}`, status: 'unread', is_saved: 0,
  created_at: '2026-09-01T00:00:00Z', published_at: '2026-09-01T00:00:00Z',
  feed_name: 'F', feed_type: 'rss',
})

const ok = (id: number) => ({ items: [ITEM(id)], total: 1, has_more: false })

// Claim 4: an older request's `finally` clears the loading flag without checking
// whether a NEWER request owns it, so a stale response can leave the list
// rendering no spinner while a live request is still in flight.
//
// loadPage / loadMultiPage do NOT guard their finally; loadSavedPage DOES
// (`if (rid === fetchReq)`). The contrast is the evidence: the same interleaving
// is safe in the Saved path and unsafe in the other two.
describe('feed loading flag vs a superseded request (claim 4)', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    localStorage.clear()
    vi.clearAllMocks()
  })

  it('a superseded first-page response leaves the loading flag to the current request', async () => {
    const store = useFeedStore()
    store.setActiveFeed(5)

    const slow = deferred<never>()
    const newer = deferred<never>()
    let call = 0
    vi.mocked(feedApi.getItems).mockImplementation(async () => {
      call++
      return (call === 1 ? slow.promise : newer.promise) as never
    })

    // Request A: slow, superseded while pending.
    const pA = store.fetchFirstPage()
    expect(store.loading).toBe(true)

    // Request B: newer, still in flight after A resolves.
    const pB = store.fetchFirstPage()
    expect(store.loading).toBe(true)

    // The STALE request resolves last. Its finally clears the flag
    // unconditionally, so a live request now renders no spinner.
    slow.resolve(ok(99) as never)
    await pA
    await Promise.resolve()

    const loadingWhileNewerInFlight = store.loading

    // The fix: the guarded finally leaves the flag to the request that owns it,
    // so the list keeps its spinner while a live request is still in flight.
    expect(loadingWhileNewerInFlight).toBe(true)

    newer.resolve(ok(6) as never)
    await pB

    // The DATA was already protected (the `rid !== fetchReq` guard returns before
    // assignment), so the stale payload never overwrote items -- the loading flag
    // was the whole of the defect.
    expect(store.items.map((i: { id: number }) => i.id)).toEqual([6])
  })

  it('loadSavedPage behaves the same way, so all three list loaders agree', async () => {
    const store = useFeedStore()
    store.showingSaved = true

    const slow = deferred<never>()
    const newer = deferred<never>()
    let call = 0
    vi.mocked(feedApi.savedItems).mockImplementation(async () => {
      call++
      return (call === 1 ? slow.promise : newer.promise) as never
    })

    const pA = store.fetchSavedItems()
    expect(store.loading).toBe(true)
    const pB = store.fetchSavedItems()
    expect(store.loading).toBe(true)

    slow.resolve({ items: [], has_more: false, total: 0 } as never)
    await pA
    await Promise.resolve()

    // Already guarded before this change; the contrast is what identified the
    // defect in the other two loaders.
    const loadingWhileNewerInFlight = store.loading

    newer.resolve({ items: [], has_more: false, total: 0 } as never)
    await pB

    expect(loadingWhileNewerInFlight).toBe(true)
  })
})
