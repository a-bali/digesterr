<script setup lang="ts">
import { ref, watch, onMounted, onUnmounted, computed } from 'vue'
import { useRouter, useRoute } from 'vue-router'
import { SplitterGroup, SplitterPanel, SplitterResizeHandle } from 'reka-ui'
import Button from '../components/ui/Button'
import FeedSidebar from '../components/FeedSidebar.vue'
import ItemList from '../components/ItemList.vue'
import ItemContent from '../components/ItemContent.vue'
import { useAuthStore } from '../stores/auth'
import { useFeedStore } from '../stores/feed'
import { useCategoryStore } from '../stores/category'
import { useThemeStore } from '../stores/theme'
import { feedApi } from '../services/api'
import type { Item } from '../types/api'
import {
  Rss, Settings, LogOut, BookOpen, Sun, Moon, MessageSquare,
  Menu, ArrowLeft,
} from 'lucide-vue-next'

const router = useRouter()
const route = useRoute()
const auth = useAuthStore()
const feedStore = useFeedStore()
const categoryStore = useCategoryStore()
const theme = useThemeStore()

const viewMode = ref<'feed' | 'digest'>('feed')
const savedItems = ref<Item[]>([])
const showingSaved = ref(false)
const windowWidth = ref(window.innerWidth)
const sidebarOpen = ref(false)
const showThemeMenu = ref(false)

const isDesktop = computed(() => windowWidth.value >= 900)

const accentColors: Record<string, string> = {
  blue: '217 91% 60%',
  green: '142 71% 45%',
  purple: '271 81% 56%',
  orange: '24 95% 53%',
  rose: '346 77% 50%',
  teal: '173 80% 36%',
  amber: '38 92% 50%',
}

const pendingItemId = ref<number | null>(null)

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
      if (next >= 0 && next < items.length) {
        selectItem(items[next])
      }
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
      if (next >= 0 && next < feeds.length) {
        selectFeed(feeds[next].id)
      }
      break
    }
    case ' ':
      e.preventDefault()
      if (item) {
        feedStore.updateItemStatus(item, item.status === 'read' ? 'unread' : 'read')
      }
      break
    case 's':
    case 'S':
      if (item) {
        if (item.status === 'saved') {
          unsaveItem(item)
        } else {
          feedStore.updateItemStatus(item, 'saved')
        }
      }
      break
    case 'v':
    case 'V':
      if (item?.url) window.open(item.url, '_blank')
      break
  }
}

onMounted(() => {
  window.addEventListener('resize', onResize)
  window.addEventListener('keydown', onKeyDown)
  categoryStore.fetchCategories()
  feedStore.fetchFeeds()
})
onUnmounted(() => {
  window.removeEventListener('resize', onResize)
  window.removeEventListener('keydown', onKeyDown)
})

// URL → state sync
watch(
  () => ({ path: route.path, feedId: route.params.feedId, itemId: route.params.itemId }),
  async ({ path, feedId, itemId }) => {
    const fid = feedId ? Number(feedId) : null
    const iid = itemId ? Number(itemId) : null
    if (path.startsWith('/feeds/') && fid) {
      showingSaved.value = false
      if (feedStore.activeFeedId !== fid) {
        await feedStore.setActiveFeed(fid)
      }
      if (iid) {
        const item = feedStore.items.find(i => i.id === iid)
        if (item && feedStore.selectedItem?.id !== iid) {
          feedStore.selectItem(item)
          pendingItemId.value = null
        } else if (!item) {
          pendingItemId.value = iid
        }
      } else {
        feedStore.selectedItem = null
        pendingItemId.value = null
      }
    } else if (path === '/saved') {
      showingSaved.value = true
      sidebarOpen.value = false
      feedStore.activeFeedId = null
      feedStore.selectedItem = null
      pendingItemId.value = null
      if (savedItems.value.length === 0) {
        for (const feed of feedStore.feeds) {
          try { savedItems.value.push(...(await feedApi.getItems(feed.id, { status: 'saved' })).items) }
          catch { /* skip */ }
        }
      }
    } else if (path === '/') {
      showingSaved.value = false
      feedStore.activeFeedId = null
      feedStore.selectedItem = null
      pendingItemId.value = null
    }
  },
  { immediate: true }
)

