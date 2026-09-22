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

// The header actions are icon-only, so each carries a stable data-action hook
// plus an aria-label. Selecting by hook (not by text) keeps this suite honest:
// an icon button has no text, and matching on icon shape would be untestable.
function deleteExtractedButton(wrapper: ReturnType<typeof mountItemContent>) {
  return wrapper.find('[data-action-delete-extracted]')
}
function extractButton(wrapper: ReturnType<typeof mountItemContent>) {
  return wrapper.find('[data-action-extract]')
}

beforeEach(() => {
  mockDeleteExtraction.mockReset()
  mockDeleteExtraction.mockResolvedValue(undefined)
})

describe('Delete extracted', () => {
  it('offers the control next to Re-extract when an extracted body exists', () => {
    const wrapper = mountItemContent(makeItem())
    const button = deleteExtractedButton(wrapper)
    expect(button, 'Delete extracted must be rendered for a done extraction').toBeTruthy()
    // Reachability, not just presence: a disabled control is the thing under
    // test, so assert it can actually be pressed.
    expect(button!.attributes('disabled')).toBeUndefined()
    // It sits directly after the Re-extract button the request calls out.
    const hooks = wrapper.findAll('button[data-action-extract], button[data-action-delete-extracted]')
      .map(b => b.attributes('data-action-extract') !== undefined ? 'extract' : 'delete')
    expect(hooks).toEqual(['extract', 'delete'])
  })

  it('hides the control when there is no extracted body to delete', () => {
    const plain = mountItemContent(makeItem({ extract_status: '', extract_content: '' }))
    expect(deleteExtractedButton(plain).exists()).toBe(false)
    // "Extract", not "Re-extract": nothing has been extracted for this item.
    expect(extractButton(plain).attributes('aria-label')).toBe('Extract')
  })

  it('hides the control after the user already deleted the extraction', () => {
    const cleared = mountItemContent(makeItem({ extract_status: 'cleared', extract_content: '' }))
    expect(deleteExtractedButton(cleared).exists()).toBe(false)
  })

  it('deletes the extraction through the store when clicked', async () => {
    const wrapper = mountItemContent(makeItem())
    await deleteExtractedButton(wrapper).trigger('click')
    expect(mockDeleteExtraction).toHaveBeenCalledTimes(1)
    expect(mockDeleteExtraction.mock.calls[0][0].id).toBe(1)
  })

  it('does not offer the control for feed types where extraction is unsupported', () => {
    const twitter = mountItemContent(makeItem(), [makeFeed({ type: 'twitter' })])
    expect(deleteExtractedButton(twitter).exists()).toBe(false)
  })

  it('renders the original content plainly once the extraction is cleared', () => {
    // A cleared extraction must read like a never-extracted item: one body, no
    // Extracted/Original split with an empty extracted section. Assert on the
    // section HEADERS (the toggle buttons), not on body text — the body itself
    // contains the word "Original".
    const wrapper = mountItemContent(makeItem({ extract_status: 'cleared', extract_content: '' }))
    expect(wrapper.text()).toContain('Original body')
    expect(wrapper.find('[data-view-toggle]').exists()).toBe(false)
  })

  it('falls back to the original body after the store clears the item', async () => {
    // The live flow: the pane shows the split view, the user clicks delete, and
    // the store replaces the item with the server's cleared copy.
    const wrapper = mountItemContent(makeItem())
    expect(wrapper.text()).toContain('Extracted body')
    expect(wrapper.find('[data-view-toggle]').exists()).toBe(true)

    useFeedStore().selectedItem = makeItem({ extract_status: 'cleared', extract_content: '', extract_date: null })
    await flushPromises()

    expect(wrapper.text()).toContain('Original body')
    expect(wrapper.text()).not.toContain('Extracted body')
    expect(deleteExtractedButton(wrapper).exists()).toBe(false)
  })
})
