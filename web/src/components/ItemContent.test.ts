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

// The two-position content toggle. It replaced a single "Show original" button,
// which named an ACTION and so read as misleading: the label described what a
// click would do rather than which body was on screen. The toggle shows both
// options with the active one accented, left = original, right = extracted.
function viewToggle(wrapper: ReturnType<typeof mountItemContent>) {
  return wrapper.find('[data-view-toggle]')
}
function viewOption(wrapper: ReturnType<typeof mountItemContent>, which: 'original' | 'extracted') {
  return wrapper.find(`[data-view-option="${which}"]`)
}
// Which segment is currently selected, per the accessibility state.
function checkedView(wrapper: ReturnType<typeof mountItemContent>) {
  return viewOption(wrapper, 'original').attributes('aria-checked') === 'true' ? 'original' : 'extracted'
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
  //
  // The toggle replaced a single "Show original" button, which was misleading:
  // it named an action rather than the state, so its label described what a click
  // would do instead of which body was on screen. A two-position toggle shows both
  // options at once with the active one accented.
  it('shows the extracted body by default with the extracted segment active', () => {
    expect(wrapper.text()).toContain('Extracted body')
    expect(wrapper.text()).not.toContain('Original body')
    expect(checkedView(wrapper)).toBe('extracted')
  })

  it('offers both bodies as options, original left and extracted right', () => {
    const opts = wrapper.findAll('[data-view-option]')
    expect(opts.map(o => o.text().trim())).toEqual(['Original', 'Extracted'])
    expect(opts[0].attributes('data-view-option')).toBe('original')
    expect(opts[1].attributes('data-view-option')).toBe('extracted')
  })

  it('switches to the original body when the original segment is chosen', async () => {
    await viewOption(wrapper, 'original').trigger('click')

    expect(wrapper.text()).toContain('Original body')
    expect(wrapper.text()).not.toContain('Extracted body')
    expect(checkedView(wrapper)).toBe('original')
  })

  it('returns to the extracted body when the extracted segment is chosen', async () => {
    await viewOption(wrapper, 'original').trigger('click')
    await viewOption(wrapper, 'extracted').trigger('click')

    expect(wrapper.text()).toContain('Extracted body')
    expect(wrapper.text()).not.toContain('Original body')
    expect(checkedView(wrapper)).toBe('extracted')
  })

  // The toggle uses the accent for the ACTIVE segment, in either position — the
  // user asked for accent-colored state, not a coloured control.
  it('accents whichever segment is active', async () => {
    const cls = (which: 'original' | 'extracted') => viewOption(wrapper, which).classes()
    // Extracted active on arrival.
    expect(cls('extracted')).toContain('bg-accent')
    expect(cls('extracted')).toContain('text-accent-foreground')
    expect(cls('original')).not.toContain('bg-accent')

    await viewOption(wrapper, 'original').trigger('click')

    // Accent follows the selection to the other segment.
    expect(cls('original')).toContain('bg-accent')
    expect(cls('original')).toContain('text-accent-foreground')
    expect(cls('extracted')).not.toContain('bg-accent')
  })

  it('marks exactly one segment checked at a time', async () => {
    const checked = () => wrapper.findAll('[data-view-option][aria-checked="true"]').length
    expect(checked()).toBe(1)
    await viewOption(wrapper, 'original').trigger('click')
    expect(checked()).toBe(1)
  })

  it('is a radiogroup, so assistive tech reads it as one choice', () => {
    const toggle = viewToggle(wrapper)
    expect(toggle.attributes('role')).toBe('radiogroup')
    expect(toggle.attributes('aria-label')).toBe('Content view')
    expect(viewOption(wrapper, 'original').attributes('role')).toBe('radio')
  })

  // Standard radiogroup keyboard contract: only the checked segment is tabbable
  // (roving tabindex) and the arrows move between the options.
  it('keeps only the active segment in the tab order', async () => {
    expect(viewOption(wrapper, 'extracted').attributes('tabindex')).toBe('0')
    expect(viewOption(wrapper, 'original').attributes('tabindex')).toBe('-1')

    await viewOption(wrapper, 'original').trigger('click')

    expect(viewOption(wrapper, 'original').attributes('tabindex')).toBe('0')
    expect(viewOption(wrapper, 'extracted').attributes('tabindex')).toBe('-1')
  })

  it('moves the selection with the arrow keys', async () => {
    await viewToggle(wrapper).trigger('keydown', { key: 'ArrowLeft' })

    expect(checkedView(wrapper)).toBe('original')
    expect(wrapper.text()).toContain('Original body')

    await viewToggle(wrapper).trigger('keydown', { key: 'ArrowRight' })

    expect(checkedView(wrapper)).toBe('extracted')
    expect(wrapper.text()).toContain('Extracted body')
  })

  it('never renders both bodies at once', async () => {
    const both = () => wrapper.text().includes('Extracted body') && wrapper.text().includes('Original body')
    expect(both()).toBe(false)
    await viewOption(wrapper, 'original').trigger('click')
    expect(both()).toBe(false)
    await viewOption(wrapper, 'extracted').trigger('click')
    expect(both()).toBe(false)
  })

  // "if showing extracted view, show extraction date in small muted font below
  // toggle" — the date belongs to the extracted body, so it shows only there.
  it('shows the extraction date beneath the toggle only in the extracted view', async () => {
    const date = () => wrapper.find('[data-extract-date]')
    expect(date().exists()).toBe(true)
    expect(date().text()).toBe(`Extracted ${absoluteTime('2026-08-13T08:00:00Z')}`)
    // Small and muted.
    expect(date().classes()).toContain('text-xs')
    expect(date().classes()).toContain('text-muted-foreground/70')

    await viewOption(wrapper, 'original').trigger('click')
    expect(date().exists()).toBe(false)

    await viewOption(wrapper, 'extracted').trigger('click')
    expect(date().exists()).toBe(true)
  })

  it('aligns the toggle to the right edge', () => {
    // The container right-aligns its children (toggle, then the date below it).
    const container = viewToggle(wrapper).element.parentElement!
    expect(container.className).toContain('items-end')
    // The toggle follows the date in the DOM, i.e. the date sits BELOW it.
    expect(container.querySelector('[data-extract-date]')).not.toBeNull()
    expect(container.lastElementChild?.hasAttribute('data-extract-date')).toBe(true)
  })

  it('offers no toggle when the item has no extraction', () => {
    const plain = mountItemContent(makeItem({ extract_status: '' }))
    expect(plain.find('[data-view-toggle]').exists()).toBe(false)
    expect(plain.text()).toContain('Original body')
  })

  it('offers no toggle when the extraction is identical to the original', () => {
    // Identical bodies would make the toggle a no-op.
    const same = mountItemContent(makeItem({ content: '<p>Same</p>', extract_content: '<p>Same</p>' }))
    expect(same.find('[data-view-toggle]').exists()).toBe(false)
    expect(same.text()).toContain('Same')
  })

  it('offers no toggle when the original body is empty', () => {
    // HN/link posts carry no text: selecting "Original" would reveal nothing.
    const empty = mountItemContent(makeItem({ content: '' }))
    expect(empty.find('[data-view-toggle]').exists()).toBe(false)
    expect(empty.text()).toContain('Extracted body')
  })

  it('does not mutate the underlying item data when switching views', async () => {
    await viewOption(wrapper, 'original').trigger('click')
    const item = useFeedStore().selectedItem!
    expect(item.extract_content).toBe('<p>Extracted body</p>')
    expect(item.content).toBe('<p>Original body</p>')
    expect(item.extract_status).toBe('done')
  })

  it('keeps existing actions visible and functional while switched', async () => {
    await viewOption(wrapper, 'original').trigger('click')
    expect(wrapper.text()).toContain('Original body')
    expect(wrapper.text()).toContain('Mark Read')
    expect(wrapper.text()).toContain('Re-extract')
  })

  // A cleared extraction is not a view: the original is all there is.
  it('shows only the original for a cleared extraction, with no toggle', () => {
    const cleared = mountItemContent(makeItem({ extract_status: 'cleared' }))
    expect(cleared.find('[data-view-toggle]').exists()).toBe(false)
    expect(cleared.text()).toContain('Original body')
    expect(cleared.text()).not.toContain('Extracted body')
  })

  // 'skipped' means the server produced no body at all. The old header gated only
  // on "status is set", which drew a header over an empty body for 593 real items.
  it('shows only the original for a skipped extraction, with no toggle', () => {
    const skipped = mountItemContent(makeItem({ extract_status: 'skipped', extract_content: '' }))
    expect(skipped.find('[data-view-toggle]').exists()).toBe(false)
    expect(skipped.text()).toContain('Original body')
  })

  // The view is per-item UI state: carrying 'original' over from the previous
  // item would open the next item on the body extraction exists to replace.
  it('resets to the extracted view when a different item is opened', async () => {
    await viewOption(wrapper, 'original').trigger('click')
    expect(wrapper.text()).toContain('Original body')

    useFeedStore().selectedItem = makeItem({ id: 2, title: 'Another item' })
    await wrapper.vm.$nextTick()

    expect(wrapper.text()).toContain('Extracted body')
    expect(wrapper.text()).not.toContain('Original body')
    expect(checkedView(wrapper)).toBe('extracted')
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
    expect(html.indexOf('data-summary-box')).toBeLessThan(html.indexOf('data-view-toggle'))
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

    await viewOption(wrapper, 'original').trigger('click')
    expect(wrapper.text()).toContain('Original body')
    expect(wrapper.text()).not.toContain('Extracted body')

    const re = wrapper.findAll('button').find(b => b.text().trim() === 'Re-extract')!
    await re.trigger('click')
    await wrapper.vm.$nextTick()

    expect(wrapper.text()).toContain('Extracted body')
    expect(wrapper.text()).not.toContain('Original body')
    expect(checkedView(wrapper)).toBe('extracted')
  })

  it('returns to the original view when the extraction is deleted', async () => {
    const wrapper = mountItemContent(makeItem())
    await viewOption(wrapper, 'original').trigger('click')
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
    expect(wrapper.find('[data-view-toggle]').exists()).toBe(false)
    expect(wrapper.text()).toContain('Original body')
  })
})
