<script setup lang="ts">
import { ref, watch, onMounted, onUnmounted, computed, useTemplateRef, type Ref } from 'vue'
import { useRouter, useRoute } from 'vue-router'
import { SplitterGroup, SplitterPanel, SplitterResizeHandle } from 'reka-ui'
import Button from '../components/ui/Button.vue'
import FeedSidebar from '../components/FeedSidebar.vue'
import ItemList from '../components/ItemList.vue'
import ItemContent from '../components/ItemContent.vue'
import DigestContent from '../components/DigestContent.vue'
import DigestResultList from '../components/DigestResultList.vue'
import LogsDialog from '../components/LogsDialog.vue'
import ShortcutsDialog from '../components/ShortcutsDialog.vue'
import ThemeMenu from '../components/ThemeMenu.vue'
import AccountMenu from '../components/AccountMenu.vue'
import ChangePasswordDialog from '../components/ChangePasswordDialog.vue'
import GoogleReaderApiDialog from '../components/GoogleReaderApiDialog.vue'
import { useFeedStore } from '../stores/feed'
import { useDigestStore } from '../stores/digest'
import { useThemeStore } from '../stores/theme'
import { feedApi } from '../services/api'
import type { Item } from '../types/api'
import { useBreakpoints } from '../composables/useMediaQuery'
import { useKeyboardShortcuts } from '../composables/useKeyboardShortcuts'
import { useSwipeGesture } from '../composables/useSwipeGesture'
import { Rss, Menu, ArrowLeft } from 'lucide-vue-next'

const router = useRouter()
const route = useRoute()
const feedStore = useFeedStore()
const digestStore = useDigestStore()
const themeStore = useThemeStore()

const { isDesktop, isTablet } = useBreakpoints()

const savedItems = ref<Item[]>([])
const showingSaved = ref(false)
const sidebarOpen = ref(false)

// Left-edge swipe walks one pane to the left: content -> list, list ->
// sidebar (per user spec — no separate right-edge gesture).
const canSwipe = computed(() => !isDesktop.value) as unknown as Ref<boolean>
useSwipeGesture(canSwipe, () => {
  if (showContentPane.value) {
    if (digestMode.value) digestStore.selectedResult = null
    else feedStore.selectedItem = null
  } else {
    sidebarOpen.value = true
  }
})

const pendingItemId = ref<number | null>(null)
const showChangePassword = ref(false)
const readerApiOpen = ref(false)
const showShortcuts = ref(false)

const feedMode = computed(() => feedStore.activeFeedId != null || showingSaved.value)
const digestMode = computed(() => digestStore.activeConfigId != null && !feedMode.value)

// On mobile/tablet, one pane at a time: content when a selection exists.
const showContentPane = computed(() =>
  feedMode.value ? feedStore.selectedItem != null : digestStore.selectedResult != null,
)

// ---- Keyboard shortcuts ----
useKeyboardShortcuts({
  items: () => (showingSaved.value ? savedItems.value : feedStore.items),
  selectedItem: () => feedStore.selectedItem,
  feeds: () => feedStore.feeds,
  activeFeedId: () => feedStore.activeFeedId,
  selectItem,
  selectFeed,
  toggleRead: (item) => feedStore.updateItemStatus(item, item.status === 'read' ? 'unread' : 'read'),
  toggleSaved: (item) => {
    if (item.status === 'saved') unsaveItem(item)
    else feedStore.updateItemStatus(item, 'saved')
  },
  markAllRead: () => {
    if (feedStore.activeFeedId) requestMarkFeedRead(feedStore.activeFeedId)
  },
  toggleShowRead: () => {
    feedStore.changeFilter(feedStore.filter === 'unread' ? 'all' : 'unread')
  },
  refreshCounts: () => {
    feedStore.fetchUnreadCounts()
    digestStore.fetchUnreadCounts()
  },
  toggleTheme: () => {
    themeStore.mode = themeStore.mode === 'dark' ? 'light' : 'dark'
  },
  openHelp: () => { showShortcuts.value = true },
})

