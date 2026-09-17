import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { nextTick } from 'vue'
import { createPinia, setActivePinia } from 'pinia'
import SettingsDialog from './SettingsDialog.vue'
import { settingsApi } from '../services/api'

// The Email section no longer owns a Save button — the dialog footer does. These
// tests therefore mount the DIALOG and drive its footer Save, which is also what
// exercises the dirty/ref plumbing between the two components.
vi.mock('../services/api', () => ({
  settingsApi: {
    getLLM: vi.fn(),
    updateLLM: vi.fn(),
    resetLLM: vi.fn(),
    getEmail: vi.fn(),
    updateEmail: vi.fn(),
    resetEmail: vi.fn(),
  },
}))

vi.mock('vue-sonner', () => ({
  toast: { success: vi.fn(), error: vi.fn() },
}))

// The model picker renders inside reka-ui's DropdownMenu, whose content measures
// itself with ResizeObserver — jsdom does not provide it. Without this stub the
// menu never opens, and the failure surfaces as an unhandled rejection rather than
// a test failure, so the assertions afterwards misreport the cause.
class ResizeObserverStub {
  observe() {}
  unobserve() {}
  disconnect() {}
}
vi.stubGlobal('ResizeObserver', ResizeObserverStub)

const mocked = vi.mocked(settingsApi)
const { toast } = await import('vue-sonner')

function inboxInput(): HTMLInputElement | null {
  return document.body.querySelector('[data-email-settings-inbox]')
}
function keyInput(): HTMLInputElement | null {
  return document.body.querySelector('[data-email-settings-key]')
}
function saveButton(): HTMLButtonElement | null {
  return document.body.querySelector('[data-settings-save]')
}
function cancelButton(): HTMLButtonElement | null {
  return document.body.querySelector('[data-settings-cancel]')
}
function hintText(): string {
  return (
    document.body.querySelector('[data-email-settings-removing], [data-email-settings-clearing]')
      ?.textContent ?? ''
  )
}

// v-model caches the input value; set it through the native prototype setter so
// the cached value stays in sync, then dispatch the input event.
function fill(input: HTMLInputElement, value: string) {
  const setter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set
  setter.call(input, value)
  input.dispatchEvent(new Event('input', { bubbles: true }))
}

function click(el: Element) {
  el.dispatchEvent(new MouseEvent('click', { bubbles: true }))
}

// Simulates real typing: the browser fires keydown first and only inserts the
// character if the handler did not preventDefault. `fill` cannot exercise that
// path because it writes the value straight through.
async function typeChars(input: HTMLInputElement, text: string) {
  const setter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set
  let current = input.value
  for (const ch of text) {
    const ev = new KeyboardEvent('keydown', { key: ch, bubbles: true, cancelable: true })
    const allowed = input.dispatchEvent(ev)
    if (!allowed) {
      // The component took over the value; resync from the DOM once it re-renders.
      await nextTick()
      current = input.value
      continue
    }
    current += ch
    setter.call(input, current)
    input.dispatchEvent(new Event('input', { bubbles: true }))
  }
}

// The dialog renders through a portal, so every query goes through document.body.
function mountDialog() {
  return mount(SettingsDialog, { props: { open: true }, attachTo: document.body })
}

