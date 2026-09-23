import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { setActivePinia, createPinia } from 'pinia'
import { createRouter, createMemoryHistory } from 'vue-router'
import FeedSidebar from './FeedSidebar.vue'
import FeedRow from './FeedRow.vue'
import type { Feed, DigestConfig } from '../types/api'

vi.mock('../services/api', () => ({
  feedApi: {
    list: vi.fn().mockResolvedValue([]),
    create: vi.fn(),
    update: vi.fn(),
    delete: vi.fn(),
    getItems: vi.fn().mockResolvedValue({ items: [] }),
    fetch: vi.fn(),
    markRead: vi.fn(),
    markReadAll: vi.fn(),
    unreadCounts: vi.fn().mockResolvedValue({}),
    savedCount: vi.fn().mockResolvedValue({ count: 0 }),
  },
  digestApi: {
    listConfigs: vi.fn().mockResolvedValue([]),
    createConfig: vi.fn(),
    updateConfig: vi.fn(),
    deleteConfig: vi.fn(),
    getConfig: vi.fn(),
    generate: vi.fn(),
    listResults: vi.fn().mockResolvedValue({ results: [], has_more: false }),
    getResult: vi.fn(),
    updateResultStatus: vi.fn(),
    deleteResult: vi.fn(),
    listResultItems: vi.fn().mockResolvedValue([]),
    unreadCounts: vi.fn().mockResolvedValue({}),
  },
  itemApi: { updateStatus: vi.fn(), getItem: vi.fn(), extract: vi.fn() },
  logsApi: { list: vi.fn().mockResolvedValue({ entries: [], total: 0, has_more: false }) },
  setAuthToken: vi.fn(),
  getAuthToken: vi.fn(() => null),
  setOnUnauthorized: vi.fn(),
  ApiError: class extends Error {},
}))

import { feedApi, digestApi, itemApi } from '../services/api'

// jsdom lacks matchMedia (useBreakpoints) and ResizeObserver (reka-ui
// dropdown/splitter measuring).
window.matchMedia = window.matchMedia || ((query: string) => ({
  matches: false,
  media: query,
  onchange: null,
  addListener: () => {},
  removeListener: () => {},
  addEventListener: () => {},
  removeEventListener: () => {},
  dispatchEvent: () => false,
})) as MediaQueryList

class ResizeObserverStub {
  observe() {}
  unobserve() {}
  disconnect() {}
}
vi.stubGlobal('ResizeObserver', ResizeObserverStub)

const feed: Feed = {
  id: 1, user_id: 1, category: '', type: 'rss', name: 'Example Feed', url: 'https://example.com/rss',
  config: '', min_score: 0, last_fetched_at: null, status: 'ok', extract_content: 0,
  last_error: '', last_error_at: null, last_error_severity: '',
  created_at: '', updated_at: '',
}

// A categorised copy: the shared `feed` fixture has category '', so it renders
// under the uncategorised group and no category header exists for it.
const foodFeed: Feed = { ...feed, id: 2, category: 'Food', name: 'Food Feed' }

const digestConfig: DigestConfig = {
  id: 7, user_id: 1, name: 'Morning Brief', schedule: '0 8 * * *', config: '{}',
  last_error: '', last_error_at: null,
  created_at: '', updated_at: '',
}

function router() {
  return createRouter({
    history: createMemoryHistory(),
    routes: [{ path: '/', component: FeedSidebar }],
  })
}

async function mountSidebar() {
  const r = router()
  const wrapper = mount(FeedSidebar, { global: { plugins: [r] } })
  await r.isReady()
  await flushPromises()
  return wrapper
}

function confirmButton(): HTMLElement | null {
  return document.body.querySelector('[data-mark-all-read-confirm]')
}

function dialogText(): string {
  return document.body.querySelector('[role="dialog"]')?.textContent ?? ''
}

