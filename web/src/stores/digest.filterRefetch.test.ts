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

function result(id: number, status = 'unread') {
  return { id, status, created_at: new Date(Date.UTC(2026, 0, 1, 0, id)).toISOString(), config_id: 1 }
}

// Changing the digest status filter has to RE-QUERY, because the filter is
// applied server-side: the rows matching a new filter may not be loaded at all,
// so re-deriving from the loaded page shows either the previous query's rows or
// an empty list.
//
// Two entry points mutate the same filter: the list's dropdown and the 'u'
// shortcut. The dropdown went through a computed setter that called
// refetchList(); the shortcut assigned resultFilter directly. So pressing 'u'
// flipped the control to "Unread" while leaving the unfiltered page on screen —
// the visible state and the actual query disagreed.
describe('digest filter changes always re-query', () => {
  it('re-queries when the shortcut toggles the filter', async () => {
    const api = await mockApi()
    const store = useDigestStore()
    store.activeConfigId = 1
    ;(api.listConfigs as any).mockResolvedValue([])
    ;(api.listResults as any).mockResolvedValue({ results: [result(30), result(20)], has_more: false })
    await store.setActiveConfig(1)
    ;(api.listResults as any).mockClear()
    // A filtered query legitimately returns fewer rows.
    ;(api.listResults as any).mockResolvedValue({ results: [result(30)], has_more: false })

    store.toggleResultFilter()
    await vi.waitFor(() => expect(store.loading).toBe(false))
    await Promise.resolve()

    expect(store.resultFilter).toBe('unread')
    // The filter reached the server: without this the list still held the
    // unfiltered page while the control read "Unread".
    // The filter reached the server, and it is page ONE: no cursor, so the query
    // starts at the newest row of the new filtered set rather than continuing
    // from the previous query's boundary.
    const [cfgArg, optsArg] = (api.listResults as any).mock.calls.at(-1)!
    expect(cfgArg).toBe(1)
    expect(optsArg.status).toBe('unread')
    expect(optsArg.cursor).toBeUndefined()
    expect(optsArg.offset).toBeUndefined()
    expect(store.results.map(r => r.id)).toEqual([30])
  })

  it('re-queries when the dropdown sets an explicit filter', async () => {
    const api = await mockApi()
    const store = useDigestStore()
    store.activeConfigId = 1
    ;(api.listConfigs as any).mockResolvedValue([])
    ;(api.listResults as any).mockResolvedValue({ results: [result(30)], has_more: false })
    await store.setActiveConfig(1)
    ;(api.listResults as any).mockClear()

    store.setResultFilter('read')
    await vi.waitFor(() => expect(store.loading).toBe(false))
    await Promise.resolve()

    const [, opts] = (api.listResults as any).mock.calls.at(-1)!
    expect(opts.status).toBe('read')
    expect(opts.cursor).toBeUndefined()
  })

  it('re-queries when the sort direction changes', async () => {
    const api = await mockApi()
    const store = useDigestStore()
    store.activeConfigId = 1
    ;(api.listConfigs as any).mockResolvedValue([])
    ;(api.listResults as any).mockResolvedValue({ results: [result(30)], has_more: false })
    await store.setActiveConfig(1)
    ;(api.listResults as any).mockClear()

    store.setResultSort('oldest')
    await vi.waitFor(() => expect(store.loading).toBe(false))
    await Promise.resolve()

    // Order is applied by the server, so the change must reach it.
    const [, sortOpts] = (api.listResults as any).mock.calls.at(-1)!
    expect(sortOpts.sort).toBe('oldest')
    expect(sortOpts.cursor).toBeUndefined()
  })

  it('resets the paging cursor so a filtered query starts at the beginning', async () => {
    const api = await mockApi()
    const store = useDigestStore()
    store.activeConfigId = 1
    ;(api.listConfigs as any).mockResolvedValue([])
    ;(api.listResults as any).mockResolvedValue({ results: [result(30)], has_more: false })
    await store.setActiveConfig(1)
    // The user scrolled, so the cursor advanced to a row.
    store.nextCursor = 50
    ;(api.listResults as any).mockClear()

    store.toggleResultFilter()
    await vi.waitFor(() => expect(store.loading).toBe(false))
    await Promise.resolve()

    // Page one of the NEW query, not a continuation of the old one. Reusing the
    // old cursor would continue from a row that belongs to the previous query.
    const [cfgArg2, resetOpts] = (api.listResults as any).mock.calls.at(-1)!
    expect(cfgArg2).toBe(1)
    expect(resetOpts.cursor).toBeUndefined()
    // The boundary is now the only row the new query served.
    expect(store.nextCursor).toBe(30)
  })
})
