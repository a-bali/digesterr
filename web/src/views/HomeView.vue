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
import MetricsDialog from '../components/MetricsDialog.vue'
import ShortcutsDialog from '../components/ShortcutsDialog.vue'
import VersionBadge from '../components/VersionBadge.vue'
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
import { usePageTitle, digestResultTitle } from '../composables/usePageTitle'
import { Newspaper, Menu, ArrowLeft, CircleHelp } from 'lucide-vue-next'

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
const digestMode = computed(() => (digestStore.activeConfigId != null || digestStore.showingAllDigests) && !feedMode.value)

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
    if (feedStore.activeFeedId) {
      requestMarkFeedRead(feedStore.activeFeedId)
    } else if (feedStore.isMultiView) {
      feedStore.markAllRead(feedStore.activeCategory ?? undefined)
    }
  },
  markAllReadDialogOpen,
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
  scrollToComments: () => {
    const fn = (globalThis as any).__scrollToItemComments
    if (fn) fn()
  },
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
  // Don't refresh the item list — it causes read items to vanish from a
  // filtered (Unread) view when the server response no longer includes them.
  // The 10s poll already keeps sidebar counts fresh; the list itself stays
  // exactly as the user left it.
  feedStore.fetchUnreadCounts()
  digestStore.fetchUnreadCounts()
}