// ---- Lifecycle ----
let pollInterval: ReturnType<typeof setInterval> | null = null

onMounted(() => {
  feedStore.fetchFeeds()
  pollInterval = setInterval(() => {
    feedStore.fetchUnreadCounts()
    digestStore.fetchUnreadCounts()
    digestStore.silentRefresh()
  }, 10000)
  // External clients (e.g. NetNewsWire via the Reader API) can change item
  // statuses while this tab is backgrounded. Refresh on focus so the list
  // reflects starred/read changes made elsewhere.
  document.addEventListener('visibilitychange', onVisibilityChange)
})
onUnmounted(() => {
  if (pollInterval) clearInterval(pollInterval)
  document.removeEventListener('visibilitychange', onVisibilityChange)
})

function onVisibilityChange() {
  if (document.visibilityState !== 'visible') return
  if (showingSaved.value) fetchSavedItems()
  else if (feedStore.activeFeedId != null) feedStore.refreshItems({ silent: true })
}

// ---- URL → state sync ----
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
      await fetchSavedItems()
    } else if (path === '/') {
      showingSaved.value = false; feedStore.activeFeedId = null; feedStore.selectedItem = null; pendingItemId.value = null
    }
  },
  { immediate: true },
)

watch(
  () => feedStore.items,
  (items) => {
    if (pendingItemId.value != null) {
      const item = items.find(i => i.id === pendingItemId.value)
      if (item) { feedStore.selectItem(item); pendingItemId.value = null }
    }
  },
)

// ---- Navigation ----
function selectFeed(id: number) {
  showingSaved.value = false; sidebarOpen.value = false
  router.push({ path: `/feeds/${id}` })
}

function selectDigestConfig(id: number) {
  showingSaved.value = false; sidebarOpen.value = false
  router.push({ path: `/digests/${id}` })
}

function showSaved() { router.push({ path: '/saved' }) }

