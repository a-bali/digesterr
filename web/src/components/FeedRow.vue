<script setup lang="ts">
import { useFeedStore } from '../stores/feed'
import Tooltip from './ui/Tooltip.vue'
import { Rss, Check, Settings, RefreshCw } from 'lucide-vue-next'
import { feedIcon, absoluteTime } from '../lib/format'
import type { Feed } from '../types/api'

defineProps<{ feed: Feed; refreshing: boolean }>()
const emit = defineEmits<{
  (e: 'select', id: number): void
  (e: 'edit', feed: Feed): void
  (e: 'markRead', id: number): void
  (e: 'refresh', id: number): void
}>()

const feedStore = useFeedStore()
</script>

<template>
  <div
    class="group flex items-center w-full rounded-md px-2 py-1.5 text-sm hover:bg-accent/10 transition-colors"
    :class="{ 'bg-accent/10 font-medium text-accent': feed.id === feedStore.activeFeedId }"
  >
    <button class="flex items-center gap-2 flex-1 min-w-0" @click="emit('select', feed.id)">
      <img v-if="feedIcon(feed)" :src="feedIcon(feed)" class="size-3.5 shrink-0 rounded" @error="($event.target as HTMLImageElement).style.display = 'none'" />
      <Rss v-else class="size-3.5 shrink-0 text-accent" />
      <Tooltip>
        <template #trigger>
          <span class="truncate cursor-help">{{ feed.name }}</span>
        </template>
        <p>Last fetched: {{ absoluteTime(feed.last_fetched_at) || 'never' }}</p>
        <p v-if="feed.status === 'error'" class="text-red-500">Failed</p>
      </Tooltip>
      <span v-if="(feedStore.unreadCounts[feed.id] ?? 0) > 0" class="text-[10px] font-medium px-1.5 py-0.5 rounded-full bg-accent text-accent-foreground ml-1.5">{{ feedStore.unreadCounts[feed.id] }}</span>
    </button>
    <button class="shrink-0 opacity-0 group-hover:opacity-100 transition-opacity p-1 rounded hover:bg-accent/20" title="Settings" @click.stop="emit('edit', feed)">
      <Settings class="size-3" />
    </button>
    <button class="shrink-0 opacity-0 group-hover:opacity-100 transition-opacity p-1 rounded hover:bg-accent/20" title="Mark read" @click.stop="emit('markRead', feed.id)">
      <Check class="size-3" />
    </button>
    <button class="shrink-0 opacity-0 group-hover:opacity-100 transition-opacity p-1 rounded hover:bg-accent/20" title="Refresh" @click.stop="emit('refresh', feed.id)">
      <RefreshCw v-if="refreshing" class="size-3 animate-spin" />
      <RefreshCw v-else class="size-3" />
    </button>
  </div>
</template>
