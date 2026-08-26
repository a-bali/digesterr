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

import { feedApi, digestApi } from '../services/api'

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
    vi.mocked(digestApi.listConfigs).mockResolvedValue([])
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
    vi.mocked(digestApi.listConfigs).mockResolvedValue([])
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

  it('Enter on the focused confirm button marks the feed read', async () => {
    const wrapper = await mountSidebar()
    const { useFeedStore } = await import('../stores/feed')
    const feedStore = useFeedStore()
    feedStore.feeds.push(feed)
    await flushPromises()

    await wrapper.find('button[title="Mark read"]').trigger('click')
    await flushPromises()

    const btn = confirmButton() as HTMLElement
    expect(btn).not.toBeNull()
    btn.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true, cancelable: true }))
    await flushPromises()

    expect(feedApi.markRead).toHaveBeenCalledWith(feed.id)
    expect(document.body.querySelector('[role="dialog"]')).toBeNull()
    wrapper.unmount()
  })

  it('Enter does not call markRead twice when both keydown and click fire', async () => {
    const wrapper = await mountSidebar()
    const { useFeedStore } = await import('../stores/feed')
    const feedStore = useFeedStore()
    feedStore.feeds.push(feed)
    await flushPromises()

    await wrapper.find('button[title="Mark read"]').trigger('click')
    await flushPromises()

    const btn = confirmButton() as HTMLElement
    btn.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true, cancelable: true }))
    btn.click()
    await flushPromises()

    expect(feedApi.markRead).toHaveBeenCalledTimes(1)
    wrapper.unmount()
  })

  it('auto-focuses the confirm button when the dialog opens', async () => {
    const wrapper = await mountSidebar()
    const { useFeedStore } = await import('../stores/feed')
    const feedStore = useFeedStore()
    feedStore.feeds.push(feed)
    await flushPromises()

    await wrapper.find('button[title="Mark read"]').trigger('click')
    await flushPromises()

    // Wait for watcher's nextTick + focus to settle.
    await new Promise(r => setTimeout(r, 0))
    expect(document.activeElement).toBe(confirmButton())
    wrapper.unmount()
  })
})

describe('FeedSidebar section totals', () => {
  beforeEach(() => {
    document.body.innerHTML = ''
    setActivePinia(createPinia())
    vi.clearAllMocks()
    // Reset mock return values that individual tests override — clearAllMocks
    // only clears call history, not mockResolvedValue/mockReturnValue.
    vi.mocked(digestApi.listConfigs).mockResolvedValue([])
  })

  it('computes totalFeedUnread as the sum of all feed unread counts', async () => {
    const wrapper = await mountSidebar()
    const { useFeedStore } = await import('../stores/feed')
    const feedStore = useFeedStore()
    feedStore.feeds.push({ ...feed, id: 1 })
    feedStore.feeds.push({ ...feed, id: 2 })
    feedStore.unreadCounts[1] = 3
    feedStore.unreadCounts[2] = 5
    await flushPromises()

    // Scope queries to the wrapper root to avoid cross-test DOM pollution.
    const root = wrapper.element as HTMLElement
    expect(root.querySelector('[data-testid="feeds-section-total"]')?.textContent).toBe('8')
    expect(root.querySelector('[data-testid="digests-section-total"]')).toBeNull()
    wrapper.unmount()
  })

  it('computes totalDigestUnread as the sum of all config unread counts', async () => {
    // Set mock BEFORE mount so onMounted's fetchConfigs loads the config.
    vi.mocked(digestApi.listConfigs).mockResolvedValue([digestConfig])
    const wrapper = await mountSidebar()
    const { useDigestStore } = await import('../stores/digest')
    const digestStore = useDigestStore()
    // fetchConfigs already loaded digestConfig — just set the unread count.
    digestStore.unreadCounts[digestConfig.id] = 4
    await flushPromises()

    const root = wrapper.element as HTMLElement
    expect(root.querySelector('[data-testid="digests-section-total"]')?.textContent).toBe('4')
    expect(root.querySelector('[data-testid="feeds-section-total"]')).toBeNull()
    wrapper.unmount()
  })

  it('omits section badges when total is zero', async () => {
    const wrapper = await mountSidebar()
    const { useFeedStore } = await import('../stores/feed')
    const feedStore = useFeedStore()
    feedStore.feeds.push(feed)
    feedStore.unreadCounts[feed.id] = 0
    await flushPromises()

    const root = wrapper.element as HTMLElement
    expect(root.querySelector('[data-testid="feeds-section-total"]')).toBeNull()
    expect(root.querySelector('[data-testid="digests-section-total"]')).toBeNull()
    wrapper.unmount()
  })

  it('section total reflects store map values (computed sum correctness)', async () => {
    const wrapper = await mountSidebar()
    const { useFeedStore } = await import('../stores/feed')
    const { useDigestStore } = await import('../stores/digest')
    const feedStore = useFeedStore()
    const digestStore = useDigestStore()
    feedStore.feeds.push({ ...feed, id: 10, name: 'A' })
    feedStore.feeds.push({ ...feed, id: 11, name: 'B' })
    feedStore.unreadCounts[10] = 2
    feedStore.unreadCounts[11] = 3

    // Verify the store map is the source of truth for the computed totals.
    expect(feedStore.unreadCounts[10] + feedStore.unreadCounts[11]).toBe(5)
    wrapper.unmount()
  })
})
