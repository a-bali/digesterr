<script setup lang="ts">
import { ref, computed } from 'vue'
import { useDigestStore } from '../stores/digest'
import FilterBar from './FilterBar.vue'
import { Circle, CircleDot } from 'lucide-vue-next'
import type { DigestResult } from '../types/api'

const emit = defineEmits<{ (e: 'select', result: DigestResult): void }>()

const digestStore = useDigestStore()
const digestFilter = ref<'all' | 'unread' | 'read'>('all')

const filteredResults = computed<DigestResult[]>(() => {
  const r = digestStore.results
  return digestFilter.value === 'all' ? r : r.filter(res => res.status === digestFilter.value)
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
    <div class="mb-2">
      <FilterBar
        :options="[
          { value: 'all', label: 'All' },
          { value: 'unread', label: 'Unread' },
          { value: 'read', label: 'Read' },
        ]"
        v-model="digestFilter"
      />
    </div>
    <div v-if="digestStore.loading" class="text-sm text-muted-foreground p-2">Loading...</div>
    <div v-else-if="filteredResults.length === 0" class="text-xs text-muted-foreground p-2">No results.</div>
    <div v-else class="space-y-1">
      <button v-for="result in filteredResults" :key="result.id"
        class="w-full text-left h-20 p-2 border-b hover:bg-accent/10 transition-colors relative overflow-hidden"
        :class="{ 'bg-accent/10 font-medium text-accent': isSelected(result), 'text-foreground': result.status === 'unread', 'text-muted-foreground': result.status === 'read' }"
        @click="onSelect(result)"
      >
        <div class="flex items-start gap-1 min-w-0">
          <CircleDot v-if="result.status === 'unread'" class="size-3 shrink-0 mt-0.5 text-accent" />
          <Circle v-else class="size-3 shrink-0 mt-0.5 text-muted-foreground/40" />
          <p class="text-sm font-semibold truncate flex-1 min-w-0">{{ new Date(result.created_at).toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit' }) }}</p>
        </div>
        <p class="text-xs text-muted-foreground/70 mt-0.5 min-w-0">{{ new Date(result.created_at).toLocaleDateString(undefined, { month: 'short', day: 'numeric', year: 'numeric' }) }}</p>
        <span class="absolute right-2 bottom-2 text-[10px] text-accent font-medium">{{ itemCount(result) }} items</span>
      </button>
    </div>
  </div>
</template>
