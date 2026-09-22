import { describe, it, expect, beforeEach, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createRouter, createMemoryHistory } from 'vue-router'
import { createPinia, setActivePinia } from 'pinia'
import ItemContent from './ItemContent.vue'
import { scrollToItemComments } from '../lib/itemComments'
import { useFeedStore } from '../stores/feed'
import type { Feed, Item } from '../types/api'

// Mock the itemApi module so summarize calls go through our mock.
const mockSummarize = vi.fn()
const mockGetComments = vi.fn()
vi.mock('../services/api', () => ({
  itemApi: {
    getItem: vi.fn().mockResolvedValue({}),
    extract: vi.fn().mockResolvedValue({}),
    summarize: (...args: any[]) => mockSummarize(...args),
    getComments: (...args: any[]) => mockGetComments(...args),
    updateStatus: vi.fn().mockResolvedValue({}),
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

// The menu renders in a portal on document.body, so read the live DOM rather
// than the wrapper (see AccountMenu.test.ts for the same pattern).
function summaryLengthItems(): HTMLElement[] {
  return Array.from(document.body.querySelectorAll('[role="menuitem"]')) as HTMLElement[]
}

// Summary is a button plus a small opener: the button runs at the CURRENT length,
// the opener picks a different preset. So opening the length menu presses the opener.
async function openSummaryLengthMenu(wrapper: ReturnType<typeof mount>) {
  const trigger = wrapper.find('[data-summary-length-opener]')
  expect(trigger.exists()).toBe(true)
  await trigger.trigger('click')
  await flushPromises()
}

// Picking a preset runs the summary at that length, so "summarize at the default"
// = open the opener and pick Medium.
async function summarizeVia(wrapper: ReturnType<typeof mount>, label = 'Medium') {
  await openSummaryLengthMenu(wrapper)
  await clickSummaryLengthItem(label)
  await flushPromises()
}

async function clickSummaryLengthItem(label: string) {
  const item = summaryLengthItems().find(i => i.textContent?.includes(label))
  if (!item) throw new Error(`summary length item not found: ${label}`)
  item.dispatchEvent(new MouseEvent('click', { bubbles: true }))
  await flushPromises()
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
    category: 'tech',
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

describe('ItemContent scroll-to-comments', () => {
  it('shows the scroll-to-comments arrow when comments are loaded', async () => {
    const wrapper = mountItemContent(makeItem({ comment_url: 'https://example.com/comments' }))

    // Simulate loaded comments by setting the component's topComments directly.
    const vm = wrapper.vm as any
    vm.topComments = [
      { author: 'alice', text: 'Great post!', points: 5, valid_points: true, url: '' },
    ]
    await wrapper.vm.$nextTick()

    const scrollBtn = wrapper.find('a[title="Scroll to comments (c)"]')
    expect(scrollBtn.exists()).toBe(true)
  })

  it('hides the scroll-to-comments arrow when no comments', () => {
    const wrapper = mountItemContent(makeItem({ comment_url: '', comments_count: 0 }))
    const scrollBtn = wrapper.find('a[title="Scroll to comments (c)"]')
    expect(scrollBtn.exists()).toBe(false)
  })

  it('exposes scroll-to-comments via the shared handle, and clears it on unmount', async () => {
    const scrollIntoView = vi.fn()
    Element.prototype.scrollIntoView = scrollIntoView

    const wrapper = mountItemContent(makeItem())
    await wrapper.vm.$nextTick()

    scrollToItemComments()
    await wrapper.vm.$nextTick()
    expect(scrollIntoView).toHaveBeenCalled()

    wrapper.unmount()
    scrollIntoView.mockClear()
    scrollToItemComments()
    await wrapper.vm.$nextTick()
    expect(scrollIntoView, 'an unmounted pane must not be scrolled').not.toHaveBeenCalled()
  })

  it('renders the comments section with data-comments-section attribute', () => {
    const wrapper = mountItemContent(makeItem())
    const section = wrapper.find('[data-comments-section]')
    expect(section.exists()).toBe(true)
  })

  it('judges each URL occurrence by its own preceding character', async () => {
    // Regression: the skip-if-already-in-an-attribute check used
    // restored.indexOf(match) — the FIRST occurrence of the URL in the whole
    // string. The same URL appearing twice was therefore judged twice by the
    // prefix of its first copy, so the bare second mention was left unlinked.
    const url = 'https://example.com/article'
    const wrapper = mountItemContent(makeItem({ comment_url: 'https://example.com/c' }))
    const vm = wrapper.vm as any
    // Mounting with a comment-bearing item now starts the on-demand fetch, whose
    // in-flight state renders skeletons in place of the list. Clear it so the
    // injected comments are what gets rendered.
    vm.commentsLoading = false
    vm.topComments = [
      {
        author: 'alice',
        // Apostrophes survive the escaping pass, so they still count as a
        // "already inside an attribute" prefix. The quoted mention is skipped;
        // the bare mention after it must be linked.
        text: `'${url}' and again ${url}`,
        points: 5,
        valid_points: true,
        url: '',
      },
    ]
    await wrapper.vm.$nextTick()

    const links = wrapper.findAll(`a[href="${url}"]`)
    expect(links.length, 'the bare second mention should be linked exactly once').toBe(1)

    // The anchor wraps the bare mention, not the quoted one.
    expect(wrapper.html()).toContain(`and again <a href="${url}"`)
  })

  // Regression: HomeView swaps between the desktop and tablet/mobile layout
  // branches, which mounts the replacement ItemContent before unmounting the
  // outgoing one. The outgoing instance's cleanup used to clear the shared
  // scroll handle unconditionally, which left `c` dead while a pane was
  // visibly on screen.
  it('keeps the c shortcut working after a stale pane unmounts', async () => {
    const scrollSpy = vi.fn()
    ;(HTMLElement.prototype as any).scrollIntoView = scrollSpy

    const outgoing = mountItemContent(makeItem({ id: 1 }))
    const live = mountItemContent(makeItem({ id: 2 }))
    await live.vm.$nextTick()

    outgoing.unmount()
    scrollSpy.mockClear()

    scrollToItemComments()
    await live.vm.$nextTick()
    await live.vm.$nextTick()

    expect(scrollSpy, 'the surviving pane must still scroll after a stale unmount').toHaveBeenCalled()

    live.unmount()
    delete (HTMLElement.prototype as any).scrollIntoView
  })
})

describe('ItemContent summarize button', () => {
  beforeEach(() => {
    mockSummarize.mockReset()
    // Dropdown portals from earlier tests survive their wrapper, and a stale
    // duplicate would swallow clicks meant for the current menu.
    document.body.innerHTML = ''
  })

  it('renders a Summarize button', () => {
    const wrapper = mountItemContent(makeItem())
    const btn = wrapper.find('[data-action-summarize]')
    expect(btn.exists()).toBe(true)
    // Icon-only: the label lives on the accessible name, and it names the CURRENT
    // length so the choice is visible without opening the menu.
    expect(btn.attributes('aria-label')).toBe('Summarize (Medium)')
  })

  it('shows summary section after summarizing', async () => {
    mockSummarize.mockResolvedValue({
      content_summary: 'This is the content summary.',
      comments_summary: 'These are the comment summaries.',
    })

    const wrapper = mountItemContent(makeItem())
    await summarizeVia(wrapper)

    // Wait for the async mock to resolve and Vue to re-render.
    await vi.dynamicImportSettled()
    await wrapper.vm.$nextTick()
    await wrapper.vm.$nextTick()

    expect(mockSummarize).toHaveBeenCalledWith(1, { sentence_count: 7, min_sentences: 5, max_sentences: 7, detail: 'balanced' })

    // The summary section should be visible.
    expect(wrapper.text()).toContain('LLM Summary')
    expect(wrapper.text()).toContain('This is the content summary.')
    expect(wrapper.text()).toContain('These are the comment summaries.')
    // No per-section subtitles any more: the summaries render as two plain
    // paragraphs.
    const paras = wrapper.findAll('p').map(p => p.text())
    expect(paras).toContain('This is the content summary.')
    expect(paras).toContain('These are the comment summaries.')
    expect(paras).not.toContain('Content')
    expect(paras).not.toContain('Comments')

    // The summary is a dismissible box above the body; it no longer folds the
    // bodies away, because there is now a single body section rather than two
    // collapsible ones.
    expect(wrapper.find('[data-summary-box]').exists()).toBe(true)
    expect(wrapper.find('[data-summary-box] [aria-expanded]').exists()).toBe(false)
  })

  it('splits into a run button and a length opener', async () => {
    mockSummarize.mockResolvedValue({ content_summary: 'Summary.', comments_summary: 'Comments.' })

    const wrapper = mountItemContent(makeItem())
    expect(wrapper.find('input[type="range"]').exists()).toBe(false)
    // Two controls: the button runs at the current length, the opener picks one.
    const run = wrapper.find('[data-action-summarize]')
    const opener = wrapper.find('[data-summary-length-opener]')
    expect(run.exists()).toBe(true)
    expect(opener.exists()).toBe(true)
    expect(run.attributes('aria-label')).toBe('Summarize (Medium)')

    // The RUN button summarizes at the current length without opening a menu.
    await run.trigger('click')
    await flushPromises()
    expect(mockSummarize).toHaveBeenCalledWith(1, { sentence_count: 7, min_sentences: 5, max_sentences: 7, detail: 'balanced' })
  })

  it('summarizes at medium length by default', async () => {
    mockSummarize.mockResolvedValue({ content_summary: 'Summary.', comments_summary: 'Comments.' })

    const wrapper = mountItemContent(makeItem())
    // Summarize is one control: pressing it opens its menu, and picking a length
    // runs the summary at that length. So the default flow is press -> Medium.
    await summarizeVia(wrapper)

    expect(mockSummarize).toHaveBeenCalledWith(1, { sentence_count: 7, min_sentences: 5, max_sentences: 7, detail: 'balanced' })
  })

  it('keeps the length choices hidden until the dropdown is opened', async () => {
    mockSummarize.mockResolvedValue({ content_summary: 'Summary.', comments_summary: 'Comments.' })

    const wrapper = mountItemContent(makeItem())
    expect(wrapper.text()).not.toContain('sentences')
    expect(summaryLengthItems()).toHaveLength(0)

    await openSummaryLengthMenu(wrapper)
    const items = summaryLengthItems().map(i => i.textContent ?? '')
    expect(items).toHaveLength(3)
    expect(items[0]).toContain('Short')
    expect(items[0]).toContain('1-2 sentences')
    expect(items[1]).toContain('Medium')
    expect(items[1]).toContain('5-7 sentences')
    expect(items[2]).toContain('Long')
    expect(items[2]).toContain('12-18 sentences')
  })

  // The point of the presets: each asks for a different kind of summary, not
  // the same one at three lengths. If two levels ever send the same detail, the
  // three choices stop meaning anything.
  it('sends a distinct detail level for each preset', async () => {
    mockSummarize.mockResolvedValue({ content_summary: 'Summary.', comments_summary: 'Comments.' })

    const levels: Record<string, unknown> = {}
    for (const label of ['Short', 'Medium', 'Long']) {
      mockSummarize.mockClear()
      const wrapper = mountItemContent(makeItem())
      await openSummaryLengthMenu(wrapper)
      clickSummaryLengthItem(label)
      await flushPromises()
      await wrapper.find('[data-action-summarize]').trigger('click')
      await flushPromises()
      const calls = mockSummarize.mock.calls
      const opts = (calls[calls.length - 1]?.[1] ?? {}) as Record<string, unknown>
      levels[label] = opts
    }

    const details = Object.values(levels).map(o => (o as { detail?: string }).detail)
    expect(details).toEqual(['gist', 'balanced', 'detailed'])
    // Ranges must be strictly increasing, so Short < Medium < Long holds.
    const maxes = Object.values(levels).map(o => (o as { max_sentences: number }).max_sentences)
    expect(maxes[0]).toBeLessThan(maxes[1])
    expect(maxes[1]).toBeLessThan(maxes[2])
  })

  it('marks the selected length only inside the dropdown', async () => {
    mockSummarize.mockResolvedValue({ content_summary: 'Summary.', comments_summary: 'Comments.' })

    const wrapper = mountItemContent(makeItem())
    await openSummaryLengthMenu(wrapper)

    const checked = summaryLengthItems().filter(i => i.hasAttribute('data-checked'))
    expect(checked).toHaveLength(1)
    expect(checked[0].textContent).toContain('Medium')
    // The trigger itself carries no length label.
    expect(wrapper.find('[data-action-summarize]').attributes('aria-label')).toBe('Summarize (Medium)')
  })

  it('re-summarizes at the chosen length when a preset is picked', async () => {
    mockSummarize.mockResolvedValue({ content_summary: 'Summary.', comments_summary: 'Comments.' })

    const wrapper = mountItemContent(makeItem())
    await summarizeVia(wrapper)
    expect(mockSummarize).toHaveBeenCalledTimes(1)

    await openSummaryLengthMenu(wrapper)
    await clickSummaryLengthItem('Long')

    expect(mockSummarize).toHaveBeenCalledTimes(2)
    expect(mockSummarize).toHaveBeenLastCalledWith(1, { sentence_count: 18, min_sentences: 12, max_sentences: 18, detail: 'detailed' })
  })

  it('re-runs the summary when the current length is picked again', async () => {
    mockSummarize.mockResolvedValue({ content_summary: 'Summary.', comments_summary: 'Comments.' })

    const wrapper = mountItemContent(makeItem())
    await summarizeVia(wrapper)
    expect(mockSummarize).toHaveBeenCalledTimes(1)

    // Re-picking the SAME length must re-run: with the menu as the only control,
    // an early return on an unchanged choice would leave no way to ask again.
    await openSummaryLengthMenu(wrapper)
    await clickSummaryLengthItem('Medium')
    expect(mockSummarize).toHaveBeenCalledTimes(2)
    expect(mockSummarize).toHaveBeenLastCalledWith(1, { sentence_count: 7, min_sentences: 5, max_sentences: 7, detail: 'balanced' })

    // A different length still routes through the picker and runs at that length.
    await openSummaryLengthMenu(wrapper)
    await clickSummaryLengthItem('Short')
    expect(mockSummarize).toHaveBeenLastCalledWith(1, { sentence_count: 2, min_sentences: 1, max_sentences: 2, detail: 'gist' })
  })

  it('resets summary when item changes', async () => {
    mockSummarize.mockResolvedValue({
      content_summary: 'Summary.',
      comments_summary: 'Comments.',
    })

    const wrapper = mountItemContent(makeItem())
    await summarizeVia(wrapper)
    await vi.dynamicImportSettled()
    await wrapper.vm.$nextTick()
    await wrapper.vm.$nextTick()

    expect(wrapper.text()).toContain('LLM Summary')

    // Switch to a different item.
    useFeedStore().selectedItem = makeItem({ id: 2, title: 'Other' })
    await wrapper.vm.$nextTick()

    // Summary should be gone.
    expect(wrapper.text()).not.toContain('LLM Summary')
  })

  // The summary box's dismissed state is per-item: closing it on one item must
  // not open the next item with its summary already hidden.
  it('does not carry the dismissed summary into the next item', async () => {
    mockSummarize.mockResolvedValue({
      content_summary: 'Summary.',
      comments_summary: 'Comments.',
    })

    const wrapper = mountItemContent(makeItem({ id: 1 }))
    await summarizeVia(wrapper)
    await vi.dynamicImportSettled()
    await wrapper.vm.$nextTick()
    await wrapper.vm.$nextTick()

    // Dismiss the box on the item it was generated for.
    await wrapper.find('[data-summary-close]').trigger('click')
    expect(wrapper.find('[data-summary-box]').exists()).toBe(false)

    useFeedStore().selectedItem = makeItem({
      id: 2,
      title: 'Other',
      content: '<p>Other original body</p>',
      extract_content: '<p>Other extracted body</p>',
    })
    await wrapper.vm.$nextTick()
    await wrapper.vm.$nextTick()

    // The next item's body is shown, with no stale dismissal to hide it.
    expect(wrapper.text()).toContain('Other extracted body')
    expect(wrapper.text()).not.toContain('Other original body')
  })

  it('hides the summary box when its close button is pressed', async () => {
    mockSummarize.mockResolvedValue({
      content_summary: 'The summary text.',
      comments_summary: 'Comment summary.',
    })

    const wrapper = mountItemContent(makeItem())
    await summarizeVia(wrapper)
    await vi.dynamicImportSettled()
    await wrapper.vm.$nextTick()
    await wrapper.vm.$nextTick()

    expect(wrapper.text()).toContain('The summary text.')

    // The box is dismissed with its close button, not a collapsible header.
    const close = wrapper.find('[data-summary-close]')
    expect(close.exists()).toBe(true)
    await close.trigger('click')
    await wrapper.vm.$nextTick()

    expect(wrapper.text()).not.toContain('The summary text.')
  })
})

describe('ItemContent comment fetch on mount', () => {
  // HomeView's tablet/mobile branch wraps this pane in a keyed <Transition>,
  // so selecting an item mounts a NEW instance with the item already selected.
  // The watcher therefore sees the item as its initial value, never as a change.
  const commentUrl = 'https://www.reddit.com/r/hu/comments/1we9rs8/slug/'

  beforeEach(() => {
    mockGetComments.mockReset()
    mockGetComments.mockResolvedValue({
      comments: [{ author: 'alice', text: 'Great post!', points: 3, valid_points: true, url: '' }],
      cached: false,
    })
  })

  it('fetches comments for the item the pane is mounted with', async () => {
    const wrapper = mountItemContent(makeItem({ id: 42, comment_url: commentUrl, comments_count: 7 }))

    await vi.waitFor(() => expect(mockGetComments).toHaveBeenCalled())
    expect(mockGetComments).toHaveBeenCalledWith(42, { limit: 5, refresh: false })
    expect(wrapper.text()).toContain('alice')
  })

  it('still fetches when the selection changes after mount', async () => {
    const wrapper = mountItemContent(makeItem({ id: 42, comment_url: commentUrl, comments_count: 7 }))
    await vi.waitFor(() => expect(mockGetComments).toHaveBeenCalledTimes(1))

    useFeedStore().selectedItem = makeItem({ id: 43, comment_url: commentUrl, comments_count: 9 })
    await vi.waitFor(() => expect(mockGetComments).toHaveBeenCalledTimes(2))
    expect(mockGetComments).toHaveBeenLastCalledWith(43, { limit: 5, refresh: false })
    expect(wrapper.text()).toContain('alice')
  })

  it('does not fetch when mounted with no selection', async () => {
    const wrapper = mountItemContent(null)

    // Give the mount-time path longer than its 200ms debounce to fire.
    await new Promise(resolve => setTimeout(resolve, 300))
    await flushPromises()

    expect(mockGetComments).not.toHaveBeenCalled()
    expect(wrapper.text()).toContain('Select an item')
  })

  // Per-item comment state again: switching to an item that has no comments
  // must both cancel the in-flight request for the previous item and clear the
  // loading flag, or the new item inherits a stale skeleton (and, once the old
  // response lands, the old item's comments).
  it('clears in-flight comments when switching to an item without any', async () => {
    let resolvePrevious: (value: any) => void = () => {}
    mockGetComments.mockReturnValueOnce(
      new Promise(resolve => { resolvePrevious = resolve }),
    )

    const wrapper = mountItemContent(makeItem({ id: 42, comment_url: commentUrl, comments_count: 7 }))
    // Let the debounce fire so the request is genuinely in flight.
    await new Promise(resolve => setTimeout(resolve, 300))
    await flushPromises()
    expect(mockGetComments).toHaveBeenCalledTimes(1)

    // Switch to an item with no comment source at all.
    useFeedStore().selectedItem = makeItem({ id: 43, comment_url: '', comments_count: 0 })
    await flushPromises()

    // No skeleton section left behind.
    expect(wrapper.text()).not.toContain('Top Comments')

    // And the late response for the previous item must not render here.
    resolvePrevious({
      comments: [{ author: 'alice', text: 'Great post!', points: 3, valid_points: true, url: '' }],
      cached: false,
    })
    await flushPromises()

    expect(wrapper.text()).not.toContain('alice')
    expect(wrapper.text()).not.toContain('Top Comments')
  })
})
