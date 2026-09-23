import { describe, it, expect, beforeEach, vi } from 'vitest'
import { setActivePinia, createPinia } from 'pinia'
import { useFeedStore } from './feed'

vi.mock('../services/api', () => ({
  feedApi: {
    list: vi.fn(),
    create: vi.fn(),
    update: vi.fn(),
    delete: vi.fn(),
    getItems: vi.fn(),
    multiItems: vi.fn(),
    savedItems: vi.fn(),
    fetch: vi.fn(),
  },
  itemApi: {
    updateStatus: vi.fn(),
    deleteExtracted: vi.fn(),
    getItem: vi.fn(),
  },
  ApiError: class extends Error { constructor(public status: number, body: any) { super(body?.error) } },
}))

beforeEach(() => {
  setActivePinia(createPinia())
})

// The Saved view paginates like every other list: a page of PAGE_SIZE with
// has_more, appending on scroll. The status filter goes to the SERVER, because a
// client-side filter would only ever see the pages fetched so far.
describe('feed store saved view pagination', () => {
  it('requests the first page with a limit and stores has_more/total', async () => {
    const { feedApi } = await import('../services/api')
    const store = useFeedStore()
    ;(feedApi.savedItems as any).mockResolvedValue({
      items: [{ id: 1, title: 'A' }, { id: 2, title: 'B' }],
      has_more: true,
      total: 7,
    })

    await store.fetchSavedItems()

    expect(feedApi.savedItems).toHaveBeenCalledWith({ status: undefined, limit: 50, offset: 0 })
    expect(store.savedItems).toHaveLength(2)
    expect(store.hasMore).toBe(true)
    expect(store.savedTotal).toBe(7)
  })

  it('appends the next page at the loaded offset', async () => {
    const { feedApi } = await import('../services/api')
    const store = useFeedStore()
    ;(feedApi.savedItems as any).mockResolvedValue({
      items: [{ id: 1, title: 'A' }, { id: 2, title: 'B' }],
      has_more: true,
      total: 4,
    })
    await store.fetchSavedItems()

    ;(feedApi.savedItems as any).mockResolvedValue({
      items: [{ id: 3, title: 'C' }, { id: 4, title: 'D' }],
      has_more: false,
      total: 4,
    })
    await store.fetchMoreSavedItems()

    expect(feedApi.savedItems).toHaveBeenLastCalledWith({ status: undefined, limit: 50, offset: 2 })
    expect(store.savedItems.map(i => i.id)).toEqual([1, 2, 3, 4])
    expect(store.hasMore).toBe(false)
  })

  it('sends the read-state filter to the server', async () => {
    const { feedApi } = await import('../services/api')
    const store = useFeedStore()
    ;(feedApi.savedItems as any).mockResolvedValue({ items: [], has_more: false, total: 0 })

    store.filter = 'unread'
    await store.fetchSavedItems()

    expect(feedApi.savedItems).toHaveBeenCalledWith({ status: 'unread', limit: 50, offset: 0 })
  })

  it('does not page when there is nothing more to load', async () => {
    const { feedApi } = await import('../services/api')
    const store = useFeedStore()
    ;(feedApi.savedItems as any).mockResolvedValue({ items: [{ id: 1 }], has_more: false, total: 1 })
    await store.fetchSavedItems()
    ;(feedApi.savedItems as any).mockClear()

    await store.fetchMoreSavedItems()

    expect(feedApi.savedItems).not.toHaveBeenCalled()
  })
})

// A select-time fetch must not overwrite a write that happened while it was in
// flight. HomeView.selectItem marks an unread row read immediately after
// selecting it, so the GET (started first, resolved later) returned the OLD
// status and clobbered the fresh one — leaving the content pane's read toggle
// showing "Mark Read" on an item the user had just marked read.
describe('feed store selectItem vs a concurrent status write', () => {
  it('a slow GET does not resurrect the pre-write status', async () => {
    const { itemApi } = await import('../services/api')
    const store = useFeedStore()
    const rowItem = { id: 9, title: 'X', status: 'unread' } as any
    ;(itemApi.updateStatus as any).mockResolvedValue({ id: 9, title: 'X', status: 'read' })
    let resolveGet!: (v: any) => void
    ;(itemApi.getItem as any).mockReturnValue(new Promise(r => { resolveGet = r }))

    store.selectItem(rowItem)
    await store.updateItemStatus(rowItem, 'read')
    expect(store.selectedItem?.status).toBe('read')

    // The stale response lands after the write.
    resolveGet({ id: 9, title: 'X', status: 'unread' })
    await new Promise(r => setTimeout(r, 0))

    expect(store.selectedItem?.status).toBe('read')
  })

  // The guard must not be so blunt that it drops a fetch when nothing was written:
  // the full item (extracted body, comments) still has to load.
  it('still applies the fetched item when no write intervened', async () => {
    const { itemApi } = await import('../services/api')
    const store = useFeedStore()
    ;(itemApi.getItem as any).mockResolvedValue({
      id: 9, title: 'X', status: 'unread', extract_content: '<p>full body</p>',
    })

    store.selectItem({ id: 9, title: 'X', status: 'unread' } as any)
    await new Promise(r => setTimeout(r, 0))

    expect((store.selectedItem as any)?.extract_content).toBe('<p>full body</p>')
  })
})

