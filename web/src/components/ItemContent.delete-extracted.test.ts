import { describe, it, expect, beforeEach, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createRouter, createMemoryHistory } from 'vue-router'
import { createPinia, setActivePinia } from 'pinia'
import ItemContent from './ItemContent.vue'
import { useFeedStore } from '../stores/feed'
import type { Feed, Item } from '../types/api'

// The store method is mocked at the module boundary: this suite is about the
// pane's affordance and the body it renders, not about the HTTP call (which the
// store tests cover).
const mockDeleteExtraction = vi.fn()
vi.mock('../stores/feed', async () => {
  const actual = await vi.importActual<typeof import('../stores/feed')>('../stores/feed')
  return {
    useFeedStore: () => {
      const store = actual.useFeedStore()
      return new Proxy(store, {
        get(target, prop) {
          if (prop === 'deleteExtraction') return mockDeleteExtraction
          return (target as any)[prop]
        },
      })
    },
  }
})

// Menu items render [icon][label] or [label][detail]. The detail span is the only
// one carrying text-xs, and the icon is an svg or a bare glyph span — so the label
// is the last span that is neither.
function menuItemLabel(el: Element): string {
  const spans = Array.from(el.querySelectorAll('span'))
  const label = spans.filter(s => !s.className.includes('text-xs'))
  const last = label[label.length - 1]
  return (last?.textContent ?? el.textContent ?? '').trim()
}

function makeItem(overrides: Partial<Item> = {}): Item {
  return {
    id: 1,
    feed_id: 1,
    hash: 'h',
    title: 'Test item',
    url: 'https://example.com/1',
    content: '<p>Original body</p>',
    author: 'author',
    comment_url: '',
    points: 0,
    comments_count: 0,
    published_at: '2026-08-13T07:00:00Z',
    status: 'unread',
    fetched_at: '2026-08-13T07:00:00Z',
    created_at: '2026-08-13T07:00:00Z',
    updated_at: '2026-08-13T07:00:00Z',
    last_enriched_at: null,
    extract_content: '<p>Extracted body</p>',
    extract_status: 'done',
    extract_retries: 0,
    extract_date: '2026-08-13T08:00:00Z',
    ...overrides,
  }
}

function makeFeed(overrides: Partial<Feed> = {}): Feed {
  return {
    id: 1,
    user_id: 1,
    category: '',
    type: 'rss',
    name: 'Example Feed',
    url: 'https://example.com/feed.xml',
    config: '',
    min_score: 0,
    last_fetched_at: null,
    status: 'ok',
    extract_content: 1,
    last_error: '',
    last_error_at: null,
    last_error_severity: '',
    created_at: '2026-08-13T07:00:00Z',
    updated_at: '2026-08-13T07:00:00Z',
    ...overrides,
  }
}

function mountItemContent(item: Item | null, feeds: Feed[] = [makeFeed()]) {
  const pinia = createPinia()
  setActivePinia(pinia)
  const store = useFeedStore()
  store.selectedItem = item
  store.feeds = feeds
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/', component: { template: '<div />' } },
      { path: '/feeds/:feedId(\\d+)/:itemId(\\d+)?', component: { template: '<div />' } },
    ],
  })
  return mount(ItemContent, {
    props: { unsaveItem: async () => {} },
    global: { plugins: [pinia, router] },
  })
}

// Re-extract and Delete moved into the extraction menu attached to the view
// toggle, so they are menu items in a portal on document.body, not header buttons.
function extractionOpener(wrapper: ReturnType<typeof mountItemContent>) {
  return wrapper.find('[data-extraction-opener]')
}
async function openExtractionMenu(wrapper: ReturnType<typeof mountItemContent>) {
  await extractionOpener(wrapper).trigger('click')
  await flushPromises()
}
function extractionItems(): HTMLElement[] {
  return Array.from(document.body.querySelectorAll('[role="menuitem"]')) as HTMLElement[]
}
// Each item is [icon][label]; read the label span so the icon glyph ('◐') does not
// break the match.
function extractionItem(label: string): HTMLElement | undefined {
  return extractionItems().find(
    i => (menuItemLabel(i)).trim() === label,
  )
}

beforeEach(() => {
  mockDeleteExtraction.mockReset()
  mockDeleteExtraction.mockResolvedValue(undefined)
})

describe('Delete extracted', () => {
  it('offers Re-extract then Delete inside the extraction menu', async () => {
    const wrapper = mountItemContent(makeItem())
    await openExtractionMenu(wrapper)
    const labels = extractionItems().map(i => (menuItemLabel(i)).trim())
    expect(labels).toEqual(['Re-extract', 'Delete extracted'])
    // Reachability, not just presence: a disabled item is the thing under test.
    expect(extractionItem('Delete extracted')!.hasAttribute('data-disabled')).toBe(false)
  })

  it('offers no extraction control at all without an extracted body', () => {
    // The opener lives ON the view toggle, and the toggle is offered only when both
    // bodies exist and differ — so with nothing extracted there is no control, and
    // no menu, at all. (The earlier design kept a disabled "Delete" around; hanging
    // the actions off the toggle removes that state entirely.)
    const plain = mountItemContent(makeItem({ extract_status: '', extract_content: '' }))
    expect(extractionOpener(plain).exists()).toBe(false)
    expect(plain.find('[data-view-toggle]').exists()).toBe(false)
  })

  it('offers no extraction control after the user already deleted the extraction', () => {
    const cleared = mountItemContent(makeItem({ extract_status: 'cleared', extract_content: '' }))
    expect(extractionOpener(cleared).exists()).toBe(false)
  })

  it('deletes the extraction through the store when the menu item is clicked', async () => {
    const wrapper = mountItemContent(makeItem())
    await openExtractionMenu(wrapper)
    extractionItem('Delete extracted')!.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    await flushPromises()
    expect(mockDeleteExtraction).toHaveBeenCalledTimes(1)
    expect(mockDeleteExtraction.mock.calls[0][0].id).toBe(1)
  })

  it('does not offer extraction at all for feed types where it is unsupported', () => {
    const twitter = mountItemContent(makeItem(), [makeFeed({ type: 'twitter' })])
    expect(extractionOpener(twitter).exists()).toBe(false)
  })

  it('renders the original content plainly once the extraction is cleared', () => {
    // A cleared extraction must read like a never-extracted item: one body, no
    // Extracted/Original split with an empty extracted section.
    const wrapper = mountItemContent(makeItem({ extract_status: 'cleared', extract_content: '' }))
    expect(wrapper.text()).toContain('Original body')
    expect(wrapper.find('[data-view-toggle]').exists()).toBe(false)
  })

  it('falls back to the original body after the store clears the item', async () => {
    const wrapper = mountItemContent(makeItem())
    expect(wrapper.text()).toContain('Extracted body')
    expect(wrapper.find('[data-view-toggle]').exists()).toBe(true)

    useFeedStore().selectedItem = makeItem({ extract_status: 'cleared', extract_content: '', extract_date: null })
    await flushPromises()

    expect(wrapper.text()).toContain('Original body')
    expect(wrapper.text()).not.toContain('Extracted body')
    expect(wrapper.find('[data-view-toggle]').exists()).toBe(false)
  })
})
