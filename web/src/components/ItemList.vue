<script setup lang="ts">
import { ref, computed, TransitionGroup } from 'vue'
import { useFeedStore, type FeedSort, type FeedStatusFilter } from '../stores/feed'
import FilterBar, { type FilterStatus, type FilterSort } from './FilterBar.vue'
import Skeleton from './ui/Skeleton.vue'
import Tooltip from './ui/Tooltip.vue'
import { Circle, CircleDot, ArrowUp, MessageSquare, LoaderCircle, CheckCircle2, ChevronsUp, ChevronsDown } from 'lucide-vue-next'
import { stripContent, decodeHTMLEntities } from '../lib/utils'
import { feedIcon } from '../lib/format'
import { useInfiniteScroll } from '../composables/useInfiniteScroll'
import { usePullToRefresh } from '../composables/usePullToRefresh'
import { useSwipeAction } from '../composables/useSwipeAction'
import { relativeTime, absoluteTime } from '../lib/format'
import { scopeActionLabel, scopeEmptyLabel, markRelativeLabel, markRelativeShortLabel } from '../lib/markAllRead'

const props = defineProps<{
  savedItems: any[]
  showingSaved: boolean
  // Stacked desktop layout: one-line rows (title then excerpt on the same
  // line) instead of the two-line block with a taller min-height.
  compact?: boolean
}>()

const emit = defineEmits<{
  (e: 'selectItem', item: any): void
  (e: 'markAllRead'): void
}>()

const feedStore = useFeedStore()

// Infinite scroll: load more items as the list nears its bottom.
const { onScroll } = useInfiniteScroll({
  hasMore: () => !!feedStore.hasMore,
  loading: () => feedStore.loading,
  loadingMore: () => feedStore.loadingMore,
  loadMore: () => feedStore.fetchMoreItems(),
  threshold: 100,
})

const scrollEl = ref<HTMLElement | null>(null)

// Pull-down-to-refresh on touch devices. Silent UI (no dim/error toast) but
// clean replace — user's explicit refresh action, should not merge stale items.
//
// The Saved view needs its own fetcher: refreshItems() reloads the active FEED
// (or the multi-feed view) and returns early when neither is set, so in Saved it
// did nothing at all. fetchSavedItems reloads the saved list.
const { pull: pullDistance, refreshing: pullRefreshing } = usePullToRefresh(
  scrollEl,
  () => (props.showingSaved ? feedStore.fetchSavedItems() : feedStore.refreshItems({ silent: true, replace: true })),
)

// Touch devices have no hover, so on a phone the mark-above/below action is
// reached by swiping the row: right-to-left opens "mark all read below",
// left-to-right opens "mark all read above", and tapping the opened panel
// confirms. Desktop keeps the hover chevrons, unchanged.
const {
  onTouchStart: onSwipeStart,
  onTouchMove: onSwipeMove,
  onTouchEnd: onSwipeEnd,
  actionFor: swipeActionFor,
  close: closeSwipeAction,
  shouldSuppressClick,
} = useSwipeAction()

// The row content shifts aside by exactly the panel's width (w-36 = 9rem in
// the template above; keep the two in sync).
const SWIPE_SHIFT = 'translate-x-36'

function swipeShift(item: any): string {
  const dir = swipeActionFor(item.id)
  if (dir === 'below') return `-${SWIPE_SHIFT}`
  if (dir === 'above') return SWIPE_SHIFT
  return ''
}

async function confirmSwipeAction(item: any) {
  const dir = swipeActionFor(item.id)
  closeSwipeAction()
  if (dir === 'below') await feedStore.markReadBelow(item)
  else if (dir === 'above') await feedStore.markReadAbove(item)
}

// Collapse an open panel when the list scrolls under the finger. The event
// must be forwarded: useInfiniteScroll reads scrollTop/scrollHeight off
// e.target.
function onListScroll(e: Event) {
  onScroll(e)
  closeSwipeAction()
}

// ---- Filter / sort controls (delegated to FilterBar) ----

