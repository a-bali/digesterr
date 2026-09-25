import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { setActivePinia, createPinia } from 'pinia'
import DigestDialog from './DigestDialog.vue'
import { useFeedStore } from '../stores/feed'
import type { Feed } from '../types/api'

vi.mock('../services/api', () => ({
  feedApi: {
    list: vi.fn(), unreadCounts: vi.fn(), create: vi.fn(), update: vi.fn(),
    delete: vi.fn(), getItems: vi.fn(), fetch: vi.fn(), markRead: vi.fn(),
  },
  itemApi: { updateStatus: vi.fn(), getItem: vi.fn(), extract: vi.fn() },
  authApi: { signout: vi.fn(), changePassword: vi.fn() },
  digestApi: {},
  logsApi: {},
  setAuthToken: vi.fn(),
  getAuthToken: vi.fn(() => null),
  setOnUnauthorized: vi.fn(),
  ApiError: class extends Error {},
}))

vi.mock('vue-sonner', () => ({ toast: { success: vi.fn(), error: vi.fn() } }))

function makeFeed(overrides: Partial<Feed>): Feed {
  return {
    id: 1,
    user_id: 1,
    category: 'Tech',
    type: 'rss',
    name: 'RSS Blog',
    url: 'https://example.com/rss',
    config: '{}',
    min_score: 0,
    last_fetched_at: null,
    status: 'active',
    extract_content: 0,
    last_error: '',
    last_error_at: null,
    last_error_severity: '',
    created_at: '2026-01-01T00:00:00Z',
    updated_at: '2026-01-01T00:00:00Z',
    ...overrides,
  }
}

const rssFeed = makeFeed({ id: 1, type: 'rss', name: 'RSS Blog' })
const hnFeed = makeFeed({ id: 2, type: 'hn', name: 'Hacker News' })
// Twitter items DO carry points — the server sets item.points from the tweet's
// favourite count (internal/feed/twitter_fetcher.go). The dialog's own type list
// used to omit twitter, so selecting a Twitter feed disabled min-score.
const twitterFeed = makeFeed({ id: 3, type: 'twitter', category: 'Social', name: 'X Account' })

// The dialog renders inside a reka-ui DialogPortal (teleported to
// document.body), so read the live DOM, not the wrapper.
function minScoreInput(): HTMLInputElement {
  return document.body.querySelector('input[type="number"][min="0"]') as HTMLInputElement
}

function dialogBody(): HTMLElement {
  return document.body.querySelector('[role="dialog"]') as HTMLElement
}

function findButton(label: string): HTMLElement {
  return [...document.body.querySelectorAll('button')].find(b => b.textContent?.includes(label)) as HTMLElement
}

async function selectSource(label: string) {
  const input = document.body.querySelector('input[placeholder^="Search "]') as HTMLInputElement
  input.dispatchEvent(new Event('focus', { bubbles: true }))
  const setter = Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value')!.set!
  setter.call(input, label)
  input.dispatchEvent(new Event('input', { bubbles: true }))
  await flushPromises()
  const option = [...document.body.querySelectorAll('li')].find(li => li.textContent?.includes(label))
  if (!option) throw new Error(`no option found for ${label}`)
  option.dispatchEvent(new MouseEvent('mousedown', { bubbles: true }))
  await flushPromises()
}

function fillNumber(input: HTMLInputElement, value: string) {
  const setter = Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value')!.set!
  setter.call(input, value)
  input.dispatchEvent(new Event('input', { bubbles: true }))
}

function mountDialog(props: Record<string, unknown> = {}) {
  setActivePinia(createPinia())
  const store = useFeedStore()
  store.feeds = [rssFeed, hnFeed]
  return mount(DigestDialog, { props: { config: null, ...props } })
}

