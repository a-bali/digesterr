import { describe, it, expect, beforeEach, afterEach } from 'vitest'
import { defineComponent, h, ref, nextTick } from 'vue'
import { mount, type VueWrapper } from '@vue/test-utils'
import { usePageTitle, digestResultTitle } from './usePageTitle'

interface SetupHandle<T> { value: T; wrapper: VueWrapper }
function withSetup<T>(setup: () => T): SetupHandle<T> {
  const handle: Partial<SetupHandle<T>> = {}
  const Comp = defineComponent({
    setup() {
      handle.value = setup()
      return () => h('div')
    },
  })
  handle.wrapper = mount(Comp, { attachTo: document.body })
  return handle as SetupHandle<T>
}

describe('usePageTitle', () => {
  const ORIGINAL = 'Original Title'
  beforeEach(() => { document.title = ORIGINAL })
  afterEach(() => { document.title = ORIGINAL })

  it('shows "[N] 📰" when no feed is selected', async () => {
    withSetup(() => usePageTitle({ total: 0, feedName: '', feedUnread: 0, itemTitle: '' }))
    await nextTick()
    expect(document.title).toBe('[0] 📰')
  })

  it('shows "[N] 📰" when no feed is selected and there are unreads', async () => {
    withSetup(() => usePageTitle({ total: 42, feedName: '', feedUnread: 0, itemTitle: '' }))
    await nextTick()
    expect(document.title).toBe('[42] 📰')
  })

  it('shows "[N] 📰 (FeedName: M)" when a feed is selected with no item', async () => {
    withSetup(() => usePageTitle({ total: 5, feedName: 'Hacker News', feedUnread: 12, itemTitle: '' }))
    await nextTick()
    expect(document.title).toBe('[5] 📰 (Hacker News: 12)')
  })

  it('shows "[N] 📰 (FeedName: 0)" when feed is selected with no unreads in it', async () => {
    withSetup(() => usePageTitle({ total: 42, feedName: 'Hacker News', feedUnread: 0, itemTitle: '' }))
    await nextTick()
    expect(document.title).toBe('[42] 📰 (Hacker News: 0)')
  })

  it('shows "[N] 📰 (FeedName: M) ItemTitle" when a feed + item are selected', async () => {
    withSetup(() => usePageTitle({
      total: 7,
      feedName: 'Hacker News',
      feedUnread: 12,
      itemTitle: 'Show HN: a project',
    }))
    await nextTick()
    expect(document.title).toBe('[7] 📰 (Hacker News: 12) Show HN: a project')
  })

  it('updates reactively when total changes', async () => {
    const total = ref(1)
    withSetup(() => usePageTitle({ total, feedName: 'RSS', feedUnread: 3, itemTitle: 't' }))
    await nextTick()
    expect(document.title).toBe('[1] 📰 (RSS: 3) t')
    total.value = 999
    await nextTick()
    expect(document.title).toBe('[999] 📰 (RSS: 3) t')
  })

  it('updates reactively when feedName, feedUnread, and itemTitle change', async () => {
    const feedName = ref('A')
    const feedUnread = ref(5)
    const itemTitle = ref('item1')
    withSetup(() => usePageTitle({ total: 3, feedName, feedUnread, itemTitle }))
    await nextTick()
    expect(document.title).toBe('[3] 📰 (A: 5) item1')
    feedName.value = 'B'
    feedUnread.value = 2
    itemTitle.value = 'item2'
    await nextTick()
    expect(document.title).toBe('[3] 📰 (B: 2) item2')
    feedName.value = ''
    await nextTick()
    expect(document.title).toBe('[3] 📰')
  })

  it('restores the original title on unmount', async () => {
    const { wrapper } = withSetup(() =>
      usePageTitle({ total: 4, feedName: 'X', feedUnread: 7, itemTitle: 'y' }))
    await nextTick()
    expect(document.title).toBe('[4] 📰 (X: 7) y')
    wrapper.unmount()
    expect(document.title).toBe(ORIGINAL)
  })

  it('falls back to "[0] 📰" when total is null/undefined and no feed', async () => {
    withSetup(() => usePageTitle({ total: null, feedName: '', feedUnread: 0, itemTitle: '' }))
    await nextTick()
    expect(document.title).toBe('[0] 📰')
    withSetup(() => usePageTitle({ total: undefined, feedName: '', feedUnread: 0, itemTitle: '' }))
    await nextTick()
    expect(document.title).toBe('[0] 📰')
  })

  it('trims whitespace in feedName and itemTitle', async () => {
    withSetup(() => usePageTitle({ total: 1, feedName: '  HN  ', feedUnread: 4, itemTitle: '  foo  ' }))
    await nextTick()
    expect(document.title).toBe('[1] 📰 (HN: 4) foo')
  })
})

describe('digestResultTitle', () => {
  it('returns the title from valid JSON content', () => {
    expect(digestResultTitle(JSON.stringify({ title: 'My Digest', summary: 'x' }))).toBe('My Digest')
  })

  it('returns empty string for null/empty content', () => {
    expect(digestResultTitle(null)).toBe('')
    expect(digestResultTitle(undefined)).toBe('')
    expect(digestResultTitle('')).toBe('')
  })

  it('returns empty string for invalid JSON', () => {
    expect(digestResultTitle('not json')).toBe('')
  })

  it('returns empty string when JSON has no title field', () => {
    expect(digestResultTitle(JSON.stringify({ summary: 'x' }))).toBe('')
  })
})
