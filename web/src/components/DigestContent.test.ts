import { describe, it, expect, beforeEach } from 'vitest'
import { mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createRouter, createMemoryHistory } from 'vue-router'
import DigestContent from './DigestContent.vue'
import { useDigestStore } from '../stores/digest'
import type { DigestResult } from '../types/api'

const content = JSON.stringify({
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
})