function goBackToList() {
  feedStore.selectedItem = null; pendingItemId.value = null
  digestStore.selectedResult = null
  if (feedStore.activeFeedId) router.push({ path: `/feeds/${feedStore.activeFeedId}` })
  else if (digestStore.activeConfigId) router.push({ path: `/digests/${digestStore.activeConfigId}` })
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

async function fetchSavedItems() {
  const acc: Item[] = []
  for (const feed of feedStore.feeds) {
    try { acc.push(...(await feedApi.getItems(feed.id, { status: 'saved' })).items) } catch { /* skip */ }
  }
  savedItems.value = acc
}

function onSelectDigestResult(result: { id: number }) {
  router.push(`/digests/${digestStore.activeConfigId}/${result.id}`)
}

// Keyboard Shift+A routes through the same confirmation as the sidebar action.
const sidebarRef = useTemplateRef<InstanceType<typeof FeedSidebar>>('sidebar')
function requestMarkFeedRead(id: number) {
  sidebarRef.value?.requestMarkFeedRead(id)
}
</script>

<template>
  <div class="flex h-screen flex-col">
    <!-- Header -->
    <header class="flex h-12 items-center gap-2 border-b bg-background px-4 shrink-0">
      <Button v-if="!isDesktop" variant="ghost" size="icon" @click="sidebarOpen = !sidebarOpen">
        <Menu class="size-4" />
      </Button>
      <template v-if="showContentPane && !isDesktop">
        <Button variant="ghost" size="sm" @click="goBackToList">
          <ArrowLeft class="size-4" /><span class="text-xs">Back</span>
        </Button>
      </template>
      <template v-else>
        <Rss class="size-5 text-accent shrink-0" />
        <span class="font-semibold shrink-0 hidden sm:inline">Digesterr</span>
      </template>
      <div class="ml-auto flex items-center gap-1">
        <!-- Background refresh pulse: shown while the 10s poll or a
             tab-refocus refresh is in flight. -->
        <span
          v-if="feedStore.backgroundRefreshing || digestStore.backgroundRefreshing"
          data-background-refresh-indicator
          class="size-2 rounded-full bg-accent animate-pulse"
          title="Refreshing…"
        />
        <LogsDialog />
        <ThemeMenu />
        <!-- Change password / Google Reader API dialogs hook into the events emitted here. -->
        <AccountMenu @change-password="showChangePassword = true" @google-reader-api="readerApiOpen = true" />
      </div>
    </header>

    <ChangePasswordDialog :open="showChangePassword" @update:open="showChangePassword = $event" />
    <GoogleReaderApiDialog :open="readerApiOpen" @update:open="readerApiOpen = $event" />
    <ShortcutsDialog v-model:open="showShortcuts" />

    <!-- Body -->
    <div class="flex flex-1 min-h-0">
      <!-- Desktop: 3 resizable panes -->
      <template v-if="isDesktop">
        <SplitterGroup direction="horizontal" auto-save-id="digesterr" class="flex-1">
          <SplitterPanel :default-size="18" :min-size="14" :max-size="28">
            <aside class="h-full border-r bg-sidebar overflow-y-auto p-3">
              <FeedSidebar ref="sidebar" :showing-saving="showingSaved" @select-feed="selectFeed" @select-digest-config="selectDigestConfig" @show-saved="showSaved" />
            </aside>
          </SplitterPanel>
          <SplitterResizeHandle class="w-1 hover:w-1.5 bg-border hover:bg-accent transition-all cursor-col-resize" />

          <SplitterPanel :default-size="30" :min-size="20" :max-size="50">
            <DigestResultList v-if="digestMode" class="border-r" @select="onSelectDigestResult" />
            <ItemList v-else :saved-items="savedItems" :showing-saved="showingSaved" @select-item="selectItem" />
          </SplitterPanel>
          <SplitterResizeHandle class="w-1 hover:w-1.5 bg-border hover:bg-accent transition-all cursor-col-resize" />

          <SplitterPanel :default-size="52" :min-size="30">
            <DigestContent v-if="digestMode" />
            <ItemContent v-else :unsave-item="unsaveItem" />
          </SplitterPanel>
        </SplitterGroup>
      </template>

      <!-- Tablet (2-pane) & Mobile (1-pane): drawer sidebar -->
      <template v-else>
        <div v-if="sidebarOpen" class="fixed inset-0 z-30" @click="sidebarOpen = false"><div class="absolute inset-0 bg-black/50" /></div>
        <aside
          class="fixed left-0 top-12 z-40 h-[calc(100vh-3rem)] w-64 bg-sidebar border-r shadow-xl transition-transform duration-200 overflow-y-auto p-3"
          :class="sidebarOpen ? 'translate-x-0' : '-translate-x-full'"
        >
          <FeedSidebar :showing-saving="showingSaved" @select-feed="selectFeed" @select-digest-config="selectDigestConfig" @show-saved="showSaved" />
        </aside>

        <!-- List pane -->
        <div
          class="flex-1 min-w-0"
          :class="[isTablet ? '' : (showContentPane ? 'hidden' : ''), 'flex flex-col']"
        >
          <Transition name="slide-from-left" appear>
            <div :key="showContentPane ? 'behind' : 'list'" class="flex-1 min-h-0 flex flex-col">
              <DigestResultList v-if="digestMode" class="flex-1 min-h-0" @select="onSelectDigestResult" />
              <ItemList v-else :saved-items="savedItems" :showing-saved="showingSaved" @select-item="selectItem" />
            </div>
          </Transition>
        </div>

        <!-- Content pane (tablet: side-by-side; mobile: replaces list).
             overflow-x-hidden: never allow a horizontal scroll container on
             narrow screens (phantom overflow / snap-back). -->
        <div
          class="border-l flex-1 min-w-0 overflow-x-hidden"
          :class="isTablet ? '' : (showContentPane ? '' : 'hidden')"
        >
          <Transition name="slide-from-right" appear>
            <div :key="(digestMode ? 'd' : 'i') + (showContentPane ? '-c' : '-l')" class="h-full">
              <DigestContent v-if="digestMode" />
              <ItemContent v-else :unsave-item="unsaveItem" />
            </div>
          </Transition>
        </div>
      </template>
    </div>
  </div>
</template>
