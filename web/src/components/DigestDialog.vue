<script setup lang="ts">
import { ref, reactive, computed, watch, onMounted } from 'vue'
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

// Email delivery requires server-side agentmail credentials, reported by the
// unauthenticated /api/health endpoint.
const agentMailConfigured = ref(false)
onMounted(async () => {
  try {
    const res = await fetch('/api/health')
    if (res.ok) {
      const data = await res.json()
      agentMailConfigured.value = !!data.agentmail
    }
  } catch { /* feature stays hidden */ }
})

// Feed types that produce a real score (points/upvotes) for their items.
// Plain RSS/Atom feeds never get scores.
const SCORE_CAPABLE_TYPES = ['hn', 'lobsters', 'reddit']

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
  minScore: 0,
  prompt: DEFAULT_PROMPT,
  sendEmail: false,
  emailTo: '',
})

// The min-score threshold only makes sense when at least one selected source
// can produce scores. An empty selection means "all feeds", which may include
// score-capable feeds, so it stays enabled.
const hasScoreCapableSource = computed(() => {
  if (form.sourceIds.length === 0) return true
  const ids = new Set(form.sourceIds.map(v => String(v)))
  if (sourceMode.value === 'feeds') {
    return feedStore.feeds.some(f => ids.has(String(f.id)) && SCORE_CAPABLE_TYPES.includes(f.type))
  }
  return feedStore.feeds.some(f => f.category && ids.has(f.category) && SCORE_CAPABLE_TYPES.includes(f.type))
})

watch(
  () => props.config,
  (cfg) => {
    if (cfg) {
      let ids: TagValue[] = []
      let highlights = 5, comments = 10, maxItems = 100, minScore = 0, prompt = DEFAULT_PROMPT
      let sendEmail = false, emailTo = ''
      let mode: 'categories' | 'feeds' = 'categories'
      try {
        const parsed = JSON.parse(cfg.config)
        if (parsed.default_highlights) highlights = parsed.default_highlights
        if (parsed.default_comments) comments = parsed.default_comments
        if (parsed.max_items) maxItems = parsed.max_items
        if (typeof parsed.min_score === 'number') minScore = parsed.min_score
        if (parsed.prompt) prompt = parsed.prompt
        if (parsed.send_email) sendEmail = true
        if (parsed.email_to) emailTo = parsed.email_to
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
      form.minScore = minScore
      form.prompt = prompt
      form.sendEmail = sendEmail
      form.emailTo = emailTo
      sourceMode.value = mode
    } else {
      form.name = ''
      form.schedule = '0 8 * * *'
      form.sourceIds = []
      form.highlights = 5
      form.comments = 10
      form.maxItems = 100
      form.minScore = 0
      form.prompt = DEFAULT_PROMPT
      form.sendEmail = false
      form.emailTo = ''
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
  if (sourceMode.value === 'categories') {
    c.categories = form.sourceIds.map(name => ({ name, highlights: form.highlights, comments: form.comments }))
  } else {
    c.feeds = form.sourceIds.map(id => ({ id, highlights: form.highlights, comments: form.comments }))
  }
  if (form.sendEmail && agentMailConfigured.value && form.emailTo.trim()) {
    c.send_email = true
    c.email_to = form.emailTo.trim()
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
        <label class="text-sm font-medium" :class="hasScoreCapableSource ? '' : 'text-muted-foreground'">Min score</label>
        <Input v-model.number="form.minScore" type="number" min="0" :disabled="!hasScoreCapableSource" />
        <p class="text-xs text-muted-foreground mt-0.5">
          {{ hasScoreCapableSource
            ? 'Only include items with at least this many points/upvotes (0 = include all). Scores are refreshed when the digest is generated.'
            : 'None of the selected sources provide scores (only HN, Lobsters and Reddit do); this setting has no effect.' }}
        </p>
      </div>
      <div class="space-y-1">
        <label class="text-sm font-medium">Prompt (optional)</label>
        <textarea v-model="form.prompt" rows="3" class="flex w-full rounded-md border border-border bg-background px-3 py-1.5 text-xs" />
      </div>
      <div class="space-y-2">
        <label class="flex items-center gap-2 text-sm font-medium">
          <input v-model="form.sendEmail" type="checkbox" :disabled="!agentMailConfigured" class="h-4 w-4 accent-primary" />
          Send via email
        </label>
        <p v-if="!agentMailConfigured" class="text-xs text-muted-foreground">
          Requires agentmail credentials on the server (AGENTMAIL_TOKEN and AGENTMAIL_INBOX_ID).
        </p>
        <div v-if="agentMailConfigured && form.sendEmail" class="space-y-1">
          <label class="text-sm font-medium">Email address</label>
          <Input v-model="form.emailTo" type="email" placeholder="you@example.com" required />
        </div>
      </div>
      <div class="flex gap-2">
        <Button type="submit">{{ config ? 'Save' : 'Create' }}</Button>
        <Button v-if="config" variant="outline" class="text-destructive" @click="emit('delete', config.id)">Delete</Button>
      </div>
    </form>
  </Dialog>
</template>
