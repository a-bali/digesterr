<script setup lang="ts">
import { ref, computed, watch, type Component } from 'vue'
import { useFeedStore, type FeedSort, type FeedStatusFilter } from '../stores/feed'
import Skeleton from './ui/Skeleton.vue'
import Tooltip from './ui/Tooltip.vue'
import DropdownMenu from './ui/DropdownMenu.vue'
import Button from './ui/Button.vue'
import { Circle, CircleDot, CircleCheck, Check, ArrowUp, MessageSquare, ChevronDown } from 'lucide-vue-next'
import { stripContent } from '../lib/utils'
import { relativeTime, absoluteTime } from '../lib/format'

const props = defineProps<{
  savedItems: any[]
  showingSaved: boolean
}>()

const emit = defineEmits<{
  (e: 'selectItem', item: any): void
}>()

const feedStore = useFeedStore()

const scrollEl = ref<HTMLElement | null>(null)

// ---- Filter / sort controls (approved UX: state-label buttons) ----

const STATUS_OPTIONS: { value: FeedStatusFilter; label: string }[] = [
  { value: 'all', label: 'All' },
  { value: 'unread', label: 'Unread' },
  { value: 'read', label: 'Read' },
  { value: 'digested', label: 'Digested' },
  { value: 'saved', label: 'Saved' },
]

const SORT_SHORT_LABELS: Record<FeedSort, string> = {
  date_desc: 'Newest first',
  date_asc: 'Oldest first',
  score_desc: 'Score: high → low',
  score_asc: 'Score: low → high',
}

type MenuItem = { label: string; icon?: Component; disabled?: boolean; separator?: boolean; onClick?: () => void }

// Score filter/sort are available only for score-capable feed types. The Saved
// view mixes feed types, so it is always unsupported: options are greyed out,
// never hidden.
const scoreSupported = computed(() => !props.showingSaved && feedStore.supportsScore)

// Button label text IS the applied state. The score part only appears when a
// positive threshold is active AND scores are supported (approved mockup).
const filterLabel = computed(() => {
  const status = STATUS_OPTIONS.find(o => o.value === feedStore.filter)?.label ?? 'All'
  const min = feedStore.minScore
  return min != null && min > 0 && scoreSupported.value ? `${status} · ≥${min}` : status
})

const sortLabel = computed(() => SORT_SHORT_LABELS[feedStore.sort])

const filterItems = computed<MenuItem[]>(() =>
  STATUS_OPTIONS.map(opt => ({
    label: opt.label,
    icon: feedStore.filter === opt.value ? Check : undefined,
    onClick: () => {
      if (opt.value !== feedStore.filter) feedStore.changeFilter(opt.value)
    },
  })),
)

const sortItems = computed<MenuItem[]>(() => [
  {
    label: 'Newest first',
    icon: feedStore.sort === 'date_desc' ? Check : undefined,
    onClick: () => feedStore.changeSort('date_desc'),
  },
  {
    label: 'Oldest first',
    icon: feedStore.sort === 'date_asc' ? Check : undefined,
    onClick: () => feedStore.changeSort('date_asc'),
  },
  {
    label: 'Highest first',
    separator: true,
    disabled: !scoreSupported.value,
    icon: feedStore.sort === 'score_desc' ? Check : undefined,
    onClick: () => feedStore.changeSort('score_desc'),
  },
  {
    label: 'Lowest first',
    disabled: !scoreSupported.value,
    icon: feedStore.sort === 'score_asc' ? Check : undefined,
    onClick: () => feedStore.changeSort('score_asc'),
  },
])

// Numeric min-score input: 0 / empty means "any". Applied on change (blur or
// Enter) rather than per keystroke to avoid spamming the server.
const minScoreDraft = ref('')
watch(
  () => feedStore.minScore,
  v => {
    minScoreDraft.value = v != null && v > 0 ? String(v) : ''
  },
  { immediate: true },
)

const minScoreSet = computed(() => feedStore.minScore != null && feedStore.minScore > 0)

function applyMinScore(raw: string) {
  const trimmed = raw.trim()
  const n = trimmed === '' ? null : Number(trimmed)
  const next = n != null && Number.isFinite(n) && n > 0 ? Math.floor(n) : null
  minScoreDraft.value = next != null ? String(next) : ''
  if (next !== feedStore.minScore) feedStore.changeMinScore(next)
}

function onMinScoreChange(e: Event) {
  applyMinScore((e.target as HTMLInputElement).value)
}

function clearMinScore() {
  minScoreDraft.value = ''
  if (feedStore.minScore != null) feedStore.changeMinScore(null)
}

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


function onScroll() {
  const el = scrollEl.value
  if (!el || !feedStore.hasMore || feedStore.loadingMore || feedStore.loading) return
  if (el.scrollTop + el.clientHeight >= el.scrollHeight - 100) {
    feedStore.fetchMoreItems()
  }
}

</script>

<template>
  <div class="h-full overflow-y-auto" ref="scrollEl" @scroll="onScroll">
    <div class="p-2 border-b flex items-center gap-1">
      <DropdownMenu :items="filterItems">
        <template #trigger>
          <Button variant="ghost" size="sm" aria-label="Filter items">
            <span>Filter: {{ filterLabel }}</span>
            <ChevronDown class="size-4" />
          </Button>
        </template>
        <template #separator>
          <div class="px-2 py-1.5">
            <p class="mb-1 text-[10px] font-semibold uppercase tracking-wide text-muted-foreground">Minimum score</p>
            <div class="flex items-center gap-2">
              <input
                type="number"
                min="0"
                :value="minScoreDraft"
                :disabled="!scoreSupported"
                placeholder="Any"
                aria-label="Minimum score"
                class="h-7 w-20 rounded-md border border-border bg-background px-2 text-xs focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring disabled:cursor-not-allowed disabled:opacity-50"
                @change="onMinScoreChange"
              />
              <button
                type="button"
                class="text-xs font-semibold text-accent disabled:cursor-not-allowed disabled:text-muted-foreground"
                :disabled="!scoreSupported || !minScoreSet"
                @click="clearMinScore"
              >
                {{ minScoreSet ? 'Clear' : 'Any' }}
              </button>
            </div>
          </div>
        </template>
      </DropdownMenu>
      <DropdownMenu :items="sortItems">
        <template #trigger>
          <Button variant="ghost" size="sm" aria-label="Sort items">
            <span>Sort: {{ sortLabel }}</span>
            <ChevronDown class="size-4" />
          </Button>
        </template>
      </DropdownMenu>
    </div>
    <Skeleton v-if="feedStore.loading" class="m-3 h-12" />
    <div v-else-if="!feedStore.activeFeedId && !showingSaved" class="p-6 text-center text-sm text-muted-foreground">Select a feed</div>
    <div v-else-if="showingSaved && savedItems.length === 0" class="p-6 text-center text-sm text-muted-foreground">No saved items</div>
    <template v-else>
      <div v-for="item in displayItems" :key="item.id"
      role="button" tabindex="0"
      class="w-full text-left h-20 p-2 border-b hover:bg-accent/10 transition-colors relative overflow-hidden cursor-pointer"
      :class="{ 'bg-accent/10 font-medium text-accent': item.id === feedStore.selectedItem?.id, 'text-foreground': item.status === 'unread', 'text-foreground/70': item.status === 'digested', 'text-muted-foreground': item.status === 'read', 'text-accent': item.status === 'saved' }"
      @click="emit('selectItem', item)"
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
    <div v-if="!props.showingSaved && feedStore.loadingMore" class="p-3">
      <Skeleton class="h-12" />
    </div>
    </template>
  </div>
</template>