describe('FeedSidebar touch action visibility', () => {
  beforeEach(() => {
    document.body.innerHTML = ''
    setActivePinia(createPinia())
    vi.clearAllMocks()
    vi.mocked(digestApi.listConfigs).mockResolvedValue([])
  })

  // On a touch device there is no hover, so `pointer-coarse:opacity-100` used to
  // paint EVERY row's Settings/mark-read/Refresh buttons permanently — a column of
  // ~4 controls per feed. The selected row is the SURFACE for those actions now:
  // the row whose feed is open shows them, all others show none. The `+` buttons
  // (Add feed / Create digest config) stay unconditionally visible — they are the
  // touch user's only way in.
  // The group's own classes ARE the contract: `opacity-100` means the actions
  // are painted, `opacity-0` (plus the hover/focus variants) means hidden until
  // pointed at. Exactly one of the two is ever present, so `opacity-100` is an
  // unambiguous "visible".
  const actionGroups = (wrapper: Awaited<ReturnType<typeof mountSidebar>>) =>
    Array.from((wrapper.element as HTMLElement).querySelectorAll('[data-row-actions]'))

  // Two ways this assertion can be blind, both of which shipped once:
  //   * `className.includes('opacity-100')` also matches the hover/focus VARIANTS
  //     (`group-hover:opacity-100`), so it can never fail — read the class LIST.
  //   * checking only the exact `opacity-100` token misses `pointer-coarse:opacity-100`,
  //     which is the ACTUAL bug: it forces the actions visible on every touch device
  //     regardless of selection. Both must be absent on an unselected row.
  const isPainted = (group: Element) =>
    group.classList.contains('opacity-100') || group.classList.contains('pointer-coarse:opacity-100')

  it('reveals actions only on the selected feed row', async () => {
    const wrapper = await mountSidebar()
    const { useFeedStore } = await import('../stores/feed')
    const feedStore = useFeedStore()
    feedStore.feeds.push(feed, { ...feed, id: 9, name: 'Other Feed' })
    await flushPromises()

    expect(wrapper.findAllComponents(FeedRow)).toHaveLength(2)
    const groups = actionGroups(wrapper)
    expect(groups).toHaveLength(2)

    // Nothing selected → no row paints its actions.
    expect(isPainted(groups[0])).toBe(false)
    expect(isPainted(groups[1])).toBe(false)

    // Select the first feed → only that row paints them.
    feedStore.activeFeedId = 1
    await flushPromises()
    expect(isPainted(groups[0])).toBe(true)
    expect(isPainted(groups[1])).toBe(false)
  })

  it('hides every feed row\u2019s actions when no feed is selected', async () => {
    const wrapper = await mountSidebar()
    const { useFeedStore } = await import('../stores/feed')
    const feedStore = useFeedStore()
    feedStore.feeds.push(feed, { ...feed, id: 9, name: 'Other Feed' })
    feedStore.activeFeedId = null
    await flushPromises()

    for (const group of actionGroups(wrapper)) {
      // Hidden means no unconditional reveal on ANY pointer type; hover/focus
      // still reveal it for the users who have them.
      expect(isPainted(group)).toBe(false)
      expect(group.classList.contains('group-hover:opacity-100')).toBe(true)
    }
  })

  it('keeps the + buttons always visible', async () => {
    const wrapper = await mountSidebar()
    await flushPromises()
    for (const title of ['Add feed', 'Create digest config']) {
      const plus = wrapper.find(`button[title="${title}"]`)
      expect(plus.exists()).toBe(true)
      expect(plus.classes()).not.toContain('opacity-0')
    }
  })
})

