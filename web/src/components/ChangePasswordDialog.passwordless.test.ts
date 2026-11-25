import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import ChangePasswordDialog from './ChangePasswordDialog.vue'
import { authApi } from '../services/api'

// An account provisioned through Pocket ID has no password at all, so the
// "current password" field it would have to fill in cannot be satisfied. The
// dialog therefore has to ask for a new password only, and send no old one.
//
// The flag comes from the profile (/api/auth/me -> has_password), which is what
// keeps this honest: it reflects the stored hash, not how the account was made.

vi.mock('../services/api', () => ({
  authApi: {
    changePassword: vi.fn(),
  },
  ApiError: class extends Error {
    constructor(public status: number, body: any) {
      super(body?.error || `HTTP ${status}`)
    }
  },
  setOnUnauthorized: vi.fn(),
  getAuthToken: () => null,
  setAuthToken: vi.fn(),
}))

vi.mock('vue-sonner', () => ({
  toast: { success: vi.fn(), error: vi.fn() },
}))

const mockedChangePassword = vi.mocked(authApi.changePassword)



function inputs(): HTMLInputElement[] {
  return Array.from(document.body.querySelectorAll('[data-change-password-dialog] input[type="password"]'))
}

function submitButton(): HTMLButtonElement | null {
  return document.body.querySelector('[data-change-password-dialog] button[type="submit"]')
}

function fill(input: HTMLInputElement, value: string) {
  const setter = Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value')!.set!
  setter.call(input, value)
  input.dispatchEvent(new Event('input', { bubbles: true }))
}

describe('ChangePasswordDialog for a passwordless account', () => {
  beforeEach(() => {
    document.body.innerHTML = ''
    vi.clearAllMocks()
    mockedChangePassword.mockReset()
    mockedChangePassword.mockResolvedValue(undefined)
  })

  it('asks only for a new password when the account has none', async () => {
    mount(ChangePasswordDialog, { props: { open: true, hasPassword: false } })
    await flushPromises()

    expect(inputs()).toHaveLength(2)
    expect(document.body.textContent).not.toContain('Current password')
    expect(document.body.textContent).toContain('New password')
  })

  it('does not send an old password in that case', async () => {
    mount(ChangePasswordDialog, { props: { open: true, hasPassword: false } })
    await flushPromises()

    const [next, confirm] = inputs()
    fill(next, 'firstpassword1')
    fill(confirm, 'firstpassword1')
    await flushPromises()
    expect(submitButton()!.hasAttribute('disabled')).toBe(false)

    submitButton()!.click()
    await flushPromises()
    expect(mockedChangePassword).toHaveBeenCalledWith('', 'firstpassword1')
  })

  it('still asks for the current password when one exists', async () => {
    mount(ChangePasswordDialog, { props: { open: true, hasPassword: true } })
    await flushPromises()

    expect(inputs()).toHaveLength(3)
    expect(document.body.textContent).toContain('Current password')
  })
})
