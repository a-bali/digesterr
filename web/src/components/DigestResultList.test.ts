import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import DigestResultList from './DigestResultList.vue'
import { useDigestStore } from '../stores/digest'
import { absoluteTime } from '../lib/format'
import type { DigestResult } from '../types/api'

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

function mountList() {
  const pinia = createPinia()
  setActivePinia(pinia)
  const store = useDigestStore()
  store.loading = false
  store.results = [
    makeResult(),
    makeResult({ id: 2, created_at: '2026-08-12T09:30:00Z', status: 'read' }),
  ]
  return mount(DigestResultList, { global: { plugins: [pinia] } })
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

  it('shows the full date and time for each digest', () => {
    const wrapper = mountList()
    const text = wrapper.text()
    expect(text).toContain(absoluteTime('2026-08-13T08:00:00Z'))
    expect(text).toContain(absoluteTime('2026-08-12T09:30:00Z'))
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
    expect(resultRows(wrapper)[0].text()).toContain(absoluteTime('2026-08-13T08:00:00Z'))
    expect(wrapper.html()).toContain('Unread')

    await openMenu(wrapper, 0)
    await clickItem('Read')
    expect(resultRows(wrapper)).toHaveLength(1)
    expect(resultRows(wrapper)[0].text()).toContain(absoluteTime('2026-08-12T09:30:00Z'))

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
    expect(resultRows(wrapper)[0].text()).toContain(absoluteTime('2026-08-13T08:00:00Z'))

    await openMenu(wrapper, 1)
    await clickItem('Oldest')

    const rows = resultRows(wrapper).map(r => r.text())
    expect(rows[0]).toContain(absoluteTime('2026-08-12T09:30:00Z'))
    expect(rows[1]).toContain(absoluteTime('2026-08-13T08:00:00Z'))
    expect(wrapper.html()).toContain('Oldest')
  })
})
