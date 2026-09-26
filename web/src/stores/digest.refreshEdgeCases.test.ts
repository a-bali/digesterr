import { describe, it, expect, beforeEach, vi } from 'vitest'
import { setActivePinia, createPinia } from 'pinia'
import { useDigestStore } from './digest'

vi.mock('../services/api', () => ({
  digestApi: {
    listConfigs: vi.fn(),
    listResults: vi.fn(),
    listAllResults: vi.fn(),
    unreadCounts: vi.fn(),
  },
  ApiError: class extends Error {},
}))

beforeEach(() => {
  setActivePinia(createPinia())
  vi.clearAllMocks()
})

function mockApi() {
  return import('../services/api').then(m => m.digestApi)
}

function result(id: number, status = 'unread') {
  const created = new Date(Date.UTC(2026, 0, 1, 0, id)).toISOString()
  return { id, status, created_at: created, config_id: 1 }
}

// Verification tests for the two reported digest-refresh issues. Each asserts
// the CORRECT behaviour, so a failure is the evidence the report is real.

describe('digest refresh — reported issues', () => {
  // CLAIM 4a: an EMPTY response retains stale results.
  //
  // mergeById returns `loaded` immediately when `fetched` is empty, without
  // consulting hasMore. When the server reports has_more=false the response
  // describes its complete set, so an empty one means every loaded row is gone
  // (read elsewhere, deleted, or filtered out) and must be dropped.
  it('drops stale rows when the server returns an empty complete set', async () => {
    const api = await mockApi()
    const store = useDigestStore()
    store.activeConfigId = 1
    store.results = [result(30), result(20), result(10)]
    store.hasMore = false

    // Everything was read elsewhere: the server now serves nothing, and says so
    // authoritatively with has_more=false.
    ;(api.listConfigs as any).mockResolvedValue([])
    ;(api.listResults as any).mockResolvedValue({ results: [], has_more: false })

    await store.silentRefresh()

    expect(store.results).toEqual([])
  })

  // CONTROL: the same empty response with has_more=true is NOT authoritative
  // about a loaded deeper page, so rows must be retained. Without this, the
  // test above could be satisfied by always clearing.
  it('retains loaded rows on an empty response when more pages exist', async () => {
    const api = await mockApi()
    const store = useDigestStore()
    store.activeConfigId = 1
    store.results = [result(30), result(20), result(10)]
    store.hasMore = true

    ;(api.listConfigs as any).mockResolvedValue([])
    ;(api.listResults as any).mockResolvedValue({ results: [], has_more: true })

    await store.silentRefresh()

    expect(store.results.map(r => r.id)).toEqual([30, 20, 10])
  })

  // CLAIM 4b: the All-Digests refresh lacks a request-generation check.
  //
  // The single-config branch does `const rid = ++resultsReq` and bails when
  // `rid !== resultsReq`, so a slow refresh cannot overwrite a newer fetch. The
  // All-Digests branch assigns allResults.value with no such guard, so a
  // background refresh that resolves AFTER a user-initiated load clobbers it
  // with stale page-one data.
  it('does not let a stale All-Digests refresh overwrite a newer load', async () => {
    const api = await mockApi()
    const store = useDigestStore()
    store.showingAllDigests = true
    store.allResults = [result(10)]
    store.hasMore = false

    let releaseRefresh: (v: any) => void = () => {}
    const refreshPending = new Promise(resolve => {
      releaseRefresh = resolve
    })
    ;(api.listConfigs as any).mockResolvedValue([])
    // Deterministic by CALL ORDER, not by mock-registration order: the refresh
    // is the first listAllResults call made by silentRefresh, and the explicit
    // load is the second. Registration order is racy because silentRefresh
    // awaits listConfigs before issuing its request.
    let allCalls = 0
    ;(api.listAllResults as any).mockImplementation(() => {
      allCalls += 1
      if (allCalls === 1) return refreshPending
      return Promise.resolve({ results: [result(99)], has_more: false })
    })

    const refreshing = store.silentRefresh()
    // Let silentRefresh get past listConfigs and issue its (pending) request.
    await Promise.resolve()
    await Promise.resolve()
    await Promise.resolve()

    // The user loads All-Digests explicitly while the refresh is in flight; this
    // newer load must win.
    await store.loadAllResults(true)

    // Now the stale refresh finally resolves with older page-one data.
    releaseRefresh({ results: [result(10, 'read')], has_more: false })
    await refreshing

    expect(store.allResults.map(r => r.id)).toEqual([99])
  })
})
