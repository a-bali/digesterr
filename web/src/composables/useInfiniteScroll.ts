// Shared infinite-scroll predicate. Attach the returned handler to the
// scroller's @scroll event; it fires loadMore once when the user nears the
// bottom, gated by hasMore/loading guards and a pixel threshold.
export function useInfiniteScroll(opts: {
  hasMore: () => boolean
  loading: () => boolean
  loadingMore?: () => boolean
  loadMore: () => void
  threshold?: number
}) {
  const threshold = opts.threshold ?? 100

  function onScroll(e: Event) {
    const el = e.target as HTMLElement | null
    if (!el || !opts.hasMore() || opts.loading() || opts.loadingMore?.()) return
    if (el.scrollTop + el.clientHeight >= el.scrollHeight - threshold) {
      opts.loadMore()
    }
  }

  return { onScroll }
}
