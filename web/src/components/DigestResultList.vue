<script setup lang="ts">
import { ref, computed } from 'vue'
import { useDigestStore } from '../stores/digest'
import FilterBar, { type FilterStatus, type FilterSort } from './FilterBar.vue'
import Skeleton from './ui/Skeleton.vue'
import { Circle, CircleDot, LoaderCircle, CheckCircle2 } from 'lucide-vue-next'
import { useInfiniteScroll } from '../composables/useInfiniteScroll'
import { absoluteTime } from '../lib/format'
import type { DigestResult } from '../types/api'

const props = defineProps<{
  showingAll?: boolean
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

const digestFilter = ref<FilterStatus>('all')
const sortOrder = ref<FilterSort>('newest')

const STATUS_OPTIONS: { value: FilterStatus; label: string }[] = [
  { value: 'all', label: 'All' },
  { value: 'unread', label: 'Unread' },
  { value: 'read', label: 'Read' },
]

const visibleResults = computed<DigestResult[]>(() => {
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
  try {
    const c = JSON.parse(result.content)
    return (c?.highlights?.length || 0) + (c?.items?.length || 0)
  } catch {
    return 0
  }
}

// Generated digest title (LLM-produced, stored in the content JSON).
// Falls back to the generation timestamp when absent.
function resultTitle(result: DigestResult): string {
  try {
    const t = JSON.parse(result.content)?.title
    if (typeof t === 'string' && t.trim()) return t.trim()
  } catch {
    // fall through to timestamp
  }
  return absoluteTime(result.created_at)
}

function isSelected(result: DigestResult): boolean {
  return result.id === digestStore.selectedResult?.id
}

const showMarkAllRead = computed(() =>
  visibleResults.value.some(r => r.status !== 'read')
)

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
      :item-count="visibleResults.length"
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
        class="w-full text-left h-20 p-2 border-b hover:bg-accent/10 transition-colors relative overflow-hidden cursor-pointer"
        :class="{ 'bg-accent/10': isSelected(result), 'text-foreground': result.status === 'unread', 'text-muted-foreground': result.status === 'read', 'opacity-50': digestStore.loadingMore }"
        @click="onSelect($event, result)"
        @keydown.enter.prevent="onSelect($event, result)"
        @keydown.space.prevent="onSelect($event, result)"
      >
        <div class="flex items-start gap-1 min-w-0">
          <CircleDot v-if="result.status === 'unread'" class="size-3 shrink-0 mt-0.5 text-accent" />
          <Circle v-else class="size-3 shrink-0 mt-0.5 text-muted-foreground/40" />
          <div class="flex-1 min-w-0">
            <p class="text-sm font-semibold truncate">{{ resultTitle(result) }}</p>
            <p class="text-xs text-muted-foreground truncate">{{ absoluteTime(result.created_at) }} · {{ itemCount(result) }} items<span v-if="showingAll && (result as any).config_name"> · {{ (result as any).config_name }}</span></p>
          </div>
        </div>
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
