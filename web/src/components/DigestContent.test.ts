import { describe, it, expect, beforeEach } from 'vitest'
import { mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createRouter, createMemoryHistory } from 'vue-router'
import DigestContent from './DigestContent.vue'
import { useDigestStore } from '../stores/digest'
import { absoluteTime } from '../lib/format'
import type { DigestResult } from '../types/api'

const content = JSON.stringify({
  title: 'Go Time',
  summary: 'Test summary',
  highlights: [
    {
      item_id: 1,
      feed_id: 42,
      feed_name: 'HN',
      title: 'Highlight One',
      url: 'https://example.com/1',
      comment_url: 'https://news.ycombinator.com/item?id=1',
      points: 100,
      comments_count: 30,
      published_at: '2026-08-13T07:00:00Z',
      llm_summary: 'A summary.',
      top_comments: [
        { author: 'alice', text: 'Great insight', points: 5, url: 'https://news.ycombinator.com/item?id=10' },
        { author: 'bob', text: 'Without url' },
      ],
    },
  ],
  items: [
    {
      item_id: 2,
      feed_id: 42,
      feed_name: 'HN',
      title: 'Item One',
      url: 'https://example.com/2',
      comment_url: '',
      points: 5,
      comments_count: 2,
      published_at: '2026-08-13T06:00:00Z',
    },
    {
      item_id: 3,
      feed_id: 7,
      feed_name: 'Reddit',
      title: 'Item Two',
      url: 'https://example.com/3',
      comment_url: '',
      points: 3,
      comments_count: 1,
      published_at: '2026-08-13T05:00:00Z',
    },
  ],
})

function makeResult(): DigestResult {
  return {
    id: 1,
    user_id: 1,
    config_id: 1,
    config_snapshot: '{}',
    content,
    status: 'new',
    created_at: '2026-08-13T08:00:00Z',
    updated_at: '2026-08-13T08:00:00Z',
  }
}

function mountDigestContent() {
  const pinia = createPinia()
  setActivePinia(pinia)
  useDigestStore().selectedResult = makeResult()

  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/', component: { template: '<div />' } },
      { path: '/feeds/:feedId(\\d+)/:itemId(\\d+)?', component: { template: '<div />' } },
      { path: '/digests/:configId(\\d+)/:resultId(\\d+)?', component: { template: '<div />' } },
      { path: '/saved', component: { template: '<div />' } },
    ],
  })

  return mount(DigestContent, { global: { plugins: [pinia, router] } })
}

describe('DigestContent', () => {
  let wrapper: ReturnType<typeof mountDigestContent>

  beforeEach(() => {
    wrapper = mountDigestContent()
  })

  it('renders summary and counts', () => {
    expect(wrapper.text()).toContain('Test summary')
    expect(wrapper.text()).toContain('1 highlights')
    expect(wrapper.text()).toContain('3 items')
  })

  it('renders the generated title as the heading', () => {
    const h1 = wrapper.find('h1')
    expect(h1.exists()).toBe(true)
    expect(h1.text()).toBe('Go Time')
  })

  it('shows the full date and time next to the counts when the digest has a title', () => {
    const expected = absoluteTime('2026-08-13T08:00:00Z')
    expect(wrapper.text()).toContain(expected)
    expect(wrapper.find('h1').text()).toBe('Go Time')
  })

  it('falls back to the full date and time when the digest has no title', () => {
    const pinia = createPinia()
    setActivePinia(pinia)
    const noTitle = JSON.parse(content)
    delete noTitle.title
    useDigestStore().selectedResult = { ...makeResult(), content: JSON.stringify(noTitle) }

    const router = createRouter({
      history: createMemoryHistory(),
      routes: [{ path: '/', component: { template: '<div />' } }],
    })
    const w = mount(DigestContent, { global: { plugins: [pinia, router] } })
    const expected = absoluteTime('2026-08-13T08:00:00Z')
    expect(w.find('h1').text()).toBe(expected)
  })

  it('links highlight titles with accent hover, not underline', () => {
    const hlLink = wrapper.findAll('a').find(a => a.text().includes('Highlight One'))
    expect(hlLink).toBeDefined()
    expect(hlLink!.classes()).toContain('hover:text-accent')
    expect(hlLink!.classes()).not.toContain('hover:underline')
  })

  it('renders comment author as plain text, not a link', () => {
    const authorSpans = wrapper.findAll('span').filter(s => s.text() === 'alice')
    expect(authorSpans.length).toBe(1)
    expect(wrapper.findAll('a').filter(a => a.text() === 'alice').length).toBe(0)
    // The comment text itself is still a link.
    expect(wrapper.findAll('a').some(a => a.text() === 'Great insight')).toBe(true)
  })

  it('renders comment score when present', () => {
    expect(wrapper.text()).toContain('[5]')
  })

  it('renders one feed header per feed, linking to the in-app feed view', () => {
    const hnLinks = wrapper.findAll('a').filter(a => a.attributes('href') === '/feeds/42')
    expect(hnLinks.length).toBe(1)
    const redditLinks = wrapper.findAll('a').filter(a => a.attributes('href') === '/feeds/7')
    expect(redditLinks.length).toBe(1)
  })

  it('groups each feed into one section: highlights then its all-items', () => {
    const sections = wrapper.findAll('[data-feed-section]')
    expect(sections.length).toBe(2)

    const hn = sections[0].text()
    const reddit = sections[1].text()

    // HN: highlight card first, then its non-highlighted item.
    expect(hn).toContain('Highlight One')
    expect(hn).toContain('Item One')
    expect(hn.indexOf('Highlight One')).toBeLessThan(hn.indexOf('Item One'))

    // Reddit: only an all-items row (no highlights).
    expect(reddit).toContain('Item Two')
    expect(reddit).not.toContain('Highlight One')
  })

  it('labels each feed section\'s all-items with "All items"', () => {
    const labels = wrapper.findAll('p').filter(p => p.text() === 'All items')
    expect(labels.length).toBe(2)
  })

  it('shows "Select an item" with an icon when no digest result is selected', () => {
    const pinia = createPinia()
    setActivePinia(pinia)
    const empty = mount(DigestContent, { global: { plugins: [pinia] } })
    expect(empty.text()).toContain('Select an item')
    expect(empty.find('svg').exists()).toBe(true)
  })

  it('constrains the pane width: no horizontal scroll container on mobile', () => {
    const root = wrapper.find('[data-digest-content]')
    expect(root.classes()).toContain('overflow-x-hidden')
    expect(root.classes()).toContain('min-w-0')
  })

  it('uses reduced padding on mobile, full padding from sm up', () => {
    const root = wrapper.find('[data-digest-content]')
    expect(root.classes()).toContain('px-3')
    expect(root.classes()).toContain('py-3')
    expect(root.classes()).toContain('sm:p-6')
  })

  it('resets vertical scroll when a different digest result is opened', async () => {
    const root = wrapper.find('[data-digest-content]')
    Object.defineProperty(root.element, 'scrollTop', { value: 500, writable: true })
    // Open a different result.
    useDigestStore().selectedResult = { ...makeResult(), id: 2 }
    await wrapper.vm.$nextTick()
    await wrapper.vm.$nextTick()
    expect((root.element as HTMLElement).scrollTop).toBe(0)
  })
})

