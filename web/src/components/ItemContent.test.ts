import { describe, it, expect, beforeEach } from 'vitest'
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
    last_fetched_at: null,
    status: 'ok',
    extract_content: 0,
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

function toggleButtons(wrapper: ReturnType<typeof mountItemContent>) {
  return wrapper.findAll('button').filter(b => {
    const t = b.text().trim()
    return t.startsWith('Extracted') || t.startsWith('Original')
  })
}

describe('ItemContent collapsible blocks', () => {
  let wrapper: ReturnType<typeof mountItemContent>

  beforeEach(() => {
    wrapper = mountItemContent(makeItem())
  })

  it('shows a toggle control on both the extracted and original blocks', () => {
    const toggles = toggleButtons(wrapper)
    expect(toggles[0].text()).toContain('Extracted')
    expect(toggles[1].text()).toBe('Original')
    // Both blocks are expanded by default.
    expect(wrapper.text()).toContain('Extracted body')
    expect(wrapper.text()).toContain('Original body')
  })

  it('collapses the extracted block body while keeping its header visible', async () => {
    const [extracted] = toggleButtons(wrapper)
    await extracted.trigger('click')

    expect(wrapper.text()).not.toContain('Extracted body')
    expect(wrapper.text()).toContain('Original body')
    // Header stays visible and still toggles back.
    const [extractedAgain] = toggleButtons(wrapper)
    expect(extractedAgain.text()).toContain('Extracted')
    await extractedAgain.trigger('click')
    expect(wrapper.text()).toContain('Extracted body')
  })

  it('collapses the original block body independently', async () => {
    const [, original] = toggleButtons(wrapper)
    await original.trigger('click')

    expect(wrapper.text()).toContain('Extracted body')
    expect(wrapper.text()).not.toContain('Original body')
  })

  it('keeps collapsed state per block without affecting the other', async () => {
    const [extracted, original] = toggleButtons(wrapper)
    await extracted.trigger('click')
    await original.trigger('click')

    expect(wrapper.text()).not.toContain('Extracted body')
    expect(wrapper.text()).not.toContain('Original body')

    // Expanding extracted leaves original collapsed.
    const [extractedAgain] = toggleButtons(wrapper)
    await extractedAgain.trigger('click')
    expect(wrapper.text()).toContain('Extracted body')
    expect(wrapper.text()).not.toContain('Original body')
  })

  it('does not mutate the underlying item data when collapsing', async () => {
    const [extracted] = toggleButtons(wrapper)
    await extracted.trigger('click')
    const item = useFeedStore().selectedItem!
    expect(item.extract_content).toBe('<p>Extracted body</p>')
    expect(item.content).toBe('<p>Original body</p>')
    expect(item.extract_status).toBe('done')
  })

  it('keeps existing actions visible and functional while expanded', async () => {
    expect(wrapper.text()).toContain('Extracted body')
    expect(wrapper.text()).toContain('Mark Read')
    expect(wrapper.text()).toContain('Re-extract')
  })

  it('renders raw content without toggle controls when no extraction exists', () => {
    const plain = mountItemContent(makeItem({ extract_status: '' }))
    expect(plain.text()).toContain('Original body')
    expect(toggleButtons(plain).length).toBe(0)
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
