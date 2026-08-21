import { describe, it, expect, vi } from 'vitest'
import { useInfiniteScroll } from './useInfiniteScroll'

function scrollEvent(top: number, clientHeight: number, scrollHeight: number): Event {
  const target = { scrollTop: top, clientHeight, scrollHeight } as HTMLElement
  return { target } as unknown as Event
}

describe('useInfiniteScroll', () => {
  it('fires loadMore when scrolled near the bottom', () => {
    const loadMore = vi.fn()
    const { onScroll } = useInfiniteScroll({
      hasMore: () => true,
      loading: () => false,
      loadMore,
      threshold: 100,
    })
    onScroll(scrollEvent(950, 100, 1000))
    expect(loadMore).toHaveBeenCalledTimes(1)
  })

  it('does not fire when far from the bottom', () => {
    const loadMore = vi.fn()
    const { onScroll } = useInfiniteScroll({
      hasMore: () => true,
      loading: () => false,
      loadMore,
      threshold: 100,
    })
    onScroll(scrollEvent(500, 100, 1000))
    expect(loadMore).not.toHaveBeenCalled()
  })

  it('respects hasMore and loading guards', () => {
    let hasMore = false
    let loading = true
    const loadMore = vi.fn()
    const { onScroll } = useInfiniteScroll({
      hasMore: () => hasMore,
      loading: () => loading,
      loadingMore: () => false,
      loadMore,
      threshold: 100,
    })
    const atBottom = scrollEvent(950, 100, 1000)
    onScroll(atBottom)
    expect(loadMore).not.toHaveBeenCalled()
    hasMore = true
    onScroll(atBottom)
    expect(loadMore).not.toHaveBeenCalled() // still loading
    loading = false
    onScroll(atBottom)
    expect(loadMore).toHaveBeenCalledTimes(1)
  })
})