describe('Email settings section', () => {
  beforeEach(() => {
    document.body.innerHTML = ''
    setActivePinia(createPinia())
    vi.clearAllMocks()
    mocked.getLLM.mockResolvedValue({ model: 'env-model', default_model: 'env-model', overridden: false })
    mocked.getEmail.mockResolvedValue({ configured: false, inbox: '', token_set: false })
  })

  it('prefills the inbox on open', async () => {
    mocked.getEmail.mockResolvedValue({ configured: true, inbox: 'name@agentmail.to', token_set: true })
    mountDialog()
    await flushPromises()

    expect(inboxInput()?.value).toBe('name@agentmail.to')
  })

  // The token is never sent to the client, so the field cannot be prefilled with
  // the real secret. It shows a mask instead, which is what tells the user a key
  // is stored without the browser ever holding it.
  it('shows a stored API key as a mask in a password field', async () => {
    mocked.getEmail.mockResolvedValue({ configured: true, inbox: 'name@agentmail.to', token_set: true })
    mountDialog()
    await flushPromises()

    const key = keyInput()!
    expect(key.type).toBe('password')
    expect(key.value).toBe('••••••••••••••••')
  })

  it('leaves the key field empty when no key is stored', async () => {
    mountDialog()
    await flushPromises()

    expect(keyInput()?.value).toBe('')
  })

  // The mask is a VALUE, so the first typed character must replace it rather than
  // append to it — otherwise the request would carry "•••••••x" as the new key.
  it('discards the mask on the first keystroke instead of appending to it', async () => {
    mocked.getEmail.mockResolvedValue({ configured: true, inbox: 'name@agentmail.to', token_set: true })
    mountDialog()
    await flushPromises()

    const key = keyInput()!
    expect(key.value).toBe('••••••••••••••••')

    await typeChars(key, 'ab')
    await flushPromises()

    expect(key.value).toBe('ab')
    expect(key.value).not.toContain('•')
  })

  it('discards the mask on a single keystroke too', async () => {
    mocked.getEmail.mockResolvedValue({ configured: true, inbox: 'name@agentmail.to', token_set: true })
    mountDialog()
    await flushPromises()

    await typeChars(keyInput()!, 'x')
    await flushPromises()

    expect(keyInput()?.value).toBe('x')
  })

  it('sends the retyped key rather than a mask-prefixed one', async () => {
    mocked.getEmail.mockResolvedValue({ configured: true, inbox: 'name@agentmail.to', token_set: true })
    mocked.updateEmail.mockResolvedValue({ configured: true, inbox: 'name@agentmail.to', token_set: true })
    mountDialog()
    await flushPromises()

    await typeChars(keyInput()!, 'newkey')
    await flushPromises()
    click(saveButton()!)
    await flushPromises()

    expect(mocked.updateEmail).toHaveBeenCalledWith('name@agentmail.to', 'newkey')
  })

  it('replaces the mask on paste instead of inserting into it', async () => {
    mocked.getEmail.mockResolvedValue({ configured: true, inbox: 'name@agentmail.to', token_set: true })
    mountDialog()
    await flushPromises()

    // What a paste into a masked field actually produces: the mask plus the
    // pasted text, at the caret.
    fill(keyInput()!, '••••••••••••••••pasted-key')
    await flushPromises()

    expect(keyInput()?.value).toBe('pasted-key')
  })

  // Backspace on a masked field is the natural gesture for "remove the key".
  it('clears the mask on backspace', async () => {
    mocked.getEmail.mockResolvedValue({ configured: true, inbox: 'name@agentmail.to', token_set: true })
    mountDialog()
    await flushPromises()

    fill(keyInput()!, '•••••••••••••••')
    await flushPromises()

    expect(keyInput()?.value).toBe('')
  })

  // Caret at the START of the field: the browser inserts before the mask, so a
  // prefix-only check would miss it and store the bullets as the key.
  it('strips the mask when text is inserted before it', async () => {
    mocked.getEmail.mockResolvedValue({ configured: true, inbox: 'name@agentmail.to', token_set: true })
    mountDialog()
    await flushPromises()

    fill(keyInput()!, 'z••••••••••••••••')
    await flushPromises()

    expect(keyInput()?.value).toBe('z')
  })

  // The case keystroke interception misses: the value changes without any keydown
  // (IME composition, autofill, a password manager, or a scripted insertion). The
  // watcher must still strip the bullets.
  it('strips an appended mask even when no keydown fires', async () => {
    mocked.getEmail.mockResolvedValue({ configured: true, inbox: 'name@agentmail.to', token_set: true })
    mountDialog()
    await flushPromises()

    const key = keyInput()!
    // Simulates the browser inserting text into the existing value.
    fill(key, '••••••••••••••••secret-part')
    await flushPromises()

    expect(key.value).toBe('secret-part')
  })

  // A chopped-down mask means "remove the key", not "store fifteen bullets".
  it('treats a partially deleted mask as clearing the key', async () => {
    mocked.getEmail.mockResolvedValue({ configured: true, inbox: 'name@agentmail.to', token_set: true })
    mountDialog()
    await flushPromises()

    fill(keyInput()!, '••••')
    await flushPromises()

    expect(keyInput()?.value).toBe('')
  })

  // The footer Save covers both sections, so it must light up from the email
  // section alone — that is the dirty emit doing its job.
  it('enables the footer Save once a fresh account has an inbox and a key', async () => {
    mountDialog()
    await flushPromises()
    expect((saveButton() as HTMLButtonElement).disabled).toBe(true)

    fill(inboxInput()!, 'fresh@agentmail.to')
    await flushPromises()
    // Inbox alone is not enough: there is no stored token to fall back on.
    expect((saveButton() as HTMLButtonElement).disabled).toBe(true)

    fill(keyInput()!, '«redacted:am_…»')
    await flushPromises()
    expect((saveButton() as HTMLButtonElement).disabled).toBe(false)
  })

  // The mask is a sentinel, not a value: an untouched field must send NO key so
  // the backend keeps the stored one.
  it('omits the key when the mask is left untouched', async () => {
    mocked.getEmail.mockResolvedValue({ configured: true, inbox: 'name@agentmail.to', token_set: true })
    mocked.updateEmail.mockResolvedValue({ configured: true, inbox: 'other@agentmail.to', token_set: true })
    mountDialog()
    await flushPromises()

    fill(inboxInput()!, 'other@agentmail.to')
    await flushPromises()
    click(saveButton()!)

    expect(mocked.updateEmail).toHaveBeenCalledWith('other@agentmail.to', undefined)
    expect(mocked.resetEmail).not.toHaveBeenCalled()
  })

  it('sends a typed key, trimmed, and re-masks it after saving', async () => {
    mocked.getEmail.mockResolvedValue({ configured: false, inbox: '', token_set: false })
    mocked.updateEmail.mockResolvedValue({ configured: true, inbox: 'fresh@agentmail.to', token_set: true })
    mountDialog()
    await flushPromises()

    fill(inboxInput()!, 'fresh@agentmail.to')
    fill(keyInput()!, '  «redacted:am_…»  ')
    await flushPromises()
    click(saveButton()!)
    await flushPromises()

    expect(mocked.updateEmail).toHaveBeenCalledWith('fresh@agentmail.to', '«redacted:am_…»')
    // Re-masked, never left showing the typed secret or an empty box that implies
    // no key is stored.
    expect(keyInput()?.value).toBe('••••••••••••••••')
  })

  // Emptying the key field is the way to remove a stored key. It warns first,
  // because the consequence is that email feeds stop receiving mail.
  it('clears the stored key when the field is emptied, warning first', async () => {
    mocked.getEmail.mockResolvedValue({ configured: true, inbox: 'name@agentmail.to', token_set: true })
    mocked.updateEmail.mockResolvedValue({ configured: true, inbox: 'name@agentmail.to', token_set: false })
    mountDialog()
    await flushPromises()

    fill(keyInput()!, '')
    await flushPromises()

    expect(hintText()).toContain('removes the stored key')
    expect((saveButton() as HTMLButtonElement).disabled).toBe(false)
    click(saveButton()!)

    expect(mocked.updateEmail).toHaveBeenCalledWith('name@agentmail.to', '')
    expect(mocked.resetEmail).not.toHaveBeenCalled()
    expect(keyInput()?.value).toBe('')
  })

  // Blanking BOTH fields is the full removal (the DELETE the reset button used to
  // perform), so that capability survives without a button of its own.
  it('removes the whole configuration when both fields are blanked', async () => {
    mocked.getEmail.mockResolvedValue({ configured: true, inbox: 'name@agentmail.to', token_set: true })
    mocked.resetEmail.mockResolvedValue({ configured: false, inbox: '', token_set: false })
    mountDialog()
    await flushPromises()

    fill(inboxInput()!, '')
    fill(keyInput()!, '')
    await flushPromises()

    expect(hintText()).toContain('removes your AgentMail credentials')
    click(saveButton()!)

    expect(mocked.resetEmail).toHaveBeenCalledTimes(1)
    expect(mocked.updateEmail).not.toHaveBeenCalled()
  })

  it('keeps the footer Save disabled when nothing has changed', async () => {
    mocked.getEmail.mockResolvedValue({ configured: true, inbox: 'name@agentmail.to', token_set: true })
    mountDialog()
    await flushPromises()

    expect((saveButton() as HTMLButtonElement).disabled).toBe(true)
    expect(mocked.updateEmail).not.toHaveBeenCalled()
  })

  it('surfaces a rejected credential and keeps the dialog open', async () => {
    mocked.getEmail.mockResolvedValue({ configured: false, inbox: '', token_set: false })
    mocked.updateEmail.mockRejectedValue(new Error('AgentMail rejected these credentials: 404'))
    const wrapper = mountDialog()
    await flushPromises()

    fill(inboxInput()!, 'fresh@agentmail.to')
    fill(keyInput()!, 'bad')
    await flushPromises()
    click(saveButton()!)
    await flushPromises()

    expect(document.body.querySelector('[data-email-settings-error]')?.textContent).toContain(
      'AgentMail rejected',
    )
    // A rejected key must not also throw away the edit: the dialog stays open.
    expect(wrapper.emitted('update:open')).toBeUndefined()
  })

  // Saving must refetch the inbox the Add Feed dialog reads from the store, or the
  // email option stays "(not configured)" until a reload.
  //
  // Asserted on the STORE's value rather than a spy: spying on a Pinia setup-store
  // action does not intercept the component's internal call (the action is wrapped),
  // so a spy-based assertion passes for the wrong reason. The observable effect is
  // what matters anyway.
  it('refreshes the cached inbox after a successful save', async () => {
    const { useFeedStore } = await import('../stores/feed')
    const store = useFeedStore()
    store.emailInbox = ''

    mocked.getEmail.mockResolvedValue({ configured: false, inbox: '', token_set: false })
    mocked.updateEmail.mockResolvedValue({ configured: true, inbox: 'fresh@agentmail.to', token_set: true })
    mountDialog()
    await flushPromises()

    fill(inboxInput()!, 'fresh@agentmail.to')
    fill(keyInput()!, '«redacted:am_…»')
    await flushPromises()

    mocked.getEmail.mockResolvedValue({ configured: true, inbox: 'fresh@agentmail.to', token_set: true })
    click(saveButton()!)
    await flushPromises()

    expect(store.emailInbox).toBe('fresh@agentmail.to')
  })

  it('closes on a successful save', async () => {
    mocked.getEmail.mockResolvedValue({ configured: true, inbox: 'name@agentmail.to', token_set: true })
    mocked.updateEmail.mockResolvedValue({ configured: true, inbox: 'name@agentmail.to', token_set: true })
    const wrapper = mountDialog()
    await flushPromises()

    fill(inboxInput()!, 'other@agentmail.to')
    await flushPromises()
    click(saveButton()!)
    await flushPromises()

    expect(toast.success).toHaveBeenCalled()
    expect(wrapper.emitted('update:open')).toEqual([[false]])
  })

  it('closes on Cancel without writing anything', async () => {
    mocked.getEmail.mockResolvedValue({ configured: false, inbox: '', token_set: false })
    const wrapper = mountDialog()
    await flushPromises()

    fill(inboxInput()!, 'fresh@agentmail.to')
    fill(keyInput()!, '«redacted:am_…»')
    await flushPromises()
    click(cancelButton()!)
    await flushPromises()

    expect(mocked.updateEmail).not.toHaveBeenCalled()
    expect(mocked.updateLLM).not.toHaveBeenCalled()
    expect(wrapper.emitted('update:open')).toEqual([[false]])
  })

  it('shows a load error without crashing', async () => {
    mocked.getEmail.mockRejectedValue(new Error('boom'))
    mountDialog()
    await flushPromises()

    expect(document.body.querySelector('[data-email-settings-error]')?.textContent).toContain('boom')
  })
})

