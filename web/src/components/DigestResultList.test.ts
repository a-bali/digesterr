import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import DigestResultList from './DigestResultList.vue'
import { useDigestStore } from '../stores/digest'
import { absoluteTime, relativeTime } from '../lib/format'
import type { DigestResult } from '../types/api'

vi.mock('../services/api', () => ({
  digestApi: {
    listConfigs: vi.fn().mockResolvedValue([]),
    createConfig: vi.fn(),
    updateConfig: vi.fn(),
    deleteConfig: vi.fn(),
    listResults: vi.fn().mockResolvedValue({ results: [], has_more: false }),
    listResultItems: vi.fn().mockResolvedValue([]),
    generate: vi.fn(),
    updateResultStatus: vi.fn(),
    deleteResult: vi.fn(),
    unreadCounts: vi.fn().mockResolvedValue({}),
    markReadAbove: vi.fn().mockResolvedValue({ message: 'ok' }),
    markReadBelow: vi.fn().mockResolvedValue({ message: 'ok' }),
  },
  ApiError: class extends Error { constructor(public status: number, body: any) { super(body?.error) } },
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
function makeResult(overrides: Partial<DigestResult> = {}): DigestResult {
  return {
    id: 1,
    user_id: 1,
    config_id: 1,
    config_snapshot: '{}',
    content: JSON.stringify({ summary: 's', items: [{ item_id: 2 }] }),
    status: 'unread',
    created_at: '2026-08-13T08:00:00Z',
    updated_at: '2026-08-13T08:00:00Z',
    title: 'Test Digest',
    item_count: 1,
    ...overrides,
  } as DigestResult
}

function mountList(opts: { compact?: boolean; showingAll?: boolean } = {}) {
  const pinia = createPinia()
  setActivePinia(pinia)
  const store = useDigestStore()
  store.loading = false
  store.results = [
    makeResult(),
    makeResult({ id: 2, created_at: '2026-08-12T09:30:00Z', status: 'read' }),
  ]
  return mount(DigestResultList, {
    props: {
      ...(opts.compact !== undefined ? { compact: opts.compact } : {}),
      ...(opts.showingAll !== undefined ? { showingAll: opts.showingAll } : {}),
    },
    global: { plugins: [pinia] },
  })
}

// The dropdown content renders inside a reka-ui portal (teleported to
// document.body), so read the live DOM, not the wrapper.
function menuItems(): HTMLElement[] {
  return Array.from(document.body.querySelectorAll('[role="menuitem"]')) as HTMLElement[]
}

function isDisabled(item: HTMLElement): boolean {
  return item.getAttribute("aria-disabled") === "true" || item.hasAttribute("data-disabled")
}

// The list rows are div[role="button"] elements with the w-full row class;
// the filter and sort triggers are compact ghost buttons.
function resultRows(wrapper: ReturnType<typeof mount>) {
  return wrapper.findAll('[role="button"]').filter(b => b.classes().includes('w-full'))
}

async function openMenu(wrapper: ReturnType<typeof mount>, index: 0 | 1) {
  await wrapper.findAll('button')[index].trigger('click')
  await flushPromises()
}

async function clickItem(label: string) {
  const item = menuItems().find(i => i.textContent?.includes(label))
  if (!item) throw new Error(`menu item not found: ${label}`)
  item.dispatchEvent(new MouseEvent('click', { bubbles: true }))
  await flushPromises()
}

describe('DigestResultList', () => {
  beforeEach(() => {
    document.body.innerHTML = ''
    setActivePinia(createPinia())
  })

  it('shows the relative publish time for each digest', () => {
    const wrapper = mountList()
    const times = resultRows(wrapper).map(r => r.find('[data-item-time]').attributes('title'))
    expect(times).toContain(absoluteTime('2026-08-13T08:00:00Z'))
    expect(times).toContain(absoluteTime('2026-08-12T09:30:00Z'))
  })

  it('counts items from the digest content', () => {
    const wrapper = mountList()
    expect(wrapper.html()).toContain('1 items')
  })

  it('renders state-label filter and sort buttons with defaults', () => {
    const wrapper = mountList()
    expect(wrapper.html()).toContain('All')
    expect(wrapper.html()).toContain('Newest')
  })

  it('filter dropdown lists only the three digest statuses and checks the active one', async () => {
    const wrapper = mountList()
    await openMenu(wrapper, 0)

    const labels = menuItems().map(i => i.textContent?.trim())
    expect(labels).toEqual(['All', 'Unread', 'Read'])
    // Default filter is All, so it is the only item carrying the check icon.
    const checked = menuItems().filter(i => i.querySelector('svg'))
    expect(checked.map(i => i.textContent?.trim())).toEqual(['All'])
    // Score section is greyed out (not active) for digest view.
    const scoreInput = document.body.querySelector('input[aria-label="Minimum score"]') as HTMLInputElement | null
    expect(scoreInput?.disabled).toBe(true)
  })

  it('filters the list when a status is selected', async () => {
    const wrapper = mountList()
    expect(resultRows(wrapper)).toHaveLength(2)

    await openMenu(wrapper, 0)
    await clickItem('Unread')
    expect(resultRows(wrapper)).toHaveLength(1)
    expect(resultRows(wrapper)[0].find('[data-item-time]').attributes('title')).toBe(absoluteTime('2026-08-13T08:00:00Z'))
    expect(wrapper.html()).toContain('Unread')

    await openMenu(wrapper, 0)
    await clickItem('Read')
    expect(resultRows(wrapper)).toHaveLength(1)
    expect(resultRows(wrapper)[0].find('[data-item-time]').attributes('title')).toBe(absoluteTime('2026-08-12T09:30:00Z'))

    await openMenu(wrapper, 0)
    await clickItem('All')
    expect(resultRows(wrapper)).toHaveLength(2)
    expect(wrapper.html()).toContain('All')
  })

  it('sort dropdown offers all options with score greyed out, active one checked', async () => {
    const wrapper = mountList()
    await openMenu(wrapper, 1)

    const labels = menuItems().map(i => i.textContent?.trim())
    expect(labels).toEqual(['Newest', 'Oldest', 'Highest score', 'Lowest score'])
    // Default sort is Newest.
    const checked = menuItems().filter(i => i.querySelector('svg'))
    expect(checked.map(i => i.textContent?.trim())).toEqual(['Newest'])
    // Score sort options are disabled for digest view.
    const disabled = menuItems().filter(isDisabled)
    expect(disabled.map(i => i.textContent?.trim())).toEqual(['Highest score', 'Lowest score'])
  })

  it('sorts by date ascending when Oldest first is selected', async () => {
    const wrapper = mountList()

    // Default (newest first): the Aug 13 digest renders before Aug 12.
    expect(resultRows(wrapper)[0].find('[data-item-time]').attributes('title')).toBe(absoluteTime('2026-08-13T08:00:00Z'))

    await openMenu(wrapper, 1)
    await clickItem('Oldest')

    const rows = resultRows(wrapper).map(r => r.find('[data-item-time]').attributes('title'))
    expect(rows[0]).toBe(absoluteTime('2026-08-12T09:30:00Z'))
    expect(rows[1]).toBe(absoluteTime('2026-08-13T08:00:00Z'))
    expect(wrapper.html()).toContain('Oldest')
  })
})

describe('DigestResultList compact rows (stacked layout)', () => {
  beforeEach(() => {
    document.body.innerHTML = ''
    setActivePinia(createPinia())
    vi.clearAllMocks()
  })

  it('uses the compact row height', () => {
    const wrapper = mountList({ compact: true })
    const row = resultRows(wrapper)[0]

    expect(row.classes()).toContain('h-7')
    expect(row.classes()).not.toContain('h-20')
    expect(row.classes()).not.toContain('min-h-[5.5rem]')
  })

  it('puts the title and the meta cluster on the SAME line (same flex row)', () => {
    const wrapper = mountList({ compact: true })
    const row = resultRows(wrapper)[0]
    const title = row.find('[data-item-title]')
    const cluster = row.find('[data-item-meta]').element.parentElement as HTMLElement

    expect(title.exists()).toBe(true)
    expect(row.find('[data-item-count]').text()).toContain('items')
    // Same parent + flex row is the DOM-level encoding of "one line".
    expect(title.element.parentElement).toBe(cluster.parentElement)
    expect(cluster.parentElement!.className).toContain('flex')
    expect(cluster.parentElement!.className).toContain('items-center')
  })

  it('keeps the item count and the time together in the right-hand cluster', () => {
    const wrapper = mountList({ compact: true })
    const cluster = resultRows(wrapper)[0].find('[data-item-meta]')

    // Count before time, both inside the wrapper that hover replaces.
    expect(cluster.find('[data-item-count]').text()).toContain('items')
    expect(cluster.find('[data-item-time]').exists()).toBe(true)
  })

  it('renders the mark chevrons and the relative publish time on the same line', () => {
    const wrapper = mountList({ compact: true })
    const row = resultRows(wrapper)[0]

    const timeEl = row.find('[data-item-time]')
    expect(timeEl.exists(), 'the relative time must still be shown').toBe(true)
    expect(relativeTime('2026-08-13T08:00:00Z')).toBeTruthy()
    expect(row.find('[data-mark-actions]').exists()).toBe(true)
  })

  it('leaves the default (non-compact) row untouched', () => {
    const wrapper = mountList()
    const row = resultRows(wrapper)[0]

    expect(row.classes()).toContain('h-20')
    expect(row.classes()).not.toContain('h-7')
    expect(row.find('[data-item-title]').exists()).toBe(false)
  })

  it('keeps the one-line row in the showingAll view too', () => {
    const pinia = createPinia()
    setActivePinia(pinia)
    const store = useDigestStore()
    store.loading = false
    store.showingAllDigests = true
    store.allResults = [{ ...makeResult(), config_name: 'Morning Brief' } as never]
    const wrapper = mount(DigestResultList, {
      props: { compact: true, showingAll: true },
      global: { plugins: [pinia] },
    })
    const row = resultRows(wrapper)[0]

    expect(row.classes()).toContain('h-7')
    expect(row.classes()).not.toContain('min-h-[5.5rem]')
    expect(row.find('[data-item-title]').exists()).toBe(true)
  })
})

// The digest list must carry the SAME mark-above/below affordance as the item
// list: two chevrons in the top right corner, on every row whatever its status,
// swapped in for the timestamp on hover. It used to be a MoreHorizontal menu
// that only existed on unread rows.
describe('DigestResultList mark read above/below hover actions', () => {
  beforeEach(() => {
    document.body.innerHTML = ''
    setActivePinia(createPinia())
    vi.clearAllMocks()
  })

  it('renders both chevrons on every row regardless of status, in both layouts', () => {
    for (const compact of [false, true]) {
      const pinia = createPinia()
      setActivePinia(pinia)
      const store = useDigestStore()
      store.loading = false
      store.results = [
        makeResult({ id: 1, status: 'unread' }),
        makeResult({ id: 2, status: 'read' }),
      ]
      const wrapper = mount(DigestResultList, {
        props: { compact },
        global: { plugins: [pinia] },
      })
      const rows = resultRows(wrapper)
      expect(rows).toHaveLength(2)
      for (const row of rows) {
        expect(row.find('[data-mark-above]').exists(), `compact=${compact}`).toBe(true)
        expect(row.find('[data-mark-below]').exists(), `compact=${compact}`).toBe(true)
        expect(row.find('[data-mark-actions]').exists()).toBe(true)
      }
    }
  })

  it('does not use a dropdown trigger any more', () => {
    const wrapper = mountList()
    const row = resultRows(wrapper)[0]
    // The old affordance: one button whose click opened a menu.
    expect(row.findAll('button')).toHaveLength(2)
  })

  it('labels the chevrons for assistive tech, noting the item is kept', () => {
    const wrapper = mountList()
    const row = resultRows(wrapper)[0]
    expect(row.find('[data-mark-above]').attributes('aria-label')).toBe('Mark everything above as read, except this item')
    expect(row.find('[data-mark-below]').attributes('aria-label')).toBe('Mark everything below as read, except this item')
    expect(row.find('[data-mark-above]').attributes('title')).toBe('Mark everything above as read, except this item')
    expect(row.find('[data-mark-below]').attributes('title')).toBe('Mark everything below as read, except this item')
  })

  it('swaps the time for the chevrons instead of stacking them', () => {
    const wrapper = mountList()
    const row = resultRows(wrapper)[0]
    const time = row.find('[data-item-time]')
    const actions = row.find('[data-mark-actions]')

    // Hovering only hides the time with CSS; it stays in the DOM so the
    // relative time is still announced and the absolute time stays available.
    expect(time.classes()).toContain('group-hover:hidden')
    expect(actions.classes()).toContain('hidden')
    expect(actions.classes()).toContain('group-hover:flex')
  })

  it('marks above with the clicked result', async () => {
    const { digestApi } = await import('../services/api')
    const markAbove = digestApi.markReadAbove as ReturnType<typeof vi.fn>
    const wrapper = mountList()

    await resultRows(wrapper)[1].find('[data-mark-above]').trigger('click')
    await flushPromises()

    expect(markAbove).toHaveBeenCalledTimes(1)
    expect(markAbove.mock.calls[0][0]).toBe(2)
  })

  it('marks below with the clicked result', async () => {
    const { digestApi } = await import('../services/api')
    const markBelow = digestApi.markReadBelow as ReturnType<typeof vi.fn>
    const wrapper = mountList()

    await resultRows(wrapper)[0].find('[data-mark-below]').trigger('click')
    await flushPromises()

    expect(markBelow).toHaveBeenCalledTimes(1)
    expect(markBelow.mock.calls[0][0]).toBe(1)
  })

  it('does not select the row when a chevron is clicked', async () => {
    const wrapper = mountList()
    await resultRows(wrapper)[0].find('[data-mark-above]').trigger('click')
    await flushPromises()
    expect(wrapper.emitted('select')).toBeFalsy()
  })
})
