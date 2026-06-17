<script setup lang="ts">
import { computed } from 'vue'
import { useFeedStore } from '../stores/feed'
import Skeleton from './ui/Skeleton.vue'
import Tooltip from './ui/Tooltip.vue'
import { Filter, ArrowUp, MessageSquare } from 'lucide-vue-next'

const props = defineProps<{
  savedItems: any[]
  showingSaved: boolean
  relativeTime: (val: any) => string
  absoluteTime: (val: any) => string
}>()

const emit = defineEmits<{
  (e: 'selectItem', item: any): void
}>()

const feedStore = useFeedStore()

const displayItems = computed(() => props.showingSaved ? props.savedItems : feedStore.filteredItems)

const scale = computed(() => {
  const items = displayItems.value
  if (items.length === 0) return { points_max: 0, points_avg: 0, comments_max: 0, comments_avg: 0 }
  let pointsMax = 0, commentsMax = 0, pointsSum = 0, commentsSum = 0, pointsCount = 0, commentsCount = 0
  for (const item of items) {
    if (item.points > 0) {
      if (item.points > pointsMax) pointsMax = item.points
      pointsSum += item.points
      pointsCount++
    }
    if (item.comments_count > 0) {
      if (item.comments_count > commentsMax) commentsMax = item.comments_count
      commentsSum += item.comments_count
      commentsCount++
    }
  }
  return {
    points_max: pointsMax,
    points_avg: pointsCount > 0 ? Math.round(pointsSum / pointsCount) : 0,
    comments_max: commentsMax,
    comments_avg: commentsCount > 0 ? Math.round(commentsSum / commentsCount) : 0,
  }
})

function accentOpacity(val: number, max: number): number {
  if (max <= 0) return 0.4
  const r = Math.log(Math.max(val, 1)) / Math.log(Math.max(max, 2))
  if (r > 0.8) return 1
  if (r > 0.5) return 0.65
  if (r > 0.2) return 0.4
  return 0.2
}

function accentStyle(val: number, max: number): string {
  return `color: hsl(var(--accent) / ${accentOpacity(val, max)})`
}

function pointsTitle(val: number): string {
  const avg = scale.value.points_avg
  const diff = val - avg
  const sign = diff >= 0 ? '+' : ''
  return `${val} points (${sign}${diff})`
}

function commentsTitle(val: number): string {
  const avg = scale.value.comments_avg
  const diff = val - avg
  const sign = diff >= 0 ? '+' : ''
  return `${val} comments (${sign}${diff})`
}

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
      <p class="text-xs text-muted-foreground/70 line-clamp-2 mt-0.5 min-w-0 pr-16">{{ stripContent(item.content) }}</p>
      <div class="absolute right-2 bottom-2 flex items-center gap-2">
        <Tooltip v-if="item.points > 0">
          <template #trigger>
            <span class="flex items-center gap-0.5 text-[10px]" :style="accentStyle(item.points, scale.points_max)">
              <ArrowUp class="size-3" /> {{ item.points }}
            </span>
          </template>
          {{ pointsTitle(item.points) }}
        </Tooltip>
        <Tooltip v-if="item.comments_count > 0">
          <template #trigger>
            <span class="flex items-center gap-0.5 text-[10px]" :style="accentStyle(item.comments_count, scale.comments_max)">
              <MessageSquare class="size-3" /> {{ item.comments_count }}
            </span>
          </template>
          {{ commentsTitle(item.comments_count) }}
        </Tooltip>
      </div>
    </button>
  </div>
</template>
