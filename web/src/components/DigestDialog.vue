<script setup lang="ts">
import { ref, reactive, computed, watch } from 'vue'
import { useFeedStore } from '../stores/feed'
import { SCORE_FEED_TYPES } from '../lib/feeds'
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

// Score capability comes from the shared SCORE_FEED_TYPES set. A local copy
// here drifted from it (it omitted twitter), which disabled the min-score field
// for Twitter sources whose items DO carry points — the server sets
// item.points from the tweet's favourite count.

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
// Selections are kept per mode, not shared. A category name and a feed id are
// different kinds of value, so a single array cannot hold both: switching modes
// used to leave the other mode's values in place, silently clearing what the
// user had picked (the TagInput could not match them) and — worse — submitting
// whatever the user then typed for the NEW mode together with stale entries.
// Keeping two arrays means a round trip through the toggle returns the user to
// exactly what they had.
const sourceIdsByMode = reactive<{ categories: TagValue[]; feeds: TagValue[] }>({
  categories: [],
  feeds: [],
})
const sourceIds = computed<TagValue[]>({
  get: () => sourceIdsByMode[sourceMode.value],
  set: (ids) => { sourceIdsByMode[sourceMode.value] = ids },
})
// Deleting a digest config discards its generated results — confirm first
// (same pattern as delete-feed in FeedDialog).
const confirmDelete = ref(false)
const form = reactive({
  name: '',
  schedule: '0 8 * * *',
  highlights: 5,
  comments: 10,
  maxItems: 100,
  minScore: 0,
  prompt: DEFAULT_PROMPT,
})

// The min-score threshold only makes sense when at least one selected source
// can produce scores. An empty selection means "all feeds", which may include
// score-capable feeds, so it stays enabled.
//
// Both modes are considered, not just the visible one: the selection for the
// OTHER mode still contributes to the digest (the user can switch back and
// forth), so a score-capable feed picked under Feeds must keep the field
// enabled while the Categories tab is showing.
const hasScoreCapableSource = computed(() => {
  const scoreCapable = (f: { type: string }) => SCORE_FEED_TYPES.has(f.type)
  if (sourceIdsByMode.categories.length === 0 && sourceIdsByMode.feeds.length === 0) return true
  const categoryIds = new Set(sourceIdsByMode.categories.map(v => String(v)))
  const feedIds = new Set(sourceIdsByMode.feeds.map(v => String(v)))
  return feedStore.feeds.some(f => {
    if (feedIds.has(String(f.id)) && scoreCapable(f)) return true
    return !!f.category && categoryIds.has(f.category) && scoreCapable(f)
  })
})

watch(
  () => props.config,
  (cfg) => {
    // A stale confirmation must not survive a config swap (e.g. Add after Edit).
    confirmDelete.value = false
    if (cfg) {
      let ids: TagValue[] = []
      let highlights = 5, comments = 10, maxItems = 100, minScore = 0, prompt = DEFAULT_PROMPT
      let mode: 'categories' | 'feeds' = 'categories'
      try {
        const parsed = JSON.parse(cfg.config)
        if (parsed.default_highlights) highlights = parsed.default_highlights
        if (parsed.default_comments) comments = parsed.default_comments
        if (parsed.max_items) maxItems = parsed.max_items
        if (typeof parsed.min_score === 'number') minScore = parsed.min_score
        if (parsed.prompt) prompt = parsed.prompt
        if (Array.isArray(parsed.categories) && parsed.categories.length) {
          mode = 'categories'; ids = parsed.categories.map((c: any) => c.name ?? c.id)
        } else if (Array.isArray(parsed.feeds) && parsed.feeds.length) {
          mode = 'feeds'; ids = parsed.feeds.map((f: any) => f.id)
        }
      } catch { /* defaults */ }
      form.name = cfg.name
      form.schedule = cfg.schedule
      // A saved config names exactly one mode's sources, so the other mode
      // starts empty rather than inheriting a stale selection.
      sourceIdsByMode.categories = mode === 'categories' ? ids : []
      sourceIdsByMode.feeds = mode === 'feeds' ? ids : []
      form.highlights = highlights
      form.comments = comments
      form.maxItems = maxItems
      form.minScore = minScore
      form.prompt = prompt
      sourceMode.value = mode
    } else {
      form.name = ''
      form.schedule = '0 8 * * *'
      sourceIdsByMode.categories = []
      sourceIdsByMode.feeds = []
      form.highlights = 5
      form.comments = 10
      form.maxItems = 100
      form.minScore = 0
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
    min_score: Number(form.minScore) || 0,
    prompt: form.prompt || undefined,
  }
  // Only the active mode's sources are emitted: the toggle chooses which
  // namespace the digest draws from, so the two arrays are alternatives, not a
  // union. The per-mode arrays exist so switching modes does not destroy the
  // other side's picks while the dialog is open (see sourceIdsByMode).
  if (sourceMode.value === 'categories') {
    c.categories = sourceIds.value.map(name => ({ name, highlights: form.highlights, comments: form.comments }))
  } else {
    c.feeds = sourceIds.value.map(id => ({ id, highlights: form.highlights, comments: form.comments }))
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
            v-model="sourceIds"
            placeholder="Search categories…"
          />
          <TagInput
            v-else
            :items="feedStore.feeds.map(f => ({ label: f.name + ' (' + f.type + ')', value: f.id }))"
            v-model="sourceIds"
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
        <label class="text-sm font-medium" :class="hasScoreCapableSource ? '' : 'text-muted-foreground'">Min score</label>
        <Input v-model.number="form.minScore" type="number" min="0" :disabled="!hasScoreCapableSource" />
        <p class="text-xs text-muted-foreground mt-0.5">
          {{ hasScoreCapableSource
            ? 'Only include items with at least this many points/upvotes (0 = include all). Scores are refreshed when the digest is generated.'
            : 'None of the selected sources provide scores (only HN, Lobsters, Reddit and Twitter do); this setting has no effect.' }}
        </p>
      </div>
      <div class="space-y-1">
        <label class="text-sm font-medium">Prompt (optional)</label>
        <textarea v-model="form.prompt" rows="3" class="flex w-full rounded-md border border-border bg-background px-3 py-1.5 text-xs" />
      </div>
      <div class="flex gap-2">
        <Button type="submit">{{ config ? 'Save' : 'Create' }}</Button>
        <Button v-if="config" variant="outline" class="text-destructive" data-delete-digest @click="confirmDelete = true">Delete</Button>
      </div>
    </form>
    <Dialog
      v-if="confirmDelete"
      :open="true"
      title="Delete digest config?"
      :description="`Delete digest config “${config?.name}”? Its generated results will be removed. This cannot be undone.`"
      @update:open="v => { if (!v) confirmDelete = false }"
    >
      <div class="flex justify-end gap-2">
        <Button variant="outline" @click="confirmDelete = false">Cancel</Button>
        <Button variant="outline" class="text-destructive" data-delete-digest-confirm @click="emit('delete', config!.id)">Delete</Button>
      </div>
    </Dialog>
  </Dialog>
</template>
