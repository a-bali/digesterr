import { describe, it, expect, beforeEach, vi } from 'vitest'

// Summarize is the one action here that calls the API directly (not via the
// store), so it needs a mock to drive. Summaries persist per request only: the
// pane does not cache them, so every test starts from an empty box.
const summarizeMock = vi.fn()
vi.mock('../services/api', () => ({
  itemApi: {
    summarize: (...args: unknown[]) => summarizeMock(...args),
    extract: vi.fn(),
    getItem: vi.fn(),
    getComments: vi.fn(async () => ({ comments: [], cached: false })),
  },
}))
import { mount } from '@vue/test-utils'
import { createRouter, createMemoryHistory } from 'vue-router'
import { createPinia, setActivePinia } from 'pinia'
import ItemContent from './ItemContent.vue'
import { useFeedStore } from '../stores/feed'
import { absoluteTime } from '../lib/format'
import type { Feed, Item } from '../types/api'

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
    category_id: null,
    type: 'rss',
    name: 'Example Feed',
    url: 'https://example.com/feed.xml',
    config: '',
    min_score: 0,
    last_fetched_at: null,
    status: 'ok',
    extract_content: 0,
    last_error: '',
    last_error_at: null,
    last_error_severity: '',
    created_at: '2026-08-13T07:00:00Z',
    updated_at: '2026-08-13T07:00:00Z',
    ...overrides,
  }
}