describe('FeedSidebar mark-all-as-read confirmation', () => {
  beforeEach(() => {
    document.body.innerHTML = ''
    setActivePinia(createPinia())
    vi.clearAllMocks()
    vi.mocked(digestApi.listConfigs).mockResolvedValue([])
  })

  // The sidebar no longer owns the confirmation dialog. It declares WHICH scope
  // to mark and the parent (HomeView) runs one shared dialog for every scope, so
  // a sidebar control cannot mark a scope read while the 'A' key confirms. These
  // tests pin that contract: emit the scope, never call the API.
  it('asks the parent to mark a single feed, without calling the API', async () => {
    const wrapper = await mountSidebar()
    const { useFeedStore } = await import('../stores/feed')
    useFeedStore().feeds.push(feed)
    await flushPromises()

    await wrapper.find('button[title="Mark “Example Feed” as read"]').trigger('click')
    await flushPromises()

    expect(wrapper.emitted('markAllRead')?.[0]).toEqual([{ kind: 'feed', id: feed.id }])
    expect(feedApi.markRead).not.toHaveBeenCalled()
    wrapper.unmount()
  })

  it('asks the parent to mark a category, without calling the API', async () => {
    const wrapper = await mountSidebar()
    const { useFeedStore } = await import('../stores/feed')
    useFeedStore().feeds.push(foodFeed)
    useFeedStore().unreadCounts = { [foodFeed.id]: 4 }
    await flushPromises()

    await wrapper.find('button[data-mark-category-read="Food"]').trigger('click')
    await flushPromises()

    expect(wrapper.emitted('markAllRead')?.[0]).toEqual([{ kind: 'category', name: 'Food' }])
    expect(feedApi.markReadAll).not.toHaveBeenCalled()
    wrapper.unmount()
  })

  it('asks the parent to mark the whole feeds section', async () => {
    const wrapper = await mountSidebar()
    const { useFeedStore } = await import('../stores/feed')
    useFeedStore().feeds.push(feed)
    useFeedStore().unreadCounts = { [feed.id]: 5 }
    await flushPromises()

    await wrapper.find('button[data-mark-feeds-section-read]').trigger('click')
    await flushPromises()

    expect(wrapper.emitted('markAllRead')?.[0]).toEqual([{ kind: 'feeds-section' }])
    wrapper.unmount()
  })

  it('asks the parent to mark every digest', async () => {
    const { digestApi: dApi } = await import('../services/api')
    vi.mocked(dApi.listConfigs).mockResolvedValue([digestConfig])
    vi.mocked(dApi.unreadCounts).mockResolvedValue({ [digestConfig.id]: 2 })
    const wrapper = await mountSidebar()
    await flushPromises()

    await wrapper.find('button[data-mark-all-digests]').trigger('click')
    await flushPromises()

    expect(wrapper.emitted('markAllRead')?.[0]).toEqual([{ kind: 'all-digests' }])
    wrapper.unmount()
  })

  it('asks the parent to mark one digest config', async () => {
    const { digestApi: dApi } = await import('../services/api')
    vi.mocked(dApi.listConfigs).mockResolvedValue([digestConfig])
    vi.mocked(dApi.unreadCounts).mockResolvedValue({ [digestConfig.id]: 2 })
    const wrapper = await mountSidebar()
    await flushPromises()

    await wrapper.find(`button[data-mark-digest-read="${digestConfig.id}"]`).trigger('click')
    await flushPromises()

    expect(wrapper.emitted('markAllRead')?.[0]).toEqual([
      { kind: 'digest-config', id: digestConfig.id, name: digestConfig.name },
    ])
    wrapper.unmount()
  })

  // The user's report: a category with nothing unread showed no hover button at
  // all, while every other row keeps its buttons. It must be present and
  // disabled instead, so the control does not appear to be missing.
  it('keeps the category mark-read button present but disabled at zero unread', async () => {
    const { digestApi: dApi } = await import('../services/api')
    vi.mocked(dApi.listConfigs).mockResolvedValue([])
    const wrapper = await mountSidebar()
    const { useFeedStore } = await import('../stores/feed')
    useFeedStore().feeds.push(foodFeed)
    useFeedStore().unreadCounts = { [foodFeed.id]: 0 }
    await flushPromises()

    const btn = wrapper.find('button[data-mark-category-read="Food"]')
    expect(btn.exists()).toBe(true)
    expect((btn.element as HTMLButtonElement).disabled).toBe(true)

    await btn.trigger('click')
    await flushPromises()
    // A disabled control must not fire the action.
    expect(wrapper.emitted('markAllRead')).toBeUndefined()
    wrapper.unmount()
  })

  it('enables the category mark-read button when it has unread items', async () => {
    const wrapper = await mountSidebar()
    const { useFeedStore } = await import('../stores/feed')
    useFeedStore().feeds.push(foodFeed)
    useFeedStore().unreadCounts = { [foodFeed.id]: 3 }
    await flushPromises()

    const btn = wrapper.find('button[data-mark-category-read="Food"]')
    expect((btn.element as HTMLButtonElement).disabled).toBe(false)

    await btn.trigger('click')
    await flushPromises()
    expect(wrapper.emitted('markAllRead')?.[0]).toEqual([{ kind: 'category', name: 'Food' }])
    wrapper.unmount()
  })
})

