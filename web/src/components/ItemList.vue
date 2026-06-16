<script setup lang="ts">
import { useFeedStore } from '../stores/feed'
import Skeleton from './ui/Skeleton.vue'
import { Filter } from 'lucide-vue-next'

defineProps<{
  savedItems: any[]
  showingSaved: boolean
  relativeTime: (val: any) => string
  absoluteTime: (val: any) => string
}>()

const emit = defineEmits<{
  (e: 'selectItem', item: any): void
}>()

const feedStore = useFeedStore()

function stripContent(content: string): string {
  if (!content) return ''
  return content
    .replace(/<[^>]*>/g, '')
    .replace(/&amp;/g, '&')
    .replace(/&lt;/g, '<')
    .replace(/&gt;/g, '>')
    .replace(/&nbsp;/g, ' ')
    .replace(/&quot;/g, '"')
    .replace(/\s+/g, ' ')
    .trim()
}
</script>

<template>
  <div class="h-full border-r overflow-y-auto">
    <div class="flex items-center gap-1 p-2 border-b">
      <Filter class="size-4 text-muted-foreground shrink-0" />
      <select v-model="feedStore.filter" class="text-xs bg-transparent border-0 outline-none w-full">
        <option value="all">All</option>
        <option value="unread">Unread</option>
        <option value="read">Read</option>
        <option value="digested">Digested</option>
        <option value="saved">Saved</option>
      </select>
    </div>
    <Skeleton v-if="feedStore.loading" class="m-3 h-12" />
    <div v-else-if="!feedStore.activeFeed && !showingSaved" class="p-6 text-center text-sm text-muted-foreground">Select a feed</div>
    <div v-else-if="showingSaved && savedItems.length === 0" class="p-6 text-center text-sm text-muted-foreground">No saved items</div>
    <button v-for="item in showingSaved ? savedItems : feedStore.filteredItems" :key="item.id"
      class="w-full text-left h-20 p-2 border-b hover:bg-accent/10 transition-colors relative overflow-hidden"
      :class="{ 'bg-accent/10 font-medium text-accent': item.id === feedStore.selectedItem?.id, 'text-foreground': item.status === 'unread', 'text-muted-foreground': item.status === 'digested' || item.status === 'read', 'text-accent': item.status === 'saved' }"
      @click="emit('selectItem', item)">
      <div class="flex items-start gap-1 min-w-0">
        <p class="text-sm font-semibold truncate flex-1 min-w-0">{{ item.title }}</p>
        <span class="text-[10px] text-muted-foreground/70 shrink-0 mt-0.5" :title="absoluteTime(item.published_at)">{{ relativeTime(item.published_at) }}</span>
      </div>
      <p class="text-xs text-muted-foreground/70 line-clamp-2 mt-0.5 min-w-0">{{ stripContent(item.content) }}</p>
      <span v-if="item.points > 0" class="text-[10px] text-muted-foreground/70 absolute right-2 bottom-2">{{ item.points }} pts</span>
    </button>
  </div>
</template>
