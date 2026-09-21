import { describe, it, expect, beforeEach, vi } from 'vitest'
import { setActivePinia, createPinia } from 'pinia'
import { toastSuccess, toastError } from './toast'
import { useFeedStore } from '../stores/feed'
import { useDigestStore } from '../stores/digest'

vi.mock('vue-sonner', () => ({ toast: { success: vi.fn(), error: vi.fn() } }))

vi.mock('../services/api', () => ({
  feedApi: {
    list: vi.fn(),
    create: vi.fn(),
    update: vi.fn(),
    delete: vi.fn(),
    getItems: vi.fn(),
    multiItems: vi.fn(),
    fetch: vi.fn(),
    markRead: vi.fn(),
    markReadAll: vi.fn(),
    unreadCounts: vi.fn(),
    savedCount: vi.fn(),
    savedItems: vi.fn(),
    getEmailConfig: vi.fn(),
  },
  itemApi: {
    updateStatus: vi.fn(),
    deleteExtracted: vi.fn(),
    markReadAbove: vi.fn(),
    markReadBelow: vi.fn(),
  },
  digestApi: {
    listConfigs: vi.fn(),
    createConfig: vi.fn(),
    updateConfig: vi.fn(),
    deleteConfig: vi.fn(),
    getConfig: vi.fn(),
    generate: vi.fn(),
    listResults: vi.fn(),
    updateResultStatus: vi.fn(),
    deleteResult: vi.fn(),
    listResultItems: vi.fn(),
    unreadCounts: vi.fn(),
    listAllResults: vi.fn(),
    markAllRead: vi.fn(),
    markReadAbove: vi.fn(),
    markReadBelow: vi.fn(),
  },
  settingsApi: { getLLM: vi.fn(), getEmail: vi.fn() },
  ApiError: class extends Error {
    constructor(public status: number, body: { error?: string }) { super(body?.error) }
  },
}))

const { toast } = await import('vue-sonner')

beforeEach(() => {
  vi.clearAllMocks()
  setActivePinia(createPinia())
  document.documentElement.classList.remove('dark')
})

describe('toast helpers', () => {
  // The user asked for the two icons explicitly: a success toast is only
  // recognisable as a success if it carries the check.
  it('passes the green-check icon on success', () => {
    toastSuccess('Feed refreshed')
    expect(toast.success).toHaveBeenCalledWith('Feed refreshed', expect.objectContaining({ icon: expect.anything() }))
  })

  it('passes the red-X icon on failure', () => {
    toastError(new Error('boom'), 'Failed to refresh feed')
    expect(toast.error).toHaveBeenCalledWith('Failed to refresh feed', expect.objectContaining({ icon: expect.anything() }))
  })

  it('carries the server message through as the description', () => {
    toastError(new Error('network unreachable'), 'Failed to refresh feed')
    expect(toast.error).toHaveBeenCalledWith(
      'Failed to refresh feed',
      expect.objectContaining({ description: 'network unreachable' }),
    )
  })

  // Regression guard: the helper is called from inside catch blocks, so it must
  // never throw. It used to import ApiError and probe it with instanceof, which
  // makes vitest throw when a test partially mocks the api module — the throw
  // landed in the caller's catch and skipped its cleanup.
  it('never throws on a non-Error rejection value', () => {
    expect(() => toastError('just a string', 'Failed')).not.toThrow()
    expect(() => toastError(undefined, 'Failed')).not.toThrow()
    expect(() => toastError(null, 'Failed')).not.toThrow()
    expect(() => toastError({}, 'Failed')).not.toThrow()
    expect(toast.error).toHaveBeenCalledTimes(4)
  })
})

