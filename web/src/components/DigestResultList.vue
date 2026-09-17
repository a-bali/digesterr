<script setup lang="ts">
import { ref, computed } from 'vue'
import { useDigestStore } from '../stores/digest'
import FilterBar, { type FilterStatus, type FilterSort } from './FilterBar.vue'
import Skeleton from './ui/Skeleton.vue'
import { Circle, CircleDot, LoaderCircle, CheckCircle2, ChevronsUp, ChevronsDown } from 'lucide-vue-next'
import { useInfiniteScroll } from '../composables/useInfiniteScroll'
import { absoluteTime, relativeTime } from '../lib/format'
import type { DigestResult, DigestResultWithConfigName } from '../types/api'

const props = defineProps<{
  showingAll?: boolean
  // Stacked desktop layout: one-line rows (title then item count on the same
  // line) instead of the taller two-line block.
  compact?: boolean
}>()

const emit = defineEmits<{ (e: 'select', result: DigestResult): void; (e: 'markAllRead'): void }>()

const digestStore = useDigestStore()

// Infinite scroll: load more results as the list nears its bottom.
const { onScroll } = useInfiniteScroll({
  hasMore: () => !!digestStore.hasMore,
  loading: () => digestStore.loading,
  loadingMore: () => digestStore.loadingMore,
  loadMore: () => digestStore.fetchMoreResults(),
  threshold: 200,
})

// The status filter lives in the digest store so the 'u' shortcut can reach it
// from HomeView; see resultFilter's comment in stores/digest.ts. Sort order is
// purely presentational, so it stays local.
const digestFilter = computed({
  get: () => digestStore.resultFilter,
  set: v => { digestStore.resultFilter = v },
})
const sortOrder = ref<FilterSort>('newest')

const STATUS_OPTIONS: { value: FilterStatus; label: string }[] = [
  { value: 'all', label: 'All' },
  { value: 'unread', label: 'Unread' },
  { value: 'read', label: 'Read' },
]

const visibleResults = computed<DigestResultWithConfigName[]>(() => {
  const source = props.showingAll ? digestStore.allResults : digestStore.results
  const filtered = digestFilter.value === 'all'
    ? source
    : source.filter(res => res.status === digestFilter.value)
  const dir = sortOrder.value === 'newest' ? -1 : 1
  return [...filtered].sort(
    (a, b) => dir * (new Date(a.created_at).getTime() - new Date(b.created_at).getTime()),
  )
})

function itemCount(result: DigestResult): number {
  return (result as any).item_count ?? 0
}

function resultTitle(result: DigestResult): string {
  const r = result as any
  if (r.title) return r.title
  return absoluteTime(result.created_at)
}

function isSelected(result: DigestResult): boolean {
  return result.id === digestStore.selectedResult?.id
}

const feedLabel = computed(() => {
  if (props.showingAll) return 'All Digests'
  return digestStore.activeConfig?.name ?? null
})

const showMarkAllRead = computed(() =>
  visibleResults.value.some(r => r.status !== 'read')
)

// Hover actions: the two chevrons in the top right corner mark everything
// above or below a row as read. Identical to the item list — same icons, same
// corner, same labels, on every row whatever its status.
async function markAboveRead(result: DigestResult) {
  await digestStore.markReadAbove(result)
}

async function markBelowRead(result: DigestResult) {
  await digestStore.markReadBelow(result)
}

function onSelect(e: MouseEvent, result: DigestResult) {
  // Blur on mouse click so no focus outline lingers after selection moves.
  ;(e.currentTarget as HTMLElement).blur()
  if (result.status !== 'read') digestStore.updateResultStatus(result.id, 'read')
  digestStore.selectResult(result)
  emit('select', result)
}

</script>

