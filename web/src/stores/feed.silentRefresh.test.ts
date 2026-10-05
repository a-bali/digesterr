import { describe, it, expect, beforeEach, vi } from 'vitest'
import { setActivePinia, createPinia } from 'pinia'
import { useFeedStore } from './feed'

// The 10-second poll must inject newly arrived items into the OPEN middle pane.
// Before the fix it refreshed only the sidebar counts, so a new item raised the
// badge while the list stayed frozen. The poll must also MERGE: it may not
// discard pages the reader scrolled to, and a new row must land where the
// server's sort puts it (the old silent merge appended it to the END, which is
// the bottom of a newest-first feed).
vi.mock('../services/api', () => ({
  feedApi: {
    list: vi.fn(),
    getItems: vi.fn(),
    multiItems: vi.fn(),
    savedItems: vi.fn(),
    unreadCounts: vi.fn().mockResolvedValue({}),
    savedCount: vi.fn().mockResolvedValue({ count: 0 }),
  },
  itemApi: {
    updateStatus: vi.fn(),
    deleteExtracted: vi.fn(),
    getItem: vi.fn(),
  },
  ApiError: class extends Error { constructor(public status: number, body: any) { super(body?.error) } },
}))

function makeItem(id: number, publishedAt: string, status = 'unread') {
  return {
    id, title: `Item ${id}`, content: 'body', comment_url: '',
    points: 0, comments_count: 0,
    // published_at arrives as the Go time.Time JSON shape, NOT a string. Fixtures
    // must use it too: a plain ISO string made the comparator look correct while
    // production rows (an object) sorted as NaN.
    published_at: { Time: publishedAt, Valid: true },
    status, is_saved: 0, extract_content: '', extract_status: '',
  }
}

beforeEach(() => {
  setActivePinia(createPinia())
  vi.clearAllMocks()
})