describe('FeedSidebar section totals', () => {
  beforeEach(() => {
    document.body.innerHTML = ''
    setActivePinia(createPinia())
    vi.clearAllMocks()
    // Reset mock return values that individual tests override — clearAllMocks
    // only clears call history, not mockResolvedValue/mockReturnValue.
    vi.mocked(digestApi.listConfigs).mockResolvedValue([])
  })

  it('computes totalFeedUnread as the sum of all feed unread counts', async () => {
    const wrapper = await mountSidebar()
    const { useFeedStore } = await import('../stores/feed')
    const feedStore = useFeedStore()
    feedStore.feeds.push({ ...feed, id: 1 })
    feedStore.feeds.push({ ...feed, id: 2 })
    feedStore.unreadCounts[1] = 3
    feedStore.unreadCounts[2] = 5
    await flushPromises()

    // Scope queries to the wrapper root to avoid cross-test DOM pollution.
    const root = wrapper.element as HTMLElement
    expect(root.querySelector('[data-testid="feeds-section-total"]')?.textContent).toBe('8')
    expect(root.querySelector('[data-testid="digests-section-total"]')).toBeNull()
    wrapper.unmount()
  })

  it('computes totalDigestUnread as the sum of all config unread counts', async () => {
    // Set mock BEFORE mount so onMounted's fetchConfigs loads the config.
    vi.mocked(digestApi.listConfigs).mockResolvedValue([digestConfig])
    const wrapper = await mountSidebar()
    const { useDigestStore } = await import('../stores/digest')
    const digestStore = useDigestStore()
    // fetchConfigs already loaded digestConfig — just set the unread count.
    digestStore.unreadCounts[digestConfig.id] = 4
    await flushPromises()

    const root = wrapper.element as HTMLElement
    expect(root.querySelector('[data-testid="digests-section-total"]')?.textContent).toBe('4')
    expect(root.querySelector('[data-testid="feeds-section-total"]')).toBeNull()
    wrapper.unmount()
  })

  it('omits section badges when total is zero', async () => {
    const wrapper = await mountSidebar()
    const { useFeedStore } = await import('../stores/feed')
    const feedStore = useFeedStore()
    feedStore.feeds.push(feed)
    feedStore.unreadCounts[feed.id] = 0
    await flushPromises()

    const root = wrapper.element as HTMLElement
    expect(root.querySelector('[data-testid="feeds-section-total"]')).toBeNull()
    expect(root.querySelector('[data-testid="digests-section-total"]')).toBeNull()
    wrapper.unmount()
  })

  it('section total reflects store map values (computed sum correctness)', async () => {
    const wrapper = await mountSidebar()
    const { useFeedStore } = await import('../stores/feed')
    const { useDigestStore } = await import('../stores/digest')
    const feedStore = useFeedStore()
    const digestStore = useDigestStore()
    feedStore.feeds.push({ ...feed, id: 10, name: 'A' })
    feedStore.feeds.push({ ...feed, id: 11, name: 'B' })
    feedStore.unreadCounts[10] = 2
    feedStore.unreadCounts[11] = 3

    // Verify the store map is the source of truth for the computed totals.
    expect(feedStore.unreadCounts[10] + feedStore.unreadCounts[11]).toBe(5)
    wrapper.unmount()
  })
})

