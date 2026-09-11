import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import FeedDialog from './FeedDialog.vue'
import type { Feed } from '../types/api'

const mockDiscover = vi.fn()
vi.mock('../services/api', () => ({
  feedApi: {
    list: vi.fn().mockResolvedValue([]),
    create: vi.fn(),
    update: vi.fn(),
    delete: vi.fn(),
    getItems: vi.fn(),
    fetch: vi.fn(),
    markRead: vi.fn(),
    unreadCounts: vi.fn().mockResolvedValue({}),
    discover: (...args: any[]) => mockDiscover(...args),
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
  itemApi: { updateStatus: vi.fn(), getItem: vi.fn(), extract: vi.fn(), summarize: vi.fn(), getComments: vi.fn().mockResolvedValue({ comments: [] }) },
  authApi: { signout: vi.fn(), changePassword: vi.fn() },
  logsApi: { list: vi.fn().mockResolvedValue({ entries: [], total: 0 }) },
  setAuthToken: vi.fn(),
  getAuthToken: vi.fn(() => 'test-token'),
  setOnUnauthorized: vi.fn(),
  ApiError: class extends Error {},
}))

vi.mock('vue-sonner', () => ({ toast: { success: vi.fn(), error: vi.fn() } }))

function makeFeed(overrides: Partial<Feed>): Feed {
  return {
    id: 1, user_id: 1, category: '', type: 'rss', name: 'Existing Feed',
    url: 'https://example.com/rss', config: '{}', min_score: 0,
    last_fetched_at: null, status: 'active', extract_content: 0,
    last_error: '', last_error_at: null, last_error_severity: '',
    created_at: '2026-01-01T00:00:00Z', updated_at: '2026-01-01T00:00:00Z',
    ...overrides,
  }
}

function mountDialog(props: { feed?: Feed | null } = {}) {
  return mount(FeedDialog, {
    props: { feed: props.feed ?? null, 'onUpdate:open': vi.fn(), 'onSave': vi.fn(), 'onDelete': vi.fn() },
    global: { plugins: [createPinia()] },
    attachTo: document.body,
  })
}

// The dialog renders inside a reka-ui DialogPortal (teleported to document.body).
function getNameInput(): HTMLInputElement {
  return document.body.querySelector('input[placeholder="Feed name"]') as HTMLInputElement
}

function getUrlInput(): HTMLInputElement {
  return document.body.querySelector('input[placeholder="subreddit"], input[placeholder="username"], input[placeholder="Feed URL"]') as HTMLInputElement
}

beforeEach(() => {
  setActivePinia(createPinia())
  vi.clearAllMocks()
  // Clean up any leftover DialogPortal content from previous tests.
  document.body.innerHTML = ''
})

describe('FeedDialog name discovery', () => {
  it('does not call discover on mount', async () => {
    mountDialog()
    await flushPromises()
    expect(mockDiscover).not.toHaveBeenCalled()
  })

  it('does not call discover when editing an existing feed', async () => {
    mountDialog({ feed: makeFeed({ name: 'My Blog', type: 'rss', url: 'https://example.com/feed.xml' }) })
    await flushPromises()
    expect(mockDiscover).not.toHaveBeenCalled()
  })

  it('prefills name on blur for RSS when name is empty', async () => {
    mockDiscover.mockResolvedValue({ name: 'Herald & Times Feed' })
    const wrapper = mountDialog()
    await flushPromises()

    // Set the URL and blur the URL field.
    const urlInput = getUrlInput()
    urlInput.value = 'https://example.com/feed.xml'
    urlInput.dispatchEvent(new Event('input'))
    await flushPromises()

    urlInput.dispatchEvent(new Event('blur'))
    await flushPromises()
    await flushPromises()

    expect(mockDiscover).toHaveBeenCalledWith('rss', 'https://example.com/feed.xml')

    // Name should be prefilled.
    const nameInput = getNameInput()
    expect(nameInput.value).toBe('Herald & Times Feed')
    wrapper.unmount()
  })

  it('does not overwrite a name the user already typed', async () => {
    mockDiscover.mockResolvedValue({ name: 'Auto Name' })
    const wrapper = mountDialog()
    await flushPromises()

    const nameInput = getNameInput()
    nameInput.value = 'My Custom Name'
    nameInput.dispatchEvent(new Event('input'))
    await flushPromises()

    const urlInput = getUrlInput()
    urlInput.value = 'https://example.com/feed.xml'
    urlInput.dispatchEvent(new Event('input'))
    await flushPromises()

    urlInput.dispatchEvent(new Event('blur'))
    await flushPromises()
    await flushPromises()

    expect(nameInput.value).toBe('My Custom Name')
    wrapper.unmount()
  })

  it('overwrites an auto-filled name when the URL changes', async () => {
    mockDiscover
      .mockResolvedValueOnce({ name: 'First Feed' })
      .mockResolvedValueOnce({ name: 'Second Feed' })

    const wrapper = mountDialog()
    await flushPromises()

    const urlInput = getUrlInput()
    // First URL.
    urlInput.value = 'https://example.com/feed1.xml'
    urlInput.dispatchEvent(new Event('input'))
    await flushPromises()
    urlInput.dispatchEvent(new Event('blur'))
    await flushPromises()
    await flushPromises()

    expect(getNameInput().value).toBe('First Feed')

    // Change URL.
    urlInput.value = 'https://example.com/feed2.xml'
    urlInput.dispatchEvent(new Event('input'))
    await flushPromises()
    urlInput.dispatchEvent(new Event('blur'))
    await flushPromises()
    await flushPromises()

    expect(mockDiscover).toHaveBeenCalledTimes(2)
    expect(getNameInput().value).toBe('Second Feed')
    wrapper.unmount()
  })

  it('clears auto-filled name on type change', async () => {
    mockDiscover.mockResolvedValue({ name: 'Hacker News' })
    const wrapper = mountDialog()
    await flushPromises()

    // Select HN type — this triggers prefillName immediately.
    const typeSelect = document.body.querySelector('select') as HTMLSelectElement
    typeSelect.value = 'hn'
    typeSelect.dispatchEvent(new Event('change'))
    await flushPromises()
    await flushPromises()

    expect(getNameInput().value).toBe('Hacker News')

    // Switch to RSS — the auto-filled name should be cleared.
    typeSelect.value = 'rss'
    typeSelect.dispatchEvent(new Event('change'))
    await flushPromises()

    expect(getNameInput().value).toBe('')
    wrapper.unmount()
  })

  it('shows empty name when discover fails', async () => {
    mockDiscover.mockRejectedValue(new Error('network'))
    const wrapper = mountDialog()
    await flushPromises()

    const urlInput = getUrlInput()
    urlInput.value = 'https://example.com/feed.xml'
    urlInput.dispatchEvent(new Event('input'))
    await flushPromises()

    urlInput.dispatchEvent(new Event('blur'))
    await flushPromises()
    await flushPromises()

    expect(getNameInput().value).toBe('')
    wrapper.unmount()
  })

  it('shows empty name when discover returns empty', async () => {
    mockDiscover.mockResolvedValue({ name: '' })
    const wrapper = mountDialog()
    await flushPromises()

    const urlInput = getUrlInput()
    urlInput.value = 'https://example.com/feed.xml'
    urlInput.dispatchEvent(new Event('input'))
    await flushPromises()

    urlInput.dispatchEvent(new Event('blur'))
    await flushPromises()
    await flushPromises()

    expect(getNameInput().value).toBe('')
    wrapper.unmount()
  })
})
