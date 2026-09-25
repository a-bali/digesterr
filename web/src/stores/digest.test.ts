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
})

// Deferred promises let us control response ordering to simulate out-of-order
// (stale-after-fresh) network responses.
function deferred<T>() {
  let resolve!: (v: T) => void
  let reject!: (e: any) => void
  const promise = new Promise<T>((res, rej) => { resolve = res; reject = rej })
  return { promise, resolve, reject }
}

describe('digest store stale-request guards', () => {
  it('drops an older setActiveConfig response that resolves after a newer one', async () => {
    const { digestApi } = await import('../services/api')
    const first = deferred<{ results: any[]; has_more: boolean }>()
    const second = deferred<{ results: any[]; has_more: boolean }>()
    ;(digestApi.listResults as ReturnType<typeof vi.fn>)
      .mockImplementationOnce(() => first.promise)
      .mockImplementationOnce(() => second.promise)

    const store = useDigestStore()
    const p1 = store.setActiveConfig(1)
    const p2 = store.setActiveConfig(2)

    // Newer request resolves first with its results.
    second.resolve({ results: [{ id: 22, status: 'unread' }], has_more: false })
    await p2
    expect(store.results.map(r => r.id)).toEqual([22])
    expect(store.loading).toBe(false)

    // Stale request finally resolves — must NOT overwrite config 2's results,
    // nor clobber the loading flag while nothing is in flight.
    first.resolve({ results: [{ id: 11, status: 'unread' }], has_more: true })
    await p1
    expect(store.results.map(r => r.id)).toEqual([22])
    expect(store.activeConfigId).toBe(2)
    expect(store.hasMore).toBe(false)
    expect(store.loading).toBe(false)
  })

  it('fetchMoreResults uses the offset captured at request time', async () => {
    const { digestApi } = await import('../services/api')
    ;(digestApi.listResults as ReturnType<typeof vi.fn>).mockResolvedValue({
      results: [{ id: 1 }, { id: 2 }],
      has_more: true,
    })

    const store = useDigestStore()
    await store.setActiveConfig(1)
    expect(store.results).toHaveLength(2)

    const pending = deferred<{ results: any[]; has_more: boolean }>()
    ;(digestApi.listResults as ReturnType<typeof vi.fn>).mockImplementationOnce(() => pending.promise)
    const morePromise = store.fetchMoreResults()

    // Offset was captured before the request resolved, regardless of later mutations.
    expect(digestApi.listResults).toHaveBeenLastCalledWith(1, { limit: 50, offset: 2 })
    pending.resolve({ results: [{ id: 3 }], has_more: false })
    await morePromise
    expect(store.results.map(r => r.id)).toEqual([1, 2, 3])
  })

  it('drops a stale fetchMoreResults append after a newer setActiveConfig', async () => {
    const { digestApi } = await import('../services/api')
    ;(digestApi.listResults as ReturnType<typeof vi.fn>)
      .mockResolvedValueOnce({ results: [{ id: 1 }], has_more: true })
    const staleMore = deferred<{ results: any[]; has_more: boolean }>()
    ;(digestApi.listResults as ReturnType<typeof vi.fn>)
      .mockImplementationOnce(() => staleMore.promise)
    ;(digestApi.listResults as ReturnType<typeof vi.fn>)
      .mockResolvedValueOnce({ results: [{ id: 99 }], has_more: false })

    const store = useDigestStore()
    await store.setActiveConfig(1)
    const morePromise = store.fetchMoreResults()
    await store.setActiveConfig(2)
    staleMore.resolve({ results: [{ id: 5 }], has_more: false })
    await morePromise

    // Stale page for config 1 must not be appended to config 2's results.
    expect(store.results.map(r => r.id)).toEqual([99])
    expect(store.loadingMore).toBe(false)
  })

  it('silentRefresh does not clobber a newer setActiveConfig', async () => {
    const { digestApi } = await import('../services/api')
    const configs = deferred<any[]>()
    ;(digestApi.listConfigs as ReturnType<typeof vi.fn>).mockImplementationOnce(() => configs.promise)
    ;(digestApi.listResults as ReturnType<typeof vi.fn>)
      .mockResolvedValueOnce({ results: [{ id: 1 }], has_more: true })   // initial setActiveConfig
    const refresh = deferred<{ results: any[]; has_more: boolean }>()
    ;(digestApi.listResults as ReturnType<typeof vi.fn>)
      .mockImplementationOnce(() => refresh.promise)                      // silentRefresh
    ;(digestApi.listResults as ReturnType<typeof vi.fn>)
      .mockResolvedValueOnce({ results: [{ id: 42 }], has_more: false })  // newer setActiveConfig

    const store = useDigestStore()
    await store.setActiveConfig(3)
    const refreshPromise = store.silentRefresh()
    // Let silentRefresh pass its listConfigs await and issue its listResults
    // request (consuming the refresh deferred).
    configs.resolve([])
    for (let i = 0; i < 5; i++) await Promise.resolve()
    // The newer config switch bumps the request counter; when the older
    // silentRefresh response finally arrives it must be dropped.
    await store.setActiveConfig(4)
    refresh.resolve({ results: [{ id: 7 }], has_more: true })
    await refreshPromise
    expect(store.results.map(r => r.id)).toEqual([42])
    expect(store.hasMore).toBe(false)
  })
})

