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
    deleteResult: vi.fn().mockResolvedValue(undefined),
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

const base = Date.UTC(2026, 8, 20)
// id 1 is the newest row (descending created_at with id), matching the report's
// numbering where results 1-100 are loaded with 1 newest. Display order is
// therefore 1,2,3,... so the deepest row held is the highest id.
function descRows(ids: number[]) {
  return ids.map(id => ({
    id, config_id: 7, status: 'unread',
    created_at: new Date(base - id * 60_000).toISOString(),
    updated_at: new Date(base - id * 60_000).toISOString(),
    title: `r${id}`, item_count: 1,
  }))
}
function seq(from: number, count: number) {
  return Array.from({ length: count }, (_, i) => from + i)
}

// A FAITHFUL server model. The real cursor query resolves the boundary by
// looking up the cursor ROW, so a deleted boundary row makes the subquery NULL
// and the page comes back EMPTY with has_more=false -- indistinguishable from
// the end of the list. Modelling that is the whole point: a mock that returns
// rows regardless would pass even while the real server cannot continue.
function liveServer(liveIds: Set<number>, total: number) {
  // Windowed by (cursor_at, cursor_id) -- the rows the query would return. The
  // boundary is supplied by the client, so it is compared as a VALUE, never
  // looked up: this is what lets the page resolve after the boundary row is
  // deleted. `boundaryAt` maps id -> created_at for the rows that once existed.
  const allCreated = new Map<number, string>()
  for (const id of seq(1, 200)) allCreated.set(id, descRows([id])[0].created_at)

  return async (_id: number, opts: { cursor?: number; cursorAt?: string } | undefined) => {
    const { cursor, cursorAt } = opts ?? {}
    const ordered = [...liveIds].sort((a, b) => a - b)
    let window = ordered
    if (cursor != null && cursorAt != null) {
      const at = allCreated.get(cursor)!
      window = ordered.filter(other => {
        const otherAt = allCreated.get(other)!
        // DESC: strictly older, or same instant with a smaller id.
        return otherAt < at || (otherAt === at && other < cursor)
      })
    }
    const page = window.slice(0, 50)
    return {
      results: descRows(page),
      has_more: page.length < window.length,
      total: ordered.length,
    }
  }
}

describe('pagination when the cursor row is deleted', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    localStorage.clear()
    vi.clearAllMocks()
  })

  it('still reaches deeper results after the cursor row is deleted remotely', async () => {
    const store = useDigestStore()
    const live = new Set(seq(1, 150))
    vi.mocked(digestApi.listResults).mockImplementation(liveServer(live, 150) as never)

    // Page one, then page two: the client holds rows 1..100.
    await store.setActiveConfig(7)
    await store.fetchMoreResults()
    expect(store.results.length).toBe(100)
    const afterFirstPages = store.nextCursor

    // A background job (or another client) deletes the DEEPEST row the client
    // holds -- the paging boundary itself. This is the reported case.
    live.delete(100)

    // The silent refresh only re-reads page one, so it cannot see the change and
    // keeps the boundary where it was.
    await store.silentRefresh()
    expect(store.nextCursor).toBe(afterFirstPages)

    // "Load more" must still reach the rows beyond the boundary. With a cursor
    // that NAMES A ROW, the server cannot resolve a deleted one and returns
    // nothing, so results 101+ become unreachable until the list is reset.
    await store.fetchMoreResults()

    const ids = store.results.map((r: { id: number }) => r.id)
    expect(ids).toContain(101)
  })

  it('still reaches deeper results after the cursor row is deleted locally', async () => {
    const store = useDigestStore()
    const live = new Set(seq(1, 150))
    vi.mocked(digestApi.listResults).mockImplementation(liveServer(live, 150) as never)

    await store.setActiveConfig(7)
    await store.fetchMoreResults()
    expect(store.results.length).toBe(100)

    // The user deletes the deepest loaded row -- the paging boundary.
    await store.deleteResult(100)
    live.delete(100)
    expect(store.results.map((r: { id: number }) => r.id)).not.toContain(100)

    await store.fetchMoreResults()

    const ids = store.results.map((r: { id: number }) => r.id)
    expect(ids).toContain(101)
  })
})
