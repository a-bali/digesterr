<script setup lang="ts">
import { ref, onUnmounted, computed } from 'vue'
import Dialog from './ui/Dialog.vue'
import Button from './ui/Button.vue'
import { logsApi } from '../services/api'
import type { LogEntry } from '../types/api'
import { ScrollText, RefreshCw } from 'lucide-vue-next'

const PAGE_SIZE = 200

const open = ref(false)
const entries = ref<LogEntry[]>([])
const loading = ref(false)
const loadingMore = ref(false)
const hasMore = ref(false)
const total = ref(0)
const levelFilter = ref('')
const typeFilter = ref('')
const error = ref('')

const types = computed(() => {
  const set = new Set<string>()
  for (const e of entries.value) if (e.type) set.add(e.type)
  return [...set].sort()
})

let timer: ReturnType<typeof setInterval> | null = null

const levelClass: Record<string, string> = {
  DEBUG: 'bg-muted text-muted-foreground',
  INFO: 'bg-muted text-foreground',
  WARN: 'bg-amber-500/15 text-amber-600 dark:text-amber-400',
  ERROR: 'bg-red-500/15 text-red-600 dark:text-red-400',
}

function entryKey(e: LogEntry): string {
  return `${e.time}|${e.level}|${e.type}|${e.message}`
}

function filterParams() {
  return {
    level: levelFilter.value || undefined,
    type: typeFilter.value || undefined,
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

function attrString(e: LogEntry): string {
  if (!e.attrs) return ''
  return Object.entries(e.attrs).map(([k, v]) => `${k}=${v}`).join(' ')
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
          v-model="typeFilter"
          class="h-8 rounded-md border border-border bg-background px-2 text-xs"
          @change="onFilterChange"
        >
          <option value="">All types</option>
          <option v-for="t in types" :key="t" :value="t">{{ t }}</option>
        </select>
        <span v-if="hasMore" class="text-xs text-muted-foreground">showing {{ entries.length }} of {{ total }}</span>
        <Button variant="outline" size="sm" class="ml-auto" :disabled="loading" @click="load">
          <RefreshCw class="size-3" :class="loading ? 'animate-spin' : ''" />
          Refresh
        </Button>
      </div>

      <p v-if="error" class="text-xs text-red-500">{{ error }}</p>

      <div class="max-h-[60vh] overflow-y-auto rounded-md border bg-muted/20" @scroll="onScroll">
        <div
          v-for="(e, i) in entries"
          :key="i"
          class="flex items-start gap-2 border-b border-border/50 px-2 py-1 font-mono text-xs leading-relaxed last:border-b-0"
          :class="e.level === 'ERROR' ? 'bg-red-500/5' : e.level === 'WARN' ? 'bg-amber-500/5' : ''"
        >
          <span class="shrink-0 text-muted-foreground">{{ fmtTime(e.time) }}</span>
          <span class="shrink-0 rounded px-1 py-px text-[10px] font-semibold" :class="levelClass[e.level] || 'bg-muted text-muted-foreground'">
            {{ e.level }}
          </span>
          <span class="shrink-0 text-muted-foreground">{{ e.type }}</span>
          <span class="min-w-0 flex-1 break-words">
            {{ e.message }}<span v-if="attrString(e)" class="text-muted-foreground"> — {{ attrString(e) }}</span>
          </span>
        </div>
        <div v-if="loadingMore" class="px-3 py-3 text-center text-xs text-muted-foreground">Loading older…</div>
        <div v-if="!entries.length && !loading" class="px-3 py-6 text-center text-xs text-muted-foreground">
          No log entries match the current filters.
        </div>
        <div v-if="loading" class="px-3 py-6 text-center text-xs text-muted-foreground">Loading…</div>
      </div>
    </div>
  </Dialog>
</template>