function mountItemContent(item: Item | null, feeds: Feed[] = []) {
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

// The one button that switches the content section between the extracted body
// and the original. There used to be two section-header toggles instead.
function toggleOriginalButton(wrapper: ReturnType<typeof mountItemContent>) {
  return wrapper.find('[data-toggle-original]')
}

// The read-state toggle. `digested` counts as READ (AGENTS.md: item digested →
// read), so a digested item must offer "Mark Unread". The old test was
// `status === 'read'`, which treated digested as unread: the button read "Mark
// Read" on an item that was already read, and clicking it wrote 'read' — the same
// state the app already considered it to be in — so it looked like a dead button.
describe('ItemContent read toggle', () => {
  function readButton(wrapper: ReturnType<typeof mountItemContent>) {
    return wrapper.findAll('button').find(b => /^Mark (Read|Unread)$/.test(b.text().trim()))
  }

  it('offers "Mark Read" for an unread item', () => {
    const wrapper = mountItemContent(makeItem({ status: 'unread' }))
    expect(readButton(wrapper)!.text().trim()).toBe('Mark Read')
  })

  it('offers "Mark Unread" for a read item', () => {
    const wrapper = mountItemContent(makeItem({ status: 'read' }))
    expect(readButton(wrapper)!.text().trim()).toBe('Mark Unread')
  })

  it('offers "Mark Unread" for a DIGESTED item, which is a read item', () => {
    const wrapper = mountItemContent(makeItem({ status: 'digested' }))
    expect(readButton(wrapper)!.text().trim()).toBe('Mark Unread')
  })

  it('clicking the toggle on a digested item clears it to unread, not to read', () => {
    const wrapper = mountItemContent(makeItem({ status: 'digested' }))
    const store = useFeedStore()
    // Record what the button writes without a network round trip.
    const writes: Array<string | undefined> = []
    store.updateItemStatus = async (_item: any, status?: string) => { writes.push(status) }

    readButton(wrapper)!.trigger('click')
    expect(writes).toEqual(['unread'])
  })
})

describe('ItemContent single content section', () => {
  let wrapper: ReturnType<typeof mountItemContent>

  beforeEach(() => {
    wrapper = mountItemContent(makeItem())
  })

  // The extracted/original blocks became ONE section with a toggle. With both
  // open the user read the same article twice; folding one away left a header
  // with nothing useful behind it.
  it('shows the extracted body by default and the original behind one button', () => {
    expect(wrapper.text()).toContain('Extracted body')
    expect(wrapper.text()).not.toContain('Original body')
    expect(toggleOriginalButton(wrapper).text().trim()).toBe('Show original')
  })

  it('switches to the original body when the button is pressed', async () => {
    await toggleOriginalButton(wrapper).trigger('click')

    expect(wrapper.text()).toContain('Original body')
    expect(wrapper.text()).not.toContain('Extracted body')
    // Label flips so the button always says what pressing it will do.
    expect(toggleOriginalButton(wrapper).text().trim()).toBe('Show extracted')
  })

  it('returns to the extracted body when pressed again', async () => {
    const btn = toggleOriginalButton(wrapper)
    await btn.trigger('click')
    await toggleOriginalButton(wrapper).trigger('click')

    expect(wrapper.text()).toContain('Extracted body')
    expect(wrapper.text()).not.toContain('Original body')
    expect(toggleOriginalButton(wrapper).text().trim()).toBe('Show original')
  })

  // "Colored to enabled state" — the user asked for the pressed state to be
  // visible, not just implied by the label. `default` is the accent-filled
  // variant and `outline` the plain bordered one, so the variant IS the cue:
  // the button is colored exactly while the original is the body on screen.
  it('colors the button only while the original is the shown body', async () => {
    // Extracted is the shown body, so the button is in its plain state.
    expect(toggleOriginalButton(wrapper).classes()).toContain('bg-background')
    await toggleOriginalButton(wrapper).trigger('click')
    // Original is now shown: the button is filled with the accent.
    const cls = toggleOriginalButton(wrapper).classes()
    expect(cls).toContain('text-accent-foreground')
    expect(cls).not.toContain('bg-background')
  })

  it('publishes the shown body as aria-pressed for assistive tech', async () => {
    expect(toggleOriginalButton(wrapper).attributes('aria-pressed')).toBe('false')
    await toggleOriginalButton(wrapper).trigger('click')
    expect(toggleOriginalButton(wrapper).attributes('aria-pressed')).toBe('true')
  })

  it('never renders both bodies at once', async () => {
    const both = () => wrapper.text().includes('Extracted body') && wrapper.text().includes('Original body')
    expect(both()).toBe(false)
    await toggleOriginalButton(wrapper).trigger('click')
    expect(both()).toBe(false)
    await toggleOriginalButton(wrapper).trigger('click')
    expect(both()).toBe(false)
  })

  it('offers no toggle when the item has no extraction', () => {
    const plain = mountItemContent(makeItem({ extract_status: '' }))
    expect(plain.find('[data-toggle-original]').exists()).toBe(false)
    expect(plain.text()).toContain('Original body')
  })

  it('offers no toggle when the extraction is identical to the original', () => {
    // Identical bodies would make the button a no-op.
    const same = mountItemContent(makeItem({ content: '<p>Same</p>', extract_content: '<p>Same</p>' }))
    expect(same.find('[data-toggle-original]').exists()).toBe(false)
    expect(same.text()).toContain('Same')
  })

  it('offers no toggle when the original body is empty', () => {
    // HN/link posts carry no text: "Show original" would reveal nothing.
    const empty = mountItemContent(makeItem({ content: '' }))
    expect(empty.find('[data-toggle-original]').exists()).toBe(false)
    expect(empty.text()).toContain('Extracted body')
  })

  it('does not mutate the underlying item data when switching views', async () => {
    await toggleOriginalButton(wrapper).trigger('click')
    const item = useFeedStore().selectedItem!
    expect(item.extract_content).toBe('<p>Extracted body</p>')
    expect(item.content).toBe('<p>Original body</p>')
    expect(item.extract_status).toBe('done')
  })

  it('keeps existing actions visible and functional while switched', async () => {
    await toggleOriginalButton(wrapper).trigger('click')
    expect(wrapper.text()).toContain('Original body')
    expect(wrapper.text()).toContain('Mark Read')
    expect(wrapper.text()).toContain('Re-extract')
  })

  // A cleared extraction is not a view: the original is all there is.
  it('shows only the original for a cleared extraction, with no toggle', () => {
    const cleared = mountItemContent(makeItem({ extract_status: 'cleared' }))
    expect(cleared.find('[data-toggle-original]').exists()).toBe(false)
    expect(cleared.text()).toContain('Original body')
    expect(cleared.text()).not.toContain('Extracted body')
  })

  // 'skipped' means the server produced no body at all. The old header gated only
  // on "status is set", which drew a header over an empty body for 593 real items.
  it('shows only the original for a skipped extraction, with no toggle', () => {
    const skipped = mountItemContent(makeItem({ extract_status: 'skipped', extract_content: '' }))
    expect(skipped.find('[data-toggle-original]').exists()).toBe(false)
    expect(skipped.text()).toContain('Original body')
  })

  // The view is per-item UI state: carrying 'original' over from the previous
  // item would open the next item on the body extraction exists to replace.
  it('resets to the extracted view when a different item is opened', async () => {
    await toggleOriginalButton(wrapper).trigger('click')
    expect(wrapper.text()).toContain('Original body')

    useFeedStore().selectedItem = makeItem({ id: 2, title: 'Another item' })
    await wrapper.vm.$nextTick()

    expect(wrapper.text()).toContain('Extracted body')
    expect(wrapper.text()).not.toContain('Original body')
  })

  it('constrains the pane width and uses reduced mobile padding', () => {
    const root = wrapper.find('[data-item-content]')
    expect(root.classes()).toContain('overflow-x-hidden')
    expect(root.classes()).toContain('min-w-0')
    expect(root.classes()).toContain('px-3')
    expect(root.classes()).toContain('py-3')
    expect(root.classes()).toContain('sm:p-6')
  })

  it('resets vertical scroll when a different item is opened', async () => {
    const root = wrapper.find('[data-item-content]')
    Object.defineProperty(root.element, 'scrollTop', { value: 500, writable: true })
    useFeedStore().selectedItem = makeItem({ id: 2, title: 'Another item' })
    await wrapper.vm.$nextTick()
    await wrapper.vm.$nextTick()
    expect((root.element as HTMLElement).scrollTop).toBe(0)
  })

  it('restores scroll position when the same item is reopened', async () => {
    const root = wrapper.find('[data-item-content]')
    // Simulate the user scrolling: the @scroll handler is what persists position
    // (a watcher-based save can't run — the component unmounts before it fires).
    Object.defineProperty(root.element, 'scrollTop', { value: 500, writable: true, configurable: true })
    await root.trigger('scroll')
    const store = useFeedStore()
    // Deselect (back to list), then reopen the same item.
    store.selectedItem = null
    await wrapper.vm.$nextTick()
    await wrapper.vm.$nextTick()
    store.selectedItem = makeItem({ id: 1 })
    await wrapper.vm.$nextTick()
    await wrapper.vm.$nextTick()
    expect((root.element as HTMLElement).scrollTop).toBe(500)
  })
})

describe('ItemContent feed link', () => {
  it('links the feed name to the in-app feed route next to the time', () => {
    const feed = makeFeed({ id: 7, name: 'HN', url: 'https://news.ycombinator.com/rss' })
    const wrapper = mountItemContent(makeItem({ feed_id: 7 }), [feed])

    const link = wrapper.find('a[title="Open feed"]')
    expect(link.exists()).toBe(true)
    expect(link.attributes('href')).toBe('/feeds/7')
    expect(link.text()).toBe('HN')
    // Feed link sits next to the unchanged timestamp in the metadata row.
    const time = wrapper.find('span.text-xs')
    expect(time.text()).toBe(absoluteTime(makeItem().published_at))
    expect(wrapper.text()).toContain('HN')
  })

  it('renders no feed link when the item feed is unknown', () => {
    const wrapper = mountItemContent(makeItem({ feed_id: 99 }), [makeFeed({ id: 1 })])
    expect(wrapper.find('a[title="Open feed"]').exists()).toBe(false)
    expect(wrapper.text()).not.toContain('Example Feed')
  })

  it('keeps the timestamp format unchanged when a feed link is present', () => {
    const wrapper = mountItemContent(makeItem(), [makeFeed()])
    const time = wrapper.find('span.text-xs')
    expect(time.text()).toBe(absoluteTime(makeItem().published_at))
  })
})

describe('ItemContent LLM summary box', () => {
  let wrapper: ReturnType<typeof mountItemContent>

  beforeEach(() => {
    summarizeMock.mockReset()
    summarizeMock.mockResolvedValue({ content_summary: 'A summary.', comments_summary: '' })
    wrapper = mountItemContent(makeItem())
  })

  async function summarize() {
    const btn = wrapper.findAll('button').find(b => b.text().trim() === 'Summarize')!
    await btn.trigger('click')
    await wrapper.vm.$nextTick()
    await wrapper.vm.$nextTick()
  }

  it('renders no summary box before one is generated', () => {
    expect(wrapper.find('[data-summary-box]').exists()).toBe(false)
  })

  it('shows the summary in a tinted box above the body with a close button', async () => {
    await summarize()

    const box = wrapper.find('[data-summary-box]')
    expect(box.exists()).toBe(true)
    expect(box.text()).toContain('A summary.')
    // Tinted: the accent mixed into the card, matching the toast treatment rather
    // than a solid fill.
    expect(box.classes()).toContain('bg-accent/5')
    // A real close control, labelled for assistive tech.
    const close = box.find('[data-summary-close]')
    expect(close.exists()).toBe(true)
    expect(close.attributes('aria-label')).toBe('Hide summary')
    // The box sits above the content body.
    const html = wrapper.html()
    expect(html.indexOf('data-summary-box')).toBeLessThan(html.indexOf('data-toggle-original'))
  })

  it('hides the box when the close button is pressed', async () => {
    await summarize()
    await wrapper.find('[data-summary-close]').trigger('click')

    expect(wrapper.find('[data-summary-box]').exists()).toBe(false)
  })

  it('is not a collapsible section: there is no header toggle to fold it', async () => {
    await summarize()
    // The old summary had an aria-expanded header. A dismiss-only box must not.
    expect(wrapper.find('[data-summary-box] [aria-expanded]').exists()).toBe(false)
  })

  it('resets the dismissed state for the next item', async () => {
    await summarize()
    await wrapper.find('[data-summary-close]').trigger('click')
    expect(wrapper.find('[data-summary-box]').exists()).toBe(false)

    useFeedStore().selectedItem = makeItem({ id: 2, title: 'Another item' })
    await wrapper.vm.$nextTick()
    await summarize()

    expect(wrapper.find('[data-summary-box]').exists()).toBe(true)
  })
})

describe('ItemContent extraction switches to the extracted body', () => {
  beforeEach(() => {
    summarizeMock.mockReset()
  })

  // "If pressing extract/re-extract, automatically switch to viewing the
  // extracted content." The switch must happen ON THE CLICK, not after the poll
  // resolves: the click is the request to read it, and waiting leaves the user
  // staring at the body they just asked to replace. Re-extract on an item that
  // already has a body proves it — the extracted text must appear immediately,
  // while the extraction request is still in flight.
  it('switches back to the extracted view when Re-extract is pressed', async () => {
    const wrapper = mountItemContent(makeItem({ extract_status: 'done' }))
    const api = await import('../services/api')
    // Never resolves: the poll must not be what causes the switch.
    ;(api.itemApi.extract as any).mockImplementation(() => new Promise(() => {}))

    await toggleOriginalButton(wrapper).trigger('click')
    expect(wrapper.text()).toContain('Original body')
    expect(wrapper.text()).not.toContain('Extracted body')

    const re = wrapper.findAll('button').find(b => b.text().trim() === 'Re-extract')!
    await re.trigger('click')
    await wrapper.vm.$nextTick()

    expect(wrapper.text()).toContain('Extracted body')
    expect(wrapper.text()).not.toContain('Original body')
    expect(toggleOriginalButton(wrapper).text().trim()).toBe('Show original')
  })

  it('returns to the original view when the extraction is deleted', async () => {
    const wrapper = mountItemContent(makeItem())
    await toggleOriginalButton(wrapper).trigger('click')
    expect(wrapper.text()).toContain('Original body')

    // Deleting needs a confirm; drive the store the way the button does.
    const store = useFeedStore()
    let deleted = false
    store.deleteExtraction = async () => { deleted = true; store.selectedItem = makeItem({ extract_status: 'cleared' }) }
    const del = wrapper.findAll('button').find(b => b.text().trim().includes('Delete extracted'))!
    await del.trigger('click')

    expect(deleted).toBe(true)
    await wrapper.vm.$nextTick()
    // With the extraction gone the pane shows the original and offers no toggle.
    expect(wrapper.find('[data-toggle-original]').exists()).toBe(false)
    expect(wrapper.text()).toContain('Original body')
  })
})