describe('DigestDialog min score', () => {
  beforeEach(() => {
    document.body.innerHTML = ''
    vi.clearAllMocks()
  })

  it('is enabled by default with no sources selected (all feeds may include scored ones)', async () => {
    mountDialog()
    await flushPromises()
    expect(minScoreInput().disabled).toBe(false)
    expect(dialogBody().textContent).toContain('Min score')
  })

  it('is disabled and explains why when only non-score feeds are selected', async () => {
    mountDialog()
    await flushPromises()

    findButton('Feeds').dispatchEvent(new MouseEvent('click', { bubbles: true }))
    await flushPromises()
    await selectSource('RSS Blog')

    expect(minScoreInput().disabled).toBe(true)
    expect(dialogBody().textContent).toContain('no effect')
  })

  it('is enabled when a score-capable feed (hn) is selected', async () => {
    mountDialog()
    await flushPromises()

    findButton('Feeds').dispatchEvent(new MouseEvent('click', { bubbles: true }))
    await flushPromises()
    await selectSource('Hacker News')

    expect(minScoreInput().disabled).toBe(false)
    expect(dialogBody().textContent).toContain('at least this many')
  })

  it('is enabled when a selected category contains a score-capable feed', async () => {
    mountDialog()
    await flushPromises()

    // Both test feeds are in the Tech category, which includes the HN feed.
    await selectSource('Tech')

    expect(minScoreInput().disabled).toBe(false)
    expect(dialogBody().textContent).toContain('at least this many')
  })

  // twitter was missing from the dialog's own capability list, so a Twitter
  // source disabled a threshold the server can actually apply. The list now
  // comes from the shared SCORE_FEED_TYPES, and this pins it.
  it('is enabled when a twitter feed is selected', async () => {
    mountDialog()
    await flushPromises()

    const store = useFeedStore()
    store.feeds = [rssFeed, twitterFeed]
    await flushPromises()

    findButton('Feeds').dispatchEvent(new MouseEvent('click', { bubbles: true }))
    await flushPromises()
    await selectSource('X Account')

    expect(minScoreInput().disabled).toBe(false)
    expect(dialogBody().textContent).toContain('at least this many')
  })

  it('is enabled when a category containing only a twitter feed is selected', async () => {
    mountDialog()
    await flushPromises()

    const store = useFeedStore()
    store.feeds = [rssFeed, twitterFeed]
    await flushPromises()

    // The Social category holds only the twitter feed.
    await selectSource('Social')

    expect(minScoreInput().disabled).toBe(false)
  })

  // The explanatory copy names the score-capable types, so it has to name them
  // all: it told users that only HN, Lobsters and Reddit provide scores.
  it('names twitter among the score-capable types in the disabled copy', async () => {
    mountDialog()
    await flushPromises()

    const store = useFeedStore()
    store.feeds = [rssFeed]
    await flushPromises()

    findButton('Feeds').dispatchEvent(new MouseEvent('click', { bubbles: true }))
    await flushPromises()
    await selectSource('RSS Blog')

    expect(minScoreInput().disabled).toBe(true)
    expect(dialogBody().textContent).toContain('Twitter')
  })

  // buildConfig saves ONLY the active mode's sources, so the inactive mode's
  // picks cannot make the digest score-capable. Counting them kept min-score
  // enabled for a digest that would apply it to nothing.
  it('ignores the inactive mode selection when deciding if scores apply', async () => {
    mountDialog()
    await flushPromises()

    // The non-capable RSS feed sits in Tech; the score-capable HN feed sits in
    // News. Picking News under Feeds and then Tech under Categories leaves a
    // capable pick in the INACTIVE mode and a non-capable one in the active
    // mode — which is what the saved config will contain.
    const hnNews = makeFeed({ id: 2, type: 'hn', category: 'News', name: 'HN News' })
    const store = useFeedStore()
    store.feeds = [rssFeed, hnNews]
    await flushPromises()

    findButton('Feeds').dispatchEvent(new MouseEvent('click', { bubbles: true }))
    await flushPromises()
    await selectSource('HN News')
    expect(minScoreInput().disabled).toBe(false)

    findButton('Categories').dispatchEvent(new MouseEvent('click', { bubbles: true }))
    await flushPromises()
    await selectSource('Tech')

    // Tech holds only the RSS feed, so min-score does nothing for this config.
    // The Feeds side still holds HN, but that selection is not saved.
    expect(minScoreInput().disabled).toBe(true)
  })

  it('includes min_score in the saved config payload', async () => {
    const wrapper = mountDialog()
    await flushPromises()

    fillNumber(minScoreInput(), '42')
    await flushPromises()

    // jsdom does not run form-submission activation on button clicks, so
    // trigger the submit event on the form itself.
    const form = document.body.querySelector('form') as HTMLFormElement
    form.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }))
    await flushPromises()

    const emitted = wrapper.emitted('save') as unknown as Array<{ config: string }>[]
    const payload = emitted[0][0]
    const parsed = JSON.parse(payload.config)
    expect(parsed.min_score).toBe(42)
  })

  it('restores min_score when editing an existing config', async () => {
    mountDialog({
      config: {
        id: 9,
        user_id: 1,
        name: 'Daily',
        schedule: '0 8 * * *',
        config: JSON.stringify({ feeds: [{ id: 2, highlights: 5, comments: 10 }], min_score: 7 }),
        created_at: '2026-01-01T00:00:00Z',
        updated_at: '2026-01-01T00:00:00Z',
      },
    })
    await flushPromises()

    expect(minScoreInput().value).toBe('7')
    // Editing a config with an hn feed keeps the field enabled.
    expect(minScoreInput().disabled).toBe(false)
  })
})

