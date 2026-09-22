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
import { mount, flushPromises } from '@vue/test-utils'
import { createRouter, createMemoryHistory } from 'vue-router'
import { createPinia, setActivePinia } from 'pinia'
import ItemContent from './ItemContent.vue'
import { useFeedStore } from '../stores/feed'
import { absoluteTime, relativeTimeAgo } from '../lib/format'
import type { Feed, Item } from '../types/api'

// reka-ui's dropdown content measures itself with ResizeObserver, which jsdom
// does not provide. The Summarize control IS a dropdown trigger now, so the
// summary-box tests need this stub.
class ResizeObserverStub {
  observe() {}
  unobserve() {}
  disconnect() {}
}
vi.stubGlobal('ResizeObserver', ResizeObserverStub)

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
describe('ItemContent status control', () => {
  // The read/digest control is now an ICON before the title (reusing the mid pane's
  // conventions: CircleDot = unread, '◐' = digested, Circle = read) whose dropdown
  // offers only the transitions that change something.
  function statusButton(wrapper: ReturnType<typeof mountItemContent>) {
    return wrapper.find('[data-action-status]')
  }
  // The menu renders in a portal on document.body. Each item is [icon][label], so
  // read the LABEL span — textContent would include the icon glyph ('◐Digested').
  function statusItems(): string[] {
    const menus = document.body.querySelectorAll('[role="menu"]')
    // Only the last-opened menu: reka keeps a closed menu in the DOM through its
    // exit transition, so a plain query can match a previous test's menu too.
    const menu = menus[menus.length - 1]
    return Array.from(menu?.querySelectorAll('[role="menuitem"]') ?? []).map(
      i => (menuItemLabel(i)).trim(),
    )
  }
  async function openStatus(wrapper: ReturnType<typeof mountItemContent>) {
    document.body.querySelectorAll('[role="menu"]').forEach(m => m.setAttribute('data-stale', '1'))
    await statusButton(wrapper).trigger('click')
    await flushPromises()
  }
  beforeEach(() => { document.body.innerHTML = '' })

  // The status mark is a CHARACTER, not an icon in a padded button: it renders as a
  // text glyph in the headline's own size/line-height, so it shares the title's
  // baseline and reads as an initial. An SVG icon gets its own line box and sits
  // like a control beside the text — regressed twice, so it is pinned here.
  // Layout (baseline sharing, chevron geometry) is measured live in the browser;
  // jsdom has no layout engine, so this asserts the markup contract instead.
  it('renders the status as a text glyph, not an icon button', () => {
    const glyphs = { unread: '◉', digested: '◐', read: '◯' }
    for (const [status, char] of Object.entries(glyphs)) {
      const wrapper = mountItemContent(makeItem({ status }))
      const glyph = wrapper.find('[data-status-glyph]')
      expect(glyph.exists()).toBe(true)
      expect(glyph.text()).toBe(char)
      // Nothing between the glyph and the title but the control's own chevron.
      expect(wrapper.find('[data-action-status] svg').exists()).toBe(true)
    }
  })

  // The chevron must sit AFTER the glyph, in normal flow — not absolutely positioned
  // over it. `absolute` would mean overlap, which is what was rejected.
  it('puts the status chevron after the glyph in flow, not overlapping it', () => {
    const wrapper = mountItemContent(makeItem({ status: 'unread' }))
    const btn = statusButton(wrapper)
    const glyph = btn.find('[data-status-glyph]')
    const chevron = btn.find('svg')
    expect(glyph.exists()).toBe(true)
    expect(chevron.exists()).toBe(true)
    // Order in the DOM puts the chevron after the glyph.
    const kids = Array.from(btn.element.children)
    expect(kids.indexOf(chevron.element)).toBeGreaterThan(kids.indexOf(glyph.element))
    // No absolute positioning on either part of the control.
    expect(chevron.classes()).not.toContain('absolute')
    expect(glyph.classes()).not.toContain('absolute')
  })

  it('shows the unread convention and offers Read + Digested for an unread item', async () => {
    const wrapper = mountItemContent(makeItem({ status: 'unread' }))
    expect(statusButton(wrapper).attributes('title')).toBe('Status: unread')
    await openStatus(wrapper)
    expect(statusItems()).toEqual(['Read', 'Digested'])
  })

  it('shows the read convention and offers Unread + Digested for a read item', async () => {
    const wrapper = mountItemContent(makeItem({ status: 'read' }))
    expect(statusButton(wrapper).attributes('title')).toBe('Status: read')
    await openStatus(wrapper)
    expect(statusItems()).toEqual(['Unread', 'Digested'])
  })

  it('shows the digested convention and offers Read + Undigested for a digested item', async () => {
    const wrapper = mountItemContent(makeItem({ status: 'digested' }))
    expect(statusButton(wrapper).attributes('title')).toBe('Status: digested')
    await openStatus(wrapper)
    // "Undigested" is the transition back to unread — digested is a stored state,
    // so clearing it means returning the item to unread.
    expect(statusItems()).toEqual(['Read', 'Undigested'])
  })

  it('never offers the state the item is already in', async () => {
    for (const [status, own] of [['unread', 'Unread'], ['read', 'Read'], ['digested', 'Digested']] as const) {
      const wrapper = mountItemContent(makeItem({ status }))
      await openStatus(wrapper)
      expect(statusItems()).not.toContain(own)
      wrapper.unmount()
      document.body.innerHTML = ''
    }
  })

  it('writes the chosen status through the store', async () => {
    const wrapper = mountItemContent(makeItem({ status: 'unread' }))
    const store = useFeedStore()
    const writes: Array<string | undefined> = []
    store.updateItemStatus = async (_item: any, status?: string) => { writes.push(status) }

    await openStatus(wrapper)
    const dig = Array.from(document.body.querySelectorAll('[role="menuitem"]'))
      .find(i => menuItemLabel(i) === 'Digested') as HTMLElement
    dig.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    await flushPromises()
    expect(writes).toEqual(['digested'])
  })

  it('clears a digested item to unread via Undigested, not to read', async () => {
    const wrapper = mountItemContent(makeItem({ status: 'digested' }))
    const store = useFeedStore()
    const writes: Array<string | undefined> = []
    store.updateItemStatus = async (_item: any, status?: string) => { writes.push(status) }

    await openStatus(wrapper)
    const und = Array.from(document.body.querySelectorAll('[role="menuitem"]'))
      .find(i => menuItemLabel(i) === 'Undigested') as HTMLElement
    und.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    await flushPromises()
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
  it('carries the extraction date on the toggle tooltip, only in the extracted view', async () => {
    // The date is a HOVER on the view toggle rather than a line in the header: it
    // only matters while the extracted body is the one on screen, so it costs no
    // permanent header space. Hence a title attribute, not a visible element.
    const stamp = `Extracted ${absoluteTime('2026-08-13T08:00:00Z')}`
    // The title sits on the toggle's bordered WRAPPER, which also holds the
    // extraction opener — so both controls share the one hover.
    const tip = () => viewToggle(wrapper).element.parentElement!.getAttribute('title') ?? undefined
    expect(tip()).toBe(stamp)

    await viewOption(wrapper, 'original').trigger('click')
    expect(tip()).toBeUndefined()

    await viewOption(wrapper, 'extracted').trigger('click')
    expect(tip()).toBe(stamp)
  })

  it('attaches the extraction menu opener to the view toggle', () => {
    // Re-extract and Delete act on the extracted body, so they hang off the toggle
    // that selects it instead of occupying their own header buttons.
    const wrap = viewToggle(wrapper).element.parentElement!
    expect(wrap.querySelector('[data-extraction-opener]')).not.toBeNull()
    // Same bordered container, so the opener reads as part of the toggle.
    expect(wrap.className).toContain('rounded-md')
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
    // Actions stay in the header row, still enabled, in both views.
    // The status control and the extraction menu survive the view switch.
    expect(wrapper.find('[data-action-status]').exists()).toBe(true)
    expect(wrapper.find('[data-extraction-opener]').attributes('disabled')).toBeUndefined()
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
    // Feed link sits in the metadata row alongside the relative time, which is
    // now phrased as an interval ("1mo ago") rather than a full timestamp.
    expect(wrapper.text()).toContain(relativeTimeAgo(makeItem().published_at))
    // The absolute timestamp is still available on hover.
    const time = wrapper.findAll('span').find(sp => sp.attributes('title') === absoluteTime(makeItem().published_at))
    expect(time, 'the relative time keeps an absolute title').toBeTruthy()
  })

  it('renders no feed link when the item feed is unknown', () => {
    const wrapper = mountItemContent(makeItem({ feed_id: 99 }), [makeFeed({ id: 1 })])
    expect(wrapper.find('a[title="Open feed"]').exists()).toBe(false)
    expect(wrapper.text()).not.toContain('Example Feed')
  })

  it('keeps the relative timestamp format when a feed link is present', () => {
    const wrapper = mountItemContent(makeItem(), [makeFeed()])
    expect(wrapper.text()).toContain(relativeTimeAgo(makeItem().published_at))
  })
})

describe('ItemContent LLM summary box', () => {
  let wrapper: ReturnType<typeof mountItemContent>

  beforeEach(() => {
    summarizeMock.mockReset()
    summarizeMock.mockResolvedValue({ content_summary: 'A summary.', comments_summary: '' })
    wrapper = mountItemContent(makeItem())
  })

  // Summary is a button plus a small opener: the button runs at the CURRENT length,
  // the opener picks a preset. The menu renders in a portal on document.body, so
  // read the live DOM rather than the wrapper.
  async function summarize() {
    await wrapper.find('[data-summary-length-opener]').trigger('click')
    await flushPromises()
    const menus = document.body.querySelectorAll('[role="menu"]')
    const menu = menus[menus.length - 1]
    // Each item renders [label][detail], so match the label with startsWith rather
    // than grabbing span:last-child (which is the detail text).
    const item = Array.from(menu?.querySelectorAll('[role="menuitem"]') ?? [])
      .find(el => menuItemLabel(el) === 'Medium') as HTMLElement | undefined
    if (!item) throw new Error('summary length item "Medium" not found')
    item.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    await flushPromises()
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
    // The box sits above the content body. (The view toggle now lives up in the
    // header, so it is no longer the anchor for "above the body" — the prose is.)
    const html = wrapper.html()
    expect(html.indexOf('data-summary-box')).toBeLessThan(html.indexOf('class="prose'))
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

    await wrapper.find('[data-extraction-opener]').trigger('click')
    await flushPromises()
    const re = Array.from(document.body.querySelectorAll('[role="menuitem"]'))
      .find(i => (i.querySelector('span:not([class*=text-xs])')?.textContent ?? '').trim() === 'Re-extract') as HTMLElement
    re.dispatchEvent(new MouseEvent('click', { bubbles: true }))
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
    await wrapper.find('[data-extraction-opener]').trigger('click')
    await flushPromises()
    const del = Array.from(document.body.querySelectorAll('[role="menuitem"]'))
      .find(i => (i.querySelector('span:not([class*=text-xs])')?.textContent ?? '').trim() === 'Delete extracted') as HTMLElement
    del.dispatchEvent(new MouseEvent('click', { bubbles: true }))

    expect(deleted).toBe(true)
    await wrapper.vm.$nextTick()
    // With the extraction gone the pane shows the original and offers no toggle.
    expect(wrapper.find('[data-view-toggle]').exists()).toBe(false)
    expect(wrapper.text()).toContain('Original body')
  })
})
