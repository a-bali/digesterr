import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import GoogleReaderApiDialog from './GoogleReaderApiDialog.vue'

vi.mock('../services/api', () => ({
  authApi: {
    getApiKey: vi.fn(),
    rotateApiKey: vi.fn(),
  },
  setAuthToken: vi.fn(),
  getAuthToken: vi.fn(() => null),
  setOnUnauthorized: vi.fn(),
  ApiError: class extends Error {},
}))

// reka-ui's dialog content measures itself with ResizeObserver, which jsdom
// does not provide.
class ResizeObserverStub {
  observe() {}
  unobserve() {}
  disconnect() {}
}
vi.stubGlobal('ResizeObserver', ResizeObserverStub)

const OLD_KEY = 'a'.repeat(64)
const NEW_KEY = 'b'.repeat(64)

function keyInput(): HTMLInputElement | null {
  return document.querySelector('#reader-api-key')
}

function feedback(kind: 'success' | 'error'): HTMLElement | null {
  return document.querySelector(`[data-feedback="${kind}"]`)
}

function buttons(): HTMLElement[] {
  return Array.from(document.body.querySelectorAll('button')) as HTMLElement[]
}

async function clickButton(label: string) {
  const btn = buttons().find(b => b.textContent?.includes(label))
  if (!btn) throw new Error(`button not found: ${label}`)
  btn.dispatchEvent(new MouseEvent('click', { bubbles: true }))
  await flushPromises()
}

async function mountOpen() {
  const wrapper = mount(GoogleReaderApiDialog, { props: { open: true } })
  await flushPromises()
  return wrapper
}

describe('GoogleReaderApiDialog', () => {
  beforeEach(() => {
    document.body.innerHTML = ''
    vi.clearAllMocks()
  })

  afterEach(() => {
    vi.restoreAllMocks()
  })

  it('shows the current API key when it opens', async () => {
    const { authApi } = await import('../services/api')
    ;(authApi.getApiKey as ReturnType<typeof vi.fn>).mockResolvedValue({ api_key: OLD_KEY })

    await mountOpen()

    expect(authApi.getApiKey).toHaveBeenCalledTimes(1)
    expect(keyInput()?.value).toBe(OLD_KEY)
  })

  it('shows an error and an empty field when loading the key fails', async () => {
    const { authApi } = await import('../services/api')
    ;(authApi.getApiKey as ReturnType<typeof vi.fn>).mockRejectedValue(new Error('load failed'))

    await mountOpen()

    expect(feedback('error')?.textContent).toContain('load failed')
    expect(keyInput()?.value).toBe('')
  })

  it('rotates the key after confirmation and keeps the dialog open with the new key', async () => {
    const { authApi } = await import('../services/api')
    ;(authApi.getApiKey as ReturnType<typeof vi.fn>).mockResolvedValue({ api_key: OLD_KEY })
    ;(authApi.rotateApiKey as ReturnType<typeof vi.fn>).mockResolvedValue({ api_key: NEW_KEY })

    await mountOpen()
    expect(keyInput()?.value).toBe(OLD_KEY)

    await clickButton('Rotate API key')
    // Confirmation step shows before any rotation happens.
    expect(authApi.rotateApiKey).not.toHaveBeenCalled()
    expect(document.body.textContent).toContain('Rotate API key?')

    await clickButton('Confirm rotation')

    expect(authApi.rotateApiKey).toHaveBeenCalledTimes(1)
    // Dialog stays open with the new key and a clear success state.
    expect(document.querySelector('[role="dialog"]')).not.toBeNull()
    expect(keyInput()?.value).toBe(NEW_KEY)
    expect(feedback('success')?.textContent).toContain('rotated')
  })

  it('does not rotate when the confirmation is cancelled', async () => {
    const { authApi } = await import('../services/api')
    ;(authApi.getApiKey as ReturnType<typeof vi.fn>).mockResolvedValue({ api_key: OLD_KEY })

    await mountOpen()

    await clickButton('Rotate API key')
    await clickButton('Cancel')

    expect(authApi.rotateApiKey).not.toHaveBeenCalled()
    expect(keyInput()?.value).toBe(OLD_KEY)
    expect(document.body.textContent).not.toContain('Rotate API key?')
  })

  it('shows an error and keeps the old key when rotation fails', async () => {
    const { authApi } = await import('../services/api')
    ;(authApi.getApiKey as ReturnType<typeof vi.fn>).mockResolvedValue({ api_key: OLD_KEY })
    ;(authApi.rotateApiKey as ReturnType<typeof vi.fn>).mockRejectedValue(new Error('rotation failed'))

    await mountOpen()

    await clickButton('Rotate API key')
    await clickButton('Confirm rotation')

    expect(authApi.rotateApiKey).toHaveBeenCalledTimes(1)
    expect(feedback('error')?.textContent).toContain('rotation failed')
    expect(keyInput()?.value).toBe(OLD_KEY)
  })

  it('copies the key to the clipboard from the copy button', async () => {
    const { authApi } = await import('../services/api')
    ;(authApi.getApiKey as ReturnType<typeof vi.fn>).mockResolvedValue({ api_key: OLD_KEY })
    const writeText = vi.fn().mockResolvedValue(undefined)
    Object.defineProperty(navigator, 'clipboard', { value: { writeText }, configurable: true })

    await mountOpen()

    const copy = buttons().find(b => b.title === 'Copy API key')
    if (!copy) throw new Error('copy button not found')
    copy.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    await flushPromises()

    expect(writeText).toHaveBeenCalledWith(OLD_KEY)
  })
})
