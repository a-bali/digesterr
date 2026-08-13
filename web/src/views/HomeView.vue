<script setup lang="ts">
import { ref, watch, onMounted, onUnmounted, computed } from 'vue'
import { useRouter, useRoute } from 'vue-router'
import { SplitterGroup, SplitterPanel, SplitterResizeHandle } from 'reka-ui'
import Button from '../components/ui/Button.vue'
import FilterBar from '../components/FilterBar.vue'
import FeedSidebar from '../components/FeedSidebar.vue'
import ItemList from '../components/ItemList.vue'
import ItemContent from '../components/ItemContent.vue'
import DigestContent from '../components/DigestContent.vue'
import Skeleton from '../components/ui/Skeleton.vue'
import Tooltip from '../components/ui/Tooltip.vue'
import { useAuthStore } from '../stores/auth'
import { useFeedStore } from '../stores/feed'
import { useCategoryStore } from '../stores/category'
import { useDigestStore } from '../stores/digest'
import { useThemeStore } from '../stores/theme'
import { feedApi } from '../services/api'
import { stripContent } from '../lib/utils'
import type { Item, DigestResult } from '../types/api'
import {
  Rss, LogOut, Sun, Moon,
  Menu, ArrowLeft, Circle, CircleDot, CircleCheck, Check, ArrowUp, MessageSquare, PanelLeftClose, PanelLeftOpen,
} from 'lucide-vue-next'

const router = useRouter()
const route = useRoute()
const auth = useAuthStore()
const feedStore = useFeedStore()
const digestStore = useDigestStore()
const categoryStore = useCategoryStore()
const theme = useThemeStore()

const savedItems = ref<Item[]>([])
const showingSaved = ref(false)
const windowWidth = ref(window.innerWidth)
const sidebarOpen = ref(false)
const showThemeMenu = ref(false)
const contentOnly = ref(false)
const digestFilter = ref<'all' | 'unread' | 'read'>('all')

const isDesktop = computed(() => windowWidth.value >= 900)

const feedMode = computed(() => feedStore.activeFeedId != null || showingSaved.value)

const accentColors: Record<string, string> = {
  blue: '217 91% 60%', green: '142 71% 45%', purple: '271 81% 56%',
  orange: '24 95% 53%', rose: '346 77% 50%', teal: '173 80% 36%', amber: '38 92% 50%',
}

const pendingItemId = ref<number | null>(null)
const mobileScrollEl = ref<HTMLElement | null>(null)

function onResize() { windowWidth.value = window.innerWidth }

function onKeyDown(e: KeyboardEvent) {
  const tag = (e.target as HTMLElement)?.tagName
  if (tag === 'INPUT' || tag === 'TEXTAREA' || tag === 'SELECT') return
  const item = feedStore.selectedItem
  switch (e.key) {
    case 'ArrowLeft':
    case 'ArrowRight': {
      const items = showingSaved.value ? savedItems.value : feedStore.items
      if (items.length === 0) return
      const idx = item ? items.findIndex(i => i.id === item.id) : -1
      const next = e.key === 'ArrowRight' ? idx + 1 : idx - 1
      if (next >= 0 && next < items.length) selectItem(items[next])
      break
    }
    case 'ArrowUp':
    case 'ArrowDown': {
      const el = document.querySelector<HTMLElement>('[data-item-content]')
      if (el) el.scrollBy(0, e.key === 'ArrowUp' ? -80 : 80)
      break
    }
    case 'PageUp':
    case 'PageDown': {
      const feeds = feedStore.feeds
      if (feeds.length === 0) return
      const cur = feedStore.activeFeedId
      const idx = cur ? feeds.findIndex(f => f.id === cur) : -1
      const next = e.key === 'PageDown' ? idx + 1 : idx - 1
      if (next >= 0 && next < feeds.length) selectFeed(feeds[next].id)
      break
    }
    case ' ':
      e.preventDefault()
      if (item) feedStore.updateItemStatus(item, item.status === 'read' ? 'unread' : 'read')
      break
    case 's': case 'S':
      if (item) {
        if (item.status === 'saved') unsaveItem(item)
        else feedStore.updateItemStatus(item, 'saved')
      }
      break
    case 'v': case 'V':
      if (item?.url) window.open(item.url, '_blank')
      break
  }
}