describe('feed store', () => {
  it('fetches feeds', async () => {
    const { feedApi } = await import('../services/api')
    const mock = feedApi.list as ReturnType<typeof vi.fn>
    mock.mockResolvedValue([{ id: 1, name: 'Test Feed' }])

    const store = useFeedStore()
    await store.fetchFeeds()
    expect(store.feeds).toHaveLength(1)
    expect(store.feeds[0].name).toBe('Test Feed')
  })

  it('sets active feed and fetches items', async () => {
    const { feedApi } = await import('../services/api')
    ;(feedApi.list as ReturnType<typeof vi.fn>).mockResolvedValue([{ id: 1 }])
	;(feedApi.getItems as ReturnType<typeof vi.fn>).mockResolvedValue({
		items: [{ id: 10, title: 'Item 1', status: 'unread' }],
		has_more: false,
	})

    const store = useFeedStore()
    await store.fetchFeeds()
    await store.setActiveFeed(1)

    expect(store.activeFeedId).toBe(1)
    expect(store.items).toHaveLength(1)
    expect(store.items[0].title).toBe('Item 1')
  })

  it('selects an item', () => {
    const store = useFeedStore()
    const item = { id: 1, title: 'Test', status: 'unread' } as any
    store.selectItem(item)
    expect(store.selectedItem?.title).toBe('Test')
  })

  it('updates item status and syncs selectedItem', async () => {
    const { itemApi } = await import('../services/api')
    ;(itemApi.updateStatus as ReturnType<typeof vi.fn>).mockResolvedValue({ id: 1, status: 'read' })

    const store = useFeedStore()
    store.items = [{ id: 1, title: 'Test', status: 'unread' }] as any
    store.selectItem(store.items[0])

    await store.updateItemStatus(store.items[0], 'read')
    expect(store.items[0].status).toBe('read')
    expect(store.selectedItem?.status).toBe('read')
  })

  it('re-renders the saved view row when an item is marked unread', async () => {
    // The reported bug: from the Saved view, marking an article unread left the
    // row showing its old (read) icon until a reload. The saved list is a
    // FOURTH place an item is held, and applyUpdatedItem only reached items,
    // multiItems and selectedItem — so the DB and the content pane updated while
    // the row did not.
    const { itemApi } = await import('../services/api')
    ;(itemApi.updateStatus as ReturnType<typeof vi.fn>).mockResolvedValue({ id: 1, status: 'unread', is_saved: 1 })

    const store = useFeedStore()
    store.savedItems = [{ id: 1, title: 'Saved thing', status: 'read', is_saved: 1 }] as any

    await store.updateItemStatus(store.savedItems[0], 'unread')

    // The row is still there — saved-ness and read state are independent, so an
    // unread-and-saved item stays in the saved list...
    expect(store.savedItems).toHaveLength(1)
    // ...and it now carries the new status, which is what the row icon renders.
    expect(store.savedItems[0].status).toBe('unread')
  })

  it('sends the saved flag, not a status, when saving', async () => {
    // Saved is a FLAG on the API now. Sending status:'saved' would be rejected
    // (400) and would destroy the item's read state.
    const { itemApi } = await import('../services/api')
    ;(itemApi.updateStatus as ReturnType<typeof vi.fn>).mockResolvedValue({ id: 1, status: 'read', is_saved: 1 })

    const store = useFeedStore()
    const item = { id: 1, title: 't', status: 'unread', is_saved: 0 } as any
    store.items = [{ ...item }]

    await store.updateItemStatus(store.items[0], undefined, true)

    const lastCall = (itemApi.updateStatus as ReturnType<typeof vi.fn>).mock.calls.slice(-1)[0]
    expect(lastCall[0]).toBe(1)
    // One object body: `saved` is the flag and no `status` is sent, so this
    // cannot be mistaken for a status write (which would 400 and also destroy
    // the item's read state).
    expect(lastCall[1]).toEqual({ saved: true })
  })

  it('unsaving clears the flag and drops the row without writing a read state', async () => {
    const { itemApi } = await import('../services/api')
    ;(itemApi.updateStatus as ReturnType<typeof vi.fn>).mockResolvedValue({ id: 1, status: 'unread', is_saved: 0 })

    const store = useFeedStore()
    const item = { id: 1, title: 't', status: 'unread', is_saved: 1 } as any
    store.savedItems = [{ ...item }]
    store.items = [{ ...item }]

    await store.unsaveItem(store.savedItems[0])

    const lastCall = (itemApi.updateStatus as ReturnType<typeof vi.fn>).mock.calls.slice(-1)[0]
    // Never invents a read state: the old code sent 'read' here, which is what
    // made unsaving look like it also un-read the item.
    expect(lastCall[1]).toEqual({ saved: false })
    expect(lastCall[1].status).toBeUndefined()
    expect(store.savedItems).toHaveLength(0)
  })

  // A failed unsave must NOT remove the row. updateItemStatus catches API errors
  // and (before this) returned normally, so unsaveItem could not tell success from
  // failure and dropped the row regardless — the row then reappeared on reload,
  // which reads as the app losing the change rather than the request failing.
  it('keeps the row when the unsave request fails', async () => {
    const { itemApi } = await import('../services/api')
    ;(itemApi.updateStatus as ReturnType<typeof vi.fn>).mockRejectedValue(new Error('network down'))

    const store = useFeedStore()
    const item = { id: 1, title: 't', status: 'unread', is_saved: 1 } as any
    store.savedItems = [{ ...item }]

    await store.unsaveItem(store.savedItems[0])

    expect(store.savedItems).toHaveLength(1)
  })

  // ...and the success path still removes it, so the guard above is not simply
  // disabling the feature.
  it('removes the row when the unsave request succeeds', async () => {
    const { itemApi } = await import('../services/api')
    ;(itemApi.updateStatus as ReturnType<typeof vi.fn>).mockResolvedValue({ id: 1, status: 'unread', is_saved: 0 })

    const store = useFeedStore()
    const item = { id: 1, title: 't', status: 'unread', is_saved: 1 } as any
    store.savedItems = [{ ...item }]

    await store.unsaveItem(store.savedItems[0])

    expect(store.savedItems).toHaveLength(0)
  })

  it('deleteExtraction clears the extracted body in the list, the multi-view and the selection', async () => {
    const { itemApi } = await import('../services/api')
    ;(itemApi.deleteExtracted as ReturnType<typeof vi.fn>).mockResolvedValue({
      id: 1, extract_status: 'cleared', extract_content: '',
    })

    const store = useFeedStore()
    const item = { id: 1, title: 'Test', status: 'unread', extract_status: 'done', extract_content: '<p>x</p>' } as any
    store.items = [{ ...item }]
    store.multiItems = [{ ...item, feed_name: 'HN', feed_type: 'hn' }] as any
    store.selectItem(store.items[0])

    await store.deleteExtraction(store.items[0])

    expect(store.items[0].extract_status).toBe('cleared')
    expect(store.items[0].extract_content).toBe('')
    expect(store.selectedItem?.extract_status).toBe('cleared')
    // The multi-view row must keep its source-feed label: the item endpoints
    // return a plain Item with no feed_name/feed_type, so a wholesale replace
    // makes the label vanish from the middle pane.
    expect(store.multiItems[0].feed_name).toBe('HN')
    expect(store.multiItems[0].extract_status).toBe('cleared')
  })

  it('deleteExtraction reports a failure instead of silently keeping the body', async () => {
    const { itemApi } = await import('../services/api')
    ;(itemApi.deleteExtracted as ReturnType<typeof vi.fn>).mockRejectedValue(new Error('nope'))

    const store = useFeedStore()
    const item = { id: 1, title: 'Test', status: 'unread', extract_status: 'done', extract_content: '<p>x</p>' } as any
    store.items = [{ ...item }]
    store.selectItem(store.items[0])

    await store.deleteExtraction(store.items[0])

    expect(store.selectedItem?.extract_status).toBe('done')
  })

  it('passes min_score and sort to getItems and refetches on change', async () => {
    const { feedApi } = await import('../services/api')
    ;(feedApi.list as ReturnType<typeof vi.fn>).mockResolvedValue([{ id: 1, type: 'rss', min_score: 0 }])
    const getItems = feedApi.getItems as ReturnType<typeof vi.fn>
    getItems.mockResolvedValue({ items: [], has_more: false })

    const store = useFeedStore()
    await store.fetchFeeds()
    await store.setActiveFeed(1)

    expect(getItems).toHaveBeenLastCalledWith(1, expect.objectContaining({ sort: 'date_desc', min_score: undefined }))

    store.changeSort('score_desc')
    expect(getItems).toHaveBeenLastCalledWith(1, expect.objectContaining({ sort: 'score_desc' }))

    store.changeMinScore(50)
    expect(getItems).toHaveBeenLastCalledWith(1, expect.objectContaining({ min_score: 50 }))
  })

  it('seeds the score filter from the feed min_score on activation', async () => {
    const { feedApi } = await import('../services/api')
    ;(feedApi.list as ReturnType<typeof vi.fn>).mockResolvedValue([
      { id: 1, type: 'reddit', min_score: 300 },
      { id: 2, type: 'rss', min_score: 0 },
      { id: 3, type: 'reddit', min_score: 50 },
    ])
    const getItems = feedApi.getItems as ReturnType<typeof vi.fn>
    getItems.mockResolvedValue({ items: [], has_more: false })

    const store = useFeedStore()
    await store.fetchFeeds()

    // The FilterBar label and the request must agree with what the API will
    // return by default: the feed's own threshold.
    await store.setActiveFeed(1)
    expect(store.minScore).toBe(300)
    expect(getItems).toHaveBeenLastCalledWith(1, expect.objectContaining({ min_score: 300 }))

    // A feed with no threshold clears the filter.
    await store.setActiveFeed(2)
    expect(store.minScore).toBe(null)
    expect(getItems).toHaveBeenLastCalledWith(2, expect.objectContaining({ min_score: undefined }))

    await store.setActiveFeed(3)
    expect(store.minScore).toBe(50)

    // Multi-feed views carry mixed thresholds server-side, so the client's
    // single value starts cleared rather than keeping the last feed's seed.
    const multi = feedApi.multiItems as ReturnType<typeof vi.fn>
    multi.mockResolvedValue({ items: [], total: 0, has_more: false })
    await store.setActiveCategory('tech')
    expect(store.minScore).toBe(null)
    // Leave no calls behind: a later test in this file asserts that no
    // multi-items request is ever sent without a scope.
    multi.mockClear()
  })

  it('supportsScore is true only for score-capable feed types', async () => {
    const { feedApi } = await import('../services/api')
    ;(feedApi.list as ReturnType<typeof vi.fn>).mockResolvedValue([
      { id: 1, type: 'hn' },
      { id: 2, type: 'lobsters' },
      { id: 3, type: 'reddit' },
      { id: 4, type: 'rss' },
    ])
    const store = useFeedStore()
    await store.fetchFeeds()

    store.activeFeedId = 1
    expect(store.supportsScore).toBe(true)
    store.activeFeedId = 2
    expect(store.supportsScore).toBe(true)
    store.activeFeedId = 3
    expect(store.supportsScore).toBe(true)
    store.activeFeedId = 4
    expect(store.supportsScore).toBe(false)
    store.activeFeedId = null
    expect(store.supportsScore).toBe(false)
  })

  it('loads the "Feeds" section without an empty feed_ids list', async () => {
    const { feedApi } = await import('../services/api')
    const multi = feedApi.multiItems as ReturnType<typeof vi.fn>
    multi.mockResolvedValue({ items: [], total: 0, has_more: false })

    const store = useFeedStore()
    // Cold start / deep link: the section is selected BEFORE the feeds list has
    // resolved. An empty feed_ids is dropped by the query-string helper, so the
    // request carried no scope at all and the API answered 400
    // "feed_ids or category is required".
    vi.mocked(feedApi.list).mockResolvedValue([])
    await store.setActiveSection('feeds')

    // With no feeds there is no scope to send, so the request must not be made
    // at all — that is what produced the 400 before.
    expect(multi, 'an unscoped multi-items request must never be sent').not.toHaveBeenCalled()
    expect(feedApi.list).toHaveBeenCalled()
    // And the pane settles on an empty state instead of a stale/loading one.
    expect(store.multiItems).toEqual([])
    expect(store.totalItems).toBe(0)
    expect(store.loading).toBe(false)
  })

  it('loads the "Feeds" section with the real feed ids once feeds resolve', async () => {
    const { feedApi } = await import('../services/api')
    const multi = feedApi.multiItems as ReturnType<typeof vi.fn>
    multi.mockResolvedValue({ items: [], total: 0, has_more: false })
    vi.mocked(feedApi.list).mockResolvedValue([{ id: 7 }, { id: 9 }] as never)

    const store = useFeedStore()
    await store.setActiveSection('feeds')

    expect(multi).toHaveBeenCalled()
    const opts = multi.mock.calls[multi.mock.calls.length - 1][0]
    expect(opts.feed_ids).toEqual([7, 9])
  })

  it('never sends an empty feed_ids from a refresh either', async () => {
    const { feedApi } = await import('../services/api')
    const multi = feedApi.multiItems as ReturnType<typeof vi.fn>
    multi.mockResolvedValue({ items: [], total: 0, has_more: false })
    vi.mocked(feedApi.list).mockResolvedValue([])

    const store = useFeedStore()
    await store.setActiveSection('feeds')
    multi.mockClear()

    await store.refreshItems()

    if (multi.mock.calls.length > 0) {
      expect(multi.mock.calls[0][0].feed_ids).not.toEqual([])
    }
  })
})