const STATUS_OPTIONS: { value: FilterStatus; label: string }[] = [
  { value: 'all', label: 'All' },
  { value: 'unread', label: 'Unread' },
  { value: 'read', label: 'Read' },
  { value: 'digested', label: 'Digested' },
  { value: 'saved', label: 'Saved' },
]

// Map between FilterBar types and feed store types.
const SCORE_MAP: Record<string, FeedSort> = { newest: 'date_desc', oldest: 'date_asc', highest: 'score_desc', lowest: 'score_asc' }
const SCORE_MAP_REV: Record<string, FilterSort> = { date_desc: 'newest', date_asc: 'oldest', score_desc: 'highest', score_asc: 'lowest' }

const scoreSupported = computed(() => !props.showingSaved && feedStore.supportsScore)

// The middle pane's header reports loaded/total for the CURRENT filter, so the
// two numbers always describe the same set. `totalItems` is the server's
// filter-aware count (CountItemsByFeed), not a length — the loaded length alone
// would read as "50 of 50" while the feed actually holds 566.
//
// `items` is emptied only on a clean load, so a background refresh can leave the
// focused item in the array after a filter/sort change (that is deliberate: the
// selection must not vanish under the cursor). That row belongs to a different
// result set, so subtracting it keeps loaded <= total. The saved view is exempt:
// it has no pagination and no server total, so loaded == total there.
const loadedCount = computed(() => {
  if (props.showingSaved) return Math.min(displayItems.value.length, feedStore.savedTotal)
  const loaded = feedStore.isMultiView ? feedStore.multiItems.length : feedStore.items.length
  const stale = feedStore.items.some(i => i.id === feedStore.selectedItem?.id) ? 1 : 0
  return Math.min(loaded - stale, feedStore.totalItems)
})
// The Saved view now has a real server total (filter-aware), so the header's
// loaded/total pair describes the filtered set instead of the loaded page.
const totalCount = computed(() => (props.showingSaved ? feedStore.savedTotal : feedStore.totalItems))

// Whether a mark-all-read is offered here at all. The button stays RENDERED
// whenever the list has items but is disabled once nothing is unread, so it never
// silently disappears (the sidebar controls follow the same rule). An unread item
// may sit outside the loaded page, so `totalItems` gates presence and the loaded
// rows gate enabled-ness.
const markAllReadApplies = computed(() =>
  (feedStore.activeFeedId != null || feedStore.isMultiView) && !props.showingSaved && feedStore.totalItems > 0,
)
const hasUnreadLoaded = computed(() => displayItems.value.some(i => i.status === 'unread'))
const showMarkAllRead = computed(() =>
  markAllReadApplies.value && !feedStore.loadingMore && (hasUnreadLoaded.value || feedStore.totalItems > 0),
)
const markAllReadDisabled = computed(() => feedStore.loadingMore || !hasUnreadLoaded.value)

function onFilterStatus(v: FilterStatus) { feedStore.changeFilter(v as FeedStatusFilter) }
function onSortValue(v: FilterSort) { feedStore.changeSort(SCORE_MAP[v] ?? 'date_desc') }
function onMinScore(v: number | null) { feedStore.changeMinScore(v) }

// ---- List ----

const displayItems = computed(() => {
  if (feedStore.isMultiView) return feedStore.multiItems as any[]
  if (!props.showingSaved) return feedStore.items
  // Rendered exactly as received: the Saved view is paginated, so the SERVER
  // owns both the read-state filter and the order (see handleListSavedItems).
  // Filtering or sorting the loaded page here would touch only the rows in
  // memory and leave the remaining pages in the old order — which is exactly how
  // the sort label came to disagree with the list.
  return props.savedItems
})

// Simple type-based favicon URL for items that carry feed_type.
function feedTypeIcon(feedType: string): string {
  if (feedType === 'hn') return 'https://news.ycombinator.com/favicon.ico'
  if (feedType === 'lobsters') return 'https://lobste.rs/favicon.ico'
  if (feedType === 'reddit') return 'https://www.reddit.com/favicon.ico'
  if (feedType === 'twitter') return 'https://abs.twimg.com/favicons/twitter.3.ico'
  // Email feeds deliberately have no icon URL to fetch: the sender varies per
  // message, so any fixed favicon would be misleading, and an image request per
  // row is not worth it. The row simply renders without an icon.
  return ''
}

