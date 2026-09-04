import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import ItemList from './ItemList.vue'
import { useFeedStore } from '../stores/feed'
import type { Feed, Item } from '../types/api'

vi.mock('../services/api', () => ({
  feedApi: {
    list: vi.fn(),
    create: vi.fn(),
    update: vi.fn(),
    delete: vi.fn(),
    getItems: vi.fn().mockResolvedValue({ items: [], has_more: false }),
    fetch: vi.fn(),
    markRead: vi.fn(),
    unreadCounts: vi.fn().mockResolvedValue({}),
  },
  itemApi: { updateStatus: vi.fn() },
  ApiError: class extends Error {
    constructor(public status: number, body: any) { super(body?.error) }
  },
}))

// reka-ui's dropdown content measures itself with ResizeObserver, which jsdom
// does not provide.
class ResizeObserverStub {
  observe() {}
  unobserve() {}
  disconnect() {}
}
vi.stubGlobal('ResizeObserver', ResizeObserverStub)

// jsdom's matchMedia is non-writable; override with defineProperty.
Object.defineProperty(window, 'matchMedia', {
  writable: true,
  value: (query: string) => ({
    matches: /min-width/.test(query),
    media: query,
    onchange: null,
    addListener: () => {},
    removeListener: () => {},
    addEventListener: () => {},
    removeEventListener: () => {},
    dispatchEvent: () => false,
  }),
})
function makeFeed(overrides: Partial<Feed> = {}): Feed {
  return {
    id: 1,
    user_id: 1,
    category: 'Tech',
    type: 'hn',
    name: 'Hacker News',
    url: 'https://news.ycombinator.com/rss',
    config: '{}',
    min_score: 0,
    last_fetched_at: null,
    status: 'ok',
    extract_content: 0,
    last_error: '',
    last_error_at: null,
    last_error_severity: '',
    created_at: '2026-08-13T08:00:00Z',
    updated_at: '2026-08-13T08:00:00Z',
    ...overrides,
  }
}

function makeItem(overrides: Partial<Item> = {}): Item {
  return {
    id: 1,
    feed_id: 1,
    hash: 'h1',
    title: 'Item 1',
    url: 'https://example.com/1',
    content: '',
    author: '',
    comment_url: '',
    points: 0,
    comments_count: 0,
    published_at: '2026-08-13T08:00:00Z',
    status: 'unread',
    fetched_at: '2026-08-13T08:00:00Z',
    created_at: '2026-08-13T08:00:00Z',
    last_enriched_at: null,
    extract_content: '',
    extract_status: 'none',
    extract_retries: 0,
    extract_date: null,
    ...overrides,
  }
}

function mountList(opts: { savedItems?: Item[]; showingSaved?: boolean; feedType?: string; activeFeedId?: number | null } = {}) {
  const pinia = createPinia()
  setActivePinia(pinia)
  const store = useFeedStore()
  store.loading = false
  store.feeds = [makeFeed({ type: opts.feedType ?? 'hn' })]
  store.activeFeedId = opts.activeFeedId !== undefined ? opts.activeFeedId : 1
  store.items = [
    makeItem(),
    makeItem({ id: 2, title: 'Item 2', published_at: '2026-08-12T09:30:00Z' }),
  ]
  const wrapper = mount(ItemList, {
    props: {
      savedItems: opts.savedItems ?? [],
      showingSaved: opts.showingSaved ?? false,
    },
    global: { plugins: [pinia] },
  })
  return { store, wrapper }
}

// The dropdown content renders inside a reka-ui portal (teleported to
// document.body), so read the live DOM, not the wrapper.
function menuItems(): HTMLElement[] {
  return Array.from(document.body.querySelectorAll('[role="menuitem"]')) as HTMLElement[]
}

// The list rows are divs with role="button" and the w-full row class; the
// filter and sort triggers are compact ghost buttons.
function resultRows(wrapper: ReturnType<typeof mount>) {
  return wrapper.findAll('[role="button"]').filter(b => b.classes().includes('w-full'))
}

function isDisabled(item: HTMLElement): boolean {
  return item.getAttribute('aria-disabled') === 'true' || item.hasAttribute('data-disabled')
}

