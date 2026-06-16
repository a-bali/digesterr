<script setup lang="ts">
import { ref } from 'vue'
import { toast } from 'vue-sonner'
import { useCategoryStore } from '../stores/category'
import { useFeedStore } from '../stores/feed'
import { feedApi } from '../services/api'
import { BookmarkCheck, Rss, RefreshCw } from 'lucide-vue-next'

const props = withDefaults(defineProps<{
  showingSaved?: boolean
}>(), { showingSaved: false })

const emit = defineEmits<{
  (e: 'selectFeed', id: number): void
  (e: 'showSaved'): void
}>()

const feedStore = useFeedStore()
const categoryStore = useCategoryStore()
const refreshingFeeds = ref(new Set<number>())

async function refreshFeed(id: number) {
  refreshingFeeds.value = new Set(refreshingFeeds.value).add(id)
  try {
    await feedApi.fetch(id)
    await feedStore.fetchItems()
    toast.success('Feed refreshed')
  } catch (e: any) {
    toast.error('Failed to refresh feed', { description: e.message })
  } finally {
    const next = new Set(refreshingFeeds.value)
    next.delete(id)
    refreshingFeeds.value = next
  }
}
</script>

<template>
  <div>
    <button class="flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-sm hover:bg-accent/10 transition-colors mb-3" :class="{ 'bg-accent/10 font-medium text-accent': props.showingSaved }" @click="emit('showSaved')">
      <BookmarkCheck class="size-3.5 shrink-0 text-accent" /> <span class="truncate">Saved Items</span>
    </button>
    <div v-for="cat in categoryStore.categories" :key="cat.id" class="mb-3">
      <p class="text-xs font-semibold text-muted-foreground uppercase mb-1 px-2">{{ cat.name }}</p>
      <div v-for="feed in feedStore.feeds.filter(f => f.category_id === cat.id)" :key="feed.id"
        class="group flex items-center w-full rounded-md px-2 py-1.5 text-sm hover:bg-accent/10 transition-colors"
        :class="{ 'bg-accent/10 font-medium text-accent': feed.id === feedStore.activeFeedId }">
        <button class="flex items-center gap-2 flex-1 min-w-0" @click="emit('selectFeed', feed.id)">
          <Rss class="size-3.5 shrink-0 text-accent" /> <span class="truncate">{{ feed.name }}</span>
        </button>
        <button @click.stop="refreshFeed(feed.id)" class="shrink-0 opacity-0 group-hover:opacity-100 transition-opacity p-1 rounded hover:bg-accent/20">
          <RefreshCw v-if="refreshingFeeds.has(feed.id)" class="size-3 animate-spin" />
          <RefreshCw v-else class="size-3" />
        </button>
      </div>
    </div>
  </div>
</template>