// Feed name for multi-view items.
function feedNameFor(item: any): string {
  return item.feed_name ?? ''
}

const scale = computed(() => {
  const items = displayItems.value
  if (items.length === 0) return { points_max: 0, points_avg: 0, comments_max: 0, comments_avg: 0 }
  let pointsMax = 0, commentsMax = 0, pointsSum = 0, commentsSum = 0, pointsCount = 0, commentsCount = 0
  for (const item of items) {
    if (item.points > 0) {
      if (item.points > pointsMax) pointsMax = item.points
      pointsSum += item.points
      pointsCount++
    }
    if (item.comments_count > 0) {
      if (item.comments_count > commentsMax) commentsMax = item.comments_count
      commentsSum += item.comments_count
      commentsCount++
    }
  }
  return {
    points_max: pointsMax,
    points_avg: pointsCount > 0 ? Math.round(pointsSum / pointsCount) : 0,
    comments_max: commentsMax,
    comments_avg: commentsCount > 0 ? Math.round(commentsSum / commentsCount) : 0,
  }
})

function accentOpacity(val: number, max: number): number {
  if (max <= 0) return 0.4
  const r = Math.log(Math.max(val, 1)) / Math.log(Math.max(max, 2))
  if (r > 0.8) return 1
  if (r > 0.5) return 0.65
  if (r > 0.2) return 0.4
  return 0.2
}

function accentStyle(val: number, max: number): string {
  return `color: hsl(var(--accent) / ${accentOpacity(val, max)})`
}

// Mouse clicks blur the row so no focus outline lingers on it after the
// selection moves elsewhere (e.g. j/k navigation). Keyboard activation keeps
// focus for accessibility.
function onRowClick(e: MouseEvent, item: any) {
  // The browser fires a click when a swipe drag ends; it must not act on the
  // row, or the panel that swipe just opened would close again immediately.
  if (shouldSuppressClick()) return
  // While a panel is open a tap on the row dismisses it rather than opening the
  // item. The panel's own button stops propagation and confirms instead.
  if (swipeActionFor(item.id)) { closeSwipeAction(); return }
  ;(e.currentTarget as HTMLElement).blur()
  emit('selectItem', item)
}

function pointsTitle(val: number): string {
  const avg = scale.value.points_avg
  const diff = val - avg
  const sign = diff >= 0 ? '+' : ''
  return `${val} points (${sign}${diff})`
}

function commentsTitle(val: number): string {
  const avg = scale.value.comments_avg
  const diff = val - avg
  const sign = diff >= 0 ? '+' : ''
  return `${val} comments (${sign}${diff})`
}

// Hover actions: the two chevrons in the top right corner mark everything
// above or below a row as read, using the pane's current sort and filter.
async function markAboveRead(item: any) {
  await feedStore.markReadAbove(item)
}

async function markBelowRead(item: any) {
  await feedStore.markReadBelow(item)
}

</script>

