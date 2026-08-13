<script setup lang="ts">
import { reactive, watch } from 'vue'
import { useCategoryStore } from '../stores/category'
import Dialog from './ui/Dialog.vue'
import Input from './ui/Input.vue'
import Button from './ui/Button.vue'
import type { Feed } from '../types/api'

const props = defineProps<{ feed: Feed | null }>()
const emit = defineEmits<{
  (e: 'update:open', open: boolean): void
  (e: 'save', data: { type: string; name: string; url: string; category: string; extract_content: number }): void
  (e: 'delete', id: number): void
}>()

const categoryStore = useCategoryStore()

const form = reactive({ type: 'rss', name: '', url: '', category: '', extract_content: 0 })
const typeLabel = (t: string) => ({ rss: 'RSS / Atom', hn: 'Hacker News', lobsters: 'Lobste.rs', reddit: 'Reddit' }[t] || t)

function onTypeChange(type: string) {
  form.type = type
  form.url = ''
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
      form.category = feed.category_id ? (categoryStore.categories.find(c => c.id === feed.category_id)?.name || '') : ''
      form.extract_content = feed.extract_content
    } else {
      form.type = 'rss'
      form.name = ''
      form.url = ''
      form.category = ''
      form.extract_content = 0
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
        <label class="text-xs font-medium">Category</label>
        <input
          v-model="form.category"
          :list="`${dialogId}-cats`"
          placeholder="Select existing or type new name…"
          class="flex h-9 w-full rounded-md border border-border bg-background px-3 text-sm"
        />
        <datalist :id="`${dialogId}-cats`">
          <option v-for="cat in categoryStore.categories" :key="cat.id" :value="cat.name" />
        </datalist>
      </div>
      <div class="space-y-1">
        <label class="text-xs font-medium">Type</label>
        <select v-model="form.type" @change="onTypeChange(($event.target as HTMLSelectElement).value)" class="flex h-9 w-full rounded-md border border-border bg-background px-3 text-sm">
          <option v-for="t in ['rss','hn','lobsters','reddit']" :key="t" :value="t">{{ typeLabel(t) }}</option>
        </select>
      </div>
      <div class="space-y-1">
        <label class="text-xs font-medium">Name</label>
        <Input v-model="form.name" placeholder="Feed name" required />
      </div>
      <div v-if="form.type === 'hn' || form.type === 'lobsters'" class="space-y-1">
        <label class="text-xs font-medium">URL</label>
        <p class="text-sm text-muted-foreground bg-muted rounded px-2 py-1.5">{{ form.url }}</p>
      </div>
      <div v-else-if="form.type === 'reddit'" class="space-y-1">
        <label class="text-xs font-medium">URL</label>
        <div class="flex items-center gap-0">
          <span class="text-xs text-muted-foreground bg-muted rounded-l-md px-2 py-1.5 border border-border border-r-0 whitespace-nowrap">https://www.reddit.com/r/</span>
          <Input v-model="form.url" placeholder="subreddit" required class="rounded-none" />
          <span class="text-xs text-muted-foreground bg-muted rounded-r-md px-2 py-1.5 border border-border border-l-0 whitespace-nowrap">.rss</span>
        </div>
      </div>
      <div v-else class="space-y-1">
        <label class="text-xs font-medium">URL</label>
        <Input v-model="form.url" placeholder="Feed URL" required />
      </div>
      <label class="flex items-center gap-2 text-sm cursor-pointer">
        <input type="checkbox" v-model.number="form.extract_content" :true-value="1" :false-value="0" class="toggle" />
        Auto-extract full article content
      </label>
      <div class="flex gap-2">
        <Button type="submit">{{ feed ? 'Save' : 'Add' }}</Button>
        <Button v-if="feed" variant="outline" class="text-destructive" @click="emit('delete', feed.id)">Delete</Button>
      </div>
    </form>
  </Dialog>
</template>
