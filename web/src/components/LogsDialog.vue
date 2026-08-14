<script setup lang="ts">
import { ref, onUnmounted, computed } from 'vue'
import Dialog from './ui/Dialog.vue'
import Button from './ui/Button.vue'
import { logsApi } from '../services/api'
import type { LogEntry } from '../types/api'
import { ChevronRight, RefreshCw, ScrollText } from 'lucide-vue-next'

const PAGE_SIZE = 200

const open = ref(false)
const entries = ref<LogEntry[]>([])
const loading = ref(false)
const loadingMore = ref(false)
const hasMore = ref(false)
const total = ref(0)
const levelFilter = ref('')
const categoryFilter = ref('')
const expanded = ref<Set<string>>(new Set())
const error = ref('')

// Canonical display order for categories; unknown categories sort after.
const CATEGORY_ORDER = ['http', 'feed', 'item', 'digest', 'app', 'auth']

// The backend stamps an explicit "category" attribute on some records (e.g.
// item-level logs emitted from the http layer); otherwise the source-derived
// type is the category. Fall back to "app" so the badge never renders empty.
function categoryOf(e: LogEntry): string {
  return e.attrs?.category || e.type || 'app'
}

const categories = computed(() => {
  const set = new Set<string>()
  for (const e of entries.value) set.add(categoryOf(e))
  return [...set].sort((a, b) => {
    const ai = CATEGORY_ORDER.indexOf(a)
    const bi = CATEGORY_ORDER.indexOf(b)
    return (ai === -1 ? 99 : ai) - (bi === -1 ? 99 : bi) || a.localeCompare(b)
  })
})

let timer: ReturnType<typeof setInterval> | null = null

const levelClass: Record<string, string> = {
  DEBUG: 'bg-muted text-muted-foreground',
  INFO: 'bg-muted text-foreground',
  WARN: 'bg-amber-500/15 text-amber-600 dark:text-amber-400',
  ERROR: 'bg-red-500/15 text-red-600 dark:text-red-400',
}

function entryKey(e: LogEntry): string {
  return `${e.time}|${e.level}|${categoryOf(e)}|${e.message}|${JSON.stringify(e.attrs || {})}`
}

// Structured context fields to show in the expanded section. The category
// itself is already rendered as the row badge, so it is not repeated.
function attrEntries(e: LogEntry): [string, string][] {
  if (!e.attrs) return []
  return Object.entries(e.attrs).filter(([k]) => k !== 'category')
}

function expandable(e: LogEntry): boolean {
  return attrEntries(e).length > 0
}

function isExpanded(e: LogEntry): boolean {
  return expanded.value.has(entryKey(e))
}

function toggleExpanded(e: LogEntry) {
  const key = entryKey(e)
  const next = new Set(expanded.value)
  if (next.has(key)) {
    next.delete(key)
  } else {
    next.add(key)
  }
  expanded.value = next
}

function filterParams() {
  return {
    level: levelFilter.value || undefined,
    type: categoryFilter.value || undefined,
  }
}

async function load() {
  loading.value = true
  error.value = ''
  try {
    const resp = await logsApi.list({ limit: PAGE_SIZE, ...filterParams() })
    entries.value = resp.entries
    total.value = resp.total
    hasMore.value = resp.has_more
  } catch (e) {
    error.value = e instanceof Error ? e.message : 'Failed to load logs'
  } finally {
    loading.value = false
  }
}

async function loadOlder() {
  if (loadingMore.value || loading.value || !hasMore.value) return
  loadingMore.value = true
  error.value = ''
  try {
    const resp = await logsApi.list({ limit: PAGE_SIZE, offset: entries.value.length, ...filterParams() })
    // The ring buffer is live, so the filtered stream can shift between pages;
    // dedupe by key when appending to avoid duplicated rows.
    const known = new Set(entries.value.map(entryKey))
    const fresh = resp.entries.filter(e => !known.has(entryKey(e)))
    entries.value.push(...fresh)
    total.value = resp.total
    hasMore.value = resp.has_more
  } catch (e) {
    error.value = e instanceof Error ? e.message : 'Failed to load older logs'
  } finally {
    loadingMore.value = false
  }
}

function onOpenChange(v: boolean) {
  open.value = v
  if (v) {
    load()
    timer = setInterval(() => {
      // Auto-refresh only refreshes the newest page; once older entries are
      // loaded, leave them alone so scroll position isn't clobbered.
      if (entries.value.length <= PAGE_SIZE) load()
    }, 10000)
  } else {
    if (timer) clearInterval(timer)
    timer = null
  }
}

function onFilterChange() {
  expanded.value = new Set()
  if (open.value) load()
}