interface FilteredResult extends DigestResult {
  itemCount: number
}

const filteredResults = computed<FilteredResult[]>(() => {
  const r: DigestResult[] = digestStore.results
  const filtered = digestFilter.value === 'all' ? r : r.filter(res => res.status === digestFilter.value)
  return filtered.map(res => {
    let itemCount = 0
    try {
      const c = JSON.parse(res.content)
      if (c) {
        itemCount = (c.highlights?.length || 0) + (c.items?.length || 0)
      }
    } catch {}
    return { ...res, itemCount }
  })
})

function markDigestRead(result: DigestResult) {
  if (result.status !== 'read') digestStore.updateResultStatus(result.id, 'read')
}

function isSelectedResult(result: DigestResult): boolean {
  return result.id === digestStore.selectedResult?.id
}

function isSelectedItem(item: Item): boolean {
  return item.id === feedStore.selectedItem?.id
}

let pollInterval: ReturnType<typeof setInterval> | null = null

onMounted(() => {
  window.addEventListener('resize', onResize)
  window.addEventListener('keydown', onKeyDown)
  categoryStore.fetchCategories()
  feedStore.fetchFeeds()
  pollInterval = setInterval(() => {
    feedStore.fetchUnreadCounts()
    digestStore.fetchUnreadCounts()
    digestStore.silentRefresh()
  }, 10000)
})
onUnmounted(() => {
  window.removeEventListener('resize', onResize)
  window.removeEventListener('keydown', onKeyDown)
  if (pollInterval) clearInterval(pollInterval)
})

// URL → state sync
watch(
  () => ({ path: route.path, feedId: route.params.feedId, itemId: route.params.itemId }),
  async ({ path, feedId, itemId }) => {
    const fid = feedId ? Number(feedId) : null
    const iid = itemId ? Number(itemId) : null
    if (path.startsWith('/feeds/') && fid) {
      showingSaved.value = false
      digestStore.activeConfigId = null
      digestStore.selectedResult = null
      if (feedStore.activeFeedId !== fid) await feedStore.setActiveFeed(fid)
      if (iid) {
        const item = feedStore.items.find(i => i.id === iid)
        if (item && feedStore.selectedItem?.id !== iid) { feedStore.selectItem(item); pendingItemId.value = null }
        else if (!item) pendingItemId.value = iid
      } else { feedStore.selectedItem = null; pendingItemId.value = null }
    } else if (path.startsWith('/digests/')) {
      showingSaved.value = false
      feedStore.activeFeedId = null
      feedStore.selectedItem = null
      pendingItemId.value = null
      const cid = route.params.configId ? Number(route.params.configId) : null
      const rid = route.params.resultId ? Number(route.params.resultId) : null
      if (cid) {
        if (digestStore.activeConfigId !== cid) await digestStore.setActiveConfig(cid)
        if (rid) {
          const result = digestStore.results.find(r => r.id === rid)
          if (result) await digestStore.selectResult(result)
        } else { digestStore.selectedResult = null; digestStore.resultItems = [] }
      }
    } else if (path === '/saved') {
      showingSaved.value = true; sidebarOpen.value = false
      feedStore.activeFeedId = null; feedStore.selectedItem = null; pendingItemId.value = null
      digestStore.activeConfigId = null; digestStore.selectedResult = null
      if (savedItems.value.length === 0) {
        for (const feed of feedStore.feeds) {
          try { savedItems.value.push(...(await feedApi.getItems(feed.id, { status: 'saved' })).items) } catch { /* skip */ }
        }
      }
    } else if (path === '/') {
      showingSaved.value = false; feedStore.activeFeedId = null; feedStore.selectedItem = null; pendingItemId.value = null
    }
  },
  { immediate: true }
)

watch(
  () => feedStore.items,
  (items) => {
    if (pendingItemId.value != null) {
      const item = items.find(i => i.id === pendingItemId.value)
      if (item) { feedStore.selectItem(item); pendingItemId.value = null }
    }
  }
)

function selectFeed(id: number) {
  showingSaved.value = false; sidebarOpen.value = false
  router.push({ path: `/feeds/${id}` })
}