// The Reset button once rendered as an EMPTY unknown element because its icon was
// never imported — the button existed and was enabled, so every button-level
// assertion passed while the user saw a blank square. Assert a rendered icon.
describe('Settings dialog icons', () => {
  beforeEach(() => {
    document.body.innerHTML = ''
    setActivePinia(createPinia())
    vi.clearAllMocks()
    mocked.getEmail.mockResolvedValue({ configured: true, inbox: 'name@agentmail.to', token_set: true })
    mocked.getLLM.mockResolvedValue({ model: 'custom-model', default_model: 'env-model', overridden: true })
  })

  it('renders an svg inside the LLM reset button', async () => {
    mountDialog()
    await flushPromises()

    const btn = document.body.querySelector('[data-llm-settings-reset]')
    expect(btn).not.toBeNull()
    expect(btn!.querySelector('svg')).not.toBeNull()
  })

  // Both section subtitles are user-specified copy; assert the exact text so a
  // later edit cannot silently drop or reword them.
  it('shows the section subtitles', async () => {
    mountDialog()
    await flushPromises()

    const dlg = document.body.querySelector('[data-settings-dialog]')!
    const text = dlg.textContent ?? ''
    expect(text).toContain('Name of the LLM model to be called for summaries.')
    expect(text).toContain(
      'Agentmail inbox name and API key for receiving emails for email-type feeds.',
    )
  })

  // Reset stages locally: no request until Save.
  it('stages a model reset without saving it', async () => {
    mocked.resetLLM.mockResolvedValue({ model: 'env-model', default_model: 'env-model', overridden: false })
    mountDialog()
    await flushPromises()

    const before = (document.body.querySelector('input#llm-model') as HTMLInputElement).value
    expect(before).toBe('custom-model')

    click(document.body.querySelector('[data-llm-settings-reset]')!)
    await flushPromises()

    expect(mocked.resetLLM).not.toHaveBeenCalled()
    expect(mocked.updateLLM).not.toHaveBeenCalled()
    expect((document.body.querySelector('input#llm-model') as HTMLInputElement).value).toBe('')
  })
})

