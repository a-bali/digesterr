import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import LlmSettingsDialog from './LlmSettingsDialog.vue'
import { settingsApi } from '../services/api'

vi.mock('../services/api', () => ({
  settingsApi: {
    getLLM: vi.fn(),
    updateLLM: vi.fn(),
    resetLLM: vi.fn(),
  },
}))

vi.mock('vue-sonner', () => ({
  toast: { success: vi.fn(), error: vi.fn() },
}))

const mocked = vi.mocked(settingsApi)
const { toast } = await import('vue-sonner')

// The dialog content renders inside a reka-ui DialogPortal (teleported to
// document.body), so read the live DOM, not the wrapper — same pattern as
// ChangePasswordDialog.test.ts.
function dialogEl(): HTMLElement | null {
  return document.body.querySelector('[data-llm-settings-dialog]')
}

function modelInput(): HTMLInputElement | null {
  return document.body.querySelector('[data-llm-settings-dialog] input#llm-model')
}

function saveButton(): HTMLButtonElement | null {
  return document.body.querySelector('[data-llm-settings-save]')
}

function resetButton(): HTMLButtonElement | null {
  return document.body.querySelector('[data-llm-settings-reset]')
}

// Vue's v-model caches the input value; set it through the native prototype
// setter so the cached value stays in sync, then dispatch the input event.
function fill(input: HTMLInputElement, value: string) {
  const setter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!
  setter.call(input, value)
  input.dispatchEvent(new Event('input', { bubbles: true }))
}

describe('LlmSettingsDialog', () => {
  beforeEach(() => {
    document.body.innerHTML = ''
    vi.clearAllMocks()
  })

  it('prefills the field from the server on open', async () => {
    mocked.getLLM.mockResolvedValue({ model: 'custom-model', default_model: 'env-model', overridden: true })
    mount(LlmSettingsDialog, { props: { open: true } })
    await flushPromises()

    expect(mocked.getLLM).toHaveBeenCalledTimes(1)
    expect(modelInput()?.value).toBe('custom-model')
    expect(dialogEl()?.textContent).toContain('env-model')
    expect(document.body.querySelector('[data-llm-settings-override]')).not.toBeNull()
  })

  it('hides the override marker when using the default', async () => {
    mocked.getLLM.mockResolvedValue({ model: 'env-model', default_model: 'env-model', overridden: false })
    mount(LlmSettingsDialog, { props: { open: true } })
    await flushPromises()

    expect(document.body.querySelector('[data-llm-settings-override]')).toBeNull()
  })

  it('saves the trimmed model and closes', async () => {
    mocked.getLLM.mockResolvedValue({ model: 'env-model', default_model: 'env-model', overridden: false })
    mocked.updateLLM.mockResolvedValue({ model: 'new-model', default_model: 'env-model', overridden: true })
    const wrapper = mount(LlmSettingsDialog, { props: { open: true } })
    await flushPromises()

    fill(modelInput()!, '  new-model  ')
    await flushPromises()
    saveButton()!.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    await flushPromises()

    expect(mocked.updateLLM).toHaveBeenCalledWith('new-model')
    expect(toast.success).toHaveBeenCalled()
    expect(wrapper.emitted('update:open')).toEqual([[false]])
  })

  it('disables Save for an empty or unchanged value', async () => {
    mocked.getLLM.mockResolvedValue({ model: 'env-model', default_model: 'env-model', overridden: false })
    mount(LlmSettingsDialog, { props: { open: true } })
    await flushPromises()

    // Unchanged: same as default.
    expect((saveButton() as HTMLButtonElement).disabled).toBe(true)

    fill(modelInput()!, '   ')
    await flushPromises()
    expect((saveButton() as HTMLButtonElement).disabled).toBe(true)
    expect(mocked.updateLLM).not.toHaveBeenCalled()
  })

  it('resets to the default and refills the field', async () => {
    mocked.getLLM.mockResolvedValue({ model: 'custom-model', default_model: 'env-model', overridden: true })
    mocked.resetLLM.mockResolvedValue({ model: 'env-model', default_model: 'env-model', overridden: false })
    mount(LlmSettingsDialog, { props: { open: true } })
    await flushPromises()

    expect((resetButton() as HTMLButtonElement).disabled).toBe(false)
    resetButton()!.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    await flushPromises()

    expect(mocked.resetLLM).toHaveBeenCalledTimes(1)
    expect(modelInput()?.value).toBe('env-model')
    expect(document.body.querySelector('[data-llm-settings-override]')).toBeNull()
  })

  it('disables Reset when no override is active', async () => {
    mocked.getLLM.mockResolvedValue({ model: 'env-model', default_model: 'env-model', overridden: false })
    mount(LlmSettingsDialog, { props: { open: true } })
    await flushPromises()

    expect((resetButton() as HTMLButtonElement).disabled).toBe(true)
  })

  it('shows a load error without crashing', async () => {
    mocked.getLLM.mockRejectedValue(new Error('boom'))
    mount(LlmSettingsDialog, { props: { open: true } })
    await flushPromises()

    expect(document.body.querySelector('[data-llm-settings-error]')?.textContent).toContain('boom')
  })
})