function selectDigestConfig(id: number) {
  showingSaved.value = false; sidebarOpen.value = false
  router.push({ path: `/digests/${id}` })
}

async function showSaved() { router.push({ path: '/saved' }) }

function goBackToList() {
  feedStore.selectedItem = null; pendingItemId.value = null
  if (feedStore.activeFeedId) router.push({ path: `/feeds/${feedStore.activeFeedId}` })
  if (digestStore.activeConfigId) router.push({ path: `/digests/${digestStore.activeConfigId}` })
}

async function selectItem(item: Item) {
  feedStore.selectItem(item)
  if (feedStore.activeFeedId) router.push({ path: `/feeds/${feedStore.activeFeedId}/${item.id}` })
  if (item.status === 'unread') {
    await feedStore.updateItemStatus(item, 'read')
    const idx = savedItems.value.findIndex(i => i.id === item.id)
    if (idx !== -1) savedItems.value[idx] = { ...savedItems.value[idx], status: 'read' as const }
  }
}

async function unsaveItem(item: Item) {
  await feedStore.updateItemStatus(item, 'read')
  savedItems.value = savedItems.value.filter(i => i.id !== item.id)
}

function onMobileScroll() {
  const el = mobileScrollEl.value
  if (!el || !feedStore.hasMore || feedStore.loadingMore || feedStore.loading) return
  if (el.scrollTop + el.clientHeight >= el.scrollHeight - 200) {
    feedStore.fetchMoreItems()
  }
}

function onDigestScroll(e: Event) {
  const el = e.target as HTMLElement
  if (!el || !digestStore.hasMore || digestStore.loadingMore || digestStore.loading) return
  if (el.scrollTop + el.clientHeight >= el.scrollHeight - 200) {
    digestStore.fetchMoreResults()
  }
}

function parseItemDate(val: any): Date | null {
  if (!val) return null
  if (typeof val === 'object') { if (!val.Valid) return null; val = val.Time }
  if (typeof val !== 'string') return null
  const d = new Date(val); return isNaN(d.getTime()) ? null : d
}

function relativeTime(val: any): string {
  const d = parseItemDate(val)
  if (!d) return ''
  const diff = Date.now() - d.getTime(); const sec = Math.floor(diff / 1000)
  if (sec < 60) return 'now'
  const min = Math.floor(sec / 60); if (min < 60) return `${min}m`
  const h = Math.floor(min / 60); if (h < 24) return `${h}h`
  const d2 = Math.floor(h / 24); if (d2 < 30) return `${d2}d`
  const m = Math.floor(d2 / 30); if (m < 12) return `${m}mo`
  return `${Math.floor(m / 12)}y`
}

