import { describe, it, expect, beforeEach, vi } from 'vitest'
import { setActivePinia, createPinia } from 'pinia'
import { useCategoryStore } from './category'

vi.mock('../services/api', () => ({
  categoryApi: {
    list: vi.fn(),
    create: vi.fn(),
    update: vi.fn(),
    delete: vi.fn(),
  },
  ApiError: class extends Error { constructor(public status: number, body: any) { super(body?.error) } },
}))

beforeEach(() => {
  setActivePinia(createPinia())
})

describe('category store', () => {
  it('fetches categories', async () => {
    const { categoryApi } = await import('../services/api')
    ;(categoryApi.list as ReturnType<typeof vi.fn>).mockResolvedValue([{ id: 1, name: 'Tech' }])

    const store = useCategoryStore()
    await store.fetchCategories()
    expect(store.categories).toHaveLength(1)
    expect(store.categories[0].name).toBe('Tech')
  })

  it('creates a category', async () => {
    const { categoryApi } = await import('../services/api')
    ;(categoryApi.create as ReturnType<typeof vi.fn>).mockResolvedValue({ id: 2, name: 'News' })

    const store = useCategoryStore()
    await store.create('News')
    expect(store.categories).toHaveLength(1)
    expect(store.categories[0].name).toBe('News')
  })

  it('removes a category', async () => {
    const { categoryApi } = await import('../services/api')
    ;(categoryApi.list as ReturnType<typeof vi.fn>).mockResolvedValue([{ id: 1, name: 'Tech' }])
    ;(categoryApi.delete as ReturnType<typeof vi.fn>).mockResolvedValue(undefined)

    const store = useCategoryStore()
    await store.fetchCategories()
    expect(store.categories).toHaveLength(1)

    await store.remove(1)
    expect(store.categories).toHaveLength(0)
  })
})
