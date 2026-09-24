import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import ItemList from './ItemList.vue'
import { useFeedStore } from '../stores/feed'
import { itemApi } from '../services/api'
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

function mountList(opts: { savedItems?: Item[]; items?: Item[]; showingSaved?: boolean; feedType?: string; activeFeedId?: number | null; compact?: boolean } = {}) {
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
  // The Saved view's total now comes from the server (filter-aware), so seed it
  // alongside the rows: the header reports loaded/total for the filtered set.
  store.savedTotal = (opts.savedItems ?? []).length
  const wrapper = mount(ItemList, {
    props: {
      savedItems: opts.savedItems ?? [],
      showingSaved: opts.showingSaved ?? false,
      ...(opts.compact !== undefined ? { compact: opts.compact } : {}),
    },
    global: { plugins: [pinia] },
    // Pull-to-refresh binds its listeners on `document`, so a detached mount
    // never sees a touch event. Attaching is what makes the gesture testable.
    attachTo: document.body,
  })
  return { store, wrapper }
}

// Minimal TouchEvent harness: jsdom has no TouchEvent, so dispatch plain Events
// with a `touches` array. The pull-to-refresh composable listens at document
// level, so bubbling is required.
function touch(el: Element, type: string, x: number, y: number) {
  const ev = new Event(type, { cancelable: true, bubbles: true }) as any
  ev.touches = [{ clientX: x, clientY: y }]
  el.dispatchEvent(ev)
  return ev
}