function onScroll(e: Event) {
  const el = e.target as HTMLElement
  if (el.scrollTop + el.clientHeight >= el.scrollHeight - 120) loadOlder()
}

function fmtTime(t: string): string {
  const d = new Date(t)
  if (isNaN(d.getTime())) return t
  return d.toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit', second: '2-digit' })
}

onUnmounted(() => {
  if (timer) clearInterval(timer)
})
</script>

<template>
  <Dialog
    :open="open"
    @update:open="onOpenChange"
    title="Logs"
    description="Recent backend logs (newest first)."
    content-class="max-w-[min(66vw,1100px)]"
  >
    <template #trigger>
      <Button variant="ghost" size="icon" title="Logs">
        <ScrollText class="size-4" />
      </Button>
    </template>

    <div class="flex flex-col gap-3" data-logs-dialog>
      <div class="flex items-center gap-2">
        <select
          v-model="levelFilter"
          class="h-8 rounded-md border border-border bg-background px-2 text-xs"
          @change="onFilterChange"
        >
          <option value="">All levels</option>
          <option value="debug">DEBUG</option>
          <option value="info">INFO</option>
          <option value="warn">WARN</option>
          <option value="error">ERROR</option>
        </select>
        <select
          v-model="categoryFilter"
          class="h-8 rounded-md border border-border bg-background px-2 text-xs"
          @change="onFilterChange"
        >
          <option value="">All categories</option>
          <option v-for="c in categories" :key="c" :value="c">{{ c }}</option>
        </select>
        <span v-if="hasMore" class="text-xs text-muted-foreground">showing {{ entries.length }} of {{ total }}</span>
        <Button variant="outline" size="sm" class="ml-auto" :disabled="loading" @click="load">
          <RefreshCw class="size-3" :class="loading ? 'animate-spin' : ''" />
          Refresh
        </Button>
      </div>

      <p v-if="error" class="text-xs text-red-500">{{ error }}</p>

      <div class="max-h-[60vh] overflow-y-auto rounded-md border bg-muted/20" @scroll="onScroll">
        <template v-for="(e, i) in entries" :key="i">
          <div
            class="flex cursor-pointer items-start gap-2 border-b border-border/50 px-2 py-1 font-mono text-xs leading-relaxed last:border-b-0 hover:bg-muted/40"
            :class="e.level === 'ERROR' ? 'bg-red-500/5' : e.level === 'WARN' ? 'bg-amber-500/5' : ''"
            :data-expandable="expandable(e)"
            @click="toggleExpanded(e)"
          >
            <button
              v-if="expandable(e)"
              class="mt-px shrink-0 text-muted-foreground hover:text-foreground"
              :aria-label="isExpanded(e) ? 'Collapse details' : 'Expand details'"
              :aria-expanded="isExpanded(e)"
              @click.stop="toggleExpanded(e)"
            >
              <ChevronRight class="size-3 transition-transform" :class="isExpanded(e) ? 'rotate-90' : ''" />
            </button>
            <span v-else class="mt-px size-3 shrink-0" />
            <span class="shrink-0 text-muted-foreground">{{ fmtTime(e.time) }}</span>
            <span class="shrink-0 rounded px-1 py-px text-[10px] font-semibold" :class="levelClass[e.level] || 'bg-muted text-muted-foreground'">
              {{ e.level }}
            </span>
            <span class="shrink-0 rounded border border-border px-1 py-px text-[10px] text-muted-foreground">
              {{ categoryOf(e) }}
            </span>
            <span class="min-w-0 flex-1 break-words">
              {{ e.message }}
            </span>
          </div>
          <div v-if="isExpanded(e)" class="border-b border-border/50 bg-background/60 px-2 pb-2 pl-5">
            <div
              v-if="attrEntries(e).length"
              class="grid grid-cols-[max-content,1fr] gap-x-4 gap-y-0.5 rounded-md border border-border/60 bg-muted/10 px-3 py-2 font-mono text-[11px]"
            >
              <template v-for="[k, v] in attrEntries(e)" :key="k">
                <span class="text-muted-foreground">{{ k }}</span>
                <span class="min-w-0 break-words text-foreground">{{ v }}</span>
              </template>
            </div>
            <p v-else class="text-[11px] text-muted-foreground">No additional fields.</p>
          </div>
        </template>
        <div v-if="loadingMore" class="px-3 py-3 text-center text-xs text-muted-foreground">Loading older…</div>
        <div v-if="!entries.length && !loading" class="px-3 py-6 text-center text-xs text-muted-foreground">
          No log entries match the current filters.
        </div>
        <div v-if="loading" class="px-3 py-6 text-center text-xs text-muted-foreground">Loading…</div>
      </div>
    </div>
  </Dialog>
</template>
