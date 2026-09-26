import { describe, it, expect, vi, beforeEach } from 'vitest'
import { setActivePinia, createPinia } from 'pinia'
import { useDigestStore } from './digest'
import { digestApi } from '../services/api'

vi.mock('../services/api', () => ({
  digestApi: {
    listConfigs: vi.fn().mockResolvedValue([]),
    listResults: vi.fn(),
    listAllResults: vi.fn(),
    getResult: vi.fn(),
    listResultItems: vi.fn().mockResolvedValue([]),
    unreadCounts: vi.fn().mockResolvedValue({}),
  },
  itemApi: {},
  feedApi: {},
  authApi: {},
  logsApi: { list: vi.fn().mockResolvedValue({ entries: [], total: 0, has_more: false }) },
  setAuthToken: vi.fn(),
  getAuthToken: vi.fn(() => null),
  setOnUnauthorized: vi.fn(),
  ApiError: class extends Error {},
}))

function rows(from: number, count: number) {
  return Array.from({ length: count }, (_, i) => ({
    id: from + i,
    config_id: 7,
    status: 'unread',
    created_at: new Date(Date.UTC(2026, 8, 20, 0, 0, from + i)).toISOString(),
    updated_at: new Date(Date.UTC(2026, 8, 20, 0, 0, from + i)).toISOString(),
    title: `r${from + i}`,
    item_count: 1,
  }))
}

// Ids ascending, time DESCENDING with id -- so id 1 is the newest row, matching
// the report's numbering (results 1-100 with 1 newest). Display order for
// newest-first is therefore 1,2,3,...,N and the last row held is the highest id.
function descRows(ids: number[]) {
  const base = Date.UTC(2026, 8, 20)
  return ids.map(id => ({
    id,
    config_id: 7,
    status: 'unread',
    created_at: new Date(base - id * 60_000).toISOString(),
    updated_at: new Date(base - id * 60_000).toISOString(),
    title: `r${id}`,
    item_count: 1,
  }))
}
function seq(from: number, count: number) {
  return Array.from({ length: count }, (_, i) => from + i)
}

// Pagination continues from a ROW (a cursor), not from a row COUNT. A count
// assumes nothing before the boundary changed, which is false: deleting a result
// -- or changing one so it stops matching the active filter -- shifts every later
// row up by one. The refresh only re-reads page one, so the client never learns
// of the shift; a count then points past a row that was never served, and that
// row is on neither the loaded page nor the next one.
describe('digest pagination continues from a cursor', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    localStorage.clear()
    vi.clearAllMocks()
  })

  it('does not skip the row that shifts into the boundary after a deletion', async () => {
    const store = useDigestStore()

    // Results 1..100 loaded, newest first. The cursor is the last row HELD.
    vi.mocked(digestApi.listResults).mockResolvedValueOnce({
      results: descRows(seq(1, 100)), has_more: true, total: 150,
    } as never)
    await store.setActiveConfig(7)
    expect(store.nextCursor).toBe(100)

    // Result 75 is deleted, so every later row shifts up by one and result 101
    // moves into the first 100. The silent refresh re-reads PAGE ONE only and
    // therefore cannot see the change (75 is past the end of page one).
    vi.mocked(digestApi.listResults).mockResolvedValueOnce({
      results: descRows(seq(1, 50)), has_more: true, total: 149,
    } as never)
    await store.silentRefresh()
    // The boundary is still a row the client holds.
    expect(store.nextCursor).toBe(100)

    // "Load more" continues AFTER row 100. In the shifted set, 101 sits exactly
    // where 100's successor is, so it IS served. A count of 100 would instead
    // make the server skip its first 100 rows -- which now include 101 -- and
    // 101 would be on neither page.
    let requestedCursor: number | undefined
    vi.mocked(digestApi.listResults).mockImplementation(async (_id, opts) => {
      requestedCursor = (opts as { cursor?: number }).cursor
      return { results: descRows(seq(101, 50)), has_more: false, total: 149 } as never
    })
    await store.fetchMoreResults()

    expect(requestedCursor).toBe(100)
    const ids = store.results.map((r: { id: number }) => r.id)
    // The row that shifted into the boundary IS served, and nothing is skipped
    // between the boundary and the new page.
    expect(ids).toContain(101)
    expect(ids).toContain(100)
    expect(ids).not.toContain(undefined as never)
  })

  it('tracks the server total for the header, separate from the loaded count', async () => {
    const store = useDigestStore()
    vi.mocked(digestApi.listResults).mockResolvedValueOnce({
      results: rows(1, 50), has_more: true, total: 120,
    } as never)
    await store.setActiveConfig(7)

    expect(store.results.length).toBe(50)
    expect(store.resultsTotal).toBe(120)
    expect(store.hasMore).toBe(true)
  })

  it('sends no cursor on the first page, so the query starts at the newest row', async () => {
    const store = useDigestStore()
    let firstCallOpts: Record<string, unknown> | undefined
    vi.mocked(digestApi.listResults).mockImplementation(async (_id, opts) => {
      firstCallOpts ??= opts as Record<string, unknown>
      return { results: rows(1, 50), has_more: false, total: 50 } as never
    })
    await store.setActiveConfig(7)

    expect(firstCallOpts?.cursor).toBeUndefined()
    expect(firstCallOpts?.offset).toBeUndefined()
  })

  it('resets the cursor when the view is left, so a stale boundary is not reused', async () => {
    const store = useDigestStore()
    vi.mocked(digestApi.listResults).mockResolvedValueOnce({
      results: rows(1, 50), has_more: true, total: 120,
    } as never)
    await store.setActiveConfig(7)
    expect(store.nextCursor).toBe(50)

    store.enterAllDigestsMode()
    expect(store.nextCursor).toBeNull()
  })

  it('leaves no cursor after an empty page, which reads as "first page"', async () => {
    const store = useDigestStore()
    vi.mocked(digestApi.listResults).mockResolvedValueOnce({
      results: [], has_more: false, total: 0,
    } as never)
    await store.setActiveConfig(7)
    expect(store.nextCursor).toBeNull()
  })
})
