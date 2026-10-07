<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import { AlignJustify, AlignLeft, Rows3, Sparkles } from 'lucide-vue-next'
import DropdownMenu from '../components/ui/DropdownMenu.vue'
import { itemApi } from '../services/api'
import type { Item, ItemSummaryResponse } from '../types/api'

type SummaryLength = 'short' | 'medium' | 'long'
type SummaryDetail = 'gist' | 'balanced' | 'detailed'

const SUMMARY_LENGTHS: Record<SummaryLength, { min: number; max: number; detail: string; level: SummaryDetail }> = {
  short: { min: 1, max: 2, detail: '1-2 sentences', level: 'gist' },
  medium: { min: 5, max: 7, detail: '5-7 sentences', level: 'balanced' },
  long: { min: 12, max: 18, detail: '12-18 sentences', level: 'detailed' },
}
const SUMMARY_ICONS = { short: AlignLeft, medium: Rows3, long: AlignJustify }

const route = useRoute()
const item = ref<Item | null>(null)
const summary = ref<ItemSummaryResponse | null>(null)
const summaryLength = ref<SummaryLength>('medium')
const loading = ref(false)
const error = ref('')
let requestToken = 0

const currentSummaryLabel = computed(() => summaryLength.value.charAt(0).toUpperCase() + summaryLength.value.slice(1))
const summaryMenuItems = computed(() =>
  (Object.keys(SUMMARY_LENGTHS) as SummaryLength[]).map(key => ({
    label: key.charAt(0).toUpperCase() + key.slice(1),
    icon: SUMMARY_ICONS[key],
    detail: SUMMARY_LENGTHS[key].detail,
    checked: summaryLength.value === key,
    onClick: () => selectSummaryLength(key),
  })),
)

async function runSummary() {
  const id = Number(route.params.itemId)
  if (!Number.isSafeInteger(id) || id <= 0) {
    error.value = 'Invalid item id'
    return
  }
  const token = ++requestToken
  loading.value = true
  error.value = ''
  try {
    if (!item.value || item.value.id !== id) item.value = await itemApi.getItem(id)
    const { min, max, level } = SUMMARY_LENGTHS[summaryLength.value]
    const result = await itemApi.summarize(id, {
      sentence_count: max,
      min_sentences: min,
      max_sentences: max,
      detail: level,
    })
    if (token === requestToken) summary.value = result
  } catch (e) {
    if (token === requestToken) error.value = e instanceof Error ? e.message : 'Failed to generate summary'
  } finally {
    if (token === requestToken) loading.value = false
  }
}

function selectSummaryLength(length: SummaryLength) {
  summaryLength.value = length
  void runSummary()
}

watch(() => route.params.itemId, () => {
  item.value = null
  summary.value = null
  summaryLength.value = 'medium'
  void runSummary()
}, { immediate: true })
</script>

<template>
  <main class="mx-auto max-w-2xl px-5 py-6">
    <h1 class="text-lg font-semibold leading-snug">{{ item?.title || 'Item summary' }}</h1>
    <a v-if="item?.url" :href="item.url" target="_blank" rel="noopener noreferrer"
       class="mt-1 inline-block text-sm text-accent hover:underline">Open original</a>

    <section class="mt-4 rounded-md border border-accent/20 bg-accent/5 px-3 py-2.5" data-summary-box>
      <div class="mb-1.5 flex items-center gap-1.5">
        <Sparkles class="size-3 shrink-0 text-accent" aria-hidden="true" />
        <span class="flex-1 text-xs font-semibold uppercase text-muted-foreground">LLM Summary</span>
      </div>
      <div v-if="loading" class="flex items-center gap-2 py-1 text-sm text-muted-foreground" role="status" aria-live="polite">
        <span>Generating summary…</span>
      </div>
      <p v-else-if="error" class="text-sm text-warning-foreground" data-summary-error>{{ error }}</p>
      <div v-else-if="summary" class="space-y-3">
        <p v-if="summary.content_summary" class="text-sm leading-relaxed">{{ summary.content_summary }}</p>
        <p v-if="summary.comments_summary" class="text-sm leading-relaxed">{{ summary.comments_summary }}</p>
        <p v-if="!summary.content_summary && !summary.comments_summary" class="text-sm text-muted-foreground" data-summary-empty>
          The model did not return a usable summary. Try again.
        </p>
      </div>
    </section>

    <div class="mt-3 inline-flex items-center">
      <button type="button" :disabled="loading" data-summary-regenerate
        class="inline-flex items-center gap-1 rounded-l-md border border-border px-3 py-1.5 text-sm hover:bg-muted disabled:opacity-50"
        :title="loading ? 'Summarizing…' : `Regenerate (${currentSummaryLabel})`"
        @click="runSummary">
        <span>{{ loading ? 'Summarizing…' : 'Regenerate' }}</span>
      </button>
      <DropdownMenu :items="summaryMenuItems" data-summary-length-menu>
        <template #trigger>
          <button type="button" :disabled="loading" data-summary-length-opener
            class="rounded-r-md border-y border-r border-border px-2 py-1.5 text-sm text-muted-foreground hover:bg-muted disabled:opacity-50"
            title="Summary length" aria-label="Summary length" aria-haspopup="menu">
            {{ currentSummaryLabel }} ▾
          </button>
        </template>
      </DropdownMenu>
    </div>
  </main>
</template>
