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
  itemApi: { updateStatus: vi.fn(), markReadAbove: vi.fn(), markReadBelow: vi.fn() },
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

function mountList(opts: { savedItems?: Item[]; items?: Item[]; showingSaved?: boolean; feedType?: string; activeFeedId?: number | null } = {}) {
  const pinia = createPinia()
  setActivePinia(pinia)
  const store = useFeedStore()
  store.loading = false
  store.feeds = [makeFeed({ type: opts.feedType ?? 'hn' })]
  store.activeFeedId = opts.activeFeedId !== undefined ? opts.activeFeedId : 1
  store.items = opts.items ?? [
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

  describe('mark read above/below hover actions', () => {
    // The action lives in the top right corner of every row: the publish time
    // gives way to the two chevrons on hover. Rows of any status get them, so
    // the control is in the same place on every item.
    it('renders both chevrons on every row regardless of status', () => {
      const { wrapper } = mountList({
        items: [
          makeItem({ id: 1, title: 'Unread', status: 'unread' }),
          makeItem({ id: 2, title: 'Read', status: 'read' }),
          makeItem({ id: 3, title: 'Saved', status: 'saved' }),
          makeItem({ id: 4, title: 'Digested', status: 'digested' }),
        ],
      })
      const rows = resultRows(wrapper)
      expect(rows).toHaveLength(4)
      for (const row of rows) {
        expect(row.find('[data-mark-above]').exists()).toBe(true)
        expect(row.find('[data-mark-below]').exists()).toBe(true)
      }
    })

    // Both chevrons exclude the row being hovered, so the labels say so.
    it('labels the chevrons for assistive tech, noting the item is kept', () => {
      const { wrapper } = mountList()
      const row = resultRows(wrapper)[0]
      expect(row.find('[data-mark-above]').attributes('aria-label')).toBe('Mark everything above as read, except this item')
      expect(row.find('[data-mark-below]').attributes('aria-label')).toBe('Mark everything below as read, except this item')
      expect(row.find('[data-mark-above]').attributes('title')).toContain('except this item')
      expect(row.find('[data-mark-below]').attributes('title')).toContain('except this item')
    })

    it('still shows the publish time, so the timestamp is not lost', () => {
      const { wrapper } = mountList()
      const row = resultRows(wrapper)[0]
      // Hovering only hides the time with CSS; it stays in the DOM so the
      // relative time is still announced and the absolute time stays available.
      const time = row.find('[data-item-time]')
      expect(time.exists()).toBe(true)
      expect(time.text().length).toBeGreaterThan(0)
      expect(time.attributes('title')).toBeTruthy()
    })

    // The pane's sort and score filter are forwarded to the server, which
    // resolves above/below in SQL so rows that are not loaded yet, or are
    // filtered out, still land on the right side of the boundary.
    it('marks above with the clicked item and forwards the pane ordering', async () => {
      const { itemApi } = await import('../services/api')
      const markAbove = itemApi.markReadAbove as ReturnType<typeof vi.fn>
      markAbove.mockResolvedValue({ message: 'ok' })

      const { store, wrapper } = mountList()
      store.sort = 'score_desc'
      store.minScore = 40

      await resultRows(wrapper)[1].find('[data-mark-above]').trigger('click')
      await flushPromises()

      expect(markAbove).toHaveBeenCalledTimes(1)
      const [id, opts] = markAbove.mock.calls[0]
      expect(id).toBe(2)
      expect(opts).toMatchObject({ sort: 'score_desc', min_score: 40 })
    })

    it('marks below with the clicked item and forwards the pane ordering', async () => {
      const { itemApi } = await import('../services/api')
      const markBelow = itemApi.markReadBelow as ReturnType<typeof vi.fn>
      markBelow.mockResolvedValue({ message: 'ok' })

      const { store, wrapper } = mountList()
      store.sort = 'date_asc'
      store.minScore = null

      await resultRows(wrapper)[0].find('[data-mark-below]').trigger('click')
      await flushPromises()

      expect(markBelow).toHaveBeenCalledTimes(1)
      const [id, opts] = markBelow.mock.calls[0]
      expect(id).toBe(1)
      expect(opts).toMatchObject({ sort: 'date_asc', min_score: 0 })
    })

    it('does not select the row when a chevron is clicked', async () => {
      const { wrapper } = mountList()
      const row = resultRows(wrapper)[0]
      await row.find('[data-mark-above]').trigger('click')
      expect(wrapper.emitted('selectItem')).toBeFalsy()
    })

    it('hides the chevrons in the saved view', () => {
      const saved = [makeItem({ id: 1, title: 'Saved one', status: 'saved' })]
      const { wrapper } = mountList({ showingSaved: true, savedItems: saved, activeFeedId: null })
      const row = resultRows(wrapper)[0]
      expect(row.find('[data-mark-above]').exists()).toBe(false)
      expect(row.find('[data-mark-below]').exists()).toBe(false)
    })
  })
})

