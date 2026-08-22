<script setup lang="ts">
import { ref, computed, TransitionGroup } from 'vue'
import { useFeedStore, type FeedSort, type FeedStatusFilter } from '../stores/feed'
import FilterBar, { type FilterStatus, type FilterSort } from './FilterBar.vue'
import Skeleton from './ui/Skeleton.vue'
import Tooltip from './ui/Tooltip.vue'
import { Circle, CircleDot, CircleCheck, Check, ArrowUp, MessageSquare, LoaderCircle } from 'lucide-vue-next'
import { stripContent } from '../lib/utils'
import { useInfiniteScroll } from '../composables/useInfiniteScroll'
import { usePullToRefresh } from '../composables/usePullToRefresh'
import { relativeTime, absoluteTime } from '../lib/format'

const props = defineProps<{
  savedItems: any[]
  showingSaved: boolean
}>()

const emit = defineEmits<{
  (e: 'selectItem', item: any): void
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

// Pull-down-to-refresh on touch devices. Silent path: corner pulse + item
// slide-in, no full loading state. overscroll-behavior-y on the container
// suppresses the browser's native pull-to-refresh.
const { pull: pullDistance, refreshing: pullRefreshing } = usePullToRefresh(
  scrollEl,
  () => feedStore.refreshItems({ silent: true }),
)

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

function onFilterStatus(v: FilterStatus) { feedStore.changeFilter(v as FeedStatusFilter) }
function onSortValue(v: FilterSort) { feedStore.changeSort(SCORE_MAP[v] ?? 'date_desc') }
function onMinScore(v: number | null) { feedStore.changeMinScore(v) }

// ---- List ----

const displayItems = computed(() => {
  if (!props.showingSaved) return feedStore.items
  const filtered = feedStore.filter === 'all'
    ? props.savedItems
    : props.savedItems.filter(i => i.status === feedStore.filter)
  return sortSavedItems(filtered, feedStore.sort)
})

// The Saved view fetches once without server-side ordering, so filtering and
// sorting happen client-side there. Feed views rely on the server params.
function sortSavedItems(list: any[], sort: FeedSort): any[] {
  const sorted = [...list]
  const date = (i: any) => new Date(i.published_at ?? i.created_at).getTime() || 0
  const points = (i: any) => i.points ?? 0
  switch (sort) {
    case 'date_asc': return sorted.sort((a, b) => date(a) - date(b))
    case 'score_asc': return sorted.sort((a, b) => points(a) - points(b))
    case 'score_desc': return sorted.sort((a, b) => points(b) - points(a))
    default: return sorted.sort((a, b) => date(b) - date(a))
  }
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

</script>

<template>
  <div class="h-full overflow-y-auto overscroll-contain" ref="scrollEl" @scroll="onScroll">
    <FilterBar
      :status-filter="(feedStore.filter as FilterStatus)"
      :sort-value="(SCORE_MAP_REV[feedStore.sort] ?? 'newest') as FilterSort"
      :min-score="feedStore.minScore"
      :score-supported="scoreSupported"
      :item-count="showingSaved ? displayItems.length : feedStore.totalItems"
      :feed-name="showingSaved ? null : feedStore.activeFeed?.name ?? null"
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
    <div v-if="feedStore.loading && displayItems.length === 0" class="flex justify-center py-10" data-loading-spinner>
      <LoaderCircle class="size-6 animate-spin text-muted-foreground" />
    </div>
    <div v-else-if="!feedStore.activeFeedId && !showingSaved" class="p-6 text-center text-sm text-muted-foreground">Select a feed</div>
    <div v-else-if="showingSaved && savedItems.length === 0" class="p-6 text-center text-sm text-muted-foreground">No saved items</div>
    <template v-else>
      <!-- Refresh in flight with rows present: dim instead of replacing, so
           the list and scroll position stay visible during the reload.
           TransitionGroup: newly fetched items slide in from the top. -->
      <TransitionGroup name="item-enter" tag="div">
      <div v-for="item in displayItems" :key="item.id"
      role="button" tabindex="0"
      class="w-full text-left h-20 p-2 border-b hover:bg-accent/10 transition-colors relative overflow-hidden cursor-pointer"
      :class="{ 'bg-accent/10': item.id === feedStore.selectedItem?.id, 'text-foreground': item.status === 'unread', 'text-foreground/70': item.status === 'digested', 'text-muted-foreground': item.status === 'read', 'text-accent': item.status === 'saved', 'opacity-50 transition-opacity': feedStore.loading }"
      :data-selected-item="item.id === feedStore.selectedItem?.id ? '' : undefined"
      @click="onRowClick($event, item)"
      @keydown.enter.prevent="emit('selectItem', item)"
      @keydown.space.prevent="emit('selectItem', item)">
      <div class="flex items-start gap-1 min-w-0">
        <CircleDot v-if="item.status === 'unread'" class="size-3 shrink-0 mt-0.5 text-accent" />
        <CircleCheck v-else-if="item.status === 'saved'" class="size-3 shrink-0 mt-0.5 text-accent" />
        <span v-else-if="item.status === 'digested'" class="size-3 shrink-0 mt-0.5 inline-flex items-center justify-center text-[10px] leading-none text-muted-foreground/60" title="digested">◐</span>
        <Circle v-else class="size-3 shrink-0 mt-0.5 text-muted-foreground/40" />
        <span v-if="item.status === 'saved'" class="text-accent shrink-0 mt-0.5"><Check class="size-3" /></span>
        <p class="text-sm font-semibold truncate flex-1 min-w-0">{{ item.title }}</p>
        <span class="text-[10px] text-muted-foreground/70 shrink-0 mt-0.5" :title="absoluteTime(item.published_at)">{{ relativeTime(item.published_at) }}</span>
      </div>
      <p class="text-xs text-muted-foreground/70 line-clamp-2 mt-0.5 min-w-0 pr-16">{{ stripContent(item.extract_status === 'done' ? item.extract_content : item.content) }}</p>
      <div class="absolute right-2 bottom-2 flex items-center gap-2">
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
    </TransitionGroup>
    <div v-if="!props.showingSaved && feedStore.loadingMore" class="p-3">
      <Skeleton class="h-12" />
    </div>
    </template>
  </div>
</template>