describe('DigestDialog delete confirmation', () => {
  const existingConfig = {
    id: 9,
    user_id: 1,
    name: 'Daily',
    schedule: '0 8 * * *',
    config: JSON.stringify({ feeds: [{ id: 2, highlights: 5, comments: 10 }] }),
    created_at: '2026-01-01T00:00:00Z',
    updated_at: '2026-01-01T00:00:00Z',
  }

  beforeEach(() => {
    document.body.innerHTML = ''
    vi.clearAllMocks()
  })

  // Deleting a digest config discards its generated results, so the button must
  // not be a one-click delete.
  it('does not delete on the first click — it confirms first', async () => {
    const wrapper = mountDialog({ config: existingConfig })
    await flushPromises()

    findButton('Delete').dispatchEvent(new MouseEvent('click', { bubbles: true }))
    await flushPromises()

    expect(wrapper.emitted('delete')).toBeUndefined()
    expect(document.body.textContent).toContain('Delete digest config?')
  })

  it('emits delete only after the confirmation is accepted', async () => {
    const wrapper = mountDialog({ config: existingConfig })
    await flushPromises()

    findButton('Delete').dispatchEvent(new MouseEvent('click', { bubbles: true }))
    await flushPromises()

    const confirm = document.body.querySelector('[data-delete-digest-confirm]') as HTMLElement
    expect(confirm).not.toBeNull()
    confirm.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    await flushPromises()

    expect(wrapper.emitted('delete')?.[0]).toEqual([9])
  })

  it('cancelling marks nothing deleted', async () => {
    const wrapper = mountDialog({ config: existingConfig })
    await flushPromises()

    findButton('Delete').dispatchEvent(new MouseEvent('click', { bubbles: true }))
    await flushPromises()
    findButton('Cancel').dispatchEvent(new MouseEvent('click', { bubbles: true }))
    await flushPromises()

    expect(wrapper.emitted('delete')).toBeUndefined()
    expect(document.body.textContent).not.toContain('Delete digest config?')
  })

  // The confirm action must stay reachable: a disabled button would make the
  // gate a dead end (the same trap as a control disabled until its own change).
  it('exposes an enabled delete action in the confirmation', async () => {
    mountDialog({ config: existingConfig })
    await flushPromises()

    findButton('Delete').dispatchEvent(new MouseEvent('click', { bubbles: true }))
    await flushPromises()

    const confirm = document.body.querySelector('[data-delete-digest-confirm]') as HTMLButtonElement
    expect(confirm.disabled).toBe(false)
  })

  it('offers no delete action when creating a new config', async () => {
    mountDialog()
    await flushPromises()

    expect(document.body.querySelector('[data-delete-digest]')).toBeNull()
  })
})