describe('DigestContent with sections', () => {
  const sectionContent = JSON.stringify({
    summary: 'Sectioned summary',
    sections: [
      {
        name: 'Tech',
        kind: 'category',
        sections: [
          {
            name: 'HN',
            kind: 'feed',
            feed_id: 42,
            highlights: [
              {
                item_id: 11,
                feed_id: 42,
                feed_name: 'HN',
                title: 'Tech Highlight',
                url: 'https://example.com/11',
                points: 90,
                comments_count: 20,
                published_at: '2026-08-13T07:00:00Z',
                llm_summary: 'Tech highlight summary.',
              },
            ],
            items: [
              {
                item_id: 12,
                feed_id: 42,
                feed_name: 'HN',
                title: 'Tech Item',
                url: 'https://example.com/12',
                points: 4,
                comments_count: 1,
                published_at: '2026-08-13T06:00:00Z',
              },
            ],
          },
        ],
      },
      {
        name: 'Solo Feed',
        kind: 'feed',
        feed_id: 7,
        highlights: [],
        items: [
          {
            item_id: 13,
            feed_id: 7,
            feed_name: 'Solo Feed',
            title: 'Solo Item',
            url: 'https://example.com/13',
            points: 1,
            comments_count: 0,
            published_at: '2026-08-13T05:00:00Z',
          },
        ],
      },
    ],
    highlights: [
      {
        item_id: 11,
        feed_id: 42,
        feed_name: 'HN',
        title: 'Tech Highlight',
        url: 'https://example.com/11',
        points: 90,
        comments_count: 20,
        published_at: '2026-08-13T07:00:00Z',
        llm_summary: 'Tech highlight summary.',
      },
    ],
    items: [
      {
        item_id: 12,
        feed_id: 42,
        feed_name: 'HN',
        title: 'Tech Item',
        url: 'https://example.com/12',
        points: 4,
        comments_count: 1,
        published_at: '2026-08-13T06:00:00Z',
      },
      {
        item_id: 13,
        feed_id: 7,
        feed_name: 'Solo Feed',
        title: 'Solo Item',
        url: 'https://example.com/13',
        points: 1,
        comments_count: 0,
        published_at: '2026-08-13T05:00:00Z',
      },
    ],
  })

  function mountSectioned() {
    const pinia = createPinia()
    setActivePinia(pinia)
    useDigestStore().selectedResult = { ...makeResult(), content: sectionContent }

    const router = createRouter({
      history: createMemoryHistory(),
      routes: [
        { path: '/', component: { template: '<div />' } },
        { path: '/feeds/:feedId(\\d+)/:itemId(\\d+)?', component: { template: '<div />' } },
      ],
    })

    return mount(DigestContent, { global: { plugins: [pinia, router] } })
  }

  it('renders category sections wrapping their feed subsections', () => {
    const wrapper = mountSectioned()

    const categoryHeaders = wrapper.findAll('[data-category-section]')
    expect(categoryHeaders.length).toBe(1)
    expect(categoryHeaders[0].text()).toContain('Tech')

    // Category section contains its nested feed section (HN) with both
    // highlight and all-items content.
    const hn = categoryHeaders[0].find('[data-feed-section]')
    expect(hn.exists()).toBe(true)
    expect(hn!.text()).toContain('Tech Highlight')
    expect(hn!.text()).toContain('Tech Item')
    expect(hn!.text().indexOf('Tech Highlight')).toBeLessThan(hn!.text().indexOf('Tech Item'))

    // Standalone feed section for the ungrouped feed.
    const sections = wrapper.findAll('[data-feed-section]')
    expect(sections.length).toBe(2)
    const solo = sections.find(s => s.text().includes('Solo Feed'))
    expect(solo).toBeDefined()
    expect(solo!.text()).toContain('Solo Item')
    expect(solo!.find('a').attributes('href')).toBe('/feeds/7')
  })
})
