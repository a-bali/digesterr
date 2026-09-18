import { describe, it, expect, beforeEach, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createRouter, createMemoryHistory } from 'vue-router'
import FeedRow from './FeedRow.vue'
import { useFeedStore } from '../stores/feed'
import type { Feed } from '../types/api'

vi.mock('../services/api', () => ({
  feedApi: {
    list: vi.fn().mockResolvedValue([]),
    unreadCounts: vi.fn().mockImplementation(() => Promise.resolve({})),
  },
}))

function makeFeed(overrides: Partial<Feed> = {}): Feed {
  return {
    id: 1, user_id: 1, category: '', type: 'rss', name: 'Test Feed',
    url: 'https://example.com/rss', config: '', min_score: 0,
    last_fetched_at: null, status: 'ok', extract_content: 0,
    last_error: '', last_error_at: null, last_error_severity: '',
    created_at: '', updated_at: '', ...overrides,
  }
}

function mountFeedRow(feed: Feed) {
  const pinia = createPinia()
  setActivePinia(pinia)
  const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/', component: { template: '<div />' } }] })
  return mount(FeedRow, {
    props: { feed, refreshing: false },
    global: { plugins: [pinia, router] },
  })
}

// The name span is the observable carrier of the dimmed state.
function nameClasses(wrapper: ReturnType<typeof mountFeedRow>): string[] {
  return wrapper.find('span.truncate').classes()
}

describe('FeedRow unread-aware dimming', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.clearAllMocks()
  })

  it('renders an envelope for email feeds instead of the generic Rss icon', async () => {
    // An email feed's url is a mail address ("inbox+suffix@domain"), which
    // feedIcon() cannot parse into a favicon, so it used to fall through to the
    // generic <Rss> icon — indistinguishable from a web feed with a bad URL.
    const emailFeed = makeFeed({ id: 7, type: 'email', name: 'Newsletter', url: 'inbox+news@agentmail.to' })
    const wrapper = mountFeedRow(emailFeed)
    await wrapper.vm.$nextTick()

    // No favicon image, and the Rss fallback is gone.
    expect(wrapper.find('img').exists()).toBe(false)
    const icons = wrapper.findAll('svg')
    // lucide's Mail renders a <rect> + a <path>; Rss renders only <path>/<circle>.
    expect(icons.some(s => s.html().includes('<rect'))).toBe(true)
    wrapper.unmount()
  })

  it('still uses the Rss icon for a non-email feed with no favicon', async () => {
    const wrapper = mountFeedRow(makeFeed({ id: 8, type: 'rss', url: 'not a url' }))
    await wrapper.vm.$nextTick()
    expect(wrapper.find('img').exists()).toBe(false)
    // Control: the envelope must be conditional on the feed type, not a blanket
    // replacement of the fallback icon.
    expect(wrapper.findAll('svg').some(s => s.html().includes('<rect'))).toBe(false)
    wrapper.unmount()
  })

  it('dims the Rss fallback icon when no favicon can be derived', async () => {
    // feedIcon() returns '' only for a URL that fails to parse, which is when the
    // Rss svg renders instead of an <img>.
    const wrapper = mountFeedRow(makeFeed({ id: 5, url: 'not a url' }))
    const store = useFeedStore()
    store.unreadCounts[5] = 0
    await wrapper.vm.$nextTick()

    expect(nameClasses(wrapper)).toContain('opacity-60')
    // The leading icon carries it too; a dimmed name beside a full-strength icon
    // looks like a rendering bug.
    expect(wrapper.find('svg.size-3\\.5').classes()).toContain('opacity-60')
    expect(wrapper.find('img').exists()).toBe(false)
    wrapper.unmount()
  })

  it('dims the favicon when a favicon-backed feed has no unread items', async () => {
    const wrapper = mountFeedRow(makeFeed({ id: 15, type: 'rss' }))
    const store = useFeedStore()
    store.unreadCounts[15] = 0
    await wrapper.vm.$nextTick()

    const img = wrapper.find('img')
    expect(img.exists()).toBe(true)
    expect(img.classes()).toContain('opacity-60')
    wrapper.unmount()
  })

  it('does not dim when the feed has unread items', async () => {
    const wrapper = mountFeedRow(makeFeed({ id: 6 }))
    const store = useFeedStore()
    store.unreadCounts[6] = 3
    await wrapper.vm.$nextTick()

    expect(nameClasses(wrapper)).not.toContain('opacity-60')
    wrapper.unmount()
  })

  it('does not dim while unread counts are still loading', async () => {
    // /unread-counts is a separate request from the feed list, so a feed has no
    // key until it resolves. Treating a missing key as zero would flash every
    // row in the sidebar on first paint, and a failed request would render as
    // "everything is read".
    const wrapper = mountFeedRow(makeFeed({ id: 7 }))
    const store = useFeedStore()
    expect(store.unreadCounts[7]).toBeUndefined()

    expect(nameClasses(wrapper)).not.toContain('opacity-60')
    wrapper.unmount()
  })

  it('keeps the error indicator at full strength on a dimmed row', async () => {
    // A feed with a problem and nothing unread must still read as broken; a
    // dimmed warning icon would understate a real failure.
    const wrapper = mountFeedRow(makeFeed({
      id: 8, last_error: 'connection refused', last_error_severity: 'error',
    }))
    const store = useFeedStore()
    store.unreadCounts[8] = 0
    await wrapper.vm.$nextTick()

    expect(nameClasses(wrapper)).toContain('opacity-60')
    const errIcon = wrapper.find('.text-red-500')
    expect(errIcon.exists()).toBe(true)
    expect(errIcon.classes()).not.toContain('opacity-60')
    wrapper.unmount()
  })

  it('keeps action buttons at full strength on a dimmed row', async () => {
    // The row wrapper must NOT be dimmed: the settings/mark-read/refresh buttons
    // are opacity-0 until hover, so a parent opacity would compound on hover and
    // strand them at 60% instead of 100%.
    const wrapper = mountFeedRow(makeFeed({ id: 9 }))
    const store = useFeedStore()
    store.unreadCounts[9] = 0
    await wrapper.vm.$nextTick()

    const row = wrapper.find('div.group')
    expect(row.classes()).not.toContain('opacity-60')
    const refreshBtn = wrapper.find('button[title="Refresh"]')
    expect(refreshBtn.classes()).not.toContain('opacity-60')
    expect(refreshBtn.classes()).toContain('group-hover:opacity-100')
    wrapper.unmount()
  })
})
