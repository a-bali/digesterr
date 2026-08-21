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
