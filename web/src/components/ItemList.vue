<script setup lang="ts">
import { ref, computed, onMounted, onUnmounted } from 'vue'
import { useFeedStore } from '../stores/feed'
import Skeleton from './ui/Skeleton.vue'
import Tooltip from './ui/Tooltip.vue'
import FilterBar from './FilterBar.vue'
import { Circle, CircleDot, CircleCheck, Check, ArrowUp, MessageSquare } from 'lucide-vue-next'
import { stripContent } from '../lib/utils'

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

const scrollEl = ref<HTMLElement | null>(null)

const displayItems = computed(() => props.showingSaved ? props.savedItems : feedStore.items)

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


function onScroll() {
  const el = scrollEl.value
  if (!el || !feedStore.hasMore || feedStore.loadingMore || feedStore.loading) return
  if (el.scrollTop + el.clientHeight >= el.scrollHeight - 100) {
    feedStore.fetchMoreItems()
  }
}

function onFilterChange(val: string) {
  if (val !== feedStore.filter) {
    feedStore.changeFilter(val as any)
  }
}

// Reset scroll position when items are replaced (filter change, feed switch)
const prevItemsLen = ref(0)
onMounted(() => {})
onUnmounted(() => {})
</script>

<template>
  <div class="h-full border-r overflow-y-auto" ref="scrollEl" @scroll="onScroll">
    <div class="p-2 border-b">
      <FilterBar
        :options="[
          { value: 'all', label: 'All' },
          { value: 'unread', label: 'Unread' },
          { value: 'read', label: 'Read' },
          { value: 'digested', label: 'Digested' },
          { value: 'saved', label: 'Saved' },
        ]"
        :model-value="feedStore.filter"
        @update:model-value="onFilterChange"
      />
    </div>
    <Skeleton v-if="feedStore.loading" class="m-3 h-12" />
    <div v-else-if="!feedStore.activeFeedId && !showingSaved" class="p-6 text-center text-sm text-muted-foreground">Select a feed</div>
    <div v-else-if="showingSaved && savedItems.length === 0" class="p-6 text-center text-sm text-muted-foreground">No saved items</div>
    <template v-else>
      <div v-for="item in displayItems" :key="item.id"
      role="button" tabindex="0"
      class="w-full text-left h-20 p-2 border-b hover:bg-accent/10 transition-colors relative overflow-hidden cursor-pointer"
      :class="{ 'bg-accent/10 font-medium text-accent': item.id === feedStore.selectedItem?.id, 'text-foreground': item.status === 'unread', 'text-foreground/70': item.status === 'digested', 'text-muted-foreground': item.status === 'read', 'text-accent': item.status === 'saved' }"
      @click="emit('selectItem', item)"
      @keydown.enter.prevent="emit('selectItem', item)"
      @keydown.space.prevent="emit('selectItem', item)">
      <div class="flex items-start gap-1 min-w-0">
        <CircleDot v-if="item.status === 'unread'" class="size-3 shrink-0 mt-0.5 text-accent" />
        <CircleCheck v-else-if="item.status === 'saved'" class="size-3 shrink-0 mt-0.5 text-accent" />
        <span v-else-if="item.status === 'digested'" class="size-3 shrink-0 mt-0.5 inline-flex items-center justify-center text-[10px] leading-none text-muted-foreground/60" title="digested">◐</span>
        <Circle v-else class="size-3 shrink-0 mt-0.5 text-muted-foreground/40" />
        <span v-if="item.status === 'saved'" class="text-accent shrink-0 mt-0.5"><Check class="size-3" /></span>
        <p class="text-sm font-semibold truncate flex-1 min-w-0">{{ item.title }}</p>
        <span class="text-[10px] text-muted-foreground/70 shrink-0 mt-0.5" :title="absoluteTime(item.published_at)">{{ relativeTime(item.published_at) }}</span>
      </div>
      <p class="text-xs text-muted-foreground/70 line-clamp-2 mt-0.5 min-w-0 pr-16">{{ stripContent(item.extract_status === 'done' ? item.extract_content : item.content) }}</p>
      <div class="absolute right-2 bottom-2 flex items-center gap-2">
        <Tooltip v-if="item.points > 0">
          <template #trigger>
            <span class="flex items-center gap-0.5 text-[10px]" :style="accentStyle(item.points, scale.points_max)">
              <ArrowUp class="size-3" /> {{ item.points }}
            </span>
          </template>
          {{ pointsTitle(item.points) }}
        </Tooltip>
        <Tooltip v-if="item.comments_count > 0 || item.comment_url">
          <template #trigger>
            <span class="flex items-center gap-0.5 text-[10px]" :style="item.comments_count > 0 ? accentStyle(item.comments_count, scale.comments_max) : 'color: hsl(var(--accent) / 0.3)'">
              <MessageSquare class="size-3" /> <template v-if="item.comments_count > 0">{{ item.comments_count }}</template>
            </span>
          </template>
          <template v-if="item.comments_count > 0">{{ commentsTitle(item.comments_count) }}</template>
        </Tooltip>
      </div>
    </div>
    <div v-if="!props.showingSaved && feedStore.loadingMore" class="p-3">
      <Skeleton class="h-12" />
    </div>
    </template>
  </div>
</template>