describe('digest store status normalisation', () => {
  // The API stores digests as status "new"; the UI works in terms of "unread".
  // The All-Digests path skipped normalize(), so unread results there rendered
  // as read and the Unread filter (and the 'u' shortcut) matched nothing.
  it('maps "new" to "unread" in the All-Digests list', async () => {
    const { digestApi } = await import('../services/api')
    ;(digestApi.listAllResults as ReturnType<typeof vi.fn>).mockResolvedValue({
      results: [
        { id: 1, status: 'new', config_name: 'c' },
        { id: 2, status: 'read', config_name: 'c' },
      ],
      has_more: false,
    })

    const store = useDigestStore()
    await store.loadAllResults(true)

    expect(store.allResults.map(r => r.status)).toEqual(['unread', 'read'])
  })

  it('normalises appended pages too, not just the first', async () => {
    const { digestApi } = await import('../services/api')
    ;(digestApi.listAllResults as ReturnType<typeof vi.fn>).mockResolvedValue({
      results: [{ id: 9, status: 'new', config_name: 'c' }],
      has_more: false,
    })

    const store = useDigestStore()
    await store.loadAllResults(false)

    expect(store.allResults.map(r => r.status)).toEqual(['unread'])
  })

  it('maps "new" to "unread" in the per-config list (unchanged behaviour)', async () => {
    const { digestApi } = await import('../services/api')
    ;(digestApi.listResults as ReturnType<typeof vi.fn>).mockResolvedValue({
      results: [{ id: 3, status: 'new' }],
      has_more: false,
    })

    const store = useDigestStore()
    await store.setActiveConfig(1)

    expect(store.results.map(r => r.status)).toEqual(['unread'])
  })
})

// The All-Digests list wires its infinite-scroll handler to fetchMoreResults,
// which used to bail out immediately whenever there was no active config — i.e.
// always, in that mode. The list therefore stopped at the first page with the
// server still reporting more, and no amount of scrolling revealed the rest.
describe('digest store All-Digests pagination', () => {
  // This file's global beforeEach only resets Pinia, so mock call counts and
  // implementations carry over between tests; these assertions are all about
  // call counts.
  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('fetchMoreResults appends the next page while in All-Digests mode', async () => {
    const { digestApi } = await import('../services/api')
    ;(digestApi.listAllResults as ReturnType<typeof vi.fn>)
      .mockResolvedValueOnce({ results: [{ id: 1, status: 'new', config_name: 'c' }], has_more: true })
      .mockResolvedValueOnce({ results: [{ id: 2, status: 'new', config_name: 'c' }], has_more: false })

    const store = useDigestStore()
    await store.enterAllDigestsMode()
    expect(store.allResults.map(r => r.id)).toEqual([1])
    expect(store.hasMore).toBe(true)

    await store.fetchMoreResults()

    expect(store.allResults.map(r => r.id)).toEqual([1, 2])
    expect(store.hasMore).toBe(false)
    // The second call must be a continuation, not a restart.
    expect(digestApi.listAllResults).toHaveBeenLastCalledWith({ limit: expect.any(Number), offset: 1 })
  })

  it('fetchMoreResults is a no-op in All-Digests mode when the server says there is no more', async () => {
    const { digestApi } = await import('../services/api')
    ;(digestApi.listAllResults as ReturnType<typeof vi.fn>)
      .mockResolvedValue({ results: [{ id: 1, status: 'new', config_name: 'c' }], has_more: false })

    const store = useDigestStore()
    await store.enterAllDigestsMode()
    expect(digestApi.listAllResults).toHaveBeenCalledTimes(1)

    await store.fetchMoreResults()

    expect(digestApi.listAllResults).toHaveBeenCalledTimes(1)
    expect(store.allResults.map(r => r.id)).toEqual([1])
  })

  it('fetchMoreResults does nothing when neither a config nor All-Digests mode is active', async () => {
    const { digestApi } = await import('../services/api')

    const store = useDigestStore()
    await store.fetchMoreResults()

    expect(digestApi.listAllResults).not.toHaveBeenCalled()
    expect(digestApi.listResults).not.toHaveBeenCalled()
    expect(store.allResults).toEqual([])
  })

  it('fetchMoreResults still pages a single config rather than All-Digests', async () => {
    const { digestApi } = await import('../services/api')
    ;(digestApi.listResults as ReturnType<typeof vi.fn>)
      .mockResolvedValueOnce({ results: [{ id: 1, status: 'new' }], has_more: true })
      .mockResolvedValueOnce({ results: [{ id: 2, status: 'new' }], has_more: false })

    const store = useDigestStore()
    await store.setActiveConfig(5)
    await store.fetchMoreResults()

    expect(store.results.map(r => r.id)).toEqual([1, 2])
    expect(digestApi.listAllResults).not.toHaveBeenCalled()
  })
})
