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
})