<template>
  <div class="h-full overflow-y-auto overflow-x-hidden overscroll-contain" ref="scrollEl" @scroll="onListScroll">
    <FilterBar
      :status-filter="(feedStore.filter as FilterStatus)"
      :sort-value="(SCORE_MAP_REV[showingSaved ? feedStore.savedSort : feedStore.sort] ?? 'newest') as FilterSort"
      :min-score="feedStore.minScore"
      :score-supported="scoreSupported"
      :status-supported="!showingSaved"
      :loaded-count="loadedCount"
      :total-count="totalCount"
      :feed-name="showingSaved ? 'Saved' : (feedStore.isMultiView ? (feedStore.activeCategory ?? 'All Feeds') : feedStore.activeFeed?.name ?? null)"
      :status-options="STATUS_OPTIONS"
      @update:status-filter="onFilterStatus"
      @update:sort-value="onSortValue"
      @update:min-score="onMinScore"
    />
    <!-- Pull-to-refresh indicator: drops in with the finger while dragging,
         spins while the triggered refresh runs. Sits below the filter bar so
         its position matches the initial-loading spinner. -->
    <div
      class="flex items-center justify-center overflow-hidden transition-none"
      :style="{ height: pullRefreshing ? '48px' : `${pullDistance}px`, opacity: pullRefreshing ? 1 : Math.min(pullDistance / 70, 1) }"
    >
      <LoaderCircle
        class="size-5 text-muted-foreground"
        :class="pullRefreshing ? 'animate-spin' : ''"
        :style="pullRefreshing ? {} : { transform: `rotate(${pullDistance * 3}deg)` }"
      />
    </div>
    <!-- Empty + in flight: centered spinner. -->
    <div v-if="feedStore.loading && displayItems.length === 0" class="flex justify-center py-10" data-loading-spinner role="status" aria-live="polite">
      <LoaderCircle class="size-6 animate-spin text-muted-foreground" aria-hidden="true" />
      <span class="sr-only">Loading items</span>
    </div>
    <div v-else-if="!feedStore.activeFeedId && !feedStore.isMultiView && !showingSaved" class="p-6 text-center text-sm text-muted-foreground">Select a feed</div>
    <div v-else-if="showingSaved && savedItems.length === 0" class="p-6 text-center text-sm text-muted-foreground">{{ feedStore.filter === 'all' || feedStore.filter === 'saved' ? 'No saved items' : 'No saved items match this filter' }}</div>
    <template v-else>
      <!-- Refresh in flight with rows present: dim instead of replacing, so
           the list and scroll position stay visible during the reload.
           The row window is capped (see visibleItems): beyond a few hundred
           rows the DOM cost dominates scrolling, so the entrance animation
           only runs on small lists where every row is on screen anyway. -->
      <component :is="displayItems.length > 50 ? 'div' : TransitionGroup" name="item-enter" tag="div">
      <div v-for="item in displayItems" :key="item.id"
      role="button" tabindex="0"
      :aria-label="item.title"
      :aria-selected="item.id === feedStore.selectedItem?.id ? 'true' : undefined"
      class="group w-full text-left border-b hover:bg-accent/10 transition-colors relative overflow-hidden cursor-pointer [touch-action:pan-y_pinch-zoom]"
      :class="[
        compact ? 'h-7' : 'p-2',
        !compact && (feedStore.isMultiView ? 'min-h-[5.5rem]' : 'h-20'),
        { 'bg-accent/10': item.id === feedStore.selectedItem?.id, 'text-foreground': item.status === 'unread', 'text-foreground/70': item.status === 'digested', 'text-muted-foreground': item.status === 'read', 'text-accent': item.is_saved === 1, 'opacity-50 transition-opacity': feedStore.loading },
      ]"
      :data-selected-item="item.id === feedStore.selectedItem?.id ? '' : undefined"
      :data-swipe-action="swipeActionFor(item.id) ?? undefined"
      @click="onRowClick($event, item)"
      @touchstart="onSwipeStart($event, item.id)"
      @touchmove="onSwipeMove($event)"
      @touchend="onSwipeEnd"
      @touchcancel="onSwipeEnd"
      @keydown.enter.prevent="emit('selectItem', item)"
      @keydown.space.prevent="emit('selectItem', item)">

      <!-- Swipe action panels. Both are always mounted and parked off-screen so
           they can slide in when the row is swiped; the inactive one stays out
           of the a11y tree and the tab order. Tapping a panel confirms; tapping
           the row instead puts it away. -->
      <template v-if="!props.showingSaved">
        <button
          type="button"
          class="absolute inset-y-0 right-0 w-36 flex items-center justify-center gap-1 px-2 text-[11px] font-medium bg-accent text-accent-foreground transition-transform duration-150"
          :class="swipeActionFor(item.id) === 'below' ? 'translate-x-0' : 'translate-x-full'"
          data-swipe-below
          :aria-label="markRelativeLabel('below')"
          :aria-hidden="swipeActionFor(item.id) === 'below' ? undefined : 'true'"
          :tabindex="swipeActionFor(item.id) === 'below' ? undefined : -1"
          @click.stop="confirmSwipeAction(item)"
        >
          <ChevronsDown class="size-3.5 shrink-0" />
          <span class="leading-tight">{{ markRelativeShortLabel('below') }}</span>
        </button>
        <button
          type="button"
          class="absolute inset-y-0 left-0 w-36 flex items-center justify-center gap-1 px-2 text-[11px] font-medium bg-accent text-accent-foreground transition-transform duration-150"
          :class="swipeActionFor(item.id) === 'above' ? 'translate-x-0' : '-translate-x-full'"
          data-swipe-above
          :aria-label="markRelativeLabel('above')"
          :aria-hidden="swipeActionFor(item.id) === 'above' ? undefined : 'true'"
          :tabindex="swipeActionFor(item.id) === 'above' ? undefined : -1"
          @click.stop="confirmSwipeAction(item)"
        >
          <ChevronsUp class="size-3.5 shrink-0" />
          <span class="leading-tight">{{ markRelativeShortLabel('above') }}</span>
        </button>
      </template>

      <!-- Row content, shifted aside while the panel is open. In the compact
           (stacked) layout this must fill the row's content box: the inner line
           is <code>h-full items-center</code>, and a wrapper sized to its own
           content leaves that line 20px tall inside the 27px box, so the text
           sits 4px above the row's centre. The non-compact rows are padded and
           align to the top on purpose, so they keep the auto height. -->
      <div class="transition-transform duration-150" :class="[swipeShift(item), compact ? 'h-full' : '']" data-row-content>
      <!-- Stacked layout: one line — status, source feed, title, excerpt, time.
           The title and excerpt are SIBLINGS so the excerpt can only ever sit
           beside the title, never under it. -->
      <div v-if="compact" class="flex items-center gap-1.5 h-full min-w-0 px-2 text-sm">
        <!-- ONE merged state mark: saved wins it (★, accent — the same mark the
             header rail shows), else the read-state mark. The saved FLAG stays
             orthogonal in the data; only the display collapses, so a saved row
             shows exactly one mark before its title. -->
        <span
          class="shrink-0 inline-flex items-center justify-center size-3"
          :class="item.is_saved === 1 ? 'text-accent' : ''"
          :title="item.is_saved === 1 ? 'saved' : undefined"
        >
          <span v-if="item.is_saved === 1" class="text-[10px] leading-none">★</span>
          <CircleDot v-else-if="item.status === 'unread'" class="size-3 text-accent" />
          <span v-else-if="item.status === 'digested'" class="text-[10px] leading-none text-muted-foreground/60" title="digested">◐</span>
          <Circle v-else class="size-3 text-muted-foreground/40" />
        </span>
        <img
          v-if="feedStore.isMultiView && feedTypeIcon(item.feed_type)"
          :src="feedTypeIcon(item.feed_type)"
          class="size-3 shrink-0 rounded-sm"
          loading="lazy"
          alt=""
          aria-hidden="true"
          @error="($event.target as HTMLImageElement).style.display = 'none'"
        />
        <span
          v-if="feedStore.isMultiView && feedNameFor(item)"
          class="text-[10px] text-accent/70 shrink-0 max-w-[7rem] truncate"
        >{{ feedNameFor(item) }}</span>
        <span data-item-title class="font-semibold truncate shrink-0 max-w-[55%]">{{ decodeHTMLEntities(item.title) }}</span>
        <!-- The server flattens the bodies into `excerpt`; the client fallback
             covers a row holding a FULL item (opening one merges the fetched
             item into the list, and the detail response carries no excerpt). -->
        <span data-item-excerpt class="text-xs text-muted-foreground/70 truncate flex-1 min-w-0">{{ item.excerpt ?? stripContent(item.extract_status === 'done' ? item.extract_content : item.content) }}</span>
        <!-- Score, comments and the publish time form one cluster on the right;
             on hover the whole cluster gives way to the two chevrons, so the
             meta does not fight the action for the same pixels. -->
        <span class="shrink-0 relative flex items-center justify-end gap-1.5 h-4">
          <span class="group-hover:hidden flex items-center gap-1.5" data-item-meta>
            <Tooltip v-if="item.points > 0">
              <template #trigger>
                <span class="flex items-center gap-0.5 text-[10px]" :style="accentStyle(item.points, scale.points_max)" data-item-score>
                  <ArrowUp class="size-3" /> {{ item.points }}
                </span>
              </template>
              {{ pointsTitle(item.points) }}
            </Tooltip>
            <Tooltip v-if="item.comments_count > 0 || item.comment_url">
              <template #trigger>
                <span class="flex items-center gap-0.5 text-[10px]" :style="item.comments_count > 0 ? accentStyle(item.comments_count, scale.comments_max) : undefined" :class="item.comments_count > 0 ? '' : 'text-accent/30'" data-item-comments>
                  <MessageSquare class="size-3" /> <template v-if="item.comments_count > 0">{{ item.comments_count }}</template>
                </span>
              </template>
              <template v-if="item.comments_count > 0">{{ commentsTitle(item.comments_count) }}</template>
            </Tooltip>
            <span
              class="text-[10px] text-muted-foreground/70"
              data-item-time
              :title="absoluteTime(item.published_at)"
            >{{ relativeTime(item.published_at) }}</span>
          </span>
          <span v-if="!props.showingSaved" class="hidden group-hover:flex items-center gap-0.5" data-mark-actions>
            <button
              class="text-muted-foreground/50 hover:text-accent p-0.5"
              :title="markRelativeLabel('above')"
              :aria-label="markRelativeLabel('above')"
              data-mark-above
              @click.stop="markAboveRead(item)"
            >
              <ChevronsUp class="size-3" />
            </button>
            <button
              class="text-muted-foreground/50 hover:text-accent p-0.5"
              :title="markRelativeLabel('below')"
              :aria-label="markRelativeLabel('below')"
              data-mark-below
              @click.stop="markBelowRead(item)"
            >
              <ChevronsDown class="size-3" />
            </button>
          </span>
        </span>
      </div>
      <template v-else>
      <div class="flex items-start gap-1 min-w-0">
        <!-- Same ONE merged state mark as the compact row (and the header rail):
             saved wins the mark, else the read state. -->
        <span
          class="shrink-0 mt-0.5 inline-flex items-center justify-center size-3"
          :class="item.is_saved === 1 ? 'text-accent' : ''"
          :title="item.is_saved === 1 ? 'saved' : undefined"
        >
          <span v-if="item.is_saved === 1" class="text-[10px] leading-none">★</span>
          <CircleDot v-else-if="item.status === 'unread'" class="size-3 text-accent" />
          <span v-else-if="item.status === 'digested'" class="text-[10px] leading-none text-muted-foreground/60" title="digested">◐</span>
          <Circle v-else class="size-3 text-muted-foreground/40" />
        </span>
        <p class="text-sm font-semibold truncate flex-1 min-w-0">{{ decodeHTMLEntities(item.title) }}</p>
        <!-- Top right corner: the publish time gives way to the mark-read
             chevrons on hover. Every row gets them, whatever its status, so
             the action is in the same place on every item. Neither chevron
             marks the row being hovered — only the rows beyond it. -->
        <span class="shrink-0 mt-0.5 relative flex items-center justify-end h-4">
          <span
            class="text-[10px] text-muted-foreground/70 group-hover:hidden"
            data-item-time
            :title="absoluteTime(item.published_at)"
          >{{ relativeTime(item.published_at) }}</span>
          <span v-if="!props.showingSaved" class="hidden group-hover:flex items-center gap-0.5" data-mark-actions>
            <button
              class="text-muted-foreground/50 hover:text-accent p-0.5"
              :title="markRelativeLabel('above')"
              :aria-label="markRelativeLabel('above')"
              data-mark-above
              @click.stop="markAboveRead(item)"
            >
              <ChevronsUp class="size-3.5" />
            </button>
            <button
              class="text-muted-foreground/50 hover:text-accent p-0.5"
              :title="markRelativeLabel('below')"
              :aria-label="markRelativeLabel('below')"
              data-mark-below
              @click.stop="markBelowRead(item)"
            >
              <ChevronsDown class="size-3.5" />
            </button>
          </span>
        </span>
      </div>
      <p class="text-xs text-muted-foreground/70 line-clamp-2 mt-0.5 min-w-0" :class="feedStore.isMultiView ? '' : 'pr-16'">{{ item.excerpt ?? stripContent(item.extract_status === 'done' ? item.extract_content : item.content) }}</p>
      <!-- Multi-view bottom: feed name + score/comments on same line -->
      <div v-if="feedStore.isMultiView" class="flex items-center justify-between mt-2">
        <div v-if="feedNameFor(item)" class="flex items-center gap-1 min-w-0">
          <img v-if="feedTypeIcon(item.feed_type)" :src="feedTypeIcon(item.feed_type)" class="size-3 shrink-0 rounded-sm" loading="lazy" alt="" aria-hidden="true" @error="($event.target as HTMLImageElement).style.display = 'none'" />
          <span class="text-[10px] text-accent/70 truncate">{{ feedNameFor(item) }}</span>
        </div>
        <span v-else />
        <div class="flex items-center gap-2 shrink-0">
          <Tooltip v-if="item.points > 0">
            <template #trigger>
              <span class="flex items-center gap-0.5 text-[10px]" :style="accentStyle(item.points, scale.points_max)">
                <ArrowUp class="size-3" /> {{ item.points }}
              </span>
            </template>
            {{ pointsTitle(item.points) }}
          </Tooltip>
          <Tooltip v-if="item.comments_count > 0 || item.comment_url">
            <template #trigger>
              <span class="flex items-center gap-0.5 text-[10px]" :style="item.comments_count > 0 ? accentStyle(item.comments_count, scale.comments_max) : undefined" :class="item.comments_count > 0 ? '' : 'text-accent/30'">
                <MessageSquare class="size-3" /> <template v-if="item.comments_count > 0">{{ item.comments_count }}</template>
              </span>
            </template>
            <template v-if="item.comments_count > 0">{{ commentsTitle(item.comments_count) }}</template>
          </Tooltip>
        </div>
      </div>
      <!-- Single-view: absolute score/comments -->
      <div v-else class="absolute right-2 bottom-2 flex items-center gap-2">
        <Tooltip v-if="item.points > 0">
          <template #trigger>
            <span class="flex items-center gap-0.5 text-[10px]" :style="accentStyle(item.points, scale.points_max)">
              <ArrowUp class="size-3" /> {{ item.points }}
            </span>
          </template>
          {{ pointsTitle(item.points) }}
        </Tooltip>
        <Tooltip v-if="item.comments_count > 0 || item.comment_url">
          <template #trigger>
            <span class="flex items-center gap-0.5 text-[10px]" :style="item.comments_count > 0 ? accentStyle(item.comments_count, scale.comments_max) : undefined" :class="item.comments_count > 0 ? '' : 'text-accent/30'">
              <MessageSquare class="size-3" /> <template v-if="item.comments_count > 0">{{ item.comments_count }}</template>
            </span>
          </template>
          <template v-if="item.comments_count > 0">{{ commentsTitle(item.comments_count) }}</template>
        </Tooltip>
      </div>
      </template>
      </div>
      </div>
    </component>
    <div v-if="feedStore.loadingMore" class="p-3">
      <Skeleton class="h-12" />
    </div>
    </template>
    <!-- End-of-list mark-all-read button. Present-but-disabled once nothing is
         unread, matching the sidebar controls, so the action never looks absent. -->
    <div v-if="showMarkAllRead" class="px-4 py-6 flex items-center justify-center">
      <button
        class="inline-flex items-center gap-2 px-5 py-2.5 rounded-lg border border-accent/20 bg-accent/5 text-accent text-sm font-medium hover:bg-accent/10 active:bg-accent/20 transition-colors disabled:opacity-40 disabled:cursor-default disabled:hover:bg-accent/5"
        :disabled="markAllReadDisabled"
        :title="markAllReadDisabled ? scopeEmptyLabel({ kind: 'feeds-section' }) : scopeActionLabel({ kind: 'feeds-section' })"
        data-mark-all-read
        @click="emit('markAllRead')"
      >
        <CheckCircle2 class="size-4" />
        {{ scopeActionLabel({ kind: 'feeds-section' }) }}
      </button>
    </div>
  </div>
</template>
