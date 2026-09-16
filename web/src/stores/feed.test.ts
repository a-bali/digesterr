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
    fetch: vi.fn(),
  },
  itemApi: {
    updateStatus: vi.fn(),
  },
  ApiError: class extends Error { constructor(public status: number, body: any) { super(body?.error) } },
}))

beforeEach(() => {
  setActivePinia(createPinia())
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

  it('passes min_score and sort to getItems and refetches on change', async () => {
    const { feedApi } = await import('../services/api')
    ;(feedApi.list as ReturnType<typeof vi.fn>).mockResolvedValue([{ id: 1 }])
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
