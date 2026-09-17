// The item list and the digest result list must offer the SAME mark-above/below
// affordance: same icons, same corner, same labels, same hover swap. They
// drifted apart once (digests used a MoreHorizontal dropdown, and only on unread
// rows), so this asserts the two rendered fragments are identical rather than
// checking each component against its own expectations.
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import ItemList from './ItemList.vue'
import DigestResultList from './DigestResultList.vue'
import { useFeedStore } from '../stores/feed'
import { useDigestStore } from '../stores/digest'
import type { Feed, Item, DigestResult } from '../types/api'

vi.mock('../services/api', () => ({
  feedApi: {
    list: vi.fn(),
    create: vi.fn(),
    update: vi.fn(),
    delete: vi.fn(),
    getItems: vi.fn().mockResolvedValue({ items: [], has_more: false }),
    fetch: vi.fn(),
    markRead: vi.fn(),
    unreadCounts: vi.fn().mockResolvedValue({}),
  },
  itemApi: {
    updateStatus: vi.fn(),
    markReadAbove: vi.fn().mockResolvedValue({ message: 'ok' }),
    markReadBelow: vi.fn().mockResolvedValue({ message: 'ok' }),
  },
  digestApi: {
    listConfigs: vi.fn().mockResolvedValue([]),
    listResults: vi.fn().mockResolvedValue({ results: [], has_more: false }),
    unreadCounts: vi.fn().mockResolvedValue({}),
    markReadAbove: vi.fn().mockResolvedValue({ message: 'ok' }),
    markReadBelow: vi.fn().mockResolvedValue({ message: 'ok' }),
  },
  ApiError: class extends Error { constructor(public status: number, body: any) { super(body?.error) } },
}))

class ResizeObserverStub {
  observe() {}
  unobserve() {}
  disconnect() {}
}
vi.stubGlobal('ResizeObserver', ResizeObserverStub)

Object.defineProperty(window, 'matchMedia', {
  writable: true,
  value: (query: string) => ({
    matches: /min-width/.test(query),
    media: query,
    onchange: null,
    addListener: () => {},
    removeListener: () => {},
    addEventListener: () => {},
    removeEventListener: () => {},
    dispatchEvent: () => false,
  }),
})

function makeFeed(): Feed {
  return {
    id: 1, user_id: 1, category: 'Tech', type: 'hn', name: 'Hacker News',
    url: 'https://news.ycombinator.com/rss', config: '{}', min_score: 0,
    last_fetched_at: null, status: 'ok', extract_content: 0, last_error: '',
    last_error_at: null, last_error_severity: '',
    created_at: '2026-08-13T08:00:00Z', updated_at: '2026-08-13T08:00:00Z',
  } as Feed
}

function makeItem(): Item {
  return {
    id: 1, feed_id: 1, hash: 'h1', title: 'Item 1', url: 'https://example.com/1',
    content: '', author: '', comment_url: '', points: 10, comments_count: 3,
    published_at: '2026-08-13T08:00:00Z', status: 'unread',
    fetched_at: '2026-08-13T08:00:00Z', created_at: '2026-08-13T08:00:00Z',
    last_enriched_at: null, extract_content: '', extract_status: 'none',
    extract_retries: 0, extract_date: null,
  } as Item
}

function makeResult(): DigestResult {
  return {
    id: 1, user_id: 1, config_id: 1, config_snapshot: '{}',
    content: JSON.stringify({ summary: 's', items: [] }), status: 'unread',
    created_at: '2026-08-13T08:00:00Z', updated_at: '2026-08-13T08:00:00Z',
    title: 'Test Digest', item_count: 1,
  } as DigestResult
}

function mountFeeds(compact: boolean) {
  const pinia = createPinia()
  setActivePinia(pinia)
  const store = useFeedStore()
  store.loading = false
  store.feeds = [makeFeed()]
  store.activeFeedId = 1
  store.items = [makeItem()]
  return mount(ItemList, {
    props: { savedItems: [], showingSaved: false, compact },
    global: { plugins: [pinia] },
  })
}

function mountDigests(compact: boolean) {
  const pinia = createPinia()
  setActivePinia(pinia)
  const store = useDigestStore()
  store.loading = false
  store.results = [makeResult()]
  return mount(DigestResultList, {
    props: { compact, showingAll: false },
    global: { plugins: [pinia] },
  })
}

// The cluster that hover swaps out (score/comments/time, or item count/time)
// and the chevron pair that replaces it.
function actionsOf(wrapper: ReturnType<typeof mount>): string {
  const rows = wrapper.findAll('[role="button"]').filter(b => b.classes().includes('w-full'))
  expect(rows.length).toBeGreaterThan(0)
  return rows[0].find('[data-mark-actions]').html()
}

function timeOf(wrapper: ReturnType<typeof mount>): string {
  const rows = wrapper.findAll('[role="button"]').filter(b => b.classes().includes('w-full'))
  return rows[0].find('[data-item-time]').html()
}

beforeEach(() => {
  document.body.innerHTML = ''
  setActivePinia(createPinia())
  vi.clearAllMocks()
})

describe('item list and digest list hover actions are identical', () => {
  it('renders the same chevron pair, classes and labels in both lists (3-pane)', () => {
    expect(actionsOf(mountDigests(false))).toBe(actionsOf(mountFeeds(false)))
  })

  it('renders the same chevron pair, classes and labels in both lists (stacked)', () => {
    expect(actionsOf(mountDigests(true))).toBe(actionsOf(mountFeeds(true)))
  })

  it('swaps the timestamp out for the chevrons in both lists', () => {
    for (const compact of [false, true]) {
      expect(timeOf(mountDigests(compact))).toBe(timeOf(mountFeeds(compact)))
    }
  })

  it('anchors the pair on a fixed-height right-aligned wrapper in both lists', () => {
    for (const compact of [false, true]) {
      const rowsOf = (w: ReturnType<typeof mount>) =>
        w.findAll('[role="button"]').filter(b => b.classes().includes('w-full'))[0]
      const wrapperOf = (w: ReturnType<typeof mount>) => {
        const actions = rowsOf(w).find('[data-mark-actions]')
        // wrapper of the actions span == the swap container
        const el = actions.element.parentElement as HTMLElement
        return el.className.split(/\s+/).sort().join(' ')
      }
      expect(wrapperOf(mountDigests(compact))).toBe(wrapperOf(mountFeeds(compact)))
    }
  })
})
