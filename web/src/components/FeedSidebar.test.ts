import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { setActivePinia, createPinia } from 'pinia'
import { createRouter, createMemoryHistory } from 'vue-router'
import FeedSidebar from './FeedSidebar.vue'
import type { Feed, DigestConfig } from '../types/api'

vi.mock('../services/api', () => ({
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
  itemApi: { updateStatus: vi.fn(), getItem: vi.fn(), extract: vi.fn() },
  logsApi: { list: vi.fn().mockResolvedValue({ entries: [], total: 0, has_more: false }) },
  setAuthToken: vi.fn(),
  getAuthToken: vi.fn(() => null),
  setOnUnauthorized: vi.fn(),
  ApiError: class extends Error {},
}))

import { feedApi } from '../services/api'

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

const feed: Feed = {
  id: 1, user_id: 1, category: '', type: 'rss', name: 'Example Feed', url: 'https://example.com/rss',
  config: '', last_fetched_at: null, status: 'ok', extract_content: 0, created_at: '', updated_at: '',
}

const digestConfig: DigestConfig = {
  id: 7, user_id: 1, name: 'Morning Brief', schedule: '0 8 * * *', config: '{}', created_at: '', updated_at: '',
}

function router() {
  return createRouter({
    history: createMemoryHistory(),
    routes: [{ path: '/', component: FeedSidebar }],
  })
}

async function mountSidebar() {
  const r = router()
  const wrapper = mount(FeedSidebar, { global: { plugins: [r] } })
  await r.isReady()
  await flushPromises()
  return wrapper
}

function confirmButton(): HTMLElement | null {
  return document.body.querySelector('[data-mark-all-read-confirm]')
}

function dialogText(): string {
  return document.body.querySelector('[role="dialog"]')?.textContent ?? ''
}

describe('FeedSidebar hover actions', () => {
  beforeEach(() => {
    document.body.innerHTML = ''
    setActivePinia(createPinia())
    vi.clearAllMocks()
  })

  it('keeps feed action buttons reachable on touch devices (pointer-coarse) and via keyboard focus', async () => {
    const wrapper = await mountSidebar()
    const { useFeedStore } = await import('../stores/feed')
    const feedStore = useFeedStore()
    feedStore.feeds.push(feed)
    await flushPromises()

    for (const title of ['Settings', 'Mark read', 'Refresh']) {
      const btn = wrapper.find(`button[title="${title}"]`)
      expect(btn.exists()).toBe(true)
      expect(btn.classes()).toContain('pointer-coarse:opacity-100')
      expect(btn.classes()).toContain('group-focus-within:opacity-100')
    }
    wrapper.unmount()
  })

  it('keeps digest config action buttons reachable on touch devices', async () => {
    const { digestApi } = await import('../services/api')
    vi.mocked(digestApi.listConfigs).mockResolvedValue([digestConfig])
    const wrapper = await mountSidebar()
    await flushPromises()

    const settingsBtn = wrapper.find('button[title="Settings"]')
    expect(settingsBtn.exists()).toBe(true)
    const actionsContainer = settingsBtn.element.parentElement
    expect(actionsContainer?.className).toContain('pointer-coarse:opacity-100')
    wrapper.unmount()
  })
})

describe('FeedSidebar mark-all-as-read confirmation', () => {
  beforeEach(() => {
    document.body.innerHTML = ''
    setActivePinia(createPinia())
    vi.clearAllMocks()
  })

  it('opens a confirmation dialog before marking a feed read, without calling the API yet', async () => {
    const wrapper = await mountSidebar()
    const { useFeedStore } = await import('../stores/feed')
    const feedStore = useFeedStore()
    feedStore.feeds.push(feed)
    await flushPromises()

    await wrapper.find('button[title="Mark read"]').trigger('click')
    await flushPromises()

    expect(dialogText()).toContain('Mark all as read?')
    expect(dialogText()).toContain('Example Feed')
    expect(confirmButton()).not.toBeNull()
    expect(feedApi.markRead).not.toHaveBeenCalled()
    wrapper.unmount()
  })

  it('cancelling closes the dialog without marking read', async () => {
    const wrapper = await mountSidebar()
    const { useFeedStore } = await import('../stores/feed')
    const feedStore = useFeedStore()
    feedStore.feeds.push(feed)
    await flushPromises()

    await wrapper.find('button[title="Mark read"]').trigger('click')
    await flushPromises()

    const cancel = Array.from(document.body.querySelectorAll('button')).find(b => b.textContent?.trim() === 'Cancel')
    expect(cancel).toBeDefined()
    cancel!.click()
    await flushPromises()

    expect(document.body.querySelector('[role="dialog"]')).toBeNull()
    expect(feedApi.markRead).not.toHaveBeenCalled()
    wrapper.unmount()
  })

  it('confirming marks the feed read and closes the dialog', async () => {
    const wrapper = await mountSidebar()
    const { useFeedStore } = await import('../stores/feed')
    const feedStore = useFeedStore()
    feedStore.feeds.push(feed)
    await flushPromises()

    await wrapper.find('button[title="Mark read"]').trigger('click')
    await flushPromises()

    confirmButton()!.click()
    await flushPromises()

    expect(feedApi.markRead).toHaveBeenCalledWith(feed.id)
    expect(document.body.querySelector('[role="dialog"]')).toBeNull()
    wrapper.unmount()
  })

  it('closes on Escape without marking read', async () => {
    const wrapper = await mountSidebar()
    const { useFeedStore } = await import('../stores/feed')
    const feedStore = useFeedStore()
    feedStore.feeds.push(feed)
    await flushPromises()

    await wrapper.find('button[title="Mark read"]').trigger('click')
    await flushPromises()
    expect(document.body.querySelector('[role="dialog"]')).not.toBeNull()

    document.body.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }))
    await flushPromises()

    expect(document.body.querySelector('[role="dialog"]')).toBeNull()
    expect(feedApi.markRead).not.toHaveBeenCalled()
    wrapper.unmount()
  })
})