// Perform a full pull-down gesture on the list: down 200px (past the 70px
// threshold after the 2x damping) and release.
async function pullToRefresh(wrapper: ReturnType<typeof mount>) {
  const row = resultRows(wrapper)[0]?.element ?? wrapper.element
  touch(row, 'touchstart', 100, 100)
  touch(row, 'touchmove', 100, 300)
  touch(row, 'touchend', 100, 300)
  await flushPromises()
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

    it('renders saved items in server order (newest first)', async () => {
      const saved = [
        makeItem({ id: 1, title: 'Newer', published_at: '2026-08-14T08:00:00Z' }),
        makeItem({ id: 2, title: 'Older', published_at: '2026-08-10T08:00:00Z' }),
      ]
      const { wrapper } = mountList({ showingSaved: true, savedItems: saved, activeFeedId: null })

      // GET /api/saved returns newest-first; the view renders that order as-is.
      const rows = resultRows(wrapper)
      expect(rows[0].text()).toContain('Newer')
      expect(rows[1].text()).toContain('Older')
    })

    it('renders every row the server returned, without re-filtering', async () => {
      // The Saved view's read-state filter is applied SERVER-side (the list is
      // paginated, so a client-side filter would only see loaded pages). What the
      // component must do is render exactly what it was given.
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


// Touch devices have no hover, so on a phone the mark-above/below action is
// reached by swiping the row: right-to-left opens "mark all read below",
// left-to-right opens "mark all read above", and tapping the panel confirms.
// See useSwipeAction.
describe('ItemList swipe action panels', () => {
  beforeEach(() => {
    document.body.innerHTML = ''
    setActivePinia(createPinia())
    vi.clearAllMocks()
    localStorage.clear()
  })

  /** One full swipe gesture over `row`, starting at x=300 and moving by `dx`. */
  async function swipe(row: ReturnType<typeof resultRows>[number], dx: number, dy = 0) {
    await row.trigger('touchstart', { touches: [{ clientX: 300, clientY: 100 }] })
    await row.trigger('touchmove', { touches: [{ clientX: 300 + dx, clientY: 100 + dy }] })
    await row.trigger('touchend', { changedTouches: [{ clientX: 300 + dx, clientY: 100 + dy }] })
    await flushPromises()
  }

  it('keeps both panels parked off-screen until the row is swiped', () => {
    const { wrapper } = mountList()
    const row = resultRows(wrapper)[0]

    expect(row.element.hasAttribute('data-swipe-action')).toBe(false)
    expect(row.find('[data-swipe-below]').classes()).toContain('translate-x-full')
    expect(row.find('[data-swipe-above]').classes()).toContain('-translate-x-full')
    // Parked panels stay out of the tab order.
    expect(row.find('[data-swipe-below]').attributes('tabindex')).toBe('-1')
  })

  it('opens the "below" panel on a right-to-left swipe and slides it in from the right', async () => {
    const { wrapper } = mountList()
    const row = resultRows(wrapper)[0]
    await swipe(row, -60)

    expect(row.attributes('data-swipe-action')).toBe('below')
    expect(row.find('[data-swipe-below]').classes()).toContain('translate-x-0')
    expect(row.find('[data-swipe-below]').classes()).toContain('right-0')
    expect(row.find('[data-swipe-below]').text()).toContain('Mark below read')
    expect(row.find('[data-swipe-above]').classes()).toContain('-translate-x-full')
  })

  it('opens the "above" panel on a left-to-right swipe and slides it in from the left', async () => {
    const { wrapper } = mountList()
    const row = resultRows(wrapper)[0]
    await swipe(row, 60)

    expect(row.attributes('data-swipe-action')).toBe('above')
    expect(row.find('[data-swipe-above]').classes()).toContain('translate-x-0')
    expect(row.find('[data-swipe-above]').classes()).toContain('left-0')
    expect(row.find('[data-swipe-above]').text()).toContain('Mark above read')
  })

  it('shifts the row content aside by the panel width while open', async () => {
    const { wrapper } = mountList()
    const row = resultRows(wrapper)[0]
    const content = row.find('[data-row-content]')

    expect(content.classes()).not.toContain('-translate-x-36')
    await swipe(row, -60)
    expect(content.classes()).toContain('-translate-x-36')
  })

  it('leaves the panel open after the swipe, and does not commit', async () => {
    const { itemApi } = await import('../services/api')
    const { wrapper } = mountList()
    const row = resultRows(wrapper)[0]
    await swipe(row, -60)

    // Two-step by design: the gesture only opens the panel.
    expect(row.attributes('data-swipe-action')).toBe('below')
    expect(itemApi.markReadBelow).not.toHaveBeenCalled()
    expect(itemApi.markReadAbove).not.toHaveBeenCalled()
  })

  it('does not open on a short swipe or a vertical drag', async () => {
    const { wrapper } = mountList()
    const row = resultRows(wrapper)[0]

    await swipe(row, -10)
    expect(row.element.hasAttribute('data-swipe-action')).toBe(false)

    // 60px left but 50px down: not clearly horizontal, so the list keeps scrolling.
    await swipe(row, -60, 50)
    expect(row.element.hasAttribute('data-swipe-action')).toBe(false)
  })

  it('does not open the item when the swipe ends', async () => {
    const { wrapper } = mountList()
    const row = resultRows(wrapper)[0]
    await swipe(row, -60)

    // The browser fires a click when the drag ends; it must not select the row.
    await row.trigger('click')
    await flushPromises()

    expect(wrapper.emitted('selectItem')).toBeFalsy()
    expect(row.attributes('data-swipe-action')).toBe('below')
  })

  it('marks everything below when the "below" panel is tapped', async () => {
    const { itemApi } = await import('../services/api')
    const markBelow = itemApi.markReadBelow as ReturnType<typeof vi.fn>
    const markAbove = itemApi.markReadAbove as ReturnType<typeof vi.fn>
    markBelow.mockResolvedValue({ message: 'ok' })
    markAbove.mockResolvedValue({ message: 'ok' })

    const { wrapper } = mountList()
    const row = resultRows(wrapper)[1]
    await swipe(row, -60)
    await row.find('[data-swipe-below]').trigger('click')
    await flushPromises()

    expect(markBelow).toHaveBeenCalledTimes(1)
    expect(markAbove).not.toHaveBeenCalled()
    expect(markBelow.mock.calls[0][0]).toBe(2)
    // The panel closes once it has been used.
    expect(row.element.hasAttribute('data-swipe-action')).toBe(false)
  })

  it('marks everything above when the "above" panel is tapped', async () => {
    const { itemApi } = await import('../services/api')
    const markAbove = itemApi.markReadAbove as ReturnType<typeof vi.fn>
    const markBelow = itemApi.markReadBelow as ReturnType<typeof vi.fn>
    markAbove.mockResolvedValue({ message: 'ok' })
    markBelow.mockResolvedValue({ message: 'ok' })

    const { wrapper } = mountList()
    const row = resultRows(wrapper)[0]
    await swipe(row, 60)
    await row.find('[data-swipe-above]').trigger('click')
    await flushPromises()

    expect(markAbove).toHaveBeenCalledTimes(1)
    expect(markBelow).not.toHaveBeenCalled()
  })

  it('forwards the pane ordering with the confirmation', async () => {
    const { itemApi } = await import('../services/api')
    const markBelow = itemApi.markReadBelow as ReturnType<typeof vi.fn>
    markBelow.mockResolvedValue({ message: 'ok' })

    const { store, wrapper } = mountList()
    store.sort = 'score_desc'
    store.minScore = 40

    const row = resultRows(wrapper)[0]
    await swipe(row, -60)
    await row.find('[data-swipe-below]').trigger('click')
    await flushPromises()

    expect(markBelow.mock.calls[0][1]).toMatchObject({ sort: 'score_desc', min_score: 40 })
  })

  it('closes the panel when the row is tapped instead of the panel', async () => {
    const { wrapper } = mountList()
    const row = resultRows(wrapper)[0]
    await swipe(row, -60)
    expect(row.attributes('data-swipe-action')).toBe('below')

    // First click is the browser's synthetic one, fired as the drag ended —
    // it must be swallowed or the panel would close the instant it opened.
    await row.trigger('click')
    await flushPromises()
    expect(row.attributes('data-swipe-action')).toBe('below')

    // The next tap is a real one: it dismisses the panel.
    await row.trigger('click')
    await flushPromises()

    expect(row.element.hasAttribute('data-swipe-action')).toBe(false)
    // A dismissing tap must not also open the item.
    expect(wrapper.emitted('selectItem')).toBeFalsy()
  })

  it('toggles the panel away when the same row is swiped the same way again', async () => {
    const { wrapper } = mountList()
    const row = resultRows(wrapper)[0]
    await swipe(row, -60)
    expect(row.attributes('data-swipe-action')).toBe('below')

    await swipe(row, -60)
    expect(row.element.hasAttribute('data-swipe-action')).toBe(false)
  })

  it('switches panels when the row is swiped the other way', async () => {
    const { wrapper } = mountList()
    const row = resultRows(wrapper)[0]
    await swipe(row, -60)
    expect(row.attributes('data-swipe-action')).toBe('below')

    await swipe(row, 60)
    expect(row.attributes('data-swipe-action')).toBe('above')
  })

  it('keeps only one row open at a time', async () => {
    const { wrapper } = mountList()
    const rows = resultRows(wrapper)
    await swipe(rows[0], -60)
    await swipe(rows[1], -60)

    expect(rows[1].attributes('data-swipe-action')).toBe('below')
    expect(rows[0].element.hasAttribute('data-swipe-action')).toBe(false)
  })

  it('closes the panel when the list scrolls', async () => {
    const { wrapper } = mountList()
    const row = resultRows(wrapper)[0]
    await swipe(row, -60)
    expect(row.attributes('data-swipe-action')).toBe('below')

    await wrapper.find('.overflow-y-auto').trigger('scroll')
    await flushPromises()

    expect(row.element.hasAttribute('data-swipe-action')).toBe(false)
  })

  it('has no swipe panels in the saved view', () => {
    const saved = [makeItem({ id: 1, title: 'Saved one', status: 'saved' })]
    const { wrapper } = mountList({ showingSaved: true, savedItems: saved, activeFeedId: null })
    const row = resultRows(wrapper)[0]

    expect(row.find('[data-swipe-below]').exists()).toBe(false)
    expect(row.find('[data-swipe-above]').exists()).toBe(false)
  })

  it('still selects the row on a plain tap while no panel is open', async () => {
    const { wrapper } = mountList()
    await resultRows(wrapper)[0].trigger('click')
    await flushPromises()

    expect(wrapper.emitted('selectItem')).toBeTruthy()
  })
})

describe('ItemList compact rows (stacked layout)', () => {
  beforeEach(() => {
    document.body.innerHTML = ''
    setActivePinia(createPinia())
    vi.clearAllMocks()
    localStorage.clear()
  })

  it('uses the compact row height and drops the two-line block', () => {
    const { wrapper } = mountList({ compact: true })
    const row = resultRows(wrapper)[0]

    expect(row.classes()).toContain('h-7')
    expect(row.classes()).not.toContain('h-20')
    // The two-line variant's wrapper must be gone entirely.
    expect(row.find('.line-clamp-2').exists()).toBe(false)
  })

  it('puts the title and the excerpt on the SAME line (adjacent siblings)', () => {
    const { wrapper } = mountList({ compact: true })
    const row = resultRows(wrapper)[0]
    const title = row.find('[data-item-title]')
    const excerpt = row.find('[data-item-excerpt]')

    expect(title.exists()).toBe(true)
    expect(excerpt.exists()).toBe(true)
    // jsdom has no layout engine, so shared parentage with both nodes flex
    // siblings is the assertion that actually encodes "on one line" — the
    // regression this guards against is the excerpt wrapping to its own row.
    expect(title.element.parentElement).toBe(excerpt.element.parentElement)
    expect(title.element.parentElement!.className).toContain('flex')
    expect(title.element.parentElement!.className).toContain('items-center')
  })

  it('keeps the feed name inline in a multi-feed view (source-label rule)', async () => {
    const { wrapper, store } = mountList({ compact: true })
    store.activeFeedId = null
    store.activeSection = 'feeds'
    store.multiItems = [
      { ...makeItem({ id: 5, title: 'From a feed' }), feed_name: 'Hacker News', feed_type: 'hn' },
    ] as never
    await flushPromises()

    const row = resultRows(wrapper)[0]
    const title = row.find('[data-item-title]')
    const rowText = row.text()
    expect(rowText).toContain('Hacker News')
    expect(rowText).toContain('From a feed')
    // Still one line: the feed label sits in the same flex line as the title.
    expect(title.element.parentElement!.textContent).toContain('Hacker News')
  })

  it('keeps the publish time and the hover mark-above/below actions', () => {
    const { wrapper } = mountList({ compact: true })
    const row = resultRows(wrapper)[0]

    expect(row.find('[data-item-time]').exists()).toBe(true)
    expect(row.find('[data-mark-actions]').exists()).toBe(true)
    expect(row.find('[data-mark-above]').exists()).toBe(true)
    expect(row.find('[data-mark-below]').exists()).toBe(true)
  })

  // The stacked view showed no score or comment count at all, so the two
  // layouts disagreed about what a row says. They now sit left of the
  // timestamp.
  it('shows the score and the comment count to the LEFT of the timestamp', () => {
    const { wrapper } = mountList({
      compact: true,
      items: [makeItem({ id: 1, points: 42, comments_count: 7 })],
    })
    const row = resultRows(wrapper)[0]
    const meta = row.find('[data-item-meta]')

    expect(meta.exists()).toBe(true)
    expect(meta.find('[data-item-score]').text()).toContain('42')
    expect(meta.find('[data-item-comments]').text()).toContain('7')

    // Order inside the cluster: score, comments, time. Comparing DOM order is
    // the jsdom-safe way to encode "to the left of the timestamp".
    const html = meta.html()
    expect(html.indexOf('data-item-score')).toBeLessThan(html.indexOf('data-item-comments'))
    expect(html.indexOf('data-item-comments')).toBeLessThan(html.indexOf('data-item-time'))
  })

  it('hides the whole right-hand cluster behind the chevrons on hover', () => {
    const { wrapper } = mountList({
      compact: true,
      items: [makeItem({ id: 1, points: 42, comments_count: 7 })],
    })
    const row = resultRows(wrapper)[0]
    const meta = row.find('[data-item-meta]')
    const actions = row.find('[data-mark-actions]')

    // Hovering only hides the cluster with CSS; score, comments and time stay
    // in the DOM, so the tooltips and the absolute time remain available.
    expect(meta.classes()).toContain('group-hover:hidden')
    expect(actions.classes()).toContain('hidden')
    expect(actions.classes()).toContain('group-hover:flex')
    expect(meta.find('[data-item-time]').attributes('title')).toBeTruthy()
  })

  it('omits the score and comments when the feed does not report them', () => {
    const { wrapper } = mountList({
      compact: true,
      items: [makeItem({ id: 1, points: 0, comments_count: 0, comment_url: '' })],
    })
    const row = resultRows(wrapper)[0]

    expect(row.find('[data-item-score]').exists()).toBe(false)
    expect(row.find('[data-item-comments]').exists()).toBe(false)
    expect(row.find('[data-item-time]').exists()).toBe(true)
  })

  it('still marks above/below from the compact row', async () => {
    const { wrapper } = mountList({ compact: true })
    await resultRows(wrapper)[0].find('[data-mark-above]').trigger('click')
    await flushPromises()

    expect(itemApi.markReadAbove).toHaveBeenCalled()
  })

  it('leaves the default (non-compact) row untouched', () => {
    const { wrapper } = mountList()
    const row = resultRows(wrapper)[0]

    expect(row.classes()).toContain('h-20')
    expect(row.classes()).not.toContain('h-7')
    expect(row.find('.line-clamp-2').exists()).toBe(true)
    expect(row.find('[data-item-title]').exists()).toBe(false)
  })

  // The inner line is `h-full items-center`, so the wrapper between the row and
  // that line must itself fill the row — otherwise the line falls back to its
  // content height and the row's text sits above the row's vertical centre. The
  // defect is invisible to jsdom (no layout engine), so this pins the STRUCTURE
  // that makes centring possible and the browser pins the resulting geometry.
  it('makes the row-content wrapper fill the row so the line can centre', () => {
    const { wrapper } = mountList({ compact: true })
    const content = resultRows(wrapper)[0].find('[data-row-content]')

    expect(content.classes()).toContain('h-full')
  })

  it('does not force the wrapper to fill a padded, top-aligned row', () => {
    const { wrapper } = mountList()
    const content = resultRows(wrapper)[0].find('[data-row-content]')

    expect(content.classes()).not.toContain('h-full')
  })
})

// The middle pane's header reports "feed name x/y": x is what the lazy loader has
// actually appended, y is the server's count for the CURRENT filter. The two must
// come from one result set, so these assert against the store fields the server
// fills (items.length / totalItems) and not against a row count.
//
// Every mutation is followed by a tick: the header reads store state through a
// computed, so a synchronous DOM read straight after assigning `totalItems`
// returns the PREVIOUS render (0/0) and reads as a broken component.
describe('middle pane header counts', () => {
  function headerCount(wrapper: ReturnType<typeof mount>): string {
    return wrapper.find('[data-filter-count]').text()
  }

  it('shows loaded out of the filter-aware total, not the loaded length', async () => {
    const { store, wrapper } = mountList({ items: [makeItem(), makeItem({ id: 2 })] })
    store.totalItems = 566
    await wrapper.vm.$nextTick()

    // Two rows loaded of 566 matching: the old header claimed "566 items" while
    // showing two, which is the pair this fixes.
    expect(headerCount(wrapper)).toBe('2/566')
  })

  it('updates x as pages are appended and y when the filter changes', async () => {
    const { store, wrapper } = mountList({ items: [makeItem()] })
    store.totalItems = 566
    await wrapper.vm.$nextTick()
    expect(headerCount(wrapper)).toBe('1/566')

    // Infinite scroll appends.
    store.items = [...store.items, makeItem({ id: 2 }), makeItem({ id: 3 })]
    await wrapper.vm.$nextTick()
    expect(headerCount(wrapper)).toBe('3/566')

    // Changing filter re-counts on the server: fewer total, list reset to page 1.
    store.totalItems = 55
    store.items = [makeItem({ id: 4 })]
    await wrapper.vm.$nextTick()
    expect(headerCount(wrapper)).toBe('1/55')
  })

  it('names the feed alongside the counts in every layout', () => {
    const { wrapper } = mountList()
    expect(wrapper.find('[data-filter-feed-name]').text()).toBe('Hacker News')
    // Not hidden at desktop width: the pane is narrow there too.
    expect(wrapper.find('[data-filter-feed-name]').classes()).not.toContain('lg:hidden')
  })

  it('never reports more loaded than total when a replaced list keeps the focused row', async () => {
    const { store, wrapper } = mountList({ items: [makeItem(), makeItem({ id: 2 })] })
    store.totalItems = 1
    // A background refresh left the selected item in the array after the filter
    // changed, so the array holds one row that is not in the new result set.
    store.selectedItem = store.items[0]
    await wrapper.vm.$nextTick()

    expect(headerCount(wrapper)).toBe('1/1')
  })

  // The Saved view is paginated, so the SERVER owns the order. The component
  // must render exactly what it was given: re-sorting here could only reorder
  // the loaded page, which is how choosing "Oldest" changed the label while the
  // list stayed newest-first.
  it('renders saved items in the order the server returned them', async () => {
    const saved = [
      makeItem({ id: 1, title: 'First', published_at: '2026-08-10T08:00:00Z' }),
      makeItem({ id: 2, title: 'Second', published_at: '2026-08-14T08:00:00Z' }),
    ]
    const { store, wrapper } = mountList({ showingSaved: true, savedItems: saved })

    // Oldest first, as delivered — even though the store's sort says otherwise.
    store.sort = 'date_desc'
    await wrapper.vm.$nextTick()

    const rows = resultRows(wrapper)
    expect(rows[0].text()).toContain('First')
    expect(rows[1].text()).toContain('Second')
  })

  it('shows the Saved view the date order the server actually applied', async () => {
    const { store, wrapper } = mountList({ showingSaved: true, savedItems: [makeItem({ status: 'saved' })] })

    // A score sort picked in a feed view must not label the Saved list: the
    // Saved view only has date orders, and the server collapses anything else
    // to date_desc. An honest label is the whole point of the fix.
    store.sort = 'score_desc'
    await wrapper.vm.$nextTick()
    expect(wrapper.text()).toContain('Newest')

    store.sort = 'date_asc'
    await wrapper.vm.$nextTick()
    expect(wrapper.text()).toContain('Oldest')
  })

  // Pull-to-refresh reloads the ACTIVE view. The Saved view is not a feed, so
  // refreshItems() returned early there and the gesture did nothing at all.
  it('pull-to-refresh reloads the saved list in the Saved view', async () => {
    const { store, wrapper } = mountList({
      showingSaved: true,
      savedItems: [makeItem({ status: 'saved' })],
      activeFeedId: null,
    })
    const fetchSaved = vi.spyOn(store, 'fetchSavedItems').mockResolvedValue(undefined)
    const refreshFeed = vi.spyOn(store, 'refreshItems').mockResolvedValue(undefined)

    await pullToRefresh(wrapper)

    expect(fetchSaved).toHaveBeenCalledTimes(1)
    expect(refreshFeed).not.toHaveBeenCalled()
  })

  it('pull-to-refresh still reloads the feed in a feed view', async () => {
    const { store, wrapper } = mountList()
    const fetchSaved = vi.spyOn(store, 'fetchSavedItems').mockResolvedValue(undefined)
    const refreshFeed = vi.spyOn(store, 'refreshItems').mockResolvedValue(undefined)

    await pullToRefresh(wrapper)

    expect(refreshFeed).toHaveBeenCalledTimes(1)
    expect(fetchSaved).not.toHaveBeenCalled()
  })

  it('reports the saved view against its server total', async () => {
    const saved = [makeItem({ status: 'saved' }), makeItem({ id: 2, status: 'saved' })]
    const { store, wrapper } = mountList({ showingSaved: true, savedItems: saved })

    // Loaded page vs the server's filter-aware count, like every other list.
    expect(headerCount(wrapper)).toBe('2/2')
    expect(wrapper.find('[data-filter-feed-name]').text()).toBe('Saved')

    // A page of 2 out of 7 saved reads as "2/7", not "2/2": the saved view is
    // paginated now, so the loaded length is not the total.
    store.savedTotal = 7
    await wrapper.vm.$nextTick()
    expect(headerCount(wrapper)).toBe('2/7')
  })

  // Centring on the BAR is a layout guarantee, and jsdom does no layout — what it
  // can assert is the structure that makes it true: the bar is a 3-column grid
  // with EQUAL outer columns, so the middle column is centred by construction
  // regardless of how wide the two buttons are. Swapping this for a flex row
  // flanked by equal spacers looks equivalent and measured 14px off-centre live,
  // because the sort button is wider than the filter button.
  it('centres the cluster with a symmetric grid, not equal flex spacers', () => {
    const { wrapper } = mountList()
    const bar = wrapper.find('[data-filter-count]').element.closest('.border-b') as HTMLElement

    expect(bar.className).toContain('grid-cols-[minmax(0,1fr)_auto_minmax(0,1fr)]')
    // The centre cell is the measured node, and both buttons are still present.
    expect(bar.querySelector('[data-filter-center]')).not.toBeNull()
    expect(bar.querySelector('[aria-label="Filter items"]')).not.toBeNull()
    expect(bar.querySelector('[aria-label="Sort items"]')).not.toBeNull()
  })
})

// Status and saved collapsed into ONE leading mark per row, mirroring the
// header rail: saved wins it (★, accent, text char — the same mark the header
// shows), else the read-state glyph. The DATA stays orthogonal (is_saved flag
// + status); only the display collapses. A saved row must therefore show
// exactly ONE element before its title — the old markup stacked the status
// mark AND a separate ✓ Check span there (two elements), so this fails hard
// against it. jsdom does no layout, so the contract is element count + shape.
describe('ItemList merged status+saved mark', () => {
  // Everything in the row's first line that precedes the title = the mark chain.
  function leadingMarks(row: Element): Element[] {
    const title = row.querySelector('[data-item-title]') ?? row.querySelector('p')
    const siblings = Array.from(title!.parentElement!.children)
    return siblings.slice(0, siblings.indexOf(title!))
  }

  for (const compact of [true, false]) {
    it(`renders exactly one mark — ★ for a saved row (compact=${compact})`, () => {
      const { wrapper } = mountList({
        items: [makeItem({ id: 1, title: 'Saved thing', status: 'unread', is_saved: 1 })],
        compact,
      })
      const marks = leadingMarks(resultRows(wrapper)[0].element)
      expect(marks).toHaveLength(1)
      const star = marks[0] as HTMLElement
      expect(star.getAttribute('title')).toBe('saved')
      expect(star.textContent?.trim()).toBe('★')
      // A text char, not an icon — same rule the header glyph learned twice.
      expect(star.querySelector('svg')).toBeNull()
      expect(star.className).toContain('text-accent')
      // And no second mark anywhere in the row.
      expect(wrapper.element.querySelectorAll('[title="saved"]')).toHaveLength(1)
      expect(wrapper.element.querySelector('svg.lucide-check')).toBeNull()
    })

    it(`keeps the read-state mark alone for an unsaved row (compact=${compact})`, () => {
      const { wrapper } = mountList({
        items: [
          makeItem({ id: 1, title: 'Unread', status: 'unread', is_saved: 0 }),
          makeItem({ id: 2, title: 'Read', status: 'read', is_saved: 0 }),
        ],
        compact,
      })
      const rows = resultRows(wrapper)
      expect(leadingMarks(rows[0].element)).toHaveLength(1)
      expect(rows[0].element.querySelector('[title="saved"]')).toBeNull()
      // Unread keeps its CircleDot; read keeps its Circle — the unsaved chain
      // is untouched by the merge.
      expect(rows[0].element.querySelector('svg.lucide-circle-dot')).not.toBeNull()
      expect(rows[1].element.querySelector('svg.lucide-circle')).not.toBeNull()
      expect(rows[1].element.querySelector('[title="saved"]')).toBeNull()
    })
  }
})
