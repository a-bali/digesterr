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
      class="w-full text-left p-3 border-b hover:bg-accent/10 transition-colors"
      :class="{ 'bg-accent/10 font-medium text-accent': item.id === feedStore.selectedItem?.id, 'font-bold text-foreground': item.status === 'unread', 'font-bold text-muted-foreground': item.status === 'digested', 'font-normal text-muted-foreground': item.status === 'read', 'font-normal text-accent': item.status === 'saved' }"
      @click="emit('selectItem', item)">
      <p class="text-sm line-clamp-2">{{ item.title }}</p>
      <div class="flex items-center gap-2 mt-1">
        <span v-if="item.points > 0" class="text-xs text-muted-foreground">{{ item.points }} pts</span>
        <span v-if="item.comments_count > 0" class="text-xs text-muted-foreground">{{ item.comments_count }} comments</span>
        <span class="text-xs text-muted-foreground ml-auto" :title="absoluteTime(item.published_at)">{{ relativeTime(item.published_at) }}</span>
      </div>
    </button>
  </div>
</template>
