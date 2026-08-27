import { watch, onUnmounted, type MaybeRefOrGetter, toValue } from 'vue'

export interface PageTitleParts {
  /** Total unread count across the whole app. */
  total: MaybeRefOrGetter<number | null | undefined>
  /** Name of the currently selected feed or digest config; '' if none. */
  feedName: MaybeRefOrGetter<string>
  /** Unread count in the currently selected feed; 0 if none. */
  feedUnread: MaybeRefOrGetter<number | null | undefined>
  /** Title of the currently selected item/digest; '' if none. */
  itemTitle: MaybeRefOrGetter<string>
}

/**
 * Sets document.title reactively.
 *
 * Format:
 *   - nothing selected:       `[N] 📰`
 *   - feed selected:          `[N] 📰 (FeedName: M)`
 *   - feed + item selected:   `[N] 📰 (FeedName: M) ItemTitle`
 *   - empty feedUnread:       `[N] 📰 (FeedName: 0)` (defensive — show the zero)
 *
 * `total` is always the app-wide unread total, regardless of selection.
 * The feed name is always wrapped in parens when present.
 *
 * The original document.title is restored when the consumer component unmounts.
 * The only consumer in the app is HomeView.
 */
export function usePageTitle({ total, feedName, feedUnread, itemTitle }: PageTitleParts) {
  const originalTitle = typeof document !== 'undefined' ? document.title : ''

  watch(
    () => ({
      total: toValue(total),
      feedName: toValue(feedName),
      feedUnread: toValue(feedUnread),
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

function formatTitle(parts: {
  total: number | null | undefined
  feedName: string
  feedUnread: number | null | undefined
  itemTitle: string
}): string {
  const n = parts.total ?? 0
  const name = parts.feedName?.trim() ?? ''
  const m = parts.feedUnread ?? 0
  const title = parts.itemTitle?.trim() ?? ''
  if (!name) return `[${n}] 📰`
  const feedPart = `(${name}: ${m})`
  if (title) return `[${n}] 📰 ${feedPart} ${title}`
  return `[${n}] 📰 ${feedPart}`
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
