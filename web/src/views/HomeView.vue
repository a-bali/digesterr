<script setup lang="ts">
import { ref, onMounted, onUnmounted, computed } from 'vue'
import { useRouter } from 'vue-router'
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
const auth = useAuthStore()
const feedStore = useFeedStore()
const categoryStore = useCategoryStore()
const theme = useThemeStore()

const viewMode = ref<'feed' | 'digest'>('feed')
const savedItems = ref<Item[]>([])
const showingSaved = ref(false)
const windowWidth = ref(window.innerWidth)
const sidebarOpen = ref(false)

const isDesktop = computed(() => windowWidth.value >= 900)

function onResize() { windowWidth.value = window.innerWidth }

onMounted(() => {
  window.addEventListener('resize', onResize)
  categoryStore.fetchCategories()
  feedStore.fetchFeeds()
})
onUnmounted(() => window.removeEventListener('resize', onResize))

function selectFeed(id: number) {
  showingSaved.value = false
  sidebarOpen.value = false
  feedStore.setActiveFeed(id)
}

async function showSaved() {
  showingSaved.value = true
  sidebarOpen.value = false
  feedStore.activeFeedId = null
  feedStore.selectedItem = null
  savedItems.value = []
  for (const feed of feedStore.feeds) {
    try { savedItems.value.push(...await feedApi.getItems(feed.id, 'saved')) }
    catch { /* skip */ }
  }
}

function goBackToList() {
  feedStore.selectedItem = null
}

async function selectItem(item: Item) {
  feedStore.selectItem(item)
  if (item.status === 'unread') {
    await feedStore.updateItemStatus(item, 'read')
    const idx = savedItems.value.findIndex(i => i.id === item.id)
    if (idx !== -1) savedItems.value[idx] = { ...savedItems.value[idx], status: 'read' as const }
  }
}

async function unsaveItem(item: Item) {
  await feedStore.updateItemStatus(item, 'read')
  savedItems.value = savedItems.value.filter(i => i.id !== item.id)
  if (feedStore.selectedItem?.id === item.id) feedStore.selectedItem = null
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
  if (sec < 60) return 'just now'
  const min = Math.floor(sec / 60)
  if (min < 60) return `${min}m ago`
  const h = Math.floor(min / 60)
  if (h < 24) return `${h}h ago`
  const d2 = Math.floor(h / 24)
  if (d2 < 30) return `${d2}d ago`
  const m = Math.floor(d2 / 30)
  if (m < 12) return `${m}mo ago`
  return `${Math.floor(m / 12)}y ago`
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
      <div class="ml-auto flex items-center gap-1">
        <Button variant="ghost" size="icon" @click="theme.setMode(theme.mode === 'dark' ? 'light' : 'dark')">
          <Sun v-if="theme.mode !== 'dark'" class="size-4" />
          <Moon v-else class="size-4" />
        </Button>
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