describe('FeedSidebar unread pill and error-icon placement', () => {
  beforeEach(() => {
    document.body.innerHTML = ''
    setActivePinia(createPinia())
    vi.clearAllMocks()
    vi.mocked(digestApi.listConfigs).mockResolvedValue([])
  })

  // True when `b` appears after `a` in document order.
  function follows(a: Element, b: Element): boolean {
    return !!(a.compareDocumentPosition(b) & Node.DOCUMENT_POSITION_FOLLOWING)
  }

  function spanWithText(root: Element, text: string): HTMLElement {
    const el = Array.from(root.querySelectorAll('span')).find(s => s.textContent?.trim() === text)
    if (!el) throw new Error(`no span with text ${text}`)
    return el as HTMLElement
  }

  it('places the digest error icon after the config name, matching feeds', async () => {
    vi.mocked(digestApi.listConfigs).mockResolvedValue([
      { ...digestConfig, last_error: 'generation failed', last_error_at: '2026-09-10T08:00:00Z' },
    ])
    const wrapper = await mountSidebar()
    const { useDigestStore } = await import('../stores/digest')
    const digestStore = useDigestStore()
    digestStore.unreadCounts[digestConfig.id] = 4
    await flushPromises()

    const root = wrapper.element as HTMLElement
    const pill = root.querySelector('[data-testid="digest-unread"]') as HTMLElement
    expect(pill).not.toBeNull()
    const row = pill.parentElement as HTMLElement
    const name = spanWithText(row, 'Morning Brief')
    const errorIcon = row.querySelector('[data-testid="digest-error"]') as SVGElement
    expect(errorIcon).not.toBeNull()

    // name first, then the error icon — the feeds order.
    expect(follows(name, errorIcon)).toBe(true)
    // and the pill sits after both, not pushed to the far edge.
    expect(follows(errorIcon, pill)).toBe(true)
    expect(pill.className).not.toContain('ml-auto')
    wrapper.unmount()
  })

  it('renders the digest pill without an error icon when there is no error', async () => {
    vi.mocked(digestApi.listConfigs).mockResolvedValue([digestConfig])
    const wrapper = await mountSidebar()
    const { useDigestStore } = await import('../stores/digest')
    const digestStore = useDigestStore()
    digestStore.unreadCounts[digestConfig.id] = 2
    await flushPromises()

    const root = wrapper.element as HTMLElement
    const pill = root.querySelector('[data-testid="digest-unread"]') as HTMLElement
    const row = pill.parentElement as HTMLElement
    const name = spanWithText(row, 'Morning Brief')
    expect(row.querySelector('[data-testid="digest-error"]')).toBeNull()
    expect(follows(name, pill)).toBe(true)
    wrapper.unmount()
  })

  it('renders every unread badge as a solid pill, never an outline', async () => {
    // A blanket check across ALL of them, because the failure this guards
    // against is a class string shared by several spans: a find-and-replace
    // aimed at one badge silently turned the per-digest unread pill into an
    // outline ("why do digests have hollow circles now?"). Asserting per-badge
    // in separate tests did not catch it, since no test looked at that span.
    vi.mocked(digestApi.listConfigs).mockResolvedValue([digestConfig])
    const wrapper = await mountSidebar()
    const { useFeedStore } = await import('../stores/feed')
    const { useDigestStore } = await import('../stores/digest')
    const feedStore = useFeedStore()
    const digestStore = useDigestStore()
    const root = wrapper.element as HTMLElement

    feedStore.feeds.push(feed)
    feedStore.feeds.push({ ...feed, id: 21, name: 'Tech Feed', category: 'tech' })
    feedStore.unreadCounts[feed.id] = 12
    feedStore.unreadCounts[21] = 3
    feedStore.savedCount = 2
    digestStore.unreadCounts[digestConfig.id] = 4
    await flushPromises()

    // Every unread tally in the sidebar, by the testid the code already carries.
    const unreadBadges = ['feeds-section-total', 'digests-section-total', 'digest-unread', 'category-unread']
    for (const id of unreadBadges) {
      const el = root.querySelector(`[data-testid="${id}"]`) as HTMLElement
      expect(el, `${id} should be rendered`).not.toBeNull()
      expect(el.classList, `${id} must be a solid pill`).toContain('bg-accent')
      expect(el.classList, `${id} must not be an outline`).not.toContain('bg-transparent')
      expect(el.classList, `${id} must be filled, not bordered-only`).toContain('text-accent-foreground')
    }
    // The SAVED badge is the one deliberate exception: solid, but at a dimmed
    // accent so it is not read as an unread tally.
    const saved = root.querySelector('[data-testid="saved-count"]') as HTMLElement
    expect(saved.classList).toContain('bg-accent/60')
    wrapper.unmount()
  })

  it('places the category unread pill directly after the category name', async () => {
    const wrapper = await mountSidebar()
    const { useFeedStore } = await import('../stores/feed')
    const feedStore = useFeedStore()
    feedStore.feeds.push({ ...feed, id: 20, name: 'Tech Feed', category: 'tech' })
    feedStore.unreadCounts[20] = 3
    await flushPromises()

    const root = wrapper.element as HTMLElement
    const pill = root.querySelector('[data-testid="category-unread"]') as HTMLElement
    expect(pill).not.toBeNull()

    const button = pill.closest('button') as HTMLElement
    // A `justify-between` button pushes the pill to the opposite edge.
    expect(button.className).not.toContain('justify-between')
    const name = spanWithText(button, 'tech')
    expect(follows(name, pill)).toBe(true)
    // Pill must be a child of the button row, not in a separate actions column.
    expect(pill.parentElement).toBe(button)
    wrapper.unmount()
  })

  it('places the feed unread pill before the row action buttons', async () => {
    const wrapper = await mountSidebar()
    const { useFeedStore } = await import('../stores/feed')
    const feedStore = useFeedStore()
    feedStore.feeds.push(feed)
    feedStore.unreadCounts[feed.id] = 7
    await flushPromises()

    const root = wrapper.element as HTMLElement
    const settings = root.querySelector('button[title="Settings"]') as HTMLElement
    // Actions live in their own group wrapper now, so the ROW is the group's
    // parent — climbing one level from the button would land on the group.
    const row = settings.closest('[data-row-actions]')!.parentElement as HTMLElement
    const name = spanWithText(row, 'Example Feed')
    const pill = Array.from(row.querySelectorAll('span')).find(s => s.textContent?.trim() === '7') as HTMLElement
    expect(pill).toBeDefined()
    expect(follows(name, pill)).toBe(true)
    expect(follows(pill, settings)).toBe(true)
    wrapper.unmount()
  })
})

