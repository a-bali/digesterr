<script setup lang="ts">
import { ref, onMounted, onUnmounted, computed } from 'vue'
import { useRouter } from 'vue-router'
import { SplitterGroup, SplitterPanel, SplitterResizeHandle } from 'reka-ui'
import Button from '../components/ui/Button'
import Skeleton from '../components/ui/Skeleton'
import Separator from '../components/ui/Separator'
import { useAuthStore } from '../stores/auth'
import { useFeedStore } from '../stores/feed'
import { useCategoryStore } from '../stores/category'
import { useThemeStore } from '../stores/theme'
import { feedApi } from '../services/api'
import type { Item } from '../types/api'
import {
  Rss, Settings, LogOut, BookOpen, Sun, Moon, ExternalLink, MessageSquare,
  Check, Bookmark, BookmarkCheck, Filter, Menu, X, ArrowLeft,
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
const isTablet = computed(() => windowWidth.value >= 640 && windowWidth.value < 900)
const isMobile = computed(() => windowWidth.value < 640)

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
              <button class="flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-sm hover:bg-muted transition-colors mb-3" :class="{ 'bg-muted font-medium': showingSaved }" @click="showSaved">
                <BookmarkCheck class="size-3.5 shrink-0 text-accent" /> <span class="truncate">Saved Items</span>
              </button>
              <div v-for="cat in categoryStore.categories" :key="cat.id" class="mb-3">
                <p class="text-xs font-semibold text-muted-foreground uppercase mb-1 px-2">{{ cat.name }}</p>
                <button v-for="feed in feedStore.feeds.filter(f => f.category_id === cat.id)" :key="feed.id"
                  class="flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-sm hover:bg-muted transition-colors"
                  :class="{ 'bg-accent/10 font-medium text-accent': feed.id === feedStore.activeFeedId }"
                  @click="selectFeed(feed.id)">
                  <Rss class="size-3.5 shrink-0 text-accent" /> <span class="truncate">{{ feed.name }}</span>
                </button>
              </div>
            </aside>
          </SplitterPanel>
          <SplitterResizeHandle class="w-1 hover:w-1.5 bg-border hover:bg-accent transition-all cursor-col-resize" />

          <SplitterPanel :default-size="30" :min-size="20" :max-size="50">
            <div class="h-full border-r overflow-y-auto">
              <div class="flex items-center gap-1 p-2 border-b">
                <Filter class="size-4 text-muted-foreground shrink-0" />
                <select v-model="feedStore.filter" class="text-xs bg-transparent border-0 outline-none w-full">
                  <option value="all">All</option>
                  <option value="unread">Unread</option>
                  <option value="read">Read</option>
                  <option value="digested">Digested</option>
                  <option value="saved">Saved</option>
                </select>
              </div>
              <Skeleton v-if="feedStore.loading" class="m-3 h-12" />
              <div v-else-if="!feedStore.activeFeed && !showingSaved" class="p-6 text-center text-sm text-muted-foreground">Select a feed</div>
              <div v-else-if="showingSaved && savedItems.length === 0" class="p-6 text-center text-sm text-muted-foreground">No saved items</div>
              <button v-for="item in showingSaved ? savedItems : feedStore.filteredItems" :key="item.id"
                class="w-full text-left p-3 border-b hover:bg-muted/50 transition-colors"
                :class="{ 'bg-muted': item.id === feedStore.selectedItem?.id, 'font-bold text-foreground': item.status === 'unread', 'font-bold text-muted-foreground': item.status === 'digested', 'font-normal text-muted-foreground': item.status === 'read', 'font-normal text-accent': item.status === 'saved' }"
                @click="selectItem(item)">
                <p class="text-sm line-clamp-2">{{ item.title }}</p>
                <div class="flex items-center gap-2 mt-1">
                  <span v-if="item.points > 0" class="text-xs text-muted-foreground">{{ item.points }} pts</span>
                  <span v-if="item.comments_count > 0" class="text-xs text-muted-foreground">{{ item.comments_count }} comments</span>
                  <span class="text-xs text-muted-foreground ml-auto" :title="absoluteTime(item.published_at)">{{ relativeTime(item.published_at) }}</span>
                </div>
              </button>
            </div>
          </SplitterPanel>
          <SplitterResizeHandle class="w-1 hover:w-1.5 bg-border hover:bg-accent transition-all cursor-col-resize" />

          <SplitterPanel :default-size="52" :min-size="30">
            <div class="h-full overflow-y-auto p-6">
              <div v-if="!feedStore.selectedItem" class="flex h-full items-center justify-center text-muted-foreground">
                <BookOpen class="size-8 mr-2" /> <span>Select an item to read</span>
              </div>
              <template v-else>
                <h1 class="text-xl font-semibold mb-2">{{ feedStore.selectedItem.title }}</h1>
                <div class="flex flex-wrap items-center gap-3 text-sm text-muted-foreground mb-4">
                  <span v-if="feedStore.selectedItem.author">By {{ feedStore.selectedItem.author }}</span>
                  <span class="text-xs text-muted-foreground">{{ absoluteTime(feedStore.selectedItem.published_at) }}</span>
                  <span v-if="feedStore.selectedItem.points">▴ {{ feedStore.selectedItem.points }}</span>
                  <span v-if="feedStore.selectedItem.comments_count">💬 {{ feedStore.selectedItem.comments_count }}</span>
                  <a v-if="feedStore.selectedItem.url" :href="feedStore.selectedItem.url" target="_blank" class="flex items-center gap-1 text-accent hover:underline">Open <ExternalLink class="size-3" /></a>
                  <a v-if="feedStore.selectedItem.comment_url" :href="feedStore.selectedItem.comment_url" target="_blank" class="flex items-center gap-1 text-accent hover:underline">Comments <ExternalLink class="size-3" /></a>
                </div>
                <div class="flex gap-2 mb-4 flex-wrap">
                  <Button size="sm" variant="outline" @click="feedStore.updateItemStatus(feedStore.selectedItem!, feedStore.selectedItem!.status === 'read' ? 'unread' : 'read')">
                    <Check class="size-3" /> {{ feedStore.selectedItem?.status === 'read' ? 'Mark Unread' : 'Mark Read' }}
                  </Button>
                  <Button v-if="feedStore.selectedItem!.status === 'saved'" size="sm" variant="outline" @click="unsaveItem(feedStore.selectedItem!)">
                    <BookmarkCheck class="size-3" /> Unsave
                  </Button>
                  <Button v-else size="sm" variant="outline" @click="feedStore.updateItemStatus(feedStore.selectedItem!, 'saved')">
                    <Bookmark class="size-3" /> Save
                  </Button>
                </div>
                <Separator class="mb-4" />
                <div class="prose prose-sm dark:prose-invert max-w-none" v-html="feedStore.selectedItem.content" />
              </template>
            </div>
          </SplitterPanel>
        </SplitterGroup>
      </template>

      <!-- Tablet: 2 panes with hamburger drawer -->
      <template v-else-if="isTablet">
        <!-- Slide-out sidebar drawer -->
        <div v-if="sidebarOpen" class="fixed inset-0 z-30" @click="sidebarOpen = false">
          <div class="absolute inset-0 bg-black/50" />
        </div>
        <aside :class="['fixed left-0 top-12 z-40 h-[calc(100vh-3rem)] w-64 bg-sidebar border-r shadow-xl transition-transform duration-200 overflow-y-auto p-3', sidebarOpen ? 'translate-x-0' : '-translate-x-full']">
          <button class="flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-sm hover:bg-muted transition-colors mb-3" :class="{ 'bg-muted font-medium': showingSaved }" @click="showSaved">
            <BookmarkCheck class="size-3.5 shrink-0 text-accent" /> <span class="truncate">Saved Items</span>
          </button>
          <div v-for="cat in categoryStore.categories" :key="cat.id" class="mb-3">
            <p class="text-xs font-semibold text-muted-foreground uppercase mb-1 px-2">{{ cat.name }}</p>
            <button v-for="feed in feedStore.feeds.filter(f => f.category_id === cat.id)" :key="feed.id"
              class="flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-sm hover:bg-muted transition-colors"
              :class="{ 'bg-muted font-medium': feed.id === feedStore.activeFeedId }"
              @click="selectFeed(feed.id)">
              <Rss class="size-3.5 shrink-0 text-accent" /> <span class="truncate">{{ feed.name }}</span>
            </button>
          </div>
        </aside>

        <!-- Item list -->
        <div class="flex-1 border-r overflow-y-auto">
          <div class="flex items-center gap-1 p-2 border-b">
            <Filter class="size-4 text-muted-foreground shrink-0" />
            <select v-model="feedStore.filter" class="text-xs bg-transparent border-0 outline-none w-full">
              <option value="all">All</option>
              <option value="unread">Unread</option>
              <option value="read">Read</option>
              <option value="digested">Digested</option>
              <option value="saved">Saved</option>
            </select>
          </div>
          <Skeleton v-if="feedStore.loading" class="m-3 h-12" />
          <div v-else-if="!feedStore.activeFeed && !showingSaved" class="p-6 text-center text-sm text-muted-foreground">Select a feed</div>
          <div v-else-if="showingSaved && savedItems.length === 0" class="p-6 text-center text-sm text-muted-foreground">No saved items</div>
          <button v-for="item in showingSaved ? savedItems : feedStore.filteredItems" :key="item.id"
            class="w-full text-left p-3 border-b hover:bg-muted/50 transition-colors"
            :class="{ 'bg-muted': item.id === feedStore.selectedItem?.id, 'font-bold text-foreground': item.status === 'unread', 'font-bold text-muted-foreground': item.status === 'digested', 'font-normal text-muted-foreground': item.status === 'read', 'font-normal text-accent': item.status === 'saved' }"
            @click="selectItem(item)">
            <p class="text-sm line-clamp-2">{{ item.title }}</p>
            <div class="flex items-center gap-2 mt-1">
              <span v-if="item.points > 0" class="text-xs text-muted-foreground">{{ item.points }} pts</span>
              <span v-if="item.comments_count > 0" class="text-xs text-muted-foreground">{{ item.comments_count }} comments</span>
            </div>
          </button>
        </div>

        <!-- Content -->
        <div class="flex-1 overflow-y-auto p-6">
          <div v-if="!feedStore.selectedItem" class="flex h-full items-center justify-center text-muted-foreground">
            <BookOpen class="size-8 mr-2" /> <span>Select an item</span>
          </div>
          <template v-else>
            <h1 class="text-xl font-semibold mb-2">{{ feedStore.selectedItem.title }}</h1>
            <div class="flex flex-wrap items-center gap-3 text-sm text-muted-foreground mb-4">
              <span v-if="feedStore.selectedItem.author">By {{ feedStore.selectedItem.author }}</span>
              <span v-if="feedStore.selectedItem.points">▴ {{ feedStore.selectedItem.points }}</span>
              <span v-if="feedStore.selectedItem.comments_count">💬 {{ feedStore.selectedItem.comments_count }}</span>
              <a v-if="feedStore.selectedItem.url" :href="feedStore.selectedItem.url" target="_blank" class="flex items-center gap-1 text-accent hover:underline">Open <ExternalLink class="size-3" /></a>
              <a v-if="feedStore.selectedItem.comment_url" :href="feedStore.selectedItem.comment_url" target="_blank" class="flex items-center gap-1 text-accent hover:underline">Comments <ExternalLink class="size-3" /></a>
            </div>
            <div class="flex gap-2 mb-4 flex-wrap">
              <Button size="sm" variant="outline" @click="feedStore.updateItemStatus(feedStore.selectedItem!, feedStore.selectedItem!.status === 'read' ? 'unread' : 'read')">
                <Check class="size-3" /> {{ feedStore.selectedItem?.status === 'read' ? 'Mark Unread' : 'Mark Read' }}
              </Button>
              <Button v-if="feedStore.selectedItem!.status === 'saved'" size="sm" variant="outline" @click="unsaveItem(feedStore.selectedItem!)">
                <BookmarkCheck class="size-3" /> Unsave
              </Button>
              <Button v-else size="sm" variant="outline" @click="feedStore.updateItemStatus(feedStore.selectedItem!, 'saved')">
                <Bookmark class="size-3" /> Save
              </Button>
            </div>
            <Separator class="mb-4" />
            <div class="prose prose-sm dark:prose-invert max-w-none" v-html="feedStore.selectedItem.content" />
          </template>
        </div>
      </template>

      <!-- Mobile: single pane with hamburger drawer -->
      <template v-else>
        <!-- Slide-out sidebar drawer -->
        <div v-if="sidebarOpen" class="fixed inset-0 z-30" @click="sidebarOpen = false">
          <div class="absolute inset-0 bg-black/50" />
        </div>
        <aside :class="['fixed left-0 top-12 z-40 h-[calc(100vh-3rem)] w-64 bg-sidebar border-r shadow-xl transition-transform duration-200 overflow-y-auto p-3', sidebarOpen ? 'translate-x-0' : '-translate-x-full']">
          <button class="flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-sm hover:bg-muted transition-colors mb-3" :class="{ 'bg-muted font-medium': showingSaved }" @click="showSaved">
            <BookmarkCheck class="size-3.5 shrink-0 text-accent" /> <span class="truncate">Saved Items</span>
          </button>
          <div v-for="cat in categoryStore.categories" :key="cat.id" class="mb-3">
            <p class="text-xs font-semibold text-muted-foreground uppercase mb-1 px-2">{{ cat.name }}</p>
            <button v-for="feed in feedStore.feeds.filter(f => f.category_id === cat.id)" :key="feed.id"
              class="flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-sm hover:bg-muted transition-colors"
              :class="{ 'bg-muted font-medium': feed.id === feedStore.activeFeedId }"
              @click="selectFeed(feed.id)">
              <Rss class="size-3.5 shrink-0 text-accent" /> <span class="truncate">{{ feed.name }}</span>
            </button>
          </div>
        </aside>

        <!-- Main view: item list -->
        <div v-if="!feedStore.selectedItem" class="flex-1 overflow-y-auto">
          <div class="flex items-center gap-1 p-2 border-b">
            <Filter class="size-4 text-muted-foreground shrink-0" />
            <select v-model="feedStore.filter" class="text-xs bg-transparent border-0 outline-none w-full">
              <option value="all">All</option>
              <option value="unread">Unread</option>
              <option value="read">Read</option>
              <option value="digested">Digested</option>
              <option value="saved">Saved</option>
            </select>
          </div>
          <div v-if="!feedStore.activeFeed && !showingSaved" class="p-6 text-center text-sm text-muted-foreground">Select a feed from the menu</div>
          <div v-else-if="showingSaved && savedItems.length === 0" class="p-6 text-center text-sm text-muted-foreground">No saved items</div>
          <button v-for="item in showingSaved ? savedItems : feedStore.filteredItems" :key="item.id"
            class="w-full text-left p-3 border-b hover:bg-muted/50 transition-colors"
            :class="{ 'bg-muted': item.id === feedStore.selectedItem?.id, 'font-bold text-foreground': item.status === 'unread', 'font-bold text-muted-foreground': item.status === 'digested', 'font-normal text-muted-foreground': item.status === 'read', 'font-normal text-accent': item.status === 'saved' }"
            @click="selectItem(item)">
            <p class="text-sm line-clamp-2">{{ item.title }}</p>
            <div class="flex items-center gap-2 mt-1">
              <span v-if="item.points > 0" class="text-xs text-muted-foreground">{{ item.points }} pts</span>
              <span v-if="item.comments_count > 0" class="text-xs text-muted-foreground">{{ item.comments_count }} comments</span>
            </div>
          </button>
        </div>

        <!-- Main view: content -->
        <div v-else class="flex-1 overflow-y-auto p-4">
          <h1 class="text-lg font-semibold mb-2">{{ feedStore.selectedItem.title }}</h1>
          <div class="flex flex-wrap items-center gap-2 text-sm text-muted-foreground mb-3">
            <span v-if="feedStore.selectedItem.author">By {{ feedStore.selectedItem.author }}</span>
            <span v-if="feedStore.selectedItem.points">▴ {{ feedStore.selectedItem.points }}</span>
            <span v-if="feedStore.selectedItem.comments_count">💬 {{ feedStore.selectedItem.comments_count }}</span>
            <a v-if="feedStore.selectedItem.url" :href="feedStore.selectedItem.url" target="_blank" class="flex items-center gap-1 text-accent hover:underline">Open <ExternalLink class="size-3" /></a>
          </div>
          <div class="flex gap-2 mb-3 flex-wrap">
            <Button size="sm" variant="outline" @click="feedStore.updateItemStatus(feedStore.selectedItem!, feedStore.selectedItem!.status === 'read' ? 'unread' : 'read')">
              <Check class="size-3" /> {{ feedStore.selectedItem?.status === 'read' ? 'Mark Unread' : 'Mark Read' }}
            </Button>
            <Button v-if="feedStore.selectedItem!.status === 'saved'" size="sm" variant="outline" @click="unsaveItem(feedStore.selectedItem!)">
              <BookmarkCheck class="size-3" /> Unsave
            </Button>
            <Button v-else size="sm" variant="outline" @click="feedStore.updateItemStatus(feedStore.selectedItem!, 'saved')">
              <Bookmark class="size-3" /> Save
            </Button>
          </div>
          <Separator class="mb-3" />
          <div class="prose prose-sm dark:prose-invert max-w-none" v-html="feedStore.selectedItem.content" />
        </div>
      </template>
    </div>
  </div>
</template>