// Select pending item after items load (deferred deep-link selection)
watch(
  () => feedStore.items,
  (items) => {
    if (pendingItemId.value != null) {
      const item = items.find(i => i.id === pendingItemId.value)
      if (item) {
        feedStore.selectItem(item)
        pendingItemId.value = null
      }
    }
  }
)

function selectFeed(id: number) {
  showingSaved.value = false
  sidebarOpen.value = false
  router.push({ path: `/feeds/${id}` })
}

async function showSaved() {
  router.push({ path: '/saved' })
}

function goBackToList() {
  feedStore.selectedItem = null
  pendingItemId.value = null
  if (feedStore.activeFeedId) {
    router.push({ path: `/feeds/${feedStore.activeFeedId}` })
  }
}

async function selectItem(item: Item) {
  feedStore.selectItem(item)
  if (feedStore.activeFeedId) {
    router.push({ path: `/feeds/${feedStore.activeFeedId}/${item.id}` })
  }
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

function parseItemDate(val: any): Date | null {
  if (!val) return null
  if (typeof val === 'object') {
    if (!val.Valid) return null
    val = val.Time
  }
  if (typeof val !== 'string') return null
  const d = new Date(val)
  return isNaN(d.getTime()) ? null : d
}

function relativeTime(val: any): string {
  const d = parseItemDate(val)
  if (!d) return ''
  const diff = Date.now() - d.getTime()
  const sec = Math.floor(diff / 1000)
  if (sec < 60) return 'now'
  const min = Math.floor(sec / 60)
  if (min < 60) return `${min}m`
  const h = Math.floor(min / 60)
  if (h < 24) return `${h}h`
  const d2 = Math.floor(h / 24)
  if (d2 < 30) return `${d2}d`
  const m = Math.floor(d2 / 30)
  if (m < 12) return `${m}mo`
  return `${Math.floor(m / 12)}y`
}

function absoluteTime(val: any): string {
  const d = parseItemDate(val)
  if (!d) return ''
  return d.toLocaleString(undefined, {
    year: 'numeric', month: 'short', day: 'numeric',
    hour: '2-digit', minute: '2-digit',
  })
}
</script>

<template>
  <div class="flex h-screen flex-col">
    <!-- Header -->
    <header class="flex h-12 items-center gap-2 border-b bg-background px-4 shrink-0">
      <Button v-if="!isDesktop" variant="ghost" size="icon" @click="sidebarOpen = !sidebarOpen">
        <Menu class="size-4" />
      </Button>
      <template v-if="!isDesktop && feedStore.selectedItem">
        <Button variant="ghost" size="sm" @click="goBackToList">
          <ArrowLeft class="size-4" /><span class="text-xs">Back</span>
        </Button>
      </template>
      <template v-else>
        <Rss class="size-5 text-accent shrink-0" />
        <span class="font-semibold shrink-0 hidden sm:inline">Digesterr</span>
      </template>
      <div class="ml-4 flex gap-1">
        <Button :variant="viewMode === 'feed' ? 'default' : 'ghost'" size="sm" @click="viewMode = 'feed'">
          <BookOpen class="size-4" /> <span class="hidden sm:inline">Feeds</span>
        </Button>
        <Button :variant="viewMode === 'digest' ? 'default' : 'ghost'" size="sm" @click="viewMode = 'digest'">
          <MessageSquare class="size-4" /> <span class="hidden sm:inline">Digest</span>
        </Button>
      </div>
      <div class="ml-auto flex items-center gap-1 relative">
        <Button variant="ghost" size="icon" @click="showThemeMenu = !showThemeMenu">
          <Sun v-if="theme.mode !== 'dark'" class="size-4" />
          <Moon v-else class="size-4" />
        </Button>
        <div v-if="showThemeMenu" class="absolute right-0 top-full mt-1 z-50 min-w-40 rounded-md border bg-background p-2 shadow-md">
          <div class="flex items-center gap-2 px-1 py-1">
            <span class="text-xs text-muted-foreground">Mode</span>
            <div class="flex rounded-md border border-border overflow-hidden text-xs">
              <button class="px-2 py-1 transition-colors" :class="theme.mode === 'light' ? 'bg-accent text-accent-foreground' : 'hover:bg-muted'" @click="theme.setMode('light'); showThemeMenu = false">
                <Sun class="size-3 inline" /> Light
              </button>
              <button class="px-2 py-1 border-x border-border transition-colors" :class="theme.mode === 'dark' ? 'bg-accent text-accent-foreground' : 'hover:bg-muted'" @click="theme.setMode('dark'); showThemeMenu = false">
                <Moon class="size-3 inline" /> Dark
              </button>
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
        <Button variant="ghost" size="icon" @click="router.push('/settings')">
          <Settings class="size-4" />
        </Button>
        <Button variant="ghost" size="sm" class="hidden sm:flex" @click="auth.signout()">
          <LogOut class="size-4" /> {{ auth.user?.email }}
        </Button>
      </div>
    </header>

    <div v-if="viewMode === 'digest'" class="flex flex-1 items-center justify-center bg-muted/30">
      <div class="max-w-md text-center p-8">
        <MessageSquare class="size-12 mx-auto mb-4 text-muted-foreground" />
        <h2 class="text-xl font-semibold mb-2">Digests</h2>
        <p class="text-muted-foreground">AI-generated summaries — coming in a future release.</p>
      </div>
    </div>

    <div v-else class="flex flex-1 overflow-hidden">
      <!-- Desktop: 3 resizable panes -->
      <template v-if="isDesktop">
        <SplitterGroup direction="horizontal" auto-save-id="digesterr" class="flex-1">
          <SplitterPanel :default-size="18" :min-size="14" :max-size="28">
            <aside class="h-full border-r bg-sidebar overflow-y-auto p-3">
              <FeedSidebar :showing-saving="showingSaved" @select-feed="selectFeed" @show-saved="showSaved" />
            </aside>
          </SplitterPanel>
          <SplitterResizeHandle class="w-1 hover:w-1.5 bg-border hover:bg-accent transition-all cursor-col-resize" />
          <SplitterPanel :default-size="30" :min-size="20" :max-size="50">
            <ItemList :saved-items="savedItems" :showing-saved="showingSaved" :relative-time="relativeTime" :absolute-time="absoluteTime" @select-item="selectItem" />
          </SplitterPanel>
          <SplitterResizeHandle class="w-1 hover:w-1.5 bg-border hover:bg-accent transition-all cursor-col-resize" />
          <SplitterPanel :default-size="52" :min-size="30">
            <ItemContent :absolute-time="absoluteTime" :unsave-item="unsaveItem" />
          </SplitterPanel>
        </SplitterGroup>
      </template>

      <!-- Tablet: 2 panes with hamburger drawer -->
      <template v-else-if="windowWidth >= 640">
        <div v-if="sidebarOpen" class="fixed inset-0 z-30" @click="sidebarOpen = false">
          <div class="absolute inset-0 bg-black/50" />
        </div>
        <aside :class="['fixed left-0 top-12 z-40 h-[calc(100vh-3rem)] w-64 bg-sidebar border-r shadow-xl transition-transform duration-200 overflow-y-auto p-3', sidebarOpen ? 'translate-x-0' : '-translate-x-full']">
          <FeedSidebar :showing-saving="showingSaved" @select-feed="selectFeed" @show-saved="showSaved" />
        </aside>
        <ItemList :saved-items="savedItems" :showing-saved="showingSaved" :relative-time="relativeTime" :absolute-time="absoluteTime" @select-item="selectItem" />
        <ItemContent :absolute-time="absoluteTime" :unsave-item="unsaveItem" />
      </template>

      <!-- Mobile: single pane -->
      <template v-else>
        <div v-if="sidebarOpen" class="fixed inset-0 z-30" @click="sidebarOpen = false">
          <div class="absolute inset-0 bg-black/50" />
        </div>
        <aside :class="['fixed left-0 top-12 z-40 h-[calc(100vh-3rem)] w-64 bg-sidebar border-r shadow-xl transition-transform duration-200 overflow-y-auto p-3', sidebarOpen ? 'translate-x-0' : '-translate-x-full']">
          <FeedSidebar :showing-saving="showingSaved" @select-feed="selectFeed" @show-saved="showSaved" />
        </aside>
        <div v-if="!feedStore.selectedItem" class="flex-1 overflow-y-auto">
          <ItemList :saved-items="savedItems" :showing-saved="showingSaved" :relative-time="relativeTime" :absolute-time="absoluteTime" @select-item="selectItem" />
        </div>
        <div v-else class="flex-1 overflow-y-auto p-4">
          <ItemContent :absolute-time="absoluteTime" :unsave-item="unsaveItem" />
        </div>
      </template>
    </div>
  </div>
</template>
