import { describe, it, expect, beforeEach, vi } from 'vitest'
import { setActivePinia, createPinia } from 'pinia'
import { useDigestStore } from './digest'

vi.mock('../services/api', () => ({
  digestApi: {
    listConfigs: vi.fn(),
    createConfig: vi.fn(),
    updateConfig: vi.fn(),
    deleteConfig: vi.fn(),
    listResults: vi.fn(),
    listAllResults: vi.fn(),
    listResultItems: vi.fn(),
    generate: vi.fn(),
    updateResultStatus: vi.fn(),
    deleteResult: vi.fn(),
    unreadCounts: vi.fn(),
  },
  ApiError: class extends Error { constructor(public status: number, body: any) { super(body?.error) } },
}))

beforeEach(() => {
  setActivePinia(createPinia())
  vi.clearAllMocks()
})

function mockApi() {
  return import('../services/api').then(m => m.digestApi)
}

// Higher id = newer, so the expected newest-first order is descending id and
// the oldest-first order is ascending id.
function result(id: number, status = 'unread') {
  const created = new Date(Date.UTC(2026, 0, 1, 0, id)).toISOString()
  return { id, status, created_at: created, config_id: 1 }
}

// The background refresh merged fetched rows into the loaded list, but it did
// so with three flaws the reviewer identified:
//
//   1. mergeById always sorted NEWEST-first, even while the user had selected
//      "Oldest". A refreshed row therefore jumped to the top of an oldest-first
//      list, contradicting the order the user asked for.
//   2. It only ever ADDED or UPDATED rows, never removed any. A row that no
//      longer matched the active unread filter (marked read from another
//      client, say) stayed on screen under a filter that excludes it.
//   3. Those retained stale rows inflated the paging offset, which was taken
//      from the local array length, so the next page skipped real rows.
//
// The refresh must reconcile the loaded window against the server's current
// view: order it the way the user selected, drop rows the server no longer
// returns (or that no longer match the filter), and derive the offset from what
// the server actually served.
describe('digest store refresh reconciliation', () => {
  it('keeps the selected order instead of forcing newest-first', async () => {
    const api = await mockApi()
    const store = useDigestStore()
    store.activeConfigId = 1
    store.resultSort = 'oldest'
    store.results = [result(10), result(20), result(30)]
    store.nextCursor = 30

    ;(api.listConfigs as any).mockResolvedValue([])
    // The server returns oldest-first too, matching the query it was given.
    ;(api.listResults as any).mockResolvedValue({
      results: [result(10), result(20), result(30, 'read'), result(40)],
      has_more: false,
    })

    await store.silentRefresh()

    // Ascending, not descending: the old mergeById hardcoded a descending
    // created_at sort regardless of resultSort.
    expect(store.results.map(r => r.id)).toEqual([10, 20, 30, 40])
  })

  it('drops a refreshed row that no longer matches the active status filter', async () => {
    const api = await mockApi()
    const store = useDigestStore()
    store.activeConfigId = 1
    store.resultFilter = 'unread'
    store.results = [result(30), result(20)]
    store.nextCursor = 20

    ;(api.listConfigs as any).mockResolvedValue([])
    // 30 was marked read elsewhere; 40 is new and still unread.
    ;(api.listResults as any).mockResolvedValue({
      results: [result(40), result(30, 'read'), result(20)],
      has_more: false,
    })

    await store.silentRefresh()

    // 30 must disappear: it does not match the unread filter. The old merge
    // kept it because it only ever added or updated.
    expect(store.results.map(r => r.id)).toEqual([40, 20])
  })

  it('drops rows the server no longer returns when the window is exhausted', async () => {
    const api = await mockApi()
    const store = useDigestStore()
    store.activeConfigId = 1
    store.results = [result(30), result(20), result(10)]
    store.nextCursor = 10

    ;(api.listConfigs as any).mockResolvedValue([])
    // 20 was deleted server-side. Fewer rows come back than requested with
    // has_more false, so the server has nothing the client does not hold.
    ;(api.listResults as any).mockResolvedValue({
      results: [result(30), result(10)],
      has_more: false,
    })

    await store.silentRefresh()

    expect(store.results.map(r => r.id)).toEqual([30, 10])
  })

  it('derives the paging cursor from the last row held, not the local list length', async () => {
    const api = await mockApi()
    const store = useDigestStore()
    store.activeConfigId = 1
    // Two rows loaded, none of which the next refresh returns: the window is
    // full of newer rows, so these two are retained as older pages.
    store.results = [result(2), result(1)]
    store.nextCursor = 2
    store.hasMore = true

    ;(api.listConfigs as any).mockResolvedValue([])
    const page = Array.from({ length: 52 }, (_, i) => result(1000 - i))
    ;(api.listResults as any).mockResolvedValue({ results: page, has_more: true })

    await store.silentRefresh()

    // The union is 54 rows. A COUNT derived from the local length (54) would
    // point past rows the server never served, so the next page skipped two real
    // ones. The cursor is instead the id of the deepest row actually held, which
    // cannot drift like a count: the next page continues from that row.
    expect(store.results.length).toBe(54)
    const deepest = store.results[store.results.length - 1].id
    expect(store.nextCursor).toBe(deepest)

    ;(api.listResults as any).mockResolvedValue({ results: [], has_more: false })
    await store.fetchMoreResults()
    expect((api.listResults as any).mock.calls.at(-1)![1].cursor).toBe(deepest)
  })

  it('refreshes All-Digests with the same reconciliation', async () => {
    const api = await mockApi()
    const store = useDigestStore()
    store.enterAllDigestsMode()
    await Promise.resolve()
    store.allResults = [result(50), result(40)]
    store.resultSort = 'oldest'
    store.nextCursor = 40

    ;(api.listConfigs as any).mockResolvedValue([])
    ;(api.listResults as any).mockResolvedValue({ results: [], has_more: false })
    ;(api.listAllResults as any).mockResolvedValue({
      results: [result(40), result(50)],
      has_more: false,
    })

    await store.silentRefresh()

    expect(store.allResults.map(r => r.id)).toEqual([40, 50])
  })
})
