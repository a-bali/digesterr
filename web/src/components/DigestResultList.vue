<script setup lang="ts">
import { ref, computed } from 'vue'
import { useDigestStore } from '../stores/digest'
import FilterBar, { type FilterStatus, type FilterSort } from './FilterBar.vue'
import Skeleton from './ui/Skeleton.vue'
import { Circle, CircleDot } from 'lucide-vue-next'
import { useInfiniteScroll } from '../composables/useInfiniteScroll'
import { absoluteTime } from '../lib/format'
import type { DigestResult } from '../types/api'

const emit = defineEmits<{ (e: 'select', result: DigestResult): void }>()

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
  const filtered = digestFilter.value === 'all'
    ? digestStore.results
    : digestStore.results.filter(res => res.status === digestFilter.value)
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

function isSelected(result: DigestResult): boolean {
  return result.id === digestStore.selectedResult?.id
}

function onSelect(result: DigestResult) {
  if (result.status !== 'read') digestStore.updateResultStatus(result.id, 'read')
  digestStore.selectResult(result)
  emit('select', result)
}

</script>

<template>
  <div class="h-full overflow-y-auto" @scroll="onScroll">
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
    <!-- Results load async; skeleton rows matching the result-row height. -->
    <div v-if="digestStore.loading" class="space-y-1 p-3" data-loading-skeleton>
      <Skeleton v-for="i in 5" :key="i" class="h-20 w-full" />
    </div>
    <div v-else class="space-y-1 p-3">
      <button v-for="result in visibleResults" :key="result.id"
        class="w-full text-left h-20 p-2 border-b hover:bg-accent/10 transition-colors relative overflow-hidden"
        :class="{ 'bg-accent/10 font-medium text-accent': isSelected(result), 'text-foreground': result.status === 'unread', 'text-muted-foreground': result.status === 'read' }"
        @click="onSelect(result)"
      >
        <div class="flex items-start gap-1 min-w-0">
          <CircleDot v-if="result.status === 'unread'" class="size-3 shrink-0 mt-0.5 text-accent" />
          <Circle v-else class="size-3 shrink-0 mt-0.5 text-muted-foreground/40" />
          <p class="text-sm font-semibold truncate flex-1 min-w-0">{{ absoluteTime(result.created_at) }}</p>
        </div>
        <span class="absolute right-2 bottom-2 text-[10px] text-accent font-medium">{{ itemCount(result) }} items</span>
      </button>
    </div>
  </div>
</template>