<template>
  <div class="h-full overflow-y-auto overflow-x-hidden" @scroll="onScroll">
    <FilterBar
      :status-filter="digestFilter"
      :sort-value="sortOrder"
      :min-score="null"
      :score-supported="false"
      :loaded-count="visibleResults.length"
      :total-count="visibleResults.length"
      :feed-name="feedLabel"
      :status-options="STATUS_OPTIONS"
      @update:status-filter="digestFilter = $event"
      @update:sort-value="sortOrder = $event"
    />
    <!-- Results load async; centered spinner while the list is empty. -->
    <div v-if="digestStore.loading && visibleResults.length === 0" class="flex justify-center py-10" data-loading-spinner>
      <LoaderCircle class="size-6 animate-spin text-muted-foreground" />
    </div>
    <div v-else>
      <div v-for="result in visibleResults" :key="result.id"
        role="button" tabindex="0"
        class="group w-full text-left border-b hover:bg-accent/10 transition-colors relative overflow-hidden cursor-pointer"
        :class="[
          compact ? 'h-7' : 'p-2',
          !compact && (showingAll ? 'min-h-[5.5rem]' : 'h-20'),
          { 'bg-accent/10': isSelected(result), 'text-foreground': result.status === 'unread', 'text-muted-foreground': result.status === 'read', 'opacity-50': digestStore.loadingMore },
        ]"
        @click="onSelect($event, result)"
        @keydown.enter.prevent="onSelect($event, result)"
        @keydown.space.prevent="onSelect($event, result)"
      >
        <!-- Stacked layout: one line — status, title, item count, time, and the
             same hover actions. The title and count are SIBLINGS so the count
             can only ever sit beside the title, never under it. The whole
             right-hand cluster (count + time) gives way to the two chevrons on
             hover, exactly like the item list. -->
        <div v-if="compact" class="flex items-center gap-1.5 h-full min-w-0 px-2 text-sm">
          <CircleDot v-if="result.status === 'unread'" class="size-3 shrink-0 text-accent" />
          <Circle v-else class="size-3 shrink-0 text-muted-foreground/40" />
          <span data-item-title class="font-semibold truncate shrink-0 max-w-[55%]">{{ resultTitle(result) }}</span>
          <span class="flex-1 min-w-0" />
          <span class="shrink-0 relative flex items-center justify-end gap-1.5 h-4">
            <span class="group-hover:hidden flex items-center gap-1.5" data-item-meta>
              <span class="text-[10px] text-muted-foreground/70" data-item-count>{{ result.item_count }} items</span>
              <span class="text-[10px] text-muted-foreground/70" data-item-time :title="absoluteTime(result.created_at)">{{ relativeTime(result.created_at) }}</span>
            </span>
            <span class="hidden group-hover:flex items-center gap-0.5" data-mark-actions>
              <button
                class="text-muted-foreground/50 hover:text-accent p-0.5"
                title="Mark everything above as read, except this item"
                aria-label="Mark everything above as read, except this item"
                data-mark-above
                @click.stop="markAboveRead(result)"
              >
                <ChevronsUp class="size-3" />
              </button>
              <button
                class="text-muted-foreground/50 hover:text-accent p-0.5"
                title="Mark everything below as read, except this item"
                aria-label="Mark everything below as read, except this item"
                data-mark-below
                @click.stop="markBelowRead(result)"
              >
                <ChevronsDown class="size-3" />
              </button>
            </span>
          </span>
        </div>
        <template v-else>
        <div class="flex items-start gap-1 min-w-0">
          <CircleDot v-if="result.status === 'unread'" class="size-3 shrink-0 mt-0.5 text-accent" />
          <Circle v-else class="size-3 shrink-0 mt-0.5 text-muted-foreground/40" />
          <div class="flex-1 min-w-0">
            <p class="text-sm font-semibold truncate">{{ resultTitle(result) }}</p>
            <p class="text-xs text-muted-foreground truncate" data-item-count>{{ result.item_count }} items</p>
          </div>
          <!-- Top right corner: the publish time gives way to the mark-read
               chevrons on hover. Every row gets them, whatever its status, so
               the action is in the same place on every item. Neither chevron
               marks the row being hovered — only the rows beyond it. -->
          <span class="shrink-0 mt-0.5 relative flex items-center justify-end h-4">
            <span
              class="text-[10px] text-muted-foreground/70 group-hover:hidden"
              data-item-time
              :title="absoluteTime(result.created_at)"
            >{{ relativeTime(result.created_at) }}</span>
            <span class="hidden group-hover:flex items-center gap-0.5" data-mark-actions>
              <button
                class="text-muted-foreground/50 hover:text-accent p-0.5"
                title="Mark everything above as read, except this item"
                aria-label="Mark everything above as read, except this item"
                data-mark-above
                @click.stop="markAboveRead(result)"
              >
                <ChevronsUp class="size-3.5" />
              </button>
              <button
                class="text-muted-foreground/50 hover:text-accent p-0.5"
                title="Mark everything below as read, except this item"
                aria-label="Mark everything below as read, except this item"
                data-mark-below
                @click.stop="markBelowRead(result)"
              >
                <ChevronsDown class="size-3.5" />
              </button>
            </span>
          </span>
        </div>
        <div v-if="showingAll && result.config_name" class="flex items-center justify-between mt-2">
          <span class="text-[10px] text-accent/70 truncate">{{ result.config_name }}</span>
        </div>
        </template>
      </div>
    </div>
    <!-- End-of-list mark-all-read button -->
    <div v-if="showMarkAllRead" class="px-4 py-6 flex items-center justify-center">
      <button
        class="inline-flex items-center gap-2 px-5 py-2.5 rounded-lg border border-accent/20 bg-accent/5 text-accent text-sm font-medium hover:bg-accent/10 active:bg-accent/20 transition-colors"
        @click="emit('markAllRead')"
      >
        <CheckCircle2 class="size-4" />
        Mark all as read
      </button>
    </div>
  </div>
</template>
