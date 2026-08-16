<script setup lang="ts">
import { ref, computed, type Component } from 'vue'
import { useDigestStore } from '../stores/digest'
import DropdownMenu from './ui/DropdownMenu.vue'
import Button from './ui/Button.vue'
import { Circle, CircleDot, Check, ChevronDown } from 'lucide-vue-next'
import { absoluteTime } from '../lib/format'
import type { DigestResult } from '../types/api'

const emit = defineEmits<{ (e: 'select', result: DigestResult): void }>()

const digestStore = useDigestStore()

type DigestStatusFilter = 'all' | 'unread' | 'read'
type DigestSort = 'newest' | 'oldest'

// Digest view is limited to status filtering + date sorting (approved UX plan,
// variant 1 — state-label buttons). No score section at all.
const digestFilter = ref<DigestStatusFilter>('all')
const sortOrder = ref<DigestSort>('newest')

const STATUS_OPTIONS: { value: DigestStatusFilter; label: string }[] = [
  { value: 'all', label: 'All' },
  { value: 'unread', label: 'Unread' },
  { value: 'read', label: 'Read' },
]

const filterLabel = computed(() => STATUS_OPTIONS.find(o => o.value === digestFilter.value)?.label ?? 'All')
const sortLabel = computed(() => (sortOrder.value === 'newest' ? 'Newest first' : 'Oldest first'))

type MenuItem = { label: string; icon?: Component; onClick?: () => void }

const filterItems = computed<MenuItem[]>(() =>
  STATUS_OPTIONS.map(opt => ({
    label: opt.label,
    icon: digestFilter.value === opt.value ? Check : undefined,
    onClick: () => { digestFilter.value = opt.value },
  })),
)

const sortItems = computed<MenuItem[]>(() => [
  {
    label: 'Newest first',
    icon: sortOrder.value === 'newest' ? Check : undefined,
    onClick: () => { sortOrder.value = 'newest' },
  },
  {
    label: 'Oldest first',
    icon: sortOrder.value === 'oldest' ? Check : undefined,
    onClick: () => { sortOrder.value = 'oldest' },
  },
])

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

function onScroll(e: Event) {
  const el = e.target as HTMLElement
  if (!el || !digestStore.hasMore || digestStore.loadingMore || digestStore.loading) return
  if (el.scrollTop + el.clientHeight >= el.scrollHeight - 200) {
    digestStore.fetchMoreResults()
  }
}
</script>

<template>
  <div class="h-full overflow-y-auto p-3" @scroll="onScroll">
    <div class="mb-2 flex items-center gap-1">
      <DropdownMenu :items="filterItems">
        <template #trigger>
          <Button variant="ghost" size="sm" aria-label="Filter digests by status">
            <span>Filter: {{ filterLabel }}</span>
            <ChevronDown class="size-4" />
          </Button>
        </template>
      </DropdownMenu>
      <DropdownMenu :items="sortItems">
        <template #trigger>
          <Button variant="ghost" size="sm" aria-label="Sort digests by date">
            <span>Sort: {{ sortLabel }}</span>
            <ChevronDown class="size-4" />
          </Button>
        </template>
      </DropdownMenu>
    </div>
    <div v-if="digestStore.loading" class="text-sm text-muted-foreground p-2">Loading...</div>
    <div v-else class="space-y-1">
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