describe('mark-read actions report both outcomes', () => {
  it('feed markAllRead toasts the whole-section success', async () => {
    const { feedApi } = await import('../services/api')
    ;(feedApi.markReadAll as any).mockResolvedValue({})
    ;(feedApi.unreadCounts as any).mockResolvedValue({})
    ;(feedApi.savedCount as any).mockResolvedValue({ count: 0 })

    await useFeedStore().markAllRead()

    expect(toast.success).toHaveBeenCalledWith('All feeds marked read', expect.objectContaining({ icon: expect.anything() }))
  })

  it('feed markAllRead toasts a category-scoped success by name', async () => {
    const { feedApi } = await import('../services/api')
    ;(feedApi.markReadAll as any).mockResolvedValue({})
    ;(feedApi.unreadCounts as any).mockResolvedValue({})

    await useFeedStore().markAllRead('Tech')

    expect(toast.success).toHaveBeenCalledWith('"Tech" marked read', expect.objectContaining({ icon: expect.anything() }))
  })

  // Before this change the rejection escaped into the caller and the panes just
  // did not change, with nothing telling the user why.
  it('feed markAllRead reports failure instead of rejecting', async () => {
    const { feedApi } = await import('../services/api')
    ;(feedApi.markReadAll as any).mockRejectedValue(new Error('server down'))

    await expect(useFeedStore().markAllRead()).resolves.toBeUndefined()

    expect(toast.error).toHaveBeenCalledWith('Failed to mark read', expect.objectContaining({ icon: expect.anything() }))
  })

  it('feed markReadAbove reports success', async () => {
    const { itemApi, feedApi } = await import('../services/api')
    ;(itemApi.markReadAbove as any).mockResolvedValue({})
    ;(feedApi.unreadCounts as any).mockResolvedValue({})

    await useFeedStore().markReadAbove({ id: 5 })

    expect(toast.success).toHaveBeenCalledWith('Marked above as read', expect.objectContaining({ icon: expect.anything() }))
  })

  it('feed markReadBelow reports failure instead of rejecting', async () => {
    const { itemApi } = await import('../services/api')
    ;(itemApi.markReadBelow as any).mockRejectedValue(new Error('nope'))

    await expect(useFeedStore().markReadBelow({ id: 5 })).resolves.toBeUndefined()

    expect(toast.error).toHaveBeenCalledWith('Failed to mark below as read', expect.objectContaining({ icon: expect.anything() }))
  })

  it('digest markAllRead reports failure instead of rejecting', async () => {
    const { digestApi } = await import('../services/api')
    ;(digestApi.markAllRead as any).mockRejectedValue(new Error('nope'))

    await expect(useDigestStore().markAllRead()).resolves.toBeUndefined()

    expect(toast.error).toHaveBeenCalledWith('Failed to mark digest as read', expect.objectContaining({ icon: expect.anything() }))
  })

  it('digest markReadAbove reports success', async () => {
    const { digestApi } = await import('../services/api')
    ;(digestApi.markReadAbove as any).mockResolvedValue({})
    ;(digestApi.unreadCounts as any).mockResolvedValue({})

    await useDigestStore().markReadAbove({ id: 3 } as any)

    expect(toast.success).toHaveBeenCalledWith('Marked above as read', expect.objectContaining({ icon: expect.anything() }))
  })
})

describe('create / update / delete report both outcomes', () => {
  it('digest config create, update and delete each confirm success', async () => {
    const { digestApi } = await import('../services/api')
    const store = useDigestStore()

    ;(digestApi.createConfig as any).mockResolvedValue({ id: 1, name: 'D', schedule: '*', config: '{}' })
    await store.createConfig('D', '*', '{}')
    expect(toast.success).toHaveBeenCalledWith('Digest config created', expect.anything())

    ;(digestApi.updateConfig as any).mockResolvedValue({ id: 1, name: 'D2', schedule: '*', config: '{}' })
    await store.updateConfig(1, 'D2', '*', '{}')
    expect(toast.success).toHaveBeenCalledWith('Digest config updated', expect.anything())

    ;(digestApi.deleteConfig as any).mockResolvedValue(undefined)
    await store.deleteConfig(1)
    expect(toast.success).toHaveBeenCalledWith('Digest config deleted', expect.anything())
  })

  it('digest config failures toast the failure and do not throw', async () => {
    const { digestApi } = await import('../services/api')
    ;(digestApi.createConfig as any).mockRejectedValue(new Error('bad'))
    ;(digestApi.updateConfig as any).mockRejectedValue(new Error('bad'))
    ;(digestApi.deleteConfig as any).mockRejectedValue(new Error('bad'))

    const store = useDigestStore()
    await expect(store.createConfig('D', '*', '{}')).resolves.toBeNull()
    await expect(store.updateConfig(1, 'D', '*', '{}')).resolves.toBeUndefined()
    await expect(store.deleteConfig(1)).resolves.toBeUndefined()

    expect(toast.error).toHaveBeenCalledTimes(3)
  })

  // Digests are generated on a schedule, so completion must be announced.
  it('digest generation reports success and failure', async () => {
    const { digestApi } = await import('../services/api')
    const store = useDigestStore()

    ;(digestApi.generate as any).mockResolvedValue({ id: 7, content: '{}', status: 'new' })
    await store.generateNow(1)
    expect(toast.success).toHaveBeenCalledWith('Digest generated', expect.objectContaining({ icon: expect.anything() }))

    ;(digestApi.generate as any).mockRejectedValue(new Error('llm down'))
    await store.generateNow(1)
    expect(toast.error).toHaveBeenCalledWith('Digest generation failed', expect.objectContaining({ icon: expect.anything() }))
  })
})