async function openMenu(wrapper: ReturnType<typeof mount>, label: 'Filter items' | 'Sort items') {
  await wrapper.find(`button[aria-label="${label}"]`).trigger('click')
  await flushPromises()
}

async function closeMenu() {
  document.body.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }))
  await flushPromises()
}

async function clickItem(label: string) {
  const item = menuItems().find(i => i.textContent?.includes(label))
  if (!item) throw new Error(`menu item not found: ${label}`)
  item.dispatchEvent(new MouseEvent('click', { bubbles: true }))
  await flushPromises()
}

async function minScoreInput(): Promise<HTMLInputElement> {
  const input = document.body.querySelector('input[aria-label="Minimum score"]') as HTMLInputElement | null
  if (!input) throw new Error('min-score input not found')
  return input
}

describe('ItemList filter/sort controls', () => {
  beforeEach(() => {
    document.body.innerHTML = ''
    setActivePinia(createPinia())
    vi.clearAllMocks()
    localStorage.clear()
  })

  it('renders filter and sort labels with defaults', () => {
    const { wrapper } = mountList()
    expect(wrapper.html()).toContain('All')
    expect(wrapper.html()).toContain('Newest')
  })

  it('filter dropdown lists the five statuses, checks the active one, and shows an enabled min-score input for a score feed', async () => {
    const { wrapper } = mountList()
    await openMenu(wrapper, 'Filter items')

    const labels = menuItems().map(i => i.textContent?.trim())
    expect(labels).toEqual(['All', 'Unread', 'Read', 'Digested', 'Saved'])
    // Default filter is All, so it is the only item carrying the check icon.
    const checked = menuItems().filter(i => i.querySelector('svg'))
    expect(checked.map(i => i.textContent?.trim())).toEqual(['All'])
    // Min-score input is present and enabled for a score-capable feed (hn).
    const input = await minScoreInput()
    expect(input.disabled).toBe(false)
  })

  it('filters server-side when a status is selected', async () => {
    const { wrapper } = mountList()
    await openMenu(wrapper, 'Filter items')
    await clickItem('Unread')

    const { feedApi } = await import('../services/api')
    const getItems = feedApi.getItems as ReturnType<typeof vi.fn>
    expect(getItems).toHaveBeenCalledWith(1, expect.objectContaining({ status: 'unread' }))
    expect(wrapper.html()).toContain('Unread')
  })

  it('sort dropdown offers date and score options with the active one checked', async () => {
    const { wrapper } = mountList()
    await openMenu(wrapper, 'Sort items')

    const labels = menuItems().map(i => i.textContent?.trim())
    expect(labels).toEqual(['Newest', 'Oldest', 'Highest score', 'Lowest score'])
    // Default sort is Newest first.
    const checked = menuItems().filter(i => i.querySelector('svg'))
    expect(checked.map(i => i.textContent?.trim())).toEqual(['Newest'])
    // Score options are enabled for a score-capable feed (hn).
    const disabled = menuItems().filter(isDisabled)
    expect(disabled).toHaveLength(0)
  })

  it('sorts server-side when a sort is selected', async () => {
    const { wrapper } = mountList()
    await openMenu(wrapper, 'Sort items')
    await clickItem('Oldest')

    const { feedApi } = await import('../services/api')
    expect(feedApi.getItems).toHaveBeenCalledWith(1, expect.objectContaining({ sort: 'date_asc' }))
    expect(wrapper.html()).toContain('Oldest')
  })

  it('applies the min-score threshold from the input and shows it in the label', async () => {
    const { wrapper } = mountList()
    await openMenu(wrapper, 'Filter items')

    const input = await minScoreInput()
    input.value = '50'
    input.dispatchEvent(new Event('change', { bubbles: true }))
    await flushPromises()

    const { feedApi } = await import('../services/api')
    expect(feedApi.getItems).toHaveBeenCalledWith(1, expect.objectContaining({ min_score: 50 }))
    expect(wrapper.html()).toContain('All ≥50')
  })

  it('clears the min-score threshold with the reset button', async () => {
    const { store, wrapper } = mountList()
    store.minScore = 50
    await openMenu(wrapper, 'Filter items')

    // The reset button is the one with title "Reset to 0".
    const clearBtn = Array.from(document.body.querySelectorAll('button')).find(b => b.getAttribute('title') === 'Reset to 0')
    expect(clearBtn).toBeDefined()
    clearBtn!.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    await flushPromises()

    const { feedApi } = await import('../services/api')
    expect(feedApi.getItems).toHaveBeenCalledWith(1, expect.objectContaining({ min_score: undefined }))
    expect(store.minScore).toBeNull()
    expect(wrapper.html()).not.toContain('≥')
  })

  it('greys out the min-score input for non-score feeds', async () => {
    const { wrapper } = mountList({ feedType: 'rss' })
    await openMenu(wrapper, 'Filter items')
    const input = await minScoreInput()
    expect(input.disabled).toBe(true)
  })

  it('greys out and disables score sort items for non-score feeds', async () => {
    const { wrapper } = mountList({ feedType: 'rss' })
    await openMenu(wrapper, 'Sort items')

    const scoreItems = menuItems().filter(i => ['Highest score', 'Lowest score'].includes(i.textContent?.trim() ?? ''))
    expect(scoreItems).toHaveLength(2)
    scoreItems.forEach(i => expect(isDisabled(i)).toBe(true))
    const dateItems = menuItems().filter(i => ['Newest', 'Oldest'].includes(i.textContent?.trim() ?? ''))
    dateItems.forEach(i => expect(isDisabled(i)).toBe(false))

    // Clicking a disabled score item must not change the sort.
    await clickItem('Highest score')
    const { feedApi } = await import('../services/api')
    expect(feedApi.getItems).not.toHaveBeenCalledWith(1, expect.objectContaining({ sort: 'score_desc' }))
  })

  it('does not show the score part of the filter label when scores are unsupported', () => {
    const { store, wrapper } = mountList({ feedType: 'rss' })
    store.minScore = 50
    expect(wrapper.html()).toContain('All')
    expect(wrapper.html()).not.toContain('≥')
  })

  describe('saved view', () => {
    it('greys out the min-score input and score sort items', async () => {
      const { wrapper } = mountList({ showingSaved: true, savedItems: [makeItem()], activeFeedId: null })
      await openMenu(wrapper, 'Filter items')
      const input = await minScoreInput()
      expect(input.disabled).toBe(true)

      await closeMenu()
      await openMenu(wrapper, 'Sort items')
      const scoreItems = menuItems().filter(i => ['Highest score', 'Lowest score'].includes(i.textContent?.trim() ?? ''))
      expect(scoreItems).toHaveLength(2)
      scoreItems.forEach(i => expect(isDisabled(i)).toBe(true))
    })

    it('sorts saved items client-side by date', async () => {
      const saved = [
        makeItem({ id: 1, title: 'Older', published_at: '2026-08-10T08:00:00Z' }),
        makeItem({ id: 2, title: 'Newer', published_at: '2026-08-14T08:00:00Z' }),
      ]
      const { wrapper } = mountList({ showingSaved: true, savedItems: saved, activeFeedId: null })

      // Default sort: newest first.
      let rows = resultRows(wrapper)
      expect(rows[0].text()).toContain('Newer')
      expect(rows[1].text()).toContain('Older')

      await openMenu(wrapper, 'Sort items')
      await clickItem('Oldest')
      rows = resultRows(wrapper)
      expect(rows[0].text()).toContain('Older')
      expect(rows[1].text()).toContain('Newer')
    })

    it('filters saved items client-side by status', async () => {
      const saved = [
        makeItem({ id: 1, title: 'Saved one', status: 'saved' }),
        makeItem({ id: 2, title: 'Saved two', status: 'saved' }),
      ]
      const { wrapper } = mountList({ showingSaved: true, savedItems: saved, activeFeedId: null })
      expect(resultRows(wrapper)).toHaveLength(2)

      await openMenu(wrapper, 'Filter items')
      await clickItem('Saved')
      expect(resultRows(wrapper)).toHaveLength(2)
    })
  })
})
