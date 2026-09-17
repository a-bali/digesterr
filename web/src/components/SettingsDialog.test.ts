import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import SettingsDialog from './SettingsDialog.vue'
import { settingsApi } from '../services/api'

// The dialog hosts the Email section too, so the mock must answer getEmail — the
// section loads on open exactly as the LLM one does.
vi.mock('../services/api', () => ({
  settingsApi: {
    getLLM: vi.fn(),
    updateLLM: vi.fn(),
    resetLLM: vi.fn(),
    getEmail: vi.fn().mockResolvedValue({ configured: false, inbox: '', token_set: false }),
    updateEmail: vi.fn(),
    resetEmail: vi.fn(),
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

// One Save and one Cancel for the whole dialog: they live in the footer, not in
// either section.
function saveButton(): HTMLButtonElement | null {
  return document.body.querySelector('[data-settings-save]')
}

function cancelButton(): HTMLButtonElement | null {
  return document.body.querySelector('[data-settings-cancel]')
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

function click(el: Element) {
  el.dispatchEvent(new MouseEvent('click', { bubbles: true }))
}

describe('SettingsDialog LLM section', () => {
  beforeEach(() => {
    document.body.innerHTML = ''
    // The Email section uses the feed store (it refreshes the cached inbox after a
    // save), so the mount needs an active Pinia.
    setActivePinia(createPinia())
    vi.clearAllMocks()
    mocked.getEmail.mockResolvedValue({ configured: false, inbox: '', token_set: false })
  })

  it('prefills the field from the server on open', async () => {
    mocked.getLLM.mockResolvedValue({ model: 'custom-model', default_model: 'env-model', overridden: true })
    mount(SettingsDialog, { props: { open: true } })
    await flushPromises()

    expect(mocked.getLLM).toHaveBeenCalledTimes(1)
    expect(modelInput()?.value).toBe('custom-model')
    expect(dialogEl()?.textContent).toContain('env-model')
    expect(document.body.querySelector('[data-llm-settings-override]')).not.toBeNull()
  })

  it('hides the override marker when using the default', async () => {
    mocked.getLLM.mockResolvedValue({ model: 'env-model', default_model: 'env-model', overridden: false })
    mount(SettingsDialog, { props: { open: true } })
    await flushPromises()

    expect(document.body.querySelector('[data-llm-settings-override]')).toBeNull()
  })

  it('saves the trimmed model and closes', async () => {
    mocked.getLLM.mockResolvedValue({ model: 'env-model', default_model: 'env-model', overridden: false })
    mocked.updateLLM.mockResolvedValue({ model: 'new-model', default_model: 'env-model', overridden: true })
    const wrapper = mount(SettingsDialog, { props: { open: true } })
    await flushPromises()

    fill(modelInput()!, '  new-model  ')
    await flushPromises()
    click(saveButton()!)
    await flushPromises()

    expect(mocked.updateLLM).toHaveBeenCalledWith('new-model')
    expect(toast.success).toHaveBeenCalled()
    expect(wrapper.emitted('update:open')).toEqual([[false]])
  })

  it('disables Save for an empty or unchanged value', async () => {
    mocked.getLLM.mockResolvedValue({ model: 'env-model', default_model: 'env-model', overridden: false })
    mount(SettingsDialog, { props: { open: true } })
    await flushPromises()

    // Unchanged: same as default.
    expect(saveButton()!.disabled).toBe(true)

    fill(modelInput()!, '   ')
    await flushPromises()
    expect(saveButton()!.disabled).toBe(true)
    expect(mocked.updateLLM).not.toHaveBeenCalled()
  })

  // Reset is LOCAL now: it stages "use the default again" and must not write
  // anything until Save. The old behaviour (DELETE on click) meant this one
  // button saved while every other control waited.
  it('stages the reset locally instead of saving it', async () => {
    mocked.getLLM.mockResolvedValue({
      model: 'custom-model', default_model: 'env-model', overridden: true, models: ['env-model', 'custom-model'],
    })
    mount(SettingsDialog, { props: { open: true } })
    await flushPromises()

    expect(resetButton()!.disabled).toBe(false)
    click(resetButton()!)
    await flushPromises()

    // Nothing written yet...
    expect(mocked.resetLLM).not.toHaveBeenCalled()
    expect(mocked.updateLLM).not.toHaveBeenCalled()
    // ...the field is staged empty, and the override marker is still the stored state.
    expect(modelInput()?.value).toBe('')
    expect(document.body.querySelector('[data-llm-settings-override]')).not.toBeNull()
  })

  it('clears the override on Save after a reset', async () => {
    mocked.getLLM.mockResolvedValue({
      model: 'custom-model', default_model: 'env-model', overridden: true, models: [],
    })
    mocked.updateLLM.mockResolvedValue({
      model: 'env-model', default_model: 'env-model', overridden: false, models: [],
    })
    const wrapper = mount(SettingsDialog, { props: { open: true } })
    await flushPromises()

    click(resetButton()!)
    await flushPromises()
    click(saveButton()!)
    await flushPromises()

    // '' is the staged reset: the backend reads it as "clear the override".
    expect(mocked.updateLLM).toHaveBeenCalledWith('')
    expect(mocked.resetLLM).not.toHaveBeenCalled()
    expect(wrapper.emitted('update:open')).toEqual([[false]])
  })

  // Reset must not be offered when the account follows the default already.
  it('disables Reset when no override is active', async () => {
    mocked.getLLM.mockResolvedValue({
      model: 'env-model', default_model: 'env-model', overridden: false, models: ['env-model'],
    })
    mount(SettingsDialog, { props: { open: true } })
    await flushPromises()

    expect(resetButton()!.disabled).toBe(true)
  })

  it('shows a load error without crashing', async () => {
    mocked.getLLM.mockRejectedValue(new Error('boom'))
    mount(SettingsDialog, { props: { open: true } })
    await flushPromises()

    expect(document.body.querySelector('[data-llm-settings-error]')?.textContent).toContain('boom')
  })

  // The single footer Save writes BOTH sections, so a model edit must not be
  // saved without also persisting a pending email change.
  it('saves the email section too when only it has changes', async () => {
    mocked.getLLM.mockResolvedValue({ model: 'env-model', default_model: 'env-model', overridden: false })
    mocked.getEmail.mockResolvedValue({ configured: true, inbox: 'name@agentmail.to', token_set: true })
    mocked.updateEmail.mockResolvedValue({ configured: true, inbox: 'other@agentmail.to', token_set: true })

    const wrapper = mount(SettingsDialog, { props: { open: true } })
    await flushPromises()

    const inbox = document.body.querySelector('[data-email-settings-inbox]') as HTMLInputElement
    fill(inbox, 'other@agentmail.to')
    await flushPromises()
    click(saveButton()!)
    await flushPromises()

    expect(mocked.updateEmail).toHaveBeenCalled()
    // The LLM model was unchanged, so it must not be rewritten.
    expect(mocked.updateLLM).not.toHaveBeenCalled()
    expect(wrapper.emitted('update:open')).toEqual([[false]])
  })

  it('keeps the dialog open when the email save fails', async () => {
    mocked.getLLM.mockResolvedValue({ model: 'env-model', default_model: 'env-model', overridden: false })
    mocked.getEmail.mockResolvedValue({ configured: false, inbox: '', token_set: false })
    mocked.updateEmail.mockRejectedValue(new Error('AgentMail rejected these credentials: 401'))

    const wrapper = mount(SettingsDialog, { props: { open: true } })
    await flushPromises()

    fill(document.body.querySelector('[data-email-settings-inbox]') as HTMLInputElement, 'fresh@agentmail.to')
    fill(document.body.querySelector('[data-email-settings-key]') as HTMLInputElement, '«redacted:am_…»')
    await flushPromises()
    click(saveButton()!)
    await flushPromises()

    expect(document.body.querySelector('[data-email-settings-error]')?.textContent).toContain(
      'AgentMail rejected',
    )
    expect(wrapper.emitted('update:open')).toBeUndefined()
  })
})
