<script setup lang="ts">
import { reactive, computed, watch } from 'vue'
import { useFeedStore } from '../stores/feed'
import Dialog from './ui/Dialog.vue'
import Input from './ui/Input.vue'
import Select from './ui/Select.vue'
import Button from './ui/Button.vue'
import type { Feed } from '../types/api'

const props = defineProps<{ feed: Feed | null }>()
const emit = defineEmits<{
  (e: 'update:open', open: boolean): void
  (e: 'save', data: { type: string; name: string; url: string; category: string; extract_content: number; min_score: number }): void
  (e: 'delete', id: number): void
}>()

const feedStore = useFeedStore()

// Feeds whose items carry a score (points) that can be filtered on.
const SCORE_FEED_TYPES = ['hn', 'lobsters', 'reddit']
const isScoreType = computed(() => SCORE_FEED_TYPES.includes(form.type))

// Category suggestions come from the categories currently in use.
const categoryNames = computed(() => {
  const names = new Set<string>()
  for (const f of feedStore.feeds) {
    if (f.category) names.add(f.category)
  }
  return [...names].sort((a, b) => a.localeCompare(b))
})

const form = reactive({ type: 'rss', name: '', url: '', category: '', extract_content: 0, min_score: 0 })
const typeLabel = (t: string) => ({ rss: 'RSS / Atom', hn: 'Hacker News', lobsters: 'Lobste.rs', reddit: 'Reddit' }[t] || t)

function onTypeChange(type: string) {
  form.type = type
  form.url = ''
  // The minimum-score field is disabled for non-score feed types, so the
  // value is reset when switching away from a score-capable type.
  if (!SCORE_FEED_TYPES.includes(type)) form.min_score = 0
  if (type === 'hn') form.url = 'https://news.ycombinator.com/rss'
  else if (type === 'lobsters') form.url = 'https://lobste.rs/rss'
}

watch(
  () => props.feed,
  (feed) => {
    if (feed) {
      form.type = feed.type
      form.name = feed.name
      form.url = feed.type === 'reddit' ? feed.url.replace('https://www.reddit.com/r/', '').replace('.rss', '') : feed.url
      form.category = feed.category || ''
      form.extract_content = feed.extract_content
      form.min_score = feed.min_score
    } else {
      form.type = 'rss'
      form.name = ''
      form.url = ''
      form.category = ''
      form.extract_content = 0
      form.min_score = 0
    }
  },
  { immediate: true },
)

function submit() {
  emit('save', { ...form })
}

const dialogId = Math.random().toString(36).slice(2)
</script>

<template>
  <Dialog :open="true" :title="feed ? 'Edit Feed' : 'Add Feed'" @update:open="emit('update:open', $event)">
    <form @submit.prevent="submit" class="space-y-4">
      <div class="space-y-1">
        <label class="text-sm font-medium">Category</label>
        <Input
          v-model="form.category"
          :list="`${dialogId}-cats`"
          placeholder="Select existing or type new name…"
        />
        <datalist :id="`${dialogId}-cats`">
          <option v-for="name in categoryNames" :key="name" :value="name" />
        </datalist>
      </div>
      <div class="space-y-1">
        <label class="text-sm font-medium">Type</label>
        <Select v-model="form.type" @change="onTypeChange(($event.target as HTMLSelectElement).value)">
          <option v-for="t in ['rss','hn','lobsters','reddit']" :key="t" :value="t">{{ typeLabel(t) }}</option>
        </Select>
      </div>
      <div class="space-y-1">
        <label class="text-sm font-medium">Name</label>
        <Input v-model="form.name" placeholder="Feed name" required />
      </div>
      <div v-if="form.type === 'hn' || form.type === 'lobsters'" class="space-y-1">
        <label class="text-sm font-medium">URL</label>
        <p class="text-sm text-muted-foreground bg-muted rounded px-2 py-1.5">{{ form.url }}</p>
      </div>
      <div v-else-if="form.type === 'reddit'" class="space-y-1">
        <label class="text-sm font-medium">URL</label>
        <div class="flex items-center gap-0">
          <span class="text-xs text-muted-foreground bg-muted rounded-l-md px-2 py-1.5 border border-border border-r-0 whitespace-nowrap">https://www.reddit.com/r/</span>
          <Input v-model="form.url" placeholder="subreddit" required class="rounded-none" />
          <span class="text-xs text-muted-foreground bg-muted rounded-r-md px-2 py-1.5 border border-border border-l-0 whitespace-nowrap">.rss</span>
        </div>
      </div>
      <div v-else class="space-y-1">
        <label class="text-sm font-medium">URL</label>
        <Input v-model="form.url" placeholder="Feed URL" required />
      </div>
      <label class="flex items-center gap-2 text-sm cursor-pointer">
        <input type="checkbox" v-model.number="form.extract_content" :true-value="1" :false-value="0" class="toggle" />
        Auto-extract full article content
      </label>
      <div class="space-y-1">
        <label class="text-sm font-medium" :class="{ 'text-muted-foreground': !isScoreType }">Minimum score</label>
        <Input
          v-model.number="form.min_score"
          type="number"
          min="0"
          :disabled="!isScoreType"
          :class="{ 'opacity-50': !isScoreType }"
        />
        <p v-if="isScoreType" class="text-xs text-muted-foreground">
          Only items with at least this many points are kept. Set 0 to keep all items.
        </p>
        <p v-else class="text-xs text-muted-foreground">
          Scores are only available for Hacker News, Lobste.rs and Reddit feeds.
        </p>
      </div>
      <div class="flex gap-2">
        <Button type="submit">{{ feed ? 'Save' : 'Add' }}</Button>
        <Button v-if="feed" variant="outline" class="text-destructive" @click="emit('delete', feed.id)">Delete</Button>
      </div>
    </form>
  </Dialog>
</template>
