<script setup lang="ts">
import { ref, computed, watch, onUnmounted } from 'vue'
import Dialog from './ui/Dialog.vue'
import Button from './ui/Button.vue'
import Skeleton from './ui/Skeleton.vue'
import { metricsApi } from '../services/api'
import { toastError } from '../lib/toast'
import type { MetricsSummary, MetricsGroup, MetricsBucket } from '../types/api'
import { BarChart3, RefreshCw } from 'lucide-vue-next'

const open = ref(false)
const data = ref<MetricsSummary | null>(null)
const loading = ref(false)
const error = ref('')
const timespan = ref('6h')
const activeDims = ref(new Set(['reason', 'status']))
const sortCol = ref<'count' | 'bytes' | 'errors' | 'avg_ms'>('count')
const sortAsc = ref(false)
const showResetConfirm = ref(false)
const resetting = ref(false)

const DIMENSIONS = ['reason', 'feed', 'host', 'status'] as const

const TIMESPANS: { label: string; since: string; bucket: string }[] = [
  { label: '1h', since: '-1h', bucket: '5m' },
  { label: '6h', since: '-6h', bucket: '15m' },
  { label: '12h', since: '-12h', bucket: '15m' },
  { label: '24h', since: '-24h', bucket: '1h' },
  { label: '7d', since: '-168h', bucket: '6h' },
]

function sinceParam(): string {
  const ts = TIMESPANS.find(t => t.label === timespan.value)
  const d = new Date()
  d.setHours(d.getHours() + parseInt(ts!.since))
  return d.toISOString()
}

function bucketParam(): string {
  return TIMESPANS.find(t => t.label === timespan.value)?.bucket || '15m'
}

async function load() {
  loading.value = true
  error.value = ''
  try {
    data.value = await metricsApi.get({
      since: sinceParam(),
      group_by: [...activeDims.value].join(','),
      bucket: bucketParam(),
    })
  } catch (e: any) {
    error.value = e instanceof Error ? e.message : 'Failed to load metrics'
  } finally {
    loading.value = false
  }
}

function toggleDim(d: string) {
  if (activeDims.value.has(d)) {
    if (activeDims.value.size > 1) activeDims.value.delete(d)
  } else {
    activeDims.value.add(d)
  }
  activeDims.value = new Set(activeDims.value) // trigger reactivity
  if (open.value) load()
}

function setTimespan(label: string) {
  timespan.value = label
  if (open.value) load()
}

function toggleSort(col: typeof sortCol.value) {
  if (sortCol.value === col) sortAsc.value = !sortAsc.value
  else { sortCol.value = col; sortAsc.value = false }
}

const sortedGroups = computed(() => {
  if (!data.value) return []
  const g = [...data.value.groups]
  g.sort((a, b) => {
    const av = a[sortCol.value], bv = b[sortCol.value]
    return sortAsc.value ? (av as number) - (bv as number) : (bv as number) - (av as number)
  })
  return g
})

// Chart path generation
const chartPath = computed(() => {
  if (!data.value?.buckets?.length) return { success: '', errors: '', dots: [] as {x:number;y:number;type:string}[] }
  const buckets = data.value.buckets
  const maxCount = Math.max(...buckets.map(b => b.count), 1)
  const w = 800, h = 120, pad = 10
  const stepX = buckets.length > 1 ? (w - pad * 2) / (buckets.length - 1) : 0

  let success = '', errors = ''
  const dots: {x:number;y:number;type:string}[] = []
  buckets.forEach((b, i) => {
    const x = buckets.length > 1 ? pad + i * stepX : w / 2
    const yS = h - pad - (b.count - b.errors) / maxCount * (h - pad * 2)
    const yE = h - pad - b.errors / maxCount * (h - pad * 2)
    success += (i === 0 ? 'M' : 'L') + `${x},${yS} `
    errors += (i === 0 ? 'M' : 'L') + `${x},${yE} `
    dots.push({ x, y: yS, type: 'success' })
    if (b.errors > 0) dots.push({ x, y: yE, type: 'errors' })
  })
  return { success: success.trim(), errors: errors.trim(), dots }
})

function fmtBytes(b: number): string {
  if (b < 1024) return b + ' B'
  if (b < 1048576) return (b / 1024).toFixed(1) + ' KB'
  return (b / 1048576).toFixed(1) + ' MB'
}

function onOpenChange(v: boolean) {
  open.value = v
  if (v) load()
}

async function confirmReset() {
  resetting.value = true
  try {
    await metricsApi.reset()
    showResetConfirm.value = false
    data.value = null
    await load()
  } catch (e: any) {
    error.value = e instanceof Error ? e.message : 'Failed to reset metrics'
  } finally {
    resetting.value = false
  }
}