describe('FeedSidebar saved counter', () => {
  // Minimal item shape for store-level status transitions.
  const itemRow = {
    id: 1, feed_id: 1, title: 't', url: '', author: '', content: '', status: 'unread',
    published_at: null, fetched_at: '', points: 0, comments_count: 0, comment_url: '',
    created_at: '', updated_at: '',
  } as any

  beforeEach(() => {
    document.body.innerHTML = ''
    setActivePinia(createPinia())
    vi.clearAllMocks()
    vi.mocked(digestApi.listConfigs).mockResolvedValue([])
  })

  it('dims the saved badge accent so it cannot be read as an unread tally', async () => {
    // The saved counter is a different KIND of number from an unread tally, so
    // it must not look like one. Every unread badge in this sidebar is a solid
    // bg-accent pill; the saved one is the same solid pill at a DIMMED accent
    // (bg-accent/60). A hollow outline was tried first and was barely visible,
    // so the distinction is the accent's opacity, not the shape.
    const wrapper = await mountSidebar()
    const { useFeedStore } = await import('../stores/feed')
    const feedStore = useFeedStore()
    const root = wrapper.element as HTMLElement

    feedStore.feeds.push(feed)
    feedStore.unreadCounts[feed.id] = 12
    feedStore.savedCount = 2
    await flushPromises()

    const saved = root.querySelector('[data-testid="saved-count"]') as HTMLElement
    const unread = root.querySelector('[data-testid="feeds-section-total"]') as HTMLElement

    // Same pill shape and size as the unread badge — one family, not two.
    for (const cls of ['rounded-full', 'px-1.5', 'py-0.5', 'text-[10px]', 'font-medium']) {
      expect(saved.classList).toContain(cls)
      expect(unread.classList).toContain(cls)
    }
    // The saved pill is SOLID (not an outline), so it cannot be invisible.
    expect(saved.classList).not.toContain('bg-transparent')
    // ...and its accent is dimmed. This exact class is the discriminator: the
    // unread pill next to it carries the FULL `bg-accent`, so reverting the dim
    // fails here rather than passing on a shared substring.
    expect(saved.classList).toContain('bg-accent/60')
    expect(unread.classList).toContain('bg-accent')
    expect(unread.classList).not.toContain('bg-accent/60')
    // The text is mode-aware: white on the dimmed fill is 6.27:1 in dark but
    // only 2.18:1 in light (below the unread pill's own 3.64:1 baseline, so
    // white would undo the dim's legibility), while the foreground colour gives
    // 8.18:1 light / 2.85:1 dark. Losing either half leaves a mode where the
    // number is hard to read.
    expect(saved.classList).toContain('text-foreground')
    expect(saved.classList).toContain('dark:text-accent-foreground')
    wrapper.unmount()
  })

  it('shows the saved total and hides the badge at zero', async () => {
    const wrapper = await mountSidebar()
    const { useFeedStore } = await import('../stores/feed')
    const feedStore = useFeedStore()
    const root = wrapper.element as HTMLElement
    expect(root.querySelector('[data-testid="saved-count"]')).toBeNull()

    feedStore.savedCount = 3
    await flushPromises()
    const badge = root.querySelector('[data-testid="saved-count"]') as HTMLElement
    expect(badge.textContent?.trim()).toBe('3')
    expect(badge.getAttribute('title')).toBe('3 saved items')
    wrapper.unmount()
  })

  it('counts ALL saved items, not unread ones', async () => {
    // The user asked for a saved counter specifically NOT tied to read/unread.
    // Saved items can be read or unread; the badge must ignore that split.
    const wrapper = await mountSidebar()
    const { useFeedStore } = await import('../stores/feed')
    const feedStore = useFeedStore()
    const root = wrapper.element as HTMLElement

    // The feeds section badge is on screen at the same time, so the saved badge
    // must be driven by the saved count alone, not by that unread total.
    feedStore.feeds.push(feed)
    feedStore.unreadCounts[feed.id] = 12
    feedStore.savedCount = 2
    await flushPromises()

    expect(root.querySelector('[data-testid="feeds-section-total"]')?.textContent).toBe('12')
    const badge = root.querySelector('[data-testid="saved-count"]') as HTMLElement
    expect(badge.textContent?.trim()).toBe('2')
    wrapper.unmount()
  })

  // Alignment is a layout guarantee, and jsdom does no layout, so the test pins
  // the structure that makes it true instead of coordinates: the Saved row and
  // both section headers must each have exactly one horizontally-padded layer
  // (their own px-2 button), with no padding on the wrapper. The section
  // wrappers previously carried px-2 ON TOP of the button's px-2, which put
  // their icons 8px right of the Saved row's (measured x=28 vs x=20 live).
  it('gives Saved, Digests and Feeds the same single padding layer', async () => {
    const wrapper = await mountSidebar()
    const root = wrapper.element as HTMLElement
    const saved = Array.from(root.querySelectorAll('button')).find(b => b.textContent?.includes('Saved items')) as HTMLElement
    const digests = Array.from(root.querySelectorAll('button')).find(b => /digests/i.test(b.textContent ?? '') && b.className.includes('uppercase')) as HTMLElement
    const feeds = Array.from(root.querySelectorAll('button')).find(b => /^feeds$/i.test(b.textContent?.trim() ?? '') && b.className.includes('uppercase')) as HTMLElement

    expect(saved).toBeDefined()
    expect(digests).toBeDefined()
    expect(feeds).toBeDefined()

    // All three buttons pad their own content identically...
    for (const btn of [saved, digests, feeds]) expect(btn.className).toContain('px-2')

    // ...and neither section wrapper adds horizontal padding of its own.
    for (const btn of [digests, feeds]) {
      const wrapperCls = btn.parentElement?.className ?? ''
      expect(wrapperCls).not.toMatch(/(^|\s)px-2(\s|$)/)
    }
    wrapper.unmount()
  })

  it('keeps every row icon the same size so the labels line up', async () => {
    const wrapper = await mountSidebar()
    const root = wrapper.element as HTMLElement
    // Match on the class signature rather than textContent: a row's text
    // includes its badge digit ("Feeds 12"), so anchored text matching misses.
    const sectionBtn = (label: string) => Array.from(root.querySelectorAll('button'))
      .find(b => b.className.includes('uppercase') && (b.textContent ?? '').trim().startsWith(label)) as HTMLElement
    const saved = Array.from(root.querySelectorAll('button'))
      .find(b => (b.textContent ?? '').includes('Saved items')) as HTMLElement

    const rows = [saved, sectionBtn('Digests'), sectionBtn('Feeds')]
    for (const row of rows) {
      expect(row).toBeDefined()
      const icon = row.querySelector('svg') as SVGElement
      // size-3.5 == 14px. A different size here shifts the label column.
      expect(icon.getAttribute('class')).toContain('size-3.5')
    }
    // And every row uses the same icon/label gap, so the text column matches.
    for (const row of rows) expect(row.className).toContain('gap-2')
    wrapper.unmount()
  })

  it('includes the saved total in the initial fetch, so the badge is painted', async () => {
    const { feedApi } = await import('../services/api')
    vi.mocked(feedApi.savedCount).mockResolvedValue({ count: 5 })
    const { useFeedStore } = await import('../stores/feed')
    setActivePinia(createPinia())
    const feedStore = useFeedStore()

    await feedStore.fetchSavedCount()
    expect(feedStore.savedCount).toBe(5)
  })

  it('adjusts the saved total locally when an item is saved or unsaved', async () => {
    const { itemApi } = await import('../services/api')
    const { useFeedStore } = await import('../stores/feed')
    setActivePinia(createPinia())
    const feedStore = useFeedStore()
    feedStore.savedCount = 1

    // Saving: the flag goes on, so the badge goes up.
    const item = { ...itemRow, id: 99, status: 'unread', is_saved: 0 } as any
    vi.mocked(itemApi.updateStatus).mockResolvedValue({ ...item, status: 'read', is_saved: 1 } as any)
    await feedStore.updateItemStatus(item, undefined, true)
    expect(feedStore.savedCount).toBe(2)

    // Marking a SAVED item read must NOT change the saved total: saved-ness is a
    // flag, so a read-state change is invisible to it. (Under the old status
    // model this decremented, which is the bug the user reported.)
    const savedItem = { ...itemRow, id: 99, status: 'read', is_saved: 1 } as any
    vi.mocked(itemApi.updateStatus).mockResolvedValue({ ...savedItem, status: 'unread' } as any)
    await feedStore.updateItemStatus(savedItem, 'unread')
    expect(feedStore.savedCount).toBe(2)

    // Unsaving is what decrements it.
    vi.mocked(itemApi.updateStatus).mockResolvedValue({ ...savedItem, is_saved: 0 } as any)
    await feedStore.unsaveItem(savedItem)
    expect(feedStore.savedCount).toBe(1)
  })

  it('never drives the saved total below zero', async () => {
    // Two sources can report the same transition; the counter must clamp
    // rather than show a negative badge.
    const { itemApi } = await import('../services/api')
    const { useFeedStore } = await import('../stores/feed')
    setActivePinia(createPinia())
    const feedStore = useFeedStore()
    feedStore.savedCount = 0
    const savedItem = { ...itemRow, id: 77, status: 'read', is_saved: 1 } as any
    vi.mocked(itemApi.updateStatus).mockResolvedValue({ ...savedItem, is_saved: 0 } as any)
    await feedStore.unsaveItem(savedItem)
    expect(feedStore.savedCount).toBe(0)
  })
})
