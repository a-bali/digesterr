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
vi.mock('../services/api', () => ({
  itemApi: {
    getItem: vi.fn().mockResolvedValue({}),
    extract: vi.fn().mockResolvedValue({}),
    summarize: (...args: any[]) => mockSummarize(...args),
    getComments: vi.fn().mockResolvedValue({ comments: [], cached: false }),
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

async function openSummaryLengthMenu(wrapper: ReturnType<typeof mount>) {
  const trigger = wrapper.find('[aria-label="Summary length"]')
  expect(trigger.exists()).toBe(true)
  await trigger.trigger('click')
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

    const scrollBtn = wrapper.find('button[title="Scroll to comments (c)"]')
    expect(scrollBtn.exists()).toBe(true)
  })

  it('hides the scroll-to-comments arrow when no comments', () => {
    const wrapper = mountItemContent(makeItem({ comment_url: '', comments_count: 0 }))
    const scrollBtn = wrapper.find('button[title="Scroll to comments (c)"]')
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
    const btn = wrapper.findAll('button').find(b => b.text().includes('Summarize'))
    expect(btn).toBeDefined()
    expect(btn!.text()).toContain('Summarize')
  })

  it('shows summary section after summarizing', async () => {
    mockSummarize.mockResolvedValue({
      content_summary: 'This is the content summary.',
      comments_summary: 'These are the comment summaries.',
    })

    const wrapper = mountItemContent(makeItem())
    const btn = wrapper.findAll('button').find(b => b.text().includes('Summarize'))!
    await btn.trigger('click')

    // Wait for the async mock to resolve and Vue to re-render.
    await vi.dynamicImportSettled()
    await wrapper.vm.$nextTick()
    await wrapper.vm.$nextTick()

    expect(mockSummarize).toHaveBeenCalledWith(1, { sentence_count: 5, min_sentences: 4, max_sentences: 5 })

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

    // The bodies the summary was built from fold away automatically.
    const collapsedState = (label: string) =>
      wrapper.findAll('button').find(b => b.text().startsWith(label))?.attributes('aria-expanded')
    expect(collapsedState('Extracted')).toBe('false')
    expect(collapsedState('Original')).toBe('false')
  })

  it('has no length slider any more', async () => {
    mockSummarize.mockResolvedValue({ content_summary: 'Summary.', comments_summary: 'Comments.' })

    const wrapper = mountItemContent(makeItem())
    const btn = wrapper.findAll('button').find(b => b.text().includes('Summarize'))!
    await btn.trigger('click')
    await vi.dynamicImportSettled()
    await wrapper.vm.$nextTick()
    await wrapper.vm.$nextTick()

    expect(wrapper.find('input[type="range"]').exists()).toBe(false)
  })

  it('summarizes at medium length by default', async () => {
    mockSummarize.mockResolvedValue({ content_summary: 'Summary.', comments_summary: 'Comments.' })

    const wrapper = mountItemContent(makeItem())
    const btn = wrapper.findAll('button').find(b => b.text().includes('Summarize'))!
    await btn.trigger('click')
    await flushPromises()

    expect(mockSummarize).toHaveBeenCalledWith(1, { sentence_count: 5, min_sentences: 4, max_sentences: 5 })
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
    expect(items[0]).toContain('2-3 sentences')
    expect(items[1]).toContain('Medium')
    expect(items[1]).toContain('4-5 sentences')
    expect(items[2]).toContain('Long')
    expect(items[2]).toContain('10-15 sentences')
  })

  it('marks the selected length only inside the dropdown', async () => {
    mockSummarize.mockResolvedValue({ content_summary: 'Summary.', comments_summary: 'Comments.' })

    const wrapper = mountItemContent(makeItem())
    await openSummaryLengthMenu(wrapper)

    const checked = summaryLengthItems().filter(i => i.hasAttribute('data-checked'))
    expect(checked).toHaveLength(1)
    expect(checked[0].textContent).toContain('Medium')
    // The trigger itself carries no length label.
    expect(wrapper.findAll('button').find(b => b.text().includes('Summarize'))!.text()).toBe('Summarize')
  })

  it('re-summarizes at the chosen length when a preset is picked', async () => {
    mockSummarize.mockResolvedValue({ content_summary: 'Summary.', comments_summary: 'Comments.' })

    const wrapper = mountItemContent(makeItem())
    const btn = wrapper.findAll('button').find(b => b.text().includes('Summarize'))!
    await btn.trigger('click')
    await flushPromises()
    expect(mockSummarize).toHaveBeenCalledTimes(1)

    await openSummaryLengthMenu(wrapper)
    await clickSummaryLengthItem('Long')

    expect(mockSummarize).toHaveBeenCalledTimes(2)
    expect(mockSummarize).toHaveBeenLastCalledWith(1, { sentence_count: 15, min_sentences: 10, max_sentences: 15 })
  })

  it('remembers the picked length for the next summary without refetching before one exists', async () => {
    mockSummarize.mockResolvedValue({ content_summary: 'Summary.', comments_summary: 'Comments.' })

    const wrapper = mountItemContent(makeItem())
    await openSummaryLengthMenu(wrapper)
    await clickSummaryLengthItem('Short')
    // No summary yet, so picking a length must not fire a request.
    expect(mockSummarize).not.toHaveBeenCalled()

    await openSummaryLengthMenu(wrapper)
    const checked = summaryLengthItems().filter(i => i.hasAttribute('data-checked'))
    expect(checked).toHaveLength(1)
    expect(checked[0].textContent).toContain('Short')

    const btn = wrapper.findAll('button').find(b => b.text().includes('Summarize'))!
    await btn.trigger('click')
    await flushPromises()
    expect(mockSummarize).toHaveBeenCalledWith(1, { sentence_count: 3, min_sentences: 2, max_sentences: 3 })
  })

  it('resets summary when item changes', async () => {
    mockSummarize.mockResolvedValue({
      content_summary: 'Summary.',
      comments_summary: 'Comments.',
    })

    const wrapper = mountItemContent(makeItem())
    const btn = wrapper.findAll('button').find(b => b.text().includes('Summarize'))!
    await btn.trigger('click')
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

  it('collapses summary section on toggle', async () => {
    mockSummarize.mockResolvedValue({
      content_summary: 'The summary text.',
      comments_summary: 'Comment summary.',
    })

    const wrapper = mountItemContent(makeItem())
    const btn = wrapper.findAll('button').find(b => b.text().includes('Summarize'))!
    await btn.trigger('click')
    await vi.dynamicImportSettled()
    await wrapper.vm.$nextTick()
    await wrapper.vm.$nextTick()

    // Verify summary is visible.
    expect(wrapper.text()).toContain('The summary text.')

    // Click the LLM Summary toggle to collapse.
    const toggle = wrapper.findAll('button').find(b => b.text().includes('LLM Summary'))!
    expect(toggle.exists()).toBe(true)
    await toggle.trigger('click')
    await wrapper.vm.$nextTick()

    expect(wrapper.text()).not.toContain('The summary text.')
    // Header should still be visible.
    expect(wrapper.text()).toContain('LLM Summary')
  })
})
