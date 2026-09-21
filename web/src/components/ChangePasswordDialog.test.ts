import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import ChangePasswordDialog from './ChangePasswordDialog.vue'
import { authApi } from '../services/api'

vi.mock('../services/api', () => ({
  authApi: {
    changePassword: vi.fn(),
  },
  ApiError: class extends Error {
    constructor(public status: number, body: any) {
      super(body?.error || `HTTP ${status}`)
    }
  },
}))

vi.mock('vue-sonner', () => ({
  toast: { success: vi.fn(), error: vi.fn() },
}))

const mockedChangePassword = vi.mocked(authApi.changePassword)
const { toast } = await import('vue-sonner')

// The dialog content renders inside a reka-ui DialogPortal (teleported to
// document.body), so read the live DOM, not the wrapper.
function dialogEl(): HTMLElement | null {
  return document.body.querySelector('[data-change-password-dialog]')
}

function inputs(): HTMLInputElement[] {
  return Array.from(document.body.querySelectorAll('[data-change-password-dialog] input[type="password"]'))
}

function submitButton(): HTMLButtonElement | null {
  return document.body.querySelector('[data-change-password-dialog] button[type="submit"]')
}

// Vue's v-model caches the input value; set it through the native prototype
// setter so the cached value stays in sync, then dispatch the input event.
function fill(input: HTMLInputElement, value: string) {
  const setter = Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value')!.set!
  setter.call(input, value)
  input.dispatchEvent(new Event('input', { bubbles: true }))
}

async function fillAll(current: string, next: string, confirm: string) {
  const [cur, nxt, conf] = inputs()
  fill(cur, current)
  fill(nxt, next)
  fill(conf, confirm)
  await flushPromises()
}

function mountOpen() {
  return mount(ChangePasswordDialog, { props: { open: true } })
}

describe('ChangePasswordDialog', () => {
  beforeEach(() => {
    document.body.innerHTML = ''
    vi.clearAllMocks()
    mockedChangePassword.mockReset()
    mockedChangePassword.mockResolvedValue(undefined)
  })

  it('renders nothing while closed', () => {
    mount(ChangePasswordDialog, { props: { open: false } })
    expect(dialogEl()).toBeNull()
  })

  it('renders three password fields when open', async () => {
    mountOpen()
    await flushPromises()

    const els = inputs()
    expect(els).toHaveLength(3)
    expect(els.map(i => i.getAttribute('autocomplete'))).toEqual(['current-password', 'new-password', 'new-password'])
    expect(document.body.textContent).toContain('Current password')
    expect(document.body.textContent).toContain('New password')
    expect(document.body.textContent).toContain('Confirm new password')
  })

  it('blocks submission until all three fields are filled', async () => {
    mountOpen()
    await flushPromises()
    expect(submitButton()!.hasAttribute('disabled')).toBe(true)

    await fillAll('old-pass', 'new-pass', 'new-pass')
    expect(submitButton()!.hasAttribute('disabled')).toBe(false)
  })

  it('blocks submission and shows a hint when the confirmation does not match', async () => {
    mountOpen()
    await flushPromises()

    await fillAll('old-pass', 'new-pass', 'different')
    expect(submitButton()!.hasAttribute('disabled')).toBe(true)
    expect(document.body.textContent).toContain('Passwords do not match.')

    // Fixing the confirmation enables submission and clears the hint.
    const [,, conf] = inputs()
    fill(conf, 'new-pass')
    await flushPromises()
    expect(submitButton()!.hasAttribute('disabled')).toBe(false)
    expect(document.body.textContent).not.toContain('Passwords do not match.')
  })

  it('submits current and new password, shows a success toast, and closes on success', async () => {
    const wrapper = mountOpen()
    await flushPromises()
    await fillAll('old-pass', 'new-pass', 'new-pass')

    submitButton()!.click()
    await flushPromises()

    expect(mockedChangePassword).toHaveBeenCalledWith('old-pass', 'new-pass')
    // The success toast now carries the green-check icon (see lib/toast.ts).
    expect(toast.success).toHaveBeenCalledWith(
      'Password changed',
      expect.objectContaining({ icon: expect.anything() }),
    )
    expect(wrapper.emitted('update:open')).toEqual([[false]])
  })

  it('disables the submit button while the request is in flight', async () => {
    let resolveChange!: (v: unknown) => void
    mockedChangePassword.mockReturnValue(new Promise(r => { resolveChange = r }))

    const wrapper = mountOpen()
    await flushPromises()
    await fillAll('old-pass', 'new-pass', 'new-pass')

    submitButton()!.click()
    await flushPromises()

    expect(submitButton()!.hasAttribute('disabled')).toBe(true)
    expect(submitButton()!.textContent).toContain('Changing')
    expect(wrapper.emitted('update:open')).toBeUndefined()

    resolveChange(undefined)
    await flushPromises()
    expect(wrapper.emitted('update:open')).toEqual([[false]])
  })

  it('shows the server error and keeps the dialog open on failure', async () => {
    const { ApiError } = await import('../services/api')
    mockedChangePassword.mockRejectedValue(new ApiError(401, { error: 'invalid old password' }))

    const wrapper = mountOpen()
    await flushPromises()
    await fillAll('wrong-pass', 'new-pass', 'new-pass')

    submitButton()!.click()
    await flushPromises()

    expect(document.body.textContent).toContain('invalid old password')
    expect(wrapper.emitted('update:open')).toBeUndefined()
    expect(dialogEl()).not.toBeNull()
    expect(toast.success).not.toHaveBeenCalled()
  })

  it('resets the form when reopened', async () => {
    const wrapper = mount(ChangePasswordDialog, { props: { open: true } })
    await flushPromises()
    await fillAll('old-pass', 'new-pass', 'new-pass')
    expect(inputs().every(i => i.value !== '')).toBe(true)

    await wrapper.setProps({ open: false })
    await wrapper.setProps({ open: true })
    await flushPromises()
    expect(inputs().every(i => i.value === '')).toBe(true)
  })
})
