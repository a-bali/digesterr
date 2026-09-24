import { describe, it, expect, beforeEach, vi } from 'vitest'
import { setActivePinia, createPinia } from 'pinia'
import { useDigestStore } from './digest'

const toastSuccess = vi.fn()
const toastError = vi.fn()
const toastInfo = vi.fn()

vi.mock('../lib/toast', () => ({
  toastSuccess: (...a: unknown[]) => toastSuccess(...a),
  toastError: (...a: unknown[]) => toastError(...a),
  toastInfo: (...a: unknown[]) => toastInfo(...a),
  MARK_SUCCESS_TITLE: 'Marked read',
  MARK_FAILURE_TITLE: 'Failed to mark read',
}))

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
  ApiError: class ApiError extends Error {
    constructor(public status: number, body: any) {
      super(body?.error || `HTTP ${status}`)
    }
  },
}))

beforeEach(() => {
  setActivePinia(createPinia())
  toastSuccess.mockClear()
  toastError.mockClear()
  toastInfo.mockClear()
})

// A 409 means a generation for this config is ALREADY RUNNING (a scheduled run
// still on its LLM call, or a double click). Nothing failed, so it must not be
// reported as a failure — "Digest generation failed — already in progress"
// sends the user hunting for a fault that does not exist.
describe('digest store generation conflict', () => {
  it('reports an in-progress generation as info, not as an error', async () => {
    const { digestApi, ApiError } = await import('../services/api')
    ;(digestApi.generate as ReturnType<typeof vi.fn>).mockRejectedValueOnce(
      new ApiError(409, { error: 'a digest generation for this config is already in progress' }),
    )

    const store = useDigestStore()
    const result = await store.generateNow(7)

    expect(result).toBeNull()
    expect(toastError).not.toHaveBeenCalled()
    expect(toastInfo).toHaveBeenCalledTimes(1)
    expect(toastInfo.mock.calls[0][0]).toMatch(/already in progress/i)
  })

  it('still reports a real failure as an error', async () => {
    const { digestApi, ApiError } = await import('../services/api')
    ;(digestApi.generate as ReturnType<typeof vi.fn>).mockRejectedValueOnce(
      new ApiError(500, { error: 'failed to generate digest' }),
    )

    const store = useDigestStore()
    const result = await store.generateNow(7)

    expect(result).toBeNull()
    expect(toastInfo).not.toHaveBeenCalled()
    expect(toastError).toHaveBeenCalledTimes(1)
  })

  it('clears the generating flag on a conflict, so the button is not stuck', async () => {
    const { digestApi, ApiError } = await import('../services/api')
    ;(digestApi.generate as ReturnType<typeof vi.fn>).mockRejectedValueOnce(new ApiError(409, { error: 'busy' }))

    const store = useDigestStore()
    await store.generateNow(7)

    expect(store.generating).toBe(false)
  })
})
