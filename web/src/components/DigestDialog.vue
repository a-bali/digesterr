<script setup lang="ts">
import { ref, reactive, computed, watch } from 'vue'
import { useFeedStore } from '../stores/feed'
import Dialog from './ui/Dialog.vue'
import Input from './ui/Input.vue'
import Button from './ui/Button.vue'
import TagInput, { type TagValue } from './TagInput.vue'
import type { DigestConfig } from '../types/api'

const props = defineProps<{ config: DigestConfig | null }>()
const emit = defineEmits<{
  (e: 'update:open', open: boolean): void
  (e: 'save', data: { name: string; schedule: string; config: string }): void
  (e: 'delete', id: number): void
}>()

const feedStore = useFeedStore()

const DEFAULT_PROMPT = 'You are a research assistant digesting news articles. For each article provided below, produce a 2-3 sentence highlight giving the concrete takeaway and why it matters — skip introductory phrases like "The article discusses" or "The author explains". Use the comments and extracted content to enrich your analysis.'

// Category options are derived from feeds and selectable by name; feeds are
// selectable by id but displayed with their readable names.
const categoryItems = computed(() => {
  const names = new Set<string>()
  for (const f of feedStore.feeds) {
    if (f.category) names.add(f.category)
  }
  return [...names].sort((a, b) => a.localeCompare(b)).map(name => ({ label: name, value: name }))
})

const sourceMode = ref<'categories' | 'feeds'>('categories')
const form = reactive({
  name: '',
  schedule: '0 8 * * *',
  sourceIds: [] as TagValue[],
  highlights: 5,
  comments: 10,
  maxItems: 100,
  prompt: DEFAULT_PROMPT,
})

watch(
  () => props.config,
  (cfg) => {
    if (cfg) {
      let ids: TagValue[] = []
      let highlights = 5, comments = 10, maxItems = 100, prompt = DEFAULT_PROMPT
      let mode: 'categories' | 'feeds' = 'categories'
      try {
        const parsed = JSON.parse(cfg.config)
        if (parsed.default_highlights) highlights = parsed.default_highlights
        if (parsed.default_comments) comments = parsed.default_comments
        if (parsed.max_items) maxItems = parsed.max_items
        if (parsed.prompt) prompt = parsed.prompt
        if (Array.isArray(parsed.categories) && parsed.categories.length) {
          mode = 'categories'; ids = parsed.categories.map((c: any) => c.name ?? c.id)
        } else if (Array.isArray(parsed.feeds) && parsed.feeds.length) {
          mode = 'feeds'; ids = parsed.feeds.map((f: any) => f.id)
        }
      } catch { /* defaults */ }
      form.name = cfg.name
      form.schedule = cfg.schedule
      form.sourceIds = ids
      form.highlights = highlights
      form.comments = comments
      form.maxItems = maxItems
      form.prompt = prompt
      sourceMode.value = mode
    } else {
      form.name = ''
      form.schedule = '0 8 * * *'
      form.sourceIds = []
      form.highlights = 5
      form.comments = 10
      form.maxItems = 100
      form.prompt = DEFAULT_PROMPT
      sourceMode.value = 'categories'
    }
  },
  { immediate: true },
)

function buildConfig(): string {
  const c: any = {
    default_highlights: form.highlights,
    default_comments: form.comments,
    max_items: form.maxItems,
    prompt: form.prompt || undefined,
  }
  if (sourceMode.value === 'categories') {
    c.categories = form.sourceIds.map(name => ({ name, highlights: form.highlights, comments: form.comments }))
  } else {
    c.feeds = form.sourceIds.map(id => ({ id, highlights: form.highlights, comments: form.comments }))
  }
  return JSON.stringify(c)
}

function submit() {
  emit('save', { name: form.name, schedule: form.schedule, config: buildConfig() })
}
</script>

<template>
  <Dialog :open="true" :title="config ? 'Edit Digest Config' : 'Create Digest Config'" @update:open="emit('update:open', $event)">
    <form @submit.prevent="submit" class="space-y-4">
      <div class="space-y-1">
        <label class="text-sm font-medium">Name</label>
        <Input v-model="form.name" placeholder="Digest name (e.g. Daily Tech)" required />
      </div>
      <div class="space-y-1">
        <label class="text-sm font-medium">Schedule (cron syntax)</label>
        <Input v-model="form.schedule" placeholder="0 8 * * *" required />
        <p class="text-xs text-muted-foreground mt-0.5">0 8 * * * (daily 8am), 0 */6 * * * (every 6h)</p>
      </div>
      <div class="space-y-1">
        <label class="text-sm font-medium">Source</label>
        <div class="flex rounded-md border border-border overflow-hidden text-xs w-fit">
          <button type="button" class="px-2 py-1 transition-colors" :class="sourceMode === 'categories' ? 'bg-accent text-accent-foreground' : 'hover:bg-muted'" @click="sourceMode = 'categories'">Categories</button>
          <button type="button" class="px-2 py-1 border-x border-border transition-colors" :class="sourceMode === 'feeds' ? 'bg-accent text-accent-foreground' : 'hover:bg-muted'" @click="sourceMode = 'feeds'">Feeds</button>
        </div>
        <div class="mt-1">
          <TagInput
            v-if="sourceMode === 'categories'"
            :items="categoryItems"
            v-model="form.sourceIds"
            placeholder="Search categories…"
          />
          <TagInput
            v-else
            :items="feedStore.feeds.map(f => ({ label: f.name + ' (' + f.type + ')', value: f.id }))"
            v-model="form.sourceIds"
            placeholder="Search feeds…"
          />
        </div>
      </div>
      <div class="grid grid-cols-3 gap-3">
        <div class="space-y-1"><label class="text-sm font-medium">Max highlights</label><Input v-model.number="form.highlights" type="number" min="1" max="10" /></div>
        <div class="space-y-1"><label class="text-sm font-medium">Max comments</label><Input v-model.number="form.comments" type="number" min="1" max="50" /></div>
        <div class="space-y-1"><label class="text-sm font-medium">Max items</label><Input v-model.number="form.maxItems" type="number" min="1" max="1000" /></div>
      </div>
      <div class="space-y-1">
        <label class="text-sm font-medium">Prompt (optional)</label>
        <textarea v-model="form.prompt" rows="3" class="flex w-full rounded-md border border-border bg-background px-3 py-1.5 text-xs" />
      </div>
      <div class="flex gap-2">
        <Button type="submit">{{ config ? 'Save' : 'Create' }}</Button>
        <Button v-if="config" variant="outline" class="text-destructive" @click="emit('delete', config.id)">Delete</Button>
      </div>
    </form>
  </Dialog>
</template>
