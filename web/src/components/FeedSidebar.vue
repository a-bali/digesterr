<script setup lang="ts">
import { ref } from 'vue'
import { toast } from 'vue-sonner'
import { useCategoryStore } from '../stores/category'
import { useFeedStore } from '../stores/feed'
import { feedApi } from '../services/api'
import type { Feed } from '../types/api'
import { BookmarkCheck, Rss, RefreshCw, Check } from 'lucide-vue-next'
import Tooltip from './ui/Tooltip.vue'

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

function feedIcon(feed: Feed): string {
  if (feed.type === 'hn') return 'https://news.ycombinator.com/favicon.ico'
  if (feed.type === 'lobsters') return 'https://lobste.rs/favicon.ico'
  try {
    const domain = new URL(feed.url).hostname
    return `https://icons.duckduckgo.com/ip3/${domain}.ico`
  } catch {
    return ''
  }
}

async function refreshFeed(id: number) {
  refreshingFeeds.value = new Set(refreshingFeeds.value).add(id)
  try {
    await feedApi.fetch(id)
    await feedStore.fetchItems()
    await feedStore.fetchFeeds()
    toast.success('Feed refreshed')
  } catch (e: any) {
    toast.error('Failed to refresh feed', { description: e.message })
  } finally {
    const next = new Set(refreshingFeeds.value)
    next.delete(id)
    refreshingFeeds.value = next
  }
}

async function markFeedRead(id: number) {
  try {
    await feedApi.markRead(id)
    await feedStore.fetchFeeds()
    if (feedStore.activeFeedId === id) {
      await feedStore.fetchItems()
    }
    toast.success('Feed marked read')
  } catch (e: any) {
    toast.error('Failed to mark feed read', { description: e.message })
  }
}

function formatDate(val: any): string {
  if (!val) return ''
  if (typeof val === 'object') {
    if (!val.Valid) return ''
    val = val.Time
  }
  if (typeof val !== 'string') return ''
  const d = new Date(val)
  return isNaN(d.getTime()) ? '' : d.toLocaleString(undefined, {
    year: 'numeric', month: 'short', day: 'numeric',
    hour: '2-digit', minute: '2-digit',
  })
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
          <img v-if="feedIcon(feed)" :src="feedIcon(feed)" class="size-3.5 shrink-0 rounded" @error="($event.target as HTMLImageElement).style.display = 'none'" />
          <Rss v-else class="size-3.5 shrink-0 text-accent" />
          <Tooltip>
            <template #trigger>
              <span class="truncate cursor-help">{{ feed.name }}</span>
            </template>
            <p>Last fetched: {{ formatDate(feed.last_fetched_at) || 'never' }}</p>
            <p v-if="feed.status === 'error'" class="text-red-500">Failed</p>
          </Tooltip>
          <span v-if="(feedStore.unreadCounts[feed.id] ?? 0) > 0" class="text-[10px] font-medium px-1.5 py-0.5 rounded-full bg-accent text-accent-foreground ml-1.5">{{ feedStore.unreadCounts[feed.id] }}</span>
        </button>
        <button @click.stop="markFeedRead(feed.id)" class="shrink-0 opacity-0 group-hover:opacity-100 transition-opacity p-1 rounded hover:bg-accent/20">
          <Check class="size-3" />
        </button>
        <button @click.stop="refreshFeed(feed.id)" class="shrink-0 opacity-0 group-hover:opacity-100 transition-opacity p-1 rounded hover:bg-accent/20">
          <RefreshCw v-if="refreshingFeeds.has(feed.id)" class="size-3 animate-spin" />
          <RefreshCw v-else class="size-3" />
        </button>
      </div>
    </div>
  </div>
</template>