// Touch devices have no hover, so the row's mark-above/below chevrons are
// revealed by swiping the row leftward instead. See useRowSwipeReveal.
describe('ItemList swipe-to-reveal row actions', () => {
  beforeEach(() => {
    document.body.innerHTML = ''
    setActivePinia(createPinia())
    vi.clearAllMocks()
    localStorage.clear()
  })

  /** Swipe a row leftward by `dx` px (negative = left). */
  async function swipe(row: ReturnType<typeof resultRows>[number], dx: number, dy = 0) {
    await row.trigger('touchstart', { touches: [{ clientX: 300, clientY: 100 }] })
    await row.trigger('touchmove', { touches: [{ clientX: 300 + dx, clientY: 100 + dy }] })
    await row.trigger('touchend', { changedTouches: [{ clientX: 300 + dx, clientY: 100 + dy }] })
    await flushPromises()
  }

  it('keeps the chevrons hidden until the row is swiped', () => {
    const { wrapper } = mountList()
    const actions = resultRows(wrapper)[0].find('[data-mark-actions]')
    expect(actions.exists()).toBe(true)
    expect(actions.classes()).toContain('hidden')
    expect(actions.classes()).not.toContain('flex')
  })

  it('reveals the chevrons on a leftward swipe, in place of the timestamp', async () => {
    const { wrapper } = mountList()
    const rows = resultRows(wrapper)
    await swipe(rows[1], -60)

    expect(rows[1].element.hasAttribute('data-revealed')).toBe(true)
    expect(rows[1].find('[data-mark-actions]').classes()).toContain('flex')
    expect(rows[1].find('[data-item-time]').classes()).toContain('hidden')
    // Only the swiped row opens.
    expect(rows[0].element.hasAttribute('data-revealed')).toBe(false)
    expect(rows[0].find('[data-mark-actions]').classes()).toContain('hidden')
  })

  it('does not reveal on a short swipe or a vertical drag', async () => {
    const { wrapper } = mountList()
    const rows = resultRows(wrapper)

    await swipe(rows[0], -10)
    expect(rows[0].element.hasAttribute('data-revealed')).toBe(false)

    // 60px left but 50px down: not clearly horizontal, so the list keeps scrolling.
    await swipe(rows[0], -60, 50)
    expect(rows[0].element.hasAttribute('data-revealed')).toBe(false)
  })

  it('does not open the item when the revealing swipe ends', async () => {
    const { wrapper } = mountList()
    await swipe(resultRows(wrapper)[0], -60)

    // The browser fires a click after the drag; it must not select the row.
    await resultRows(wrapper)[0].trigger('click')
    await flushPromises()

    expect(wrapper.emitted('selectItem')).toBeFalsy()
  })

  it('hides the reveal again when the revealed row is swiped left once more', async () => {
    const { wrapper } = mountList()
    const row = resultRows(wrapper)[0]

    await swipe(row, -60)
    expect(row.element.hasAttribute('data-revealed')).toBe(true)

    await swipe(row, -60)
    expect(row.element.hasAttribute('data-revealed')).toBe(false)
  })

  it('dismisses the reveal when the list scrolls', async () => {
    const { wrapper } = mountList()
    const row = resultRows(wrapper)[0]
    await swipe(row, -60)
    expect(row.element.hasAttribute('data-revealed')).toBe(true)

    await wrapper.find('.overflow-y-auto').trigger('scroll')
    await flushPromises()

    expect(row.element.hasAttribute('data-revealed')).toBe(false)
  })

  it('closes the reveal after a chevron is used', async () => {
    const { itemApi } = await import('../services/api')
    ;(itemApi.markReadAbove as ReturnType<typeof vi.fn>).mockResolvedValue({ message: 'ok' })

    const { wrapper } = mountList()
    const row = resultRows(wrapper)[0]
    await swipe(row, -60)

    await row.find('[data-mark-above]').trigger('click')
    await flushPromises()

    expect(row.element.hasAttribute('data-revealed')).toBe(false)
    expect(itemApi.markReadAbove).toHaveBeenCalled()
  })

  it('still selects the row on a plain tap while nothing is revealed', async () => {
    const { wrapper } = mountList()
    await resultRows(wrapper)[0].trigger('click')
    await flushPromises()

    expect(wrapper.emitted('selectItem')).toBeTruthy()
  })
})
