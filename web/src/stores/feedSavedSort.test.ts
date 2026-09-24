import { describe, it, expect, vi, beforeEach } from 'vitest'
import { setActivePinia, createPinia } from 'pinia'
import { useFeedStore } from './feed'

vi.mock('../services/api', () => ({
  feedApi: {
    savedItems: vi.fn(),
    getItems: vi.fn(),
    multiItems: vi.fn(),
    listFeeds: vi.fn(),
  },
  ApiError: class extends Error {
    constructor(public status: number, body: any) { super(body?.error) }
  },
}))

beforeEach(() => {
  setActivePinia(createPinia())
  vi.clearAllMocks()
})

function savedPage(ids: number[]) {
  return { items: ids.map(id => ({ id })), has_more: false, total: ids.length }
}

// The Saved view is paginated, so the SERVER owns the order. A client-side
// re-sort could only ever touch the rows in memory and would leave the remaining
// pages in the old order — which is exactly how choosing "Oldest" changed the
// label while the list stayed newest-first.
describe('feed store saved view sorting', () => {
  it('sends the selected direction to the server', async () => {
    const { feedApi } = await import('../services/api')
    ;(feedApi.savedItems as any).mockResolvedValue(savedPage([3, 2, 1]))

    const store = useFeedStore()
    store.showingSaved = true
    store.changeSort('date_asc')
    await Promise.resolve()

    expect(feedApi.savedItems).toHaveBeenCalledWith(
      expect.objectContaining({ sort: 'date_asc' }),
    )
  })

  it('refetches the saved list when the direction changes', async () => {
    const { feedApi } = await import('../services/api')
    ;(feedApi.savedItems as any).mockResolvedValue(savedPage([1, 2]))

    const store = useFeedStore()
    store.showingSaved = true
    await store.fetchSavedItems()
    ;(feedApi.savedItems as any).mockClear()

    store.changeSort('date_asc')
    await Promise.resolve()

    expect(feedApi.savedItems).toHaveBeenCalledTimes(1)
  })

  it('defaults to newest first, and does not refetch when the sort is unchanged', async () => {
    const { feedApi } = await import('../services/api')
    ;(feedApi.savedItems as any).mockResolvedValue(savedPage([1]))

    const store = useFeedStore()
    store.showingSaved = true
    await store.fetchSavedItems()

    expect(feedApi.savedItems).toHaveBeenCalledWith(
      expect.objectContaining({ sort: 'date_desc' }),
    )
    ;(feedApi.savedItems as any).mockClear()

    store.changeSort('date_desc')
    await Promise.resolve()
    expect(feedApi.savedItems).not.toHaveBeenCalled()
  })

  it('collapses an unsupported score sort to a date order the API accepts', async () => {
    const { feedApi } = await import('../services/api')
    ;(feedApi.savedItems as any).mockResolvedValue(savedPage([1]))

    const store = useFeedStore()
    store.showingSaved = true
    // A score order picked in a feed view must not leak into the Saved request:
    // the endpoint rejects it, and the Saved view does not offer it.
    store.sort = 'score_desc'
    await store.fetchSavedItems()

    expect(feedApi.savedItems).toHaveBeenCalledWith(
      expect.objectContaining({ sort: 'date_desc' }),
    )
  })

  it('keeps the direction across pagination, so pages stay in one order', async () => {
    const { feedApi } = await import('../services/api')
    // The first page must report more rows, or the fetch-more guard returns
    // early and the assertion would pass for the wrong reason.
    ;(feedApi.savedItems as any).mockResolvedValueOnce({ items: [{ id: 1 }, { id: 2 }], has_more: true, total: 4 })
    ;(feedApi.savedItems as any).mockResolvedValueOnce({ items: [{ id: 3 }, { id: 4 }], has_more: false, total: 4 })

    const store = useFeedStore()
    store.showingSaved = true
    store.sort = 'date_asc'
    await store.fetchSavedItems()
    await store.fetchMoreSavedItems()

    expect(feedApi.savedItems).toHaveBeenLastCalledWith(
      expect.objectContaining({ sort: 'date_asc', offset: 2 }),
    )
  })
})