describe('feed store silentRefresh (10s item poll)', () => {
  it('injects a new item at the TOP of a newest-first feed', async () => {
    const { feedApi } = await import('../services/api')
    const store = useFeedStore()
    // Loaded list: two older items.
    store.activeFeedId = 7
    store.items = [makeItem(10, '2026-01-02T00:00:00Z'), makeItem(9, '2026-01-01T00:00:00Z')] as never
    store.totalItems = 2
    // Page one now reports a NEWER item at the top.
    vi.mocked(feedApi.getItems).mockResolvedValue({
      items: [makeItem(11, '2026-01-03T00:00:00Z'), makeItem(10, '2026-01-02T00:00:00Z'), makeItem(9, '2026-01-01T00:00:00Z')],
      total: 3, has_more: false,
    } as never)

    await store.silentRefresh()

    // The arrival is at the TOP, not appended to the bottom.
    expect(store.items.map(i => i.id)).toEqual([11, 10, 9])
    // The header total moves with the sidebar badge.
    expect(store.totalItems).toBe(3)
  })

  it('injects a new item at the BOTTOM of an oldest-first feed', async () => {
    const { feedApi } = await import('../services/api')
    const store = useFeedStore()
    store.activeFeedId = 7
    store.sort = 'date_asc'
    store.items = [makeItem(9, '2026-01-01T00:00:00Z'), makeItem(10, '2026-01-02T00:00:00Z')] as never
    store.totalItems = 2
    vi.mocked(feedApi.getItems).mockResolvedValue({
      items: [makeItem(9, '2026-01-01T00:00:00Z'), makeItem(10, '2026-01-02T00:00:00Z'), makeItem(11, '2026-01-03T00:00:00Z')],
      total: 3, has_more: false,
    } as never)

    await store.silentRefresh()

    expect(store.items.map(i => i.id)).toEqual([9, 10, 11])
    expect(store.totalItems).toBe(3)
  })

  it('keeps deeper pages the reader scrolled to, while injecting the arrival', async () => {
    const { feedApi } = await import('../services/api')
    const store = useFeedStore()
    store.activeFeedId = 7
    // Three rows loaded (one page scrolled past page one); page one returns only
    // the top two plus a new arrival, and says nothing about rows 9 and 8.
    store.items = [makeItem(10, '2026-01-02T00:00:00Z'), makeItem(9, '2026-01-01T00:00:00Z'), makeItem(8, '2025-12-31T00:00:00Z')] as never
    vi.mocked(feedApi.getItems).mockResolvedValue({
      items: [makeItem(11, '2026-01-03T00:00:00Z'), makeItem(10, '2026-01-02T00:00:00Z')],
      total: 3, has_more: true,
    } as never)

    await store.silentRefresh()

    // New row injected, and the deeper rows the page did not mention survive.
    expect(store.items.map(i => i.id)).toEqual([11, 10, 9, 8])
  })

  it('does not blank the pane or the count on a transient empty response', async () => {
    const { feedApi } = await import('../services/api')
    const store = useFeedStore()
    store.activeFeedId = 7
    store.items = [makeItem(10, '2026-01-02T00:00:00Z'), makeItem(9, '2026-01-01T00:00:00Z')] as never
    store.totalItems = 2
    vi.mocked(feedApi.getItems).mockResolvedValue({ items: [], total: 0, has_more: false } as never)

    await store.silentRefresh()

    expect(store.items.map(i => i.id)).toEqual([10, 9])
    // Adopting the response's total would claim the scope is empty while rows
    // are still on screen.
    expect(store.totalItems).toBe(2)
  })

  it('refreshes a category view with offset 0', async () => {
    const { feedApi } = await import('../services/api')
    const store = useFeedStore()
    store.activeCategory = 'tech'
    store.multiItems = [makeItem(10, '2026-01-02T00:00:00Z')] as never
    vi.mocked(feedApi.multiItems).mockResolvedValue({
      items: [makeItem(11, '2026-01-03T00:00:00Z'), makeItem(10, '2026-01-02T00:00:00Z')],
      total: 2, has_more: false,
    } as never)

    await store.silentRefresh()

    const opts = vi.mocked(feedApi.multiItems).mock.calls[0][0]
    expect(opts.offset).toBe(0)
    expect(opts.category).toBe('tech')
    expect(opts.feed_ids).toBeUndefined()
    expect(store.multiItems.map(i => i.id)).toEqual([11, 10])
  })

  it('drops a poll response that a user-initiated load superseded', async () => {
    const { feedApi } = await import('../services/api')
    const store = useFeedStore()
    store.activeFeedId = 7
    store.items = [makeItem(9, '2026-01-01T00:00:00Z')] as never

    let resolvePoll!: (v: unknown) => void
    const pollPromise = new Promise(r => { resolvePoll = r })
    // First getItems call is the poll (made to hang); the user load that
    // supersedes it gets the immediate user data.
    vi.mocked(feedApi.getItems).mockImplementationOnce(() => pollPromise as never)
    vi.mocked(feedApi.getItems).mockResolvedValue({
      items: [makeItem(20, '2026-02-01T00:00:00Z')], total: 1, has_more: false,
    } as never)

    const poll = store.silentRefresh()
    const user = store.fetchFirstPage() // bumps fetchReq while the poll is in flight
    resolvePoll({ items: [makeItem(9, '2026-01-01T00:00:00Z'), makeItem(8, '2025-12-31T00:00:00Z')], total: 2, has_more: false })
    await Promise.all([poll, user])

    // The superseded page-one data is NOT applied over the user's load.
    expect(store.items.map(i => i.id)).toEqual([20])
  })

  it('drops a poll response that predates a local read write', async () => {
    const { feedApi, itemApi } = await import('../services/api')
    const store = useFeedStore()
    store.activeFeedId = 7
    const unreadRow = makeItem(9, '2026-01-01T00:00:00Z', 'unread')
    store.items = [unreadRow] as never

    let resolvePoll!: (v: unknown) => void
    const pollPromise = new Promise(r => { resolvePoll = r })
    vi.mocked(feedApi.getItems).mockImplementationOnce(() => pollPromise as never)
    // The read toggle returns the row as READ.
    vi.mocked(itemApi.updateStatus).mockResolvedValue(makeItem(9, '2026-01-01T00:00:00Z', 'read') as never)

    const poll = store.silentRefresh()
    await store.updateItemStatus(unreadRow as never, 'read')
    // The poll's response was built BEFORE the write and still says unread.
    resolvePoll({ items: [makeItem(9, '2026-01-01T00:00:00Z', 'unread')], total: 1, has_more: false })
    await poll

    // The write wins; the stale status is not resurrected.
    expect(store.items[0].status).toBe('read')
  })

  it('does nothing when no feed view is open', async () => {
    const { feedApi } = await import('../services/api')
    const store = useFeedStore()

    await store.silentRefresh()

    expect(feedApi.getItems).not.toHaveBeenCalled()
    expect(feedApi.multiItems).not.toHaveBeenCalled()
  })

  it('does not poll the Saved view', async () => {
    const { feedApi } = await import('../services/api')
    const store = useFeedStore()
    store.showingSaved = true
    store.activeFeedId = 7

    await store.silentRefresh()

    expect(feedApi.getItems).not.toHaveBeenCalled()
  })
})
