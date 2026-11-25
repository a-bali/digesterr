import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { setActivePinia, createPinia } from 'pinia'
import { createRouter, createMemoryHistory } from 'vue-router'

// The login page is where the OIDC feature becomes visible, so its behaviour is
// pinned against a real mount rather than a placeholder assertion:
//
//   - the button only appears when the server reports a configured provider
//   - clicking it navigates to the server's /start route (never to the provider
//     directly: the provider URL must be minted server-side, with state)
//   - the error codes the callback redirects with are rendered as sentences
//   - no token, code or state ever reaches the URL

// Partial mock: only authApi.oidcStatus is replaced. Everything else in the
// api module (the in-memory token accessors the auth store reads at creation)
// stays real, so the mount exercises the same wiring production has.
const status = vi.fn()
vi.mock('../services/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../services/api')>()
  return {
    ...actual,
    authApi: {
      ...actual.authApi,
      oidcStatus: (...args: unknown[]) => status(...args),
    },
  }
})

import LoginView from './LoginView.vue'

function makeRouter() {
  return createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/', component: { template: '<div>home</div>' } },
      { path: '/login', component: LoginView },
    ],
  })
}

async function mountLogin(query = '') {
  setActivePinia(createPinia())
  const router = makeRouter()
  router.push('/login' + query)
  await router.isReady()
  const wrapper = mount(LoginView, { global: { plugins: [router] } })
  await flushPromises()
  return { wrapper, router }
}

describe('LoginView OIDC', () => {
  beforeEach(() => {
    status.mockReset()
    vi.clearAllMocks()
  })

  it('renders no SSO button when the server reports OIDC disabled', async () => {
    status.mockResolvedValue({ enabled: false, provider_name: '' })
    const { wrapper } = await mountLogin()
    expect(wrapper.find('[data-test="oidc-signin"]').exists()).toBe(false)
    // Local auth is untouched: the password form is still there.
    expect(wrapper.find('input[type="email"]').exists()).toBe(true)
    expect(wrapper.find('input[type="password"]').exists()).toBe(true)
  })

  it('renders the provider name and keeps the local form available', async () => {
    status.mockResolvedValue({ enabled: true, provider_name: 'Example SSO' })
    const { wrapper } = await mountLogin()
    const button = wrapper.find('[data-test="oidc-signin"]')
    expect(button.exists()).toBe(true)
    expect(button.text()).toContain('Example SSO')
    expect(wrapper.find('input[type="password"]').exists()).toBe(true)
  })

  it('still renders the local form when the status call fails', async () => {
    // An instance with OIDC off has no /status route at all, so this is the
    // normal path, not an edge case: the login page must not break.
    status.mockRejectedValue(new Error('404'))
    const { wrapper } = await mountLogin()
    expect(wrapper.find('[data-test="oidc-signin"]').exists()).toBe(false)
    expect(wrapper.find('input[type="password"]').exists()).toBe(true)
  })

  it('navigates to the server start route without putting a token in the URL', async () => {
    status.mockResolvedValue({ enabled: true, provider_name: 'Example SSO' })
    const { wrapper, router } = await mountLogin()
    const href = wrapper.find('[data-test="oidc-signin"]').attributes('href')
    expect(href).toBe('/api/auth/oidc/start')
    // The browser must not be sent to the provider by the SPA: state and PKCE
    // are minted server-side, and a client-built authorize URL would have to
    // know the client secret's exchange and could be tampered with.
    expect(href).not.toMatch(/^https?:/)
    expect(href).not.toContain('token')
    expect(router.currentRoute.value.fullPath).toBe('/login')
  })

  it('renders each callback error code as a readable message', async () => {
    status.mockResolvedValue({ enabled: true, provider_name: 'Example SSO' })
    const cases: Array<[string, string]> = [
      ['oidc_email_unverified', 'verified'],
      ['oidc_email_ambiguous', 'more than one'],
      ['oidc_denied', 'cancelled'],
      ['oidc_state_invalid', 'expired'],
      ['oidc_invalid_token', 'could not be verified'],
      ['oidc_exchange_failed', 'could not be completed'],
    ]
    for (const [code, expected] of cases) {
      const { wrapper } = await mountLogin('?error=' + code)
      const alert = wrapper.find('[data-test="login-error"]')
      expect(alert.exists(), `no error rendered for ${code}`).toBe(true)
      expect(alert.text().toLowerCase()).toContain(expected)
    }
  })

  it('does not echo an unrecognised error code into the page', async () => {
    status.mockResolvedValue({ enabled: true, provider_name: 'Example SSO' })
    const { wrapper } = await mountLogin('?error=' + encodeURIComponent('<script>alert(1)</script>'))
    const alert = wrapper.find('[data-test="login-error"]')
    expect(alert.exists()).toBe(true)
    expect(alert.text()).not.toContain('<script>')
    expect(alert.text().toLowerCase()).toContain('sign-in failed')
  })

  it('shows no error by default', async () => {
    status.mockResolvedValue({ enabled: false, provider_name: '' })
    const { wrapper } = await mountLogin()
    expect(wrapper.find('[data-test="login-error"]').exists()).toBe(false)
  })
})