function onDimensionToggle() {
  if (open.value) load()
}
</script>

<template>
  <Dialog
    :open="open"
    @update:open="onOpenChange"
    title="Operational Metrics"
    description="Outbound HTTP request metrics: bytes received, response codes, and durations."
    content-class="max-w-[min(95vw,960px)] w-[95vw]"
  >
    <template #trigger>
      <Button variant="ghost" size="icon" title="Metrics" data-metrics-trigger>
        <BarChart3 class="size-4" />
      </Button>
    </template>

    <div class="flex flex-col gap-4" data-metrics-dialog>
      <!-- Timespan + Refresh + Reset -->
      <div class="flex items-center gap-1 flex-wrap">
        <button v-for="t in TIMESPANS" :key="t.label"
          class="px-3 py-1 rounded-md border text-xs transition-colors"
          :class="timespan === t.label ? 'bg-accent text-accent-foreground border-accent' : 'border-border text-muted-foreground hover:border-muted-foreground'"
          @click="setTimespan(t.label)"
        >{{ t.label }}</button>
        <span class="flex-1" />
        <button v-if="!showResetConfirm"
          class="px-3 py-1 rounded-md border border-border text-xs text-destructive hover:bg-destructive/10 transition-colors"
          @click="showResetConfirm = true"
        >Reset</button>
        <template v-else>
          <span class="text-xs text-destructive">Reset all?</span>
          <button class="px-3 py-1 rounded-md border border-destructive text-xs text-destructive hover:bg-destructive/10 transition-colors" :disabled="resetting" @click="confirmReset">
            {{ resetting ? '…' : 'Yes' }}
          </button>
          <button class="px-3 py-1 rounded-md border border-border text-xs text-muted-foreground hover:border-muted-foreground transition-colors" @click="showResetConfirm = false">No</button>
        </template>
        <button class="px-3 py-1 rounded-md border border-border text-xs text-muted-foreground hover:border-muted-foreground transition-colors" :disabled="loading" @click="load">
          <RefreshCw class="size-3 inline" :class="loading ? 'animate-spin' : ''" />
        </button>
      </div>

      <p v-if="error" class="text-xs text-destructive">{{ error }}</p>

      <!-- Loading -->
      <div v-if="loading && !data" class="py-8"><Skeleton class="mx-auto h-48 max-w-md" /></div>

      <template v-if="data">
        <!-- Summary cards — 2 cols on mobile, 4 on sm+ -->
        <div class="grid grid-cols-2 sm:grid-cols-4 gap-3">
          <div class="rounded-lg border bg-card p-3">
            <div class="text-[10px] text-muted-foreground uppercase tracking-wide mb-1">Requests</div>
            <div class="text-xl font-bold">{{ data.total_requests.toLocaleString() }}</div>
          </div>
          <div class="rounded-lg border bg-card p-3">
            <div class="text-[10px] text-muted-foreground uppercase tracking-wide mb-1">Bytes Recv</div>
            <div class="text-xl font-bold">{{ fmtBytes(data.total_bytes) }}</div>
          </div>
          <div class="rounded-lg border bg-card p-3">
            <div class="text-[10px] text-muted-foreground uppercase tracking-wide mb-1">Success</div>
            <div class="text-xl font-bold text-green-500">{{ data.total_requests ? ((data.success_count / data.total_requests) * 100).toFixed(1) : '0' }}%</div>
            <div class="text-[10px] text-muted-foreground">{{ data.success_count }} ok · {{ data.error_count }} err</div>
          </div>
          <div class="rounded-lg border bg-card p-3">
            <div class="text-[10px] text-muted-foreground uppercase tracking-wide mb-1">Avg Duration</div>
            <div class="text-xl font-bold">{{ Math.round(data.avg_duration_ms) }}ms</div>
          </div>
        </div>

        <!-- Chart -->
        <div class="rounded-lg border bg-card p-4" v-if="data.buckets.length">
          <div class="text-xs font-semibold mb-3 flex items-center gap-3">
            Requests over time
            <span class="flex gap-3 font-normal text-muted-foreground">
              <span class="flex items-center gap-1"><span class="inline-block w-2 h-2 rounded-full bg-accent" /> success</span>
              <span class="flex items-center gap-1"><span class="inline-block w-2 h-2 rounded-full bg-destructive" /> errors</span>
            </span>
          </div>
          <svg class="w-full" :viewBox="`0 0 800 120`" preserveAspectRatio="none" style="height:120px">
            <line v-for="y in [30,60,90]" :key="y" :x1="0" :y1="y" :x2="800" :y2="y" stroke="hsl(217 33% 17%)" stroke-width="0.5" />
            <path :d="chartPath.success" fill="none" stroke="hsl(217 91% 60%)" stroke-width="2" />
            <path :d="chartPath.errors" fill="none" stroke="hsl(0 72% 51%)" stroke-width="1.5" stroke-dasharray="4,2" />
            <circle v-for="(dot, i) in chartPath.dots" :key="'d'+i" :cx="dot.x" :cy="dot.y" r="4"
              :fill="dot.type === 'success' ? 'hsl(217 91% 60%)' : 'hsl(0 72% 51%)'" />
          </svg>
        </div>

        <!-- Dimension picker -->
        <div class="flex items-center gap-2 flex-wrap">
          <span class="text-[10px] text-muted-foreground uppercase tracking-wide font-medium">Group by</span>
          <button v-for="d in DIMENSIONS" :key="d"
            class="px-2.5 py-0.5 rounded-full border text-[11px] transition-colors cursor-pointer"
            :class="activeDims.has(d) ? 'bg-accent text-accent-foreground border-accent' : 'border-border text-muted-foreground hover:border-muted-foreground'"
            @click="toggleDim(d)"
          >{{ d }}</button>
        </div>

        <!-- Pivot table -->
        <div class="rounded-lg border bg-card">
          <div class="overflow-x-auto max-h-[40vh] overflow-y-auto">
          <table class="w-full text-xs border-collapse">
            <thead>
              <tr>
                <th v-for="d in DIMENSIONS" :key="d" v-show="activeDims.has(d)"
                  class="text-left px-3 py-2 bg-muted/30 text-muted-foreground font-medium uppercase tracking-wider text-[10px]"
                >{{ d }}</th>
                <th v-for="col in (['count','bytes','errors','avg_ms'] as const)" :key="col"
                  class="text-right px-3 py-2 bg-muted/30 text-muted-foreground font-medium uppercase tracking-wider text-[10px] cursor-pointer hover:text-foreground select-none"
                  :class="{ 'text-accent': sortCol === col }"
                  @click="toggleSort(col)"
                >{{ col === 'avg_ms' ? 'ms' : col === 'bytes' ? 'recv' : col }} <span class="opacity-40">{{ sortCol === col ? (sortAsc ? '▲' : '▼') : '▲' }}</span></th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="(g, i) in sortedGroups" :key="i" class="border-t border-border/50 hover:bg-muted/20">
                <td v-show="activeDims.has('reason')" class="px-3 py-1.5">
                  <span class="inline-block px-1.5 py-0.5 rounded text-[10px] font-medium"
                    :class="{
                      'bg-accent/15 text-accent': g.reason === 'fetch',
                      'bg-purple-500/15 text-purple-400': g.reason === 'enrichment',
                      'bg-yellow-500/15 text-yellow-400': g.reason === 'extraction',
                      'bg-pink-500/15 text-pink-400': g.reason === 'llm',
                      'bg-muted text-muted-foreground': g.reason === 'server',
                    }">{{ g.reason }}</span>
                </td>
                <td v-show="activeDims.has('feed')" class="px-3 py-1.5">{{ g.feed }}</td>
                <td v-show="activeDims.has('host')" class="px-3 py-1.5">{{ g.host }}</td>
                <td v-show="activeDims.has('status')" class="px-3 py-1.5">
                  <span class="inline-block px-1.5 py-0.5 rounded text-[10px] font-medium"
                    :class="g.status.startsWith('2') || g.status === '2xx' ? 'bg-green-500/15 text-green-400' : 'bg-red-500/15 text-red-400'"
                  >{{ g.status }}</span>
                </td>
                <td class="text-right px-3 py-1.5 font-variant-numeric tabular-nums">{{ g.count.toLocaleString() }}</td>
                <td class="text-right px-3 py-1.5 font-variant-numeric tabular-nums">{{ fmtBytes(g.bytes) }}</td>
                <td class="text-right px-3 py-1.5 font-variant-numeric tabular-nums" :class="g.errors > 0 ? 'text-red-400' : ''">{{ g.errors }}</td>
                <td class="text-right px-3 py-1.5 font-variant-numeric tabular-nums">{{ g.avg_ms }}</td>
              </tr>
            </tbody>
          </table>
          </div>
          <div v-if="!data.groups.length && !loading" class="px-3 py-6 text-center text-xs text-muted-foreground">
            No request data in this time range.
          </div>
        </div>
      </template>
    </div>
  </Dialog>
</template>