// The model field gets a picker built from the endpoint's list.
describe('Settings dialog model picker', () => {
  beforeEach(() => {
    document.body.innerHTML = ''
    setActivePinia(createPinia())
    vi.clearAllMocks()
    mocked.getEmail.mockResolvedValue({ configured: false, inbox: '', token_set: false })
    mocked.getLLM.mockResolvedValue({
      model: 'virtual-free',
      default_model: 'virtual-free',
      overridden: false,
      models: ['virtual-chat', 'virtual-free', 'virtual-smart'],
    })
  })

  it('offers the endpoint list and picks a model into the field', async () => {
    mountDialog()
    await flushPromises()

    const trigger = document.body.querySelector('[data-llm-model-picker]')
    expect(trigger).not.toBeNull()
    click(trigger!)
    await flushPromises()

    // reka-ui renders the menu in a portal; find the row by its label.
    const items = [...document.body.querySelectorAll('[role=menuitem]')]
    // Each row holds an indicator span, the label span (no class), and optionally
    // a "default" detail span — take the unclassed one.
    const labelOf = (i: Element) => i.querySelector('span:not([class])')?.textContent?.trim() ?? ''
    expect(items.map(labelOf)).toEqual(['virtual-chat', 'virtual-free', 'virtual-smart'])

    click(items.find((i) => i.textContent?.includes('virtual-smart'))!)
    await flushPromises()

    expect((document.body.querySelector('input#llm-model') as HTMLInputElement).value).toBe(
      'virtual-smart',
    )
  })

  // The list is advisory, so its absence must leave a working text field rather
  // than an empty dropdown.
  it('hides the picker when the endpoint offers no list', async () => {
    mocked.getLLM.mockResolvedValue({
      model: 'virtual-free', default_model: 'virtual-free', overridden: false, models: [],
    })
    mountDialog()
    await flushPromises()

    expect(document.body.querySelector('[data-llm-model-picker]')).toBeNull()
    expect(document.body.querySelector('input#llm-model')).not.toBeNull()
  })

  // A save answers with a null list (PUT/DELETE do not re-fetch it). The picker
  // must keep what it loaded, or it would empty itself after every save.
  it('keeps the picker populated after a save', async () => {
    mocked.updateLLM.mockResolvedValue({
      model: 'virtual-smart',
      default_model: 'virtual-free',
      overridden: true,
      models: null as unknown as string[],
    })
    mountDialog()
    await flushPromises()

    fill(document.body.querySelector('input#llm-model') as HTMLInputElement, 'virtual-smart')
    await flushPromises()
    click(document.body.querySelector('[data-settings-save]')!)
    await flushPromises()

    expect(document.body.querySelector('[data-llm-model-picker]')).not.toBeNull()
    click(document.body.querySelector('[data-llm-model-picker]')!)
    await flushPromises()

    const labels = [...document.body.querySelectorAll('[role=menuitem]')].map((i) =>
      i.querySelector('span:not([class])')?.textContent?.trim(),
    )
    expect(labels).toEqual(['virtual-chat', 'virtual-free', 'virtual-smart'])
  })

  // A long gateway list must not be a wall of text: typing narrows it.
  it('narrows the list to matching models while typing', async () => {
    mountDialog()
    await flushPromises()

    fill(document.body.querySelector('input#llm-model') as HTMLInputElement, 'smart')
    await flushPromises()

    click(document.body.querySelector('[data-llm-model-picker]')!)
    await flushPromises()

    const labels = [...document.body.querySelectorAll('[role=menuitem]')].map((i) =>
      i.textContent?.trim(),
    )
    expect(labels).toEqual(['virtual-smart'])
  })
})