function absoluteTime(val: any): string {
  const d = parseItemDate(val)
  if (!d) return ''
  return d.toLocaleString(undefined, { year: 'numeric', month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' })
}

const displayItems = computed<Item[]>(() => showingSaved.value ? savedItems.value : feedStore.items)
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
</script>

<template>
  <div class="flex h-screen flex-col">
    <!-- Header -->
    <header class="flex h-12 items-center gap-2 border-b bg-background px-4 shrink-0">
      <Button v-if="!isDesktop || contentOnly" variant="ghost" size="icon" @click="sidebarOpen = !sidebarOpen">
        <Menu class="size-4" />
      </Button>
      <template v-if="(feedStore.selectedItem || digestStore.selectedResult) && (!isDesktop || contentOnly)">
        <Button variant="ghost" size="sm" @click="goBackToList">
          <ArrowLeft class="size-4" /><span class="text-xs">Back</span>
        </Button>
      </template>
      <template v-else>
        <Rss class="size-5 text-accent shrink-0" />
        <span class="font-semibold shrink-0 hidden sm:inline">Digesterr</span>
      </template>
      <div class="ml-auto flex items-center gap-1 relative">
        <Button variant="ghost" size="icon" @click="showThemeMenu = !showThemeMenu">
          <Sun v-if="theme.mode !== 'dark'" class="size-4" />
          <Moon v-else class="size-4" />
        </Button>
        <div v-if="showThemeMenu" class="absolute right-0 top-full mt-1 z-50 min-w-40 rounded-md border bg-background p-2 shadow-md">
          <div class="flex items-center gap-2 px-1 py-1">
            <span class="text-xs text-muted-foreground">Mode</span>
            <div class="flex rounded-md border border-border overflow-hidden text-xs">
              <button class="px-2 py-1 transition-colors" :class="theme.mode === 'light' ? 'bg-accent text-accent-foreground' : 'hover:bg-muted'" @click="theme.setMode('light'); showThemeMenu = false"><Sun class="size-3 inline" /> Light</button>
              <button class="px-2 py-1 border-x border-border transition-colors" :class="theme.mode === 'dark' ? 'bg-accent text-accent-foreground' : 'hover:bg-muted'" @click="theme.setMode('dark'); showThemeMenu = false"><Moon class="size-3 inline" /> Dark</button>
              <button class="px-2 py-1 transition-colors" :class="theme.mode === 'system' ? 'bg-accent text-accent-foreground' : 'hover:bg-muted'" @click="theme.setMode('system'); showThemeMenu = false">Auto</button>
            </div>
          </div>
          <div class="flex items-center gap-2 px-1 py-1">
            <span class="text-xs text-muted-foreground">Accent</span>
            <div class="flex gap-1.5">
              <button v-for="a in ['blue','green','purple','orange','rose','teal','amber']" :key="a"
                class="size-5 rounded-full border transition-all"
                :class="theme.accent === a ? 'border-foreground scale-110' : 'border-transparent'"
                :style="{ backgroundColor: `hsl(${accentColors[a]})` }"
                @click="theme.setAccent(a); showThemeMenu = false" />
            </div>
          </div>
        </div>
        <Button variant="ghost" size="sm" class="hidden sm:flex" @click="auth.signout()">
          <LogOut class="size-4" /> {{ auth.user?.email }}
        </Button>
      </div>
    </header>

    <!-- Main layout -->
    <template v-if="isDesktop">
      <template v-if="contentOnly">
        <!-- Content-only: overlay sidebar + full-width content -->
        <div v-if="sidebarOpen" class="fixed inset-0 z-30" @click="sidebarOpen = false"><div class="absolute inset-0 bg-black/50" /></div>
        <aside :class="['fixed left-0 top-12 z-40 h-[calc(100vh-3rem)] w-64 bg-sidebar border-r shadow-xl transition-transform duration-200 overflow-y-auto p-3', sidebarOpen ? 'translate-x-0' : '-translate-x-full']">
          <div class="flex items-center justify-between mb-2">
            <span class="text-xs font-semibold text-muted-foreground uppercase tracking-wider">Feeds</span>
            <Button variant="ghost" size="icon" @click="contentOnly = false" title="Expand sidebar">
              <PanelLeftOpen class="size-4" />
            </Button>
          </div>
          <FeedSidebar :showing-saving="showingSaved" @select-feed="selectFeed" @select-digest-config="selectDigestConfig" @show-saved="showSaved" />
        </aside>
        <!-- Digest content -->
        <DigestContent v-if="digestStore.selectedResult && !feedMode" class="flex-1 overflow-y-auto" />
        <!-- Digest results list -->
         <div v-else-if="digestStore.activeConfigId && !feedMode" class="flex-1 overflow-y-auto p-3 border-r" @scroll="onDigestScroll">
          <div class="mb-2">
            <FilterBar
              :options="[
                { value: 'all', label: 'All' },
                { value: 'unread', label: 'Unread' },
                { value: 'read', label: 'Read' },
              ]"
              v-model="digestFilter"
            />
          </div>
          <div v-if="digestStore.loading" class="text-sm text-muted-foreground p-2">Loading...</div>
          <div v-else-if="filteredResults.length === 0" class="text-xs text-muted-foreground p-2">No results.</div>
          <div v-else class="space-y-1">
            <button v-for="result in filteredResults" :key="result.id"
              class="w-full text-left h-20 p-2 border-b hover:bg-accent/10 transition-colors relative overflow-hidden"
              :class="{ 'bg-accent/10 font-medium text-accent': isSelectedResult(result), 'text-foreground': result.status === 'unread', 'text-muted-foreground': result.status === 'read' }"
              @click="markDigestRead(result); digestStore.selectResult(result); router.push(`/digests/${digestStore.activeConfigId}/${result.id}`)"
            >
              <div class="flex items-start gap-1 min-w-0">
                <CircleDot v-if="result.status === 'unread'" class="size-3 shrink-0 mt-0.5 text-accent" />
                <Circle v-else class="size-3 shrink-0 mt-0.5 text-muted-foreground/40" />
                <p class="text-sm font-semibold truncate flex-1 min-w-0">{{ new Date(result.created_at).toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit' }) }}</p>
              </div>
              <p class="text-xs text-muted-foreground/70 mt-0.5 min-w-0">{{ new Date(result.created_at).toLocaleDateString(undefined, { month: 'short', day: 'numeric', year: 'numeric' }) }}</p>
              <span class="absolute right-2 bottom-2 text-[10px] text-accent font-medium">{{ result.itemCount }} items</span>
            </button>
          </div>
        </div>
        <!-- Feed: item content full-width -->
        <ItemContent v-else-if="feedStore.selectedItem && feedMode" :absolute-time="absoluteTime" :unsave-item="unsaveItem" class="flex-1 overflow-y-auto" />
        <!-- Feed: item list full-width -->
        <ItemList v-else-if="feedMode" :saved-items="savedItems" :showing-saved="showingSaved" :relative-time="relativeTime" :absolute-time="absoluteTime" @select-item="selectItem" class="flex-1 overflow-y-auto" />
        <!-- Nothing selected -->
        <div v-else class="flex-1 flex items-center justify-center text-sm text-muted-foreground">Select an item to view</div>
      </template>
      <template v-else>
        <!-- 3-pane layout -->
        <SplitterGroup direction="horizontal" auto-save-id="digesterr" class="flex-1">
          <SplitterPanel :default-size="18" :min-size="14" :max-size="28">
            <aside class="h-full border-r bg-sidebar overflow-y-auto p-3">
              <div class="flex items-center justify-between mb-2">
                <span class="text-xs font-semibold text-muted-foreground uppercase tracking-wider">Feeds</span>
                <Button variant="ghost" size="icon" @click="contentOnly = true" title="Collapse sidebar">
                  <PanelLeftClose class="size-4" />
                </Button>
              </div>
              <FeedSidebar :showing-saving="showingSaved" @select-feed="selectFeed" @select-digest-config="selectDigestConfig" @show-saved="showSaved" />
            </aside>
          </SplitterPanel>
          <SplitterResizeHandle class="w-1 hover:w-1.5 bg-border hover:bg-accent transition-all cursor-col-resize" />

          <!-- Middle pane: feed items or digest results -->
          <SplitterPanel :default-size="30" :min-size="20" :max-size="50">
            <!-- Digest results list -->
            <div v-if="digestStore.activeConfigId && !feedMode" class="h-full overflow-y-auto p-3 border-r" @scroll="onDigestScroll">
              <div class="mb-2">
                <FilterBar
                  :options="[
                    { value: 'all', label: 'All' },
                    { value: 'unread', label: 'Unread' },
                    { value: 'read', label: 'Read' },
                  ]"
                  v-model="digestFilter"
                />
              </div>
              <div v-if="digestStore.loading" class="text-sm text-muted-foreground p-2">Loading...</div>
              <div v-else-if="filteredResults.length === 0" class="text-xs text-muted-foreground p-2">No results.</div>
              <div v-else class="space-y-1">
                <button v-for="result in filteredResults" :key="result.id"
            class="w-full text-left h-20 p-2 border-b hover:bg-accent/10 transition-colors relative overflow-hidden appearance-none"
                  :class="{ 'bg-accent/10 font-medium text-accent': isSelectedResult(result), 'text-foreground': result.status === 'unread', 'text-muted-foreground': result.status === 'read' }"
                  @click="markDigestRead(result); digestStore.selectResult(result); router.push(`/digests/${digestStore.activeConfigId}/${result.id}`)"
                >
                  <div class="flex items-start gap-1 min-w-0">
                    <CircleDot v-if="result.status === 'unread'" class="size-3 shrink-0 mt-0.5 text-accent" />
                    <Circle v-else class="size-3 shrink-0 mt-0.5 text-muted-foreground/40" />
                    <p class="text-sm font-semibold truncate flex-1 min-w-0">{{ new Date(result.created_at).toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit' }) }}</p>
                  </div>
                  <p class="text-xs text-muted-foreground/70 mt-0.5 min-w-0">{{ new Date(result.created_at).toLocaleDateString(undefined, { month: 'short', day: 'numeric', year: 'numeric' }) }}</p>
                  <span class="absolute right-2 bottom-2 text-[10px] text-accent font-medium">{{ result.itemCount }} items</span>
                </button>
              </div>
            </div>
            <!-- Feed items list -->
            <ItemList v-else :saved-items="savedItems" :showing-saved="showingSaved" :relative-time="relativeTime" :absolute-time="absoluteTime" @select-item="selectItem" />
          </SplitterPanel>
          <SplitterResizeHandle class="w-1 hover:w-1.5 bg-border hover:bg-accent transition-all cursor-col-resize" />

          <!-- Right pane: item content or digest content -->
          <SplitterPanel :default-size="52" :min-size="30">
            <DigestContent v-if="digestStore.selectedResult && !feedMode" />
            <ItemContent v-else :absolute-time="absoluteTime" :unsave-item="unsaveItem" />
          </SplitterPanel>
        </SplitterGroup>
      </template>
    </template>

    <!-- Tablet -->
    <template v-else-if="windowWidth >= 640">
      <div v-if="sidebarOpen" class="fixed inset-0 z-30" @click="sidebarOpen = false"><div class="absolute inset-0 bg-black/50" /></div>
      <aside :class="['fixed left-0 top-12 z-40 h-[calc(100vh-3rem)] w-64 bg-sidebar border-r shadow-xl transition-transform duration-200 overflow-y-auto p-3', sidebarOpen ? 'translate-x-0' : '-translate-x-full']">
        <FeedSidebar :showing-saving="showingSaved" @select-feed="selectFeed" @select-digest-config="selectDigestConfig" @show-saved="showSaved" />
      </aside>
      <template v-if="digestStore.activeConfigId && !feedMode">
        <DigestContent v-if="digestStore.selectedResult" class="flex-1 overflow-y-auto" />
        <div v-else class="flex-1 overflow-y-auto p-3" @scroll="onDigestScroll">
          <div class="mb-2">
            <FilterBar
              :options="[
                { value: 'all', label: 'All' },
                { value: 'unread', label: 'Unread' },
                { value: 'read', label: 'Read' },
              ]"
              v-model="digestFilter"
            />
          </div>
          <div v-if="digestStore.loading" class="text-sm text-muted-foreground p-2">Loading...</div>
          <div v-else-if="filteredResults.length === 0" class="text-xs text-muted-foreground p-2">No results.</div>
          <div v-else class="space-y-1">
            <button v-for="result in filteredResults" :key="result.id"
              class="w-full text-left h-20 p-2 border-b hover:bg-accent/10 transition-colors relative overflow-hidden"
              :class="{ 'bg-accent/10 font-medium text-accent': isSelectedResult(result), 'text-foreground': result.status === 'unread', 'text-muted-foreground': result.status === 'read' }"
              @click="markDigestRead(result); digestStore.selectResult(result); router.push(`/digests/${digestStore.activeConfigId}/${result.id}`)"
            >
              <div class="flex items-start gap-1 min-w-0">
                <CircleDot v-if="result.status === 'unread'" class="size-3 shrink-0 mt-0.5 text-accent" />
                <Circle v-else class="size-3 shrink-0 mt-0.5 text-muted-foreground/40" />
                <p class="text-sm font-semibold truncate flex-1 min-w-0">{{ new Date(result.created_at).toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit' }) }}</p>
              </div>
              <p class="text-xs text-muted-foreground/70 mt-0.5 min-w-0">{{ new Date(result.created_at).toLocaleDateString(undefined, { month: 'short', day: 'numeric', year: 'numeric' }) }}</p>
              <span class="absolute right-2 bottom-2 text-[10px] text-accent font-medium">{{ result.itemCount }} items</span>
            </button>
          </div>
        </div>
      </template>
      <template v-else>
        <ItemList :saved-items="savedItems" :showing-saved="showingSaved" :relative-time="relativeTime" :absolute-time="absoluteTime" @select-item="selectItem" />
        <ItemContent :absolute-time="absoluteTime" :unsave-item="unsaveItem" />
      </template>
    </template>

    <!-- Mobile -->
    <template v-else>
      <div v-if="sidebarOpen" class="fixed inset-0 z-30" @click="sidebarOpen = false"><div class="absolute inset-0 bg-black/50" /></div>
      <aside :class="['fixed left-0 top-12 z-40 h-[calc(100vh-3rem)] w-64 bg-sidebar border-r shadow-xl transition-transform duration-200 overflow-y-auto p-3', sidebarOpen ? 'translate-x-0' : '-translate-x-full']">
        <FeedSidebar :showing-saving="showingSaved" @select-feed="selectFeed" @select-digest-config="selectDigestConfig" @show-saved="showSaved" />
      </aside>
      <div v-if="digestStore.activeConfigId && !feedMode && digestStore.selectedResult" class="flex-1 overflow-y-auto"><DigestContent /></div>
      <div v-else-if="digestStore.activeConfigId && !feedMode" class="flex-1 overflow-y-auto p-3" @scroll="onDigestScroll">
        <div class="mb-2">
          <FilterBar
            :options="[
              { value: 'all', label: 'All' },
              { value: 'unread', label: 'Unread' },
              { value: 'read', label: 'Read' },
            ]"
            v-model="digestFilter"
          />
        </div>
        <div v-if="digestStore.loading" class="text-sm text-muted-foreground p-2">Loading...</div>
        <div v-else-if="filteredResults.length === 0" class="text-xs text-muted-foreground p-2">No results.</div>
        <div v-else class="space-y-1">
          <button v-for="result in filteredResults" :key="result.id"
            class="w-full text-left h-20 p-2 border-b hover:bg-accent/10 transition-colors relative overflow-hidden"
            :class="{ 'bg-accent/10 font-medium text-accent': isSelectedResult(result), 'text-foreground': result.status === 'unread', 'text-muted-foreground': result.status === 'read' }"
            @click="markDigestRead(result); digestStore.selectResult(result); router.push(`/digests/${digestStore.activeConfigId}/${result.id}`)"
          >
            <div class="flex items-start gap-1 min-w-0">
              <CircleDot v-if="result.status === 'unread'" class="size-3 shrink-0 mt-0.5 text-accent" />
              <Circle v-else class="size-3 shrink-0 mt-0.5 text-muted-foreground/40" />
              <p class="text-sm font-semibold truncate flex-1 min-w-0">{{ new Date(result.created_at).toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit' }) }}</p>
            </div>
            <p class="text-xs text-muted-foreground/70 mt-0.5 min-w-0">{{ new Date(result.created_at).toLocaleDateString(undefined, { month: 'short', day: 'numeric', year: 'numeric' }) }}</p>
            <span class="absolute right-2 bottom-2 text-[10px] text-accent font-medium">{{ result.itemCount }} items</span>
          </button>
        </div>
      </div>
      <div v-else-if="!feedStore.selectedItem" class="flex-1 min-h-0 overflow-y-auto"
        ref="mobileScrollEl" @scroll="onMobileScroll">
        <Skeleton v-if="feedStore.loading" class="m-3 h-12" />
        <div v-else-if="!feedStore.activeFeedId && !showingSaved" class="p-6 text-center text-sm text-muted-foreground">Select a feed</div>
        <div v-else-if="showingSaved && savedItems.length === 0" class="p-6 text-center text-sm text-muted-foreground">No saved items</div>
        <template v-else>
          <div v-for="item in displayItems" :key="item.id"
            role="button" tabindex="0"
            class="w-full text-left h-20 p-2 border-b hover:bg-accent/10 transition-colors relative overflow-hidden cursor-pointer"
            :class="{ 'bg-accent/10 font-medium text-accent': isSelectedItem(item), 'text-foreground': item.status === 'unread', 'text-foreground/70': item.status === 'digested', 'text-muted-foreground': item.status === 'read', 'text-accent': item.status === 'saved' }"
            @click="selectItem(item)"
            @keydown.enter.prevent="selectItem(item)"
            @keydown.space.prevent="selectItem(item)">
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
        </template>
      </div>
      <div v-else class="flex-1 overflow-y-auto p-4">
        <ItemContent :absolute-time="absoluteTime" :unsave-item="unsaveItem" />
      </div>
    </template>
  </div>
</template>




