import { watch, onUnmounted, type MaybeRefOrGetter, toValue } from 'vue'

export interface PageTitleParts {
  /** Total unread count across the whole app. */
  total: MaybeRefOrGetter<number | null | undefined>
  /** Name of the currently selected feed or digest config; '' if none. */
  feedName: MaybeRefOrGetter<string>
  /** Title of the currently selected item/digest; '' if none. */
  itemTitle: MaybeRefOrGetter<string>
}

/**
 * Sets document.title to `[N] (feedName) itemTitle` reactively.
 *
 * Format:
 *   - nothing selected:       `[N] Digesterr` (or `Digesterr` when N=0)
 *   - feed selected:          `[N] (FeedName) ItemTitle`
 *   - empty feedName:         `[N] Digesterr` (defensive — shouldn't happen via the standard flows)
 *
 * The original document.title is restored when the consumer component unmounts.
 * The only consumer in the app is HomeView.
 */
export function usePageTitle({ total, feedName, itemTitle }: PageTitleParts) {
  const originalTitle = typeof document !== 'undefined' ? document.title : ''

  watch(
    () => ({
      total: toValue(total),
      feedName: toValue(feedName),
      itemTitle: toValue(itemTitle),
    }),
    (parts) => {
      if (typeof document !== 'undefined') document.title = formatTitle(parts)
    },
    { immediate: true },
  )

  onUnmounted(() => {
    if (typeof document !== 'undefined') document.title = originalTitle
  })
}

function formatTitle(parts: { total: number | null | undefined; feedName: string; itemTitle: string }): string {
  const n = parts.total ?? 0
  const name = parts.feedName?.trim() ?? ''
  const title = parts.itemTitle?.trim() ?? ''
  if (!name) {
    if (n > 0) return `[${n}] Digesterr`
    return 'Digesterr'
  }
  if (title) return `[${n}] (${name}) ${title}`
  return `[${n}] (${name})`
}

/** Extract the rendered title from a digest result's JSON content blob. */
export function digestResultTitle(content: string | null | undefined): string {
  if (!content) return ''
  try {
    const parsed = JSON.parse(content)
    if (parsed && typeof parsed === 'object' && typeof parsed.title === 'string') return parsed.title
  } catch { /* legacy non-JSON */ }
  return ''
}
