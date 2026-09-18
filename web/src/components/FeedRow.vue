<script setup lang="ts">
import { computed } from 'vue'
import { useFeedStore } from '../stores/feed'
import Tooltip from './ui/Tooltip.vue'
import { Rss, Check, Settings, RefreshCw, Mail } from 'lucide-vue-next'
import { feedIcon, absoluteTime } from '../lib/format'
import type { Feed } from '../types/api'

const props = defineProps<{ feed: Feed; refreshing: boolean }>()
const emit = defineEmits<{
  (e: 'select', id: number): void
  (e: 'edit', feed: Feed): void
  (e: 'markRead', id: number): void
  (e: 'refresh', id: number): void
}>()

const feedStore = useFeedStore()

// Unread counts come from a separate request than the feed list, so a missing
// key means "not loaded yet", not zero. Only an explicit 0 dims: a falsy check
// would flash every row on first paint and would render a failed
// /unread-counts call as "everything is read".
const noUnread = computed(() => feedStore.unreadCounts[props.feed.id] === 0)
</script>

<template>
  <div
    class="group flex items-center w-full rounded-md px-2 py-1.5 text-sm hover:bg-accent/10 transition-colors"
    :class="{ 'bg-accent/10 font-medium text-accent': feed.id === feedStore.activeFeedId }"
  >
    <button class="flex items-center gap-2 flex-1 min-w-0" @click="emit('select', feed.id)">
      <img v-if="feedIcon(feed)" :src="feedIcon(feed)" class="size-3.5 shrink-0 rounded" :class="{ 'opacity-60': noUnread }" @error="($event.target as HTMLImageElement).style.display = 'none'" />
      <!-- Email feeds have an address rather than a URL, so feedIcon() finds no
           favicon for them; an envelope is what identifies the type. -->
      <Mail v-else-if="feed.type === 'email'" class="size-3.5 shrink-0 text-accent" :class="{ 'opacity-60': noUnread }" />
      <Rss v-else class="size-3.5 shrink-0 text-accent" :class="{ 'opacity-60': noUnread }" />
      <Tooltip>
        <template #trigger>
          <span class="truncate" :class="{ 'opacity-60': noUnread }">{{ feed.name }}</span>
        </template>
        <p>Last fetched: {{ absoluteTime(feed.last_fetched_at) || 'never' }}</p>
        <p v-if="feed.status === 'error'" class="text-destructive">Failed</p>
      </Tooltip>
      <Tooltip v-if="feed.last_error">
        <template #trigger>
          <svg v-if="!feed.last_error_severity || feed.last_error_severity === 'error'" viewBox="0 0 24 24" class="size-4 text-red-500 shrink-0" fill="none"><rect x="2" y="2" width="20" height="20" rx="4" fill="currentColor"/><path d="M12 7v6" stroke="white" stroke-width="2.5" stroke-linecap="round"/><circle cx="12" cy="16" r="1.3" fill="white"/></svg>
          <svg v-else-if="feed.last_error_severity === 'warning'" viewBox="0 0 24 24" class="size-4 text-yellow-500 shrink-0" fill="none"><rect x="2" y="2" width="20" height="20" rx="4" fill="currentColor"/><path d="M12 7v6" stroke="white" stroke-width="2.5" stroke-linecap="round"/><circle cx="12" cy="16" r="1.3" fill="white"/></svg>
        </template>
        <p>{{ feed.last_error }}</p>
        <p v-if="feed.last_error_at">at {{ absoluteTime(feed.last_error_at) }}</p>
      </Tooltip>
      <span v-if="(feedStore.unreadCounts[feed.id] ?? 0) > 0" class="text-[10px] font-medium px-1.5 py-0.5 rounded-full bg-accent text-accent-foreground ml-1.5">{{ feedStore.unreadCounts[feed.id] }}</span>
    </button>
    <button class="shrink-0 opacity-0 group-hover:opacity-100 group-focus-within:opacity-100 pointer-coarse:opacity-100 transition-opacity p-1 rounded hover:bg-accent/20" title="Settings" @click.stop="emit('edit', feed)">
      <Settings class="size-3" />
    </button>
    <button class="shrink-0 opacity-0 group-hover:opacity-100 group-focus-within:opacity-100 pointer-coarse:opacity-100 transition-opacity p-1 rounded hover:bg-accent/20" title="Mark read" @click.stop="emit('markRead', feed.id)">
      <Check class="size-3" />
    </button>
    <button class="shrink-0 opacity-0 group-hover:opacity-100 group-focus-within:opacity-100 pointer-coarse:opacity-100 transition-opacity p-1 rounded hover:bg-accent/20" title="Refresh" @click.stop="emit('refresh', feed.id)">
      <RefreshCw v-if="refreshing" class="size-3 animate-spin" />
      <RefreshCw v-else class="size-3" />
    </button>
  </div>
</template>