// A category name and a feed id are different kinds of value, so one shared
// selection array cannot hold both. Switching the Source toggle used to leave
// the other mode's values in place, which silently discarded the user's picks
// (and submitted stale entries from the wrong namespace).
describe('DigestDialog source-mode selections', () => {
  beforeEach(() => {
    document.body.innerHTML = ''
    vi.clearAllMocks()
  })

  function parsedEmittedConfig(wrapper: ReturnType<typeof mountDialog>) {
    const form = document.body.querySelector('form') as HTMLFormElement
    form.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }))
    const emitted = wrapper.emitted('save') as unknown as Array<{ config: string }>[]
    // Read the LATEST emit: the helper is called more than once in a test, and
    // each call submits the form again.
    return JSON.parse(emitted[emitted.length - 1][0].config)
  }

  it('restores the category selection when switching back from Feeds', async () => {
    const wrapper = mountDialog()
    await flushPromises()

    await selectSource('Tech')
    findButton('Feeds').dispatchEvent(new MouseEvent('click', { bubbles: true }))
    await flushPromises()
    findButton('Categories').dispatchEvent(new MouseEvent('click', { bubbles: true }))
    await flushPromises()

    const config = parsedEmittedConfig(wrapper)
    expect(config.categories?.map((c: any) => c.name)).toEqual(['Tech'])
  })

  it('keeps a feed selection separate from a category selection', async () => {
    const wrapper = mountDialog()
    await flushPromises()

    await selectSource('Tech')
    findButton('Feeds').dispatchEvent(new MouseEvent('click', { bubbles: true }))
    await flushPromises()
    await selectSource('Hacker News')

    const config = parsedEmittedConfig(wrapper)
    // Only the active mode is emitted, and it must not contain the category.
    expect(config.feeds?.map((f: any) => f.id)).toEqual([2])
    expect(config.categories).toBeUndefined()
  })

  it('does not carry a feed id into the category selection', async () => {
    const wrapper = mountDialog()
    await flushPromises()

    findButton('Feeds').dispatchEvent(new MouseEvent('click', { bubbles: true }))
    await flushPromises()
    await selectSource('Hacker News')
    findButton('Categories').dispatchEvent(new MouseEvent('click', { bubbles: true }))
    await flushPromises()
    await selectSource('Tech')

    const config = parsedEmittedConfig(wrapper)
    expect(config.categories?.map((c: any) => c.name)).toEqual(['Tech'])
    // The feed id 2 must not appear as a category name.
    expect(config.categories?.some((c: any) => String(c.name) === '2')).toBe(false)
  })

  it('seeds only the saved mode when editing a config', async () => {
    const wrapper = mountDialog({
      config: {
        id: 1, user_id: 1, name: 'D', schedule: '*',
        config: JSON.stringify({ feeds: [{ id: 2, highlights: 5, comments: 10 }] }),
        last_error: '', last_error_at: null, created_at: '', updated_at: '',
      },
    })
    await flushPromises()

    // The saved config names feeds, so Feeds mode is active and holds the id.
    const feedConfig = parsedEmittedConfig(wrapper)
    expect(feedConfig.feeds?.map((f: any) => f.id)).toEqual([2])

    // Categories mode starts empty: it must not inherit the feed id.
    findButton('Categories').dispatchEvent(new MouseEvent('click', { bubbles: true }))
    await flushPromises()
    const catConfig = parsedEmittedConfig(wrapper)
    expect(catConfig.categories).toEqual([])
  })
})
