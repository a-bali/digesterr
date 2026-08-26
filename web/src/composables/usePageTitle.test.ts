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

  it('shows "Digesterr" when no feed is selected and there are no unreads', async () => {
    withSetup(() => usePageTitle({ total: 0, feedName: '', itemTitle: '' }))
    await nextTick()
    expect(document.title).toBe('Digesterr')
  })

  it('shows "[N] Digesterr" when no feed is selected and there are unreads', async () => {
    withSetup(() => usePageTitle({ total: 42, feedName: '', itemTitle: '' }))
    await nextTick()
    expect(document.title).toBe('[42] Digesterr')
  })

  it('shows "[N] (FeedName)" when a feed is selected with no item', async () => {
    withSetup(() => usePageTitle({ total: 5, feedName: 'Hacker News', itemTitle: '' }))
    await nextTick()
    expect(document.title).toBe('[5] (Hacker News)')
  })

  it('shows "[N] (FeedName) ItemTitle" when a feed + item are selected', async () => {
    withSetup(() => usePageTitle({
      total: 7,
      feedName: 'Hacker News',
      itemTitle: 'Show HN: a project',
    }))
    await nextTick()
    expect(document.title).toBe('[7] (Hacker News) Show HN: a project')
  })

  it('updates reactively when total changes', async () => {
    const total = ref(1)
    withSetup(() => usePageTitle({ total, feedName: 'RSS', itemTitle: 't' }))
    await nextTick()
    expect(document.title).toBe('[1] (RSS) t')
    total.value = 999
    await nextTick()
    expect(document.title).toBe('[999] (RSS) t')
  })

  it('updates reactively when feedName and itemTitle change', async () => {
    const feedName = ref('A')
    const itemTitle = ref('item1')
    withSetup(() => usePageTitle({ total: 3, feedName, itemTitle }))
    await nextTick()
    expect(document.title).toBe('[3] (A) item1')
    feedName.value = 'B'
    itemTitle.value = 'item2'
    await nextTick()
    expect(document.title).toBe('[3] (B) item2')
    feedName.value = ''
    await nextTick()
    expect(document.title).toBe('[3] Digesterr')
  })

  it('restores the original title on unmount', async () => {
    const { wrapper } = withSetup(() => usePageTitle({ total: 4, feedName: 'X', itemTitle: 'y' }))
    await nextTick()
    expect(document.title).toBe('[4] (X) y')
    wrapper.unmount()
    expect(document.title).toBe(ORIGINAL)
  })

  it('falls back to plain "Digesterr" when total is null/undefined and no feed', async () => {
    withSetup(() => usePageTitle({ total: null, feedName: '', itemTitle: '' }))
    await nextTick()
    expect(document.title).toBe('Digesterr')
    withSetup(() => usePageTitle({ total: undefined, feedName: '', itemTitle: '' }))
    await nextTick()
    expect(document.title).toBe('Digesterr')
  })

  it('trims whitespace in feedName and itemTitle', async () => {
    withSetup(() => usePageTitle({ total: 1, feedName: '  HN  ', itemTitle: '  foo  ' }))
    await nextTick()
    expect(document.title).toBe('[1] (HN) foo')
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