// ---- URL → state sync ----
watch(
  () => route.fullPath,
  async () => {
    const path = route.path
    const feedId = route.params.feedId ? Number(route.params.feedId) : null
    const itemId = route.params.itemId ? Number(route.params.itemId) : null
    const category = route.params.category as string | undefined
    const configId = route.params.configId ? Number(route.params.configId) : null
    const resultId = route.params.resultId ? Number(route.params.resultId) : null

    if (path === '/feeds' && !feedId) {
      // All feeds section view
      showingSaved.value = false
      digestStore.exitAllDigestsMode()
      digestStore.activeConfigId = null
      digestStore.selectedResult = null
      digestStore.resultItems = []
      feedStore.setActiveSection('feeds')
    } else if (category) {
      // Category view: /feeds/category/:category(/:itemId)
      showingSaved.value = false
      digestStore.exitAllDigestsMode()
      digestStore.activeConfigId = null
      digestStore.selectedResult = null
      digestStore.resultItems = []
      if (feedStore.activeCategory !== category) await feedStore.setActiveCategory(category)
      if (itemId) {
        // Item selected within category — find in multiItems or wait for load.
        const item = feedStore.multiItems.find(i => i.id === itemId)
        if (item && feedStore.selectedItem?.id !== itemId) { feedStore.selectItem(item); pendingItemId.value = null }
        else if (!item) pendingItemId.value = itemId
      } else { feedStore.selectedItem = null; pendingItemId.value = null }
    } else if (feedId) {
      // Single feed: /feeds/:feedId(/:itemId)
      showingSaved.value = false
      digestStore.exitAllDigestsMode()
      digestStore.activeConfigId = null
      digestStore.selectedResult = null
      if (feedStore.activeFeedId !== feedId) await feedStore.setActiveFeed(feedId)
      if (itemId) {
        const item = feedStore.items.find(i => i.id === itemId)
        if (item && feedStore.selectedItem?.id !== itemId) { feedStore.selectItem(item); pendingItemId.value = null }
        else if (!item) pendingItemId.value = itemId
      } else { feedStore.selectedItem = null; pendingItemId.value = null }
    } else if (path === '/digests' && !configId) {
      // All digests section view
      showingSaved.value = false
      feedStore.clearActiveView()
      digestStore.enterAllDigestsMode()
    } else if (configId) {
      // Single digest config: /digests/:configId(/:resultId)
      showingSaved.value = false
      feedStore.clearActiveView()
      digestStore.exitAllDigestsMode()
      if (digestStore.activeConfigId !== configId) await digestStore.setActiveConfig(configId)
      if (resultId) {
        const result = digestStore.results.find(r => r.id === resultId)
        if (result) await digestStore.selectResult(result)
      } else { digestStore.selectedResult = null; digestStore.resultItems = [] }
    } else if (path === '/saved') {
      showingSaved.value = true; sidebarOpen.value = false
      feedStore.clearActiveView()
      digestStore.exitAllDigestsMode()
      digestStore.activeConfigId = null; digestStore.selectedResult = null
      await fetchSavedItems()
    } else if (path === '/') {
      showingSaved.value = false
      feedStore.clearActiveView()
      digestStore.exitAllDigestsMode()
      digestStore.activeConfigId = null
      pendingItemId.value = null
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

watch(
  () => feedStore.multiItems,
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

function selectCategory(name: string) {
  showingSaved.value = false; sidebarOpen.value = false
  router.push({ path: `/feeds/category/${encodeURIComponent(name)}` })
}

function selectSection(section: 'feeds' | 'digests') {
  showingSaved.value = false; sidebarOpen.value = false
  if (section === 'digests') {
    router.push({ path: '/digests' })
    return
  }
  router.push({ path: '/feeds' })
}

function selectDigestConfig(id: number) {
  showingSaved.value = false; sidebarOpen.value = false
  if (route.path === `/digests/${id}`) {
    // URL already matches — watcher won't fire, load manually.
    digestStore.exitAllDigestsMode()
    digestStore.setActiveConfig(id)
  } else {
    router.push({ path: `/digests/${id}` })
  }
}

function showSaved() { router.push({ path: '/saved' }) }

function goBackToList() {
  feedStore.selectedItem = null; pendingItemId.value = null
  digestStore.selectedResult = null
  if (feedStore.activeFeedId) router.push({ path: `/feeds/${feedStore.activeFeedId}` })
  else if (digestStore.activeConfigId) router.push({ path: `/digests/${digestStore.activeConfigId}` })
  else if (feedStore.activeSection === 'feeds') router.push({ path: '/feeds' })
  else if (digestStore.showingAllDigests) router.push({ path: '/digests' })
  else if (feedStore.activeCategory) router.push({ path: `/feeds/category/${encodeURIComponent(feedStore.activeCategory)}` })
}

async function selectItem(item: Item) {
  feedStore.selectItem(item)
  if (feedStore.activeFeedId) {
    router.push({ path: `/feeds/${feedStore.activeFeedId}/${item.id}` })
  } else if (feedStore.activeCategory) {
    router.push({ path: `/feeds/category/${encodeURIComponent(feedStore.activeCategory)}/${item.id}` })
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

async function fetchSavedItems() {
  const acc: Item[] = []
  for (const feed of feedStore.feeds) {
    try { acc.push(...(await feedApi.getItems(feed.id, { status: 'saved' })).items) } catch { /* skip */ }
  }
  savedItems.value = acc
}

function onSelectDigestResult(result: { id: number; config_id: number }) {
  // In "All Digests" mode, just select the result (content pane) without
  // navigating — same as how feeds work in category/section view.
  if (!digestStore.activeConfigId) return
  router.push(`/digests/${result.config_id}/${result.id}`)
}

// Opening help via the button leaves focus on it; when the dialog closes,
// reka-ui restores focus to the trigger — Space would then re-open the
// dialog and shortcuts would appear dead. Blur once the overlay is gone.
watch(showShortcuts, (open) => {
  if (!open) {
    requestAnimationFrame(() => {
      const el = document.activeElement as HTMLElement | null
      // Only blur when focus landed on our own header controls.
      if (el && (el.hasAttribute('data-help-button') || el.closest('[data-logs-trigger]'))) el.blur()
    })
  }
})

function onHelpClick() {
  showShortcuts.value = true
}

// Keyboard Shift+A routes through the same confirmation as the sidebar action.
const sidebarRef = useTemplateRef<InstanceType<typeof FeedSidebar>>('sidebar')
function requestMarkFeedRead(id: number) {
  sidebarRef.value?.requestMarkFeedRead(id)
}
function markAllReadDialogOpen() {
  return sidebarRef.value?.markAllReadDialogOpen ?? false
}

// ---- Page title ----
const totalUnread = computed(() => {
  let n = 0
  for (const feed of feedStore.feeds) n += feedStore.unreadCounts[feed.id] ?? 0
  for (const cfg of digestStore.configs) n += digestStore.unreadCounts[cfg.id] ?? 0
  return n
})
const activeFeedId = computed(() => feedStore.activeFeedId ?? digestStore.activeConfigId)
const activeFeedName = computed(() => {
  if (feedStore.activeFeed) return feedStore.activeFeed.name
  if (feedStore.isMultiView) return feedStore.activeCategory ?? 'All Feeds'
  if (digestStore.showingAllDigests) return 'All Digests'
  return digestStore.activeConfig?.name ?? ''
})
const activeFeedUnread = computed(() => {
  // Single feed selected.
  if (feedStore.activeFeedId) return feedStore.unreadCounts[feedStore.activeFeedId] ?? 0
  // Single digest config selected.
  if (digestStore.activeConfigId) return digestStore.unreadCounts[digestStore.activeConfigId] ?? 0
  // Category view: sum unread for all feeds in that category.
  if (feedStore.activeCategory) {
    let n = 0
    for (const f of feedStore.feeds) {
      if (f.category === feedStore.activeCategory) n += feedStore.unreadCounts[f.id] ?? 0
    }
    return n
  }
  // "All Feeds" section.
  if (feedStore.isMultiView) {
    let n = 0
    for (const f of feedStore.feeds) n += feedStore.unreadCounts[f.id] ?? 0
    return n
  }
  // "All Digests" section.
  if (digestStore.showingAllDigests) {
    let n = 0
    for (const cfg of digestStore.configs) n += digestStore.unreadCounts[cfg.id] ?? 0
    return n
  }
  return 0
})
const activeItemTitle = computed(() => feedStore.selectedItem?.title ?? digestResultTitle(digestStore.selectedResult?.content) ?? '')

usePageTitle({ total: totalUnread, feedName: activeFeedName, feedUnread: activeFeedUnread, itemTitle: activeItemTitle })
</script>

<template>
  <div class="flex h-dvh flex-col">
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
        <Newspaper class="size-5 text-accent shrink-0" />
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
        <!-- Help button: same overlay as pressing "?" -->
        <Button variant="ghost" size="icon" title="Keyboard shortcuts (?)" data-help-button @click="onHelpClick">
          <CircleHelp class="size-4" />
        </Button>
        <LogsDialog />
        <MetricsDialog />
        <ThemeMenu />
        <!-- Change password / Google Reader API dialogs hook into the events emitted here. -->
        <AccountMenu @change-password="showChangePassword = true" @google-reader-api="readerApiOpen = true" />
      </div>
    </header>

    <ChangePasswordDialog :open="showChangePassword" @update:open="showChangePassword = $event" />
    <GoogleReaderApiDialog :open="readerApiOpen" @update:open="readerApiOpen = $event" />
    <ShortcutsDialog v-model:open="showShortcuts" />

    <!-- Body -->
    <div class="flex flex-1 min-h-0 overflow-x-hidden">
      <!-- Desktop: 3 resizable panes -->
      <template v-if="isDesktop">
        <SplitterGroup direction="horizontal" auto-save-id="digesterr" class="flex-1">
          <SplitterPanel :default-size="18" :min-size="14" :max-size="28">
            <div class="h-full flex flex-col">
              <aside class="flex-1 min-h-0 border-r bg-sidebar overflow-y-auto p-3">
                <FeedSidebar ref="sidebar" :showing-saving="showingSaved" @select-feed="selectFeed" @select-digest-config="selectDigestConfig" @select-category="selectCategory" @select-section="selectSection" @show-saved="showSaved" />
              </aside>
              <div class="shrink-0 flex justify-center border-r bg-sidebar py-1.5">
                <VersionBadge />
              </div>
            </div>
          </SplitterPanel>
          <SplitterResizeHandle class="w-1 hover:w-1.5 bg-border hover:bg-accent transition-all cursor-col-resize" />

          <SplitterPanel :default-size="30" :min-size="20" :max-size="50">
            <DigestResultList v-if="digestMode" :showing-all="digestStore.showingAllDigests" class="border-r" @select="onSelectDigestResult" @mark-all-read="digestStore.markAllRead()" />
            <ItemList v-else :saved-items="savedItems" :showing-saved="showingSaved" @select-item="selectItem" @mark-all-read="requestMarkFeedRead(feedStore.activeFeedId!)" />
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
          class="h-drawer fixed left-0 top-12 z-40 w-64 bg-sidebar border-r shadow-xl transition-transform duration-200 flex flex-col"
          :class="sidebarOpen ? 'translate-x-0' : '-translate-x-full'"
        >
          <div class="flex-1 min-h-0 overflow-y-auto p-3">
            <FeedSidebar ref="sidebar" :showing-saving="showingSaved" @select-feed="selectFeed" @select-digest-config="selectDigestConfig" @select-category="selectCategory" @select-section="selectSection" @show-saved="showSaved" />
          </div>
          <div class="shrink-0 flex justify-center border-t border-border/50 py-1.5">
            <VersionBadge />
          </div>
        </aside>

        <!-- List pane. Never unmounted on selection change (remounting resets
             scroll). On mobile, when content is shown, the wrapper leaves the
             flex flow (zero-width + absolute overlay off-screen) so the content
             pane takes the full width — display:none would also zero scrollTop. -->
        <div
          class="min-w-0"
          :class="!isTablet && showContentPane ? 'relative w-0 flex-none' : 'flex-1'"
        >
          <div
            class="absolute inset-0 flex flex-col transition-transform duration-200"
            :class="!isTablet && showContentPane ? '-translate-x-full pointer-events-none' : 'static w-full h-full'"
          >
              <DigestResultList v-if="digestMode" :showing-all="digestStore.showingAllDigests" class="flex-1 min-h-0" @select="onSelectDigestResult" @mark-all-read="digestStore.markAllRead()" />
              <ItemList v-else :saved-items="savedItems" :showing-saved="showingSaved" @select-item="selectItem" @mark-all-read="requestMarkFeedRead(feedStore.activeFeedId!)" />
          </div>
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
