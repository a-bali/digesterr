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

// The server returns newest first, so fixtures use a descending timestamp per
// id (higher id = newer) — otherwise a merge/sort test passes on ties alone.
function result(id: number, status = 'unread') {
  // Higher id = more recent, so the expected newest-first order is by
  // descending id.
  const created = new Date(Date.UTC(2026, 0, 1, 0, id)).toISOString()
  return { id, status, created_at: created, config_id: 1 }
}

// The 10-second background refresh fetched page one and ASSIGNED it to the
// loaded list. A user who had scrolled to page three therefore watched pages
// two and three vanish every ten seconds, and any result whose status changed
// in the new page-one payload was the only one updated.
//
// The refresh must merge by id: refresh the rows it fetched, keep the rows the
// user already loaded, and not truncate the list.
describe('digest store silent refresh', () => {
  it('keeps already-loaded pages instead of replacing them with page one', async () => {
    const api = await mockApi()
    const store = useDigestStore()
    store.activeConfigId = 1
    store.results = [result(30), result(20), result(10)]
    // Only page one has been loaded, so more pages exist.
    store.hasMore = true

    // The refresh returns only the first page: one refreshed row plus one new.
    ;(api.listConfigs as any).mockResolvedValue([])
    ;(api.listResults as any).mockResolvedValue({
      results: [result(30, 'read'), result(40)],
      has_more: true,
    })

    await store.silentRefresh()

    // Page one refreshed, page one's new row added, and the OLDER loaded rows
    // still present.
    expect(store.results.map(r => r.id)).toEqual([40, 30, 20, 10])
    // The refreshed row's changed status came through.
    expect(store.results.find(r => r.id === 30)!.status).toBe('read')
    // hasMore must not be TAKEN from a first-page response. Assigning
    // resp.has_more clobbered the pagination state of a list that had already
    // loaded further pages: a first-page payload reporting has_more=false would
    // strand rows the user could still have loaded, and one reporting true
    // would leave infinite scroll firing past a fully loaded list.
    expect(store.hasMore).toBe(true)
  })

  it('drops a loaded row the server no longer returns once it reports no more', async () => {
    const api = await mockApi()
    const store = useDigestStore()
    store.activeConfigId = 1
    // Everything the server has is loaded.
    store.results = [result(30), result(20), result(10)]
    store.hasMore = false

    ;(api.listConfigs as any).mockResolvedValue([])
    ;(api.listResults as any).mockResolvedValue({
      results: [result(30), result(20)],
      has_more: false,
    })

    await store.silentRefresh()

    // 10 is gone. This assertion was the OPPOSITE when the merge first landed:
    // it demanded [30, 20, 10], on the reasoning that a refresh must never
    // shrink the list. That was wrong. With has_more false the server has
    // returned its COMPLETE set, so a row it omits no longer exists for this
    // view -- it was deleted, or its status took it out of the active filter.
    // Keeping it left the user looking at a result the server does not have,
    // and its stale presence inflated the paging offset. The no-truncation
    // guarantee is about a refresh that still has further pages to come; see
    // the has_more=true case below.
    expect(store.results.map(r => r.id)).toEqual([30, 20])
    expect(store.hasMore).toBe(false)
  })

  it('keeps rows from a deeper page while further pages still exist', async () => {
    const api = await mockApi()
    const store = useDigestStore()
    store.activeConfigId = 1
    // Three pages loaded; the refresh will only ask for page one.
    store.results = [result(30), result(20), result(10)]
    store.hasMore = true

    ;(api.listConfigs as any).mockResolvedValue([])
    ;(api.listResults as any).mockResolvedValue({
      results: [result(30), result(20)],
      has_more: true,
    })

    await store.silentRefresh()

    // 10 is kept: page one says nothing about it, and there are further pages
    // that might hold it. Dropping rows here is what made a user who had
    // scrolled to page three watch pages two and three vanish every ten seconds.
    expect(store.results.map(r => r.id)).toEqual([30, 20, 10])
    expect(store.hasMore).toBe(true)
  })

  it('refreshes the All-Digests list too, not only a single config', async () => {
    const api = await mockApi()
    const store = useDigestStore()
    // All-Digests mode: no active config.
    store.enterAllDigestsMode()
    await Promise.resolve()
    store.allResults = [result(50)]
    ;(api.listConfigs as any).mockResolvedValue([])
    ;(api.listResults as any).mockResolvedValue({ results: [], has_more: false })
    ;(api.listAllResults as any).mockResolvedValue({
      results: [result(50, 'read'), result(60)],
      has_more: true,
    })

    await store.silentRefresh()

    // Before the fix the config branch was the only one handled, so All-Digests
    // was never refreshed at all.
    expect(store.allResults.map(r => r.id)).toEqual([60, 50])
    expect(store.allResults.find(r => r.id === 50)!.status).toBe('read')
  })
})
