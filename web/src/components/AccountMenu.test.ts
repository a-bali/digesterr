import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { setActivePinia, createPinia } from 'pinia'
import AccountMenu from './AccountMenu.vue'
import { useAuthStore } from '../stores/auth'

vi.mock('../services/api', () => ({
  authApi: {
    signout: vi.fn(),
  },
  setAuthToken: vi.fn(),
  getAuthToken: vi.fn(() => null),
  setOnUnauthorized: vi.fn(),
  ApiError: class extends Error {},
}))

vi.mock('../router', () => ({
  router: { push: vi.fn() },
}))

// reka-ui's dropdown content measures itself with ResizeObserver, which jsdom
// does not provide.
class ResizeObserverStub {
  observe() {}
  unobserve() {}
  disconnect() {}
}
vi.stubGlobal('ResizeObserver', ResizeObserverStub)

// The dropdown content renders inside a reka-ui portal (teleported to
// document.body), so read the live DOM, not the wrapper.
function menuItems(): HTMLElement[] {
  return Array.from(document.body.querySelectorAll('[role="menuitem"]')) as HTMLElement[]
}

function user() {
  return { id: 1, email: 'user@example.com', created_at: '', updated_at: '' }
}

async function openMenu(wrapper: ReturnType<typeof mount>) {
  await wrapper.find('button').trigger('click')
  await flushPromises()
}

async function clickItem(label: string) {
  const item = menuItems().find(i => i.textContent?.includes(label))
  if (!item) throw new Error(`menu item not found: ${label}`)
  item.dispatchEvent(new MouseEvent('click', { bubbles: true }))
  await flushPromises()
}

describe('AccountMenu', () => {
  beforeEach(() => {
    document.body.innerHTML = ''
    setActivePinia(createPinia())
    vi.clearAllMocks()
  })

  it('renders an account trigger showing the signed-in email', () => {
    useAuthStore().user = user()
    const wrapper = mount(AccountMenu)
    expect(wrapper.text()).toContain('user@example.com')
    expect(wrapper.find('button').attributes('aria-label')).toBe('Account menu')
  })

  it('opens a dropdown with the three account actions', async () => {
    const wrapper = mount(AccountMenu)
    await openMenu(wrapper)

    const labels = menuItems().map(i => i.textContent?.trim())
    expect(labels).toContain('Change password')
    expect(labels).toContain('Google Reader API')
    expect(labels).toContain('Logout')
    // Each item carries its icon.
    expect(menuItems().every(i => i.querySelector('svg'))).toBe(true)
  })

  it('emits change-password when that item is clicked', async () => {
    const wrapper = mount(AccountMenu)
    await openMenu(wrapper)
    await clickItem('Change password')
    expect(wrapper.emitted('change-password')).toHaveLength(1)
  })

  it('emits google-reader-api when that item is clicked', async () => {
    const wrapper = mount(AccountMenu)
    await openMenu(wrapper)
    await clickItem('Google Reader API')
    expect(wrapper.emitted('google-reader-api')).toHaveLength(1)
  })

  it('signs out when Logout is clicked, preserving the auth store flow', async () => {
    const { authApi } = await import('../services/api')
    const store = useAuthStore()
    store.user = user()
    store.token = 'some-token'
    ;(authApi.signout as ReturnType<typeof vi.fn>).mockResolvedValue(undefined)

    const wrapper = mount(AccountMenu)
    await openMenu(wrapper)
    await clickItem('Logout')

    expect(authApi.signout).toHaveBeenCalledTimes(1)
    expect(store.user).toBeNull()
    expect(store.token).toBeNull()
  })

  it('closes the menu after selecting an action', async () => {
    const wrapper = mount(AccountMenu)
    await openMenu(wrapper)
    expect(menuItems().length).toBe(3)

    await clickItem('Logout')
    expect(menuItems().length).toBe(0)
  })
})
