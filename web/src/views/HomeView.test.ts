import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { setActivePinia, createPinia } from 'pinia'
import { createRouter, createMemoryHistory } from 'vue-router'
import HomeView from './HomeView.vue'

vi.mock('../services/api', () => ({
  authApi: { signout: vi.fn(), changePassword: vi.fn() },
  feedApi: {
    list: vi.fn().mockResolvedValue([]),
    create: vi.fn(),
    update: vi.fn(),
    delete: vi.fn(),
    getItems: vi.fn().mockResolvedValue({ items: [] }),
    fetch: vi.fn(),
    markRead: vi.fn(),
    unreadCounts: vi.fn().mockResolvedValue({}),
  },
  itemApi: { updateStatus: vi.fn(), getItem: vi.fn(), extract: vi.fn() },
  digestApi: {
    listConfigs: vi.fn().mockResolvedValue([]),
    createConfig: vi.fn(),
    updateConfig: vi.fn(),
    deleteConfig: vi.fn(),
    getConfig: vi.fn(),
    generate: vi.fn(),
    listResults: vi.fn().mockResolvedValue({ results: [], has_more: false }),
    getResult: vi.fn(),
    updateResultStatus: vi.fn(),
    deleteResult: vi.fn(),
    listResultItems: vi.fn().mockResolvedValue([]),
    unreadCounts: vi.fn().mockResolvedValue({}),
  },
  logsApi: { list: vi.fn().mockResolvedValue({ entries: [], total: 0, has_more: false }) },
  setAuthToken: vi.fn(),
  getAuthToken: vi.fn(() => null),
  setOnUnauthorized: vi.fn(),
  ApiError: class extends Error {},
}))

// jsdom lacks matchMedia (useBreakpoints) and ResizeObserver (reka-ui
// dropdown/splitter measuring).
window.matchMedia = window.matchMedia || ((query: string) => ({
  matches: false,
  media: query,
  onchange: null,
  addListener: () => {},
  removeListener: () => {},
  addEventListener: () => {},
  removeEventListener: () => {},
  dispatchEvent: () => false,
})) as MediaQueryList

class ResizeObserverStub {
  observe() {}
  unobserve() {}
  disconnect() {}
}
vi.stubGlobal('ResizeObserver', ResizeObserverStub)

function router() {
  return createRouter({
    history: createMemoryHistory(),
    routes: [{ path: '/', component: HomeView }],
  })
}

// reka-ui dropdown content is teleported to document.body.
function menuItems(): HTMLElement[] {
  return Array.from(document.body.querySelectorAll('[role="menuitem"]')) as HTMLElement[]
}

function passwordDialog(): HTMLElement | null {
  return document.body.querySelector('[data-change-password-dialog]')
}

async function mountHome() {
  const r = router()
  const wrapper = mount(HomeView, { global: { plugins: [r] } })
  await r.isReady()
  await flushPromises()
  return wrapper
}

async function openMenu(wrapper: ReturnType<typeof mount>) {
  await wrapper.find('button[aria-label="Account menu"]').trigger('click')
  await flushPromises()
}

async function clickMenuItem(label: string) {
  const item = menuItems().find(i => i.textContent?.includes(label))
  if (!item) throw new Error(`menu item not found: ${label}`)
  item.dispatchEvent(new MouseEvent('click', { bubbles: true }))
  await flushPromises()
}

describe('HomeView account menu → change password dialog', () => {
  beforeEach(() => {
    document.body.innerHTML = ''
    setActivePinia(createPinia())
  })

  it('does not show the change password dialog on mount', async () => {
    const wrapper = await mountHome()
    expect(passwordDialog()).toBeNull()
    wrapper.unmount()
  })

  it('opens the dialog from the "Change password" menu item and closes on Escape', async () => {
    const wrapper = await mountHome()
    expect(passwordDialog()).toBeNull()

    await openMenu(wrapper)
    await clickMenuItem('Change password')

    expect(passwordDialog()).not.toBeNull()
    const inputs = document.body.querySelectorAll('[data-change-password-dialog] input[type="password"]')
    expect(inputs).toHaveLength(3)

    // Escape closes the dialog (reka-ui) without unmounting HomeView.
    document.body.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }))
    await flushPromises()
    expect(passwordDialog()).toBeNull()

    wrapper.unmount()
  })

  it('keeps the dialog closed when other menu items are used', async () => {
    const wrapper = await mountHome()
    await openMenu(wrapper)
    await clickMenuItem('Google Reader API')
    expect(passwordDialog()).toBeNull()
    wrapper.unmount()
  })
})
