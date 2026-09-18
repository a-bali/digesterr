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
import SettingsDialog from '../components/SettingsDialog.vue'
import ChangePasswordDialog from '../components/ChangePasswordDialog.vue'
import GoogleReaderApiDialog from '../components/GoogleReaderApiDialog.vue'
import { useFeedStore } from '../stores/feed'
import { useDigestStore } from '../stores/digest'
import { useThemeStore } from '../stores/theme'
import { useLayoutStore } from '../stores/layout'
import { feedApi, itemApi } from '../services/api'
import type { Item } from '../types/api'
import { useBreakpoints } from '../composables/useMediaQuery'
import { useKeyboardShortcuts } from '../composables/useKeyboardShortcuts'
import { useSwipeGesture } from '../composables/useSwipeGesture'
import { usePageTitle, digestResultTitle } from '../composables/usePageTitle'
import { scrollToItemComments } from '../lib/itemComments'
import { Newspaper, Menu, ArrowLeft, CircleHelp, Columns3, Rows2, Settings } from 'lucide-vue-next'

const router = useRouter()
const route = useRoute()
const feedStore = useFeedStore()
const digestStore = useDigestStore()
const themeStore = useThemeStore()
const layoutStore = useLayoutStore()

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

const showChangePassword = ref(false)
const showLlmSettings = ref(false)
const readerApiOpen = ref(false)
const showShortcuts = ref(false)

const feedMode = computed(() => feedStore.activeFeedId != null || showingSaved.value)
const digestMode = computed(() => (digestStore.activeConfigId != null || digestStore.showingAllDigests) && !feedMode.value)

// Is the content pane showing an ITEM (feed/category/section/saved view) or a
// DIGEST? feedMode/digestMode cannot answer this: a category or section view is
// neither a single feed (activeFeedId is null) nor a digest, so both are false
// there and the selection was read off the digest store — which is always empty
// in an items view. That made x a no-op in exactly those views.
const inItemsMode = computed(() => !digestMode.value)

// On mobile/tablet, one pane at a time: content when a selection exists.
const showContentPane = computed(() =>
  inItemsMode.value ? feedStore.selectedItem != null : digestStore.selectedResult != null,
)

// ---- Maximized content pane (desktop) ----
// 'x' collapses the CHROME panels to zero width and lets the content take the
// whole row. They stay MOUNTED (an inline flex-grow/flex-basis override, not
// v-if), so the list keeps its scroll position and reka's saved split is left
// untouched.
const contentMaximized = ref(false)

// Only meaningful where it can be entered AND left: desktop, with something
// selected. Elsewhere it is a no-op, so pressing x cannot strand the user on a
// maximized empty pane.
const canMaximize = computed(() => isDesktop.value && showContentPane.value)

function toggleContentMaximized() {
  if (!canMaximize.value) return
  contentMaximized.value = !contentMaximized.value
}

// The maximized state must be dropped when it stops being enterable, and when
// the layout changes: the two layouts collapse different panels, so carrying it
// across would leave the new one half-collapsed with a Back button the user
// never asked for.
// The direction matters — resetting UNCONDITIONALLY also fires on false→true and
// would cancel a maximize that had just been requested (the watcher job can flush
// after x sets the flag). Only ever clear it, never on the way in.
// NB: the layout source must be a GETTER — a Pinia setup store exposes `mode` as
// an unwrapped string, and Vue silently ignores a non-ref/non-getter source.
let lastLayoutMode = layoutStore.mode
watch([canMaximize, () => layoutStore.mode], ([ok, mode]) => {
  if (!ok || mode !== lastLayoutMode) contentMaximized.value = false
  lastLayoutMode = mode
})

// `undefined` removes the whole inline style, so reka's own flex applies again.
const collapsedPanelStyle = computed(() =>
  contentMaximized.value ? { flexGrow: 0, flexBasis: 0 } : undefined,
)

// Maximizing leaves ONLY the content pane: the sidebar and the item list both
// collapse, in either layout. In stacked that means the list's half of the
// vertical split goes away too, so the content fills the right column top to
// bottom. Restoring x/Back brings every split back to its saved size.
const sidebarCollapsed = computed(() => contentMaximized.value)
const listCollapsed = computed(() => contentMaximized.value)

// Header Back: in the maximized layout it restores the 3-pane view and keeps
// the selected item; below lg it is still mobile's "leave the content pane".
function onHeaderBack() {
  if (contentMaximized.value) { contentMaximized.value = false; return }
  goBackToList()
}

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
    // Same dispatch as toggleShowRead: 'A' must act on whichever list is on
    // screen. Routing it to markAllReadInCurrentView unconditionally is what
    // made it a no-op in both digest views — that function only ever inspects
    // feed state, which a digest view has cleared.
    if (digestMode.value) return markAllReadInDigestView()
    markAllReadInCurrentView()
  },
  markAllReadDialogOpen,
  toggleShowRead: () => {
    // 'u' toggles unread-only for whichever list is on screen. In a digest view
    // the item filter is not what the user is looking at, and flipping it had
    // no visible effect while silently overwriting the persisted item filter.
    if (digestMode.value) {
      digestStore.resultFilter = digestStore.resultFilter === 'unread' ? 'all' : 'unread'
      return
    }
    feedStore.changeFilter(feedStore.filter === 'unread' ? 'all' : 'unread')
  },
  refreshCounts: () => {
    feedStore.fetchUnreadCounts()
    digestStore.fetchUnreadCounts()
    feedStore.fetchSavedCount()
  },
  toggleTheme: () => {
    themeStore.mode = themeStore.mode === 'dark' ? 'light' : 'dark'
  },
  toggleMaximize: toggleContentMaximized,
  openHelp: () => { showShortcuts.value = true },
  scrollToComments: () => scrollToItemComments(),
})

// ---- Lifecycle ----
let pollInterval: ReturnType<typeof setInterval> | null = null

onMounted(() => {
  feedStore.fetchFeeds()
  // The saved badge is part of the initial paint, not just a poll refresh: the
  // sidebar renders before any item is touched.
  feedStore.fetchSavedCount()
  pollInterval = setInterval(() => {
    feedStore.fetchUnreadCounts()
    digestStore.fetchUnreadCounts()
    feedStore.fetchSavedCount()
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
  feedStore.fetchSavedCount()
}

// Resolve the item referenced by the URL and select it. The list only holds the
// first page (50) of the active feed/category under the current filter, so a
// linked item is often not loaded — an older item, or one hidden by a persisted
// status filter. GET /items/{id} is id-based, so fetch it directly instead of
// searching only the loaded page.
let resolveSeq = 0
async function resolveItemById(itemId: number, loaded: Item[]) {
  const hit = loaded.find(i => i.id === itemId)
  if (hit) {
    if (feedStore.selectedItem?.id !== itemId) feedStore.selectItem(hit)
    return
  }
  const seq = ++resolveSeq
  try {
    const full = await itemApi.getItem(itemId)
    // Drop stale responses: a later navigation (or a click) supersedes this one.
    if (seq !== resolveSeq || Number(route.params.itemId) !== itemId) return
    feedStore.selectedItem = full
  } catch {
    // Not owned or since deleted — leave the content pane empty.
  }
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
      if (itemId) await resolveItemById(itemId, feedStore.multiItems)
      else feedStore.selectedItem = null
    } else if (feedId) {
      // Single feed: /feeds/:feedId(/:itemId)
      showingSaved.value = false
      digestStore.exitAllDigestsMode()
      digestStore.activeConfigId = null
      digestStore.selectedResult = null
      if (feedStore.activeFeedId !== feedId) await feedStore.setActiveFeed(feedId)
      if (itemId) await resolveItemById(itemId, feedStore.items)
      else feedStore.selectedItem = null
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
    }
  },
  { immediate: true },
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

// "Mark all as read" for whatever the middle pane is showing. The keyboard
// shortcut and the pane's own button both come through here so they cannot
// disagree on scope. A category marks that category, the "Feeds" section marks
// every feed; a single feed still asks for confirmation first.
//
// Digest views are NOT reached here: HomeView routes the shortcut to the digest
// store instead (see the useKeyboardShortcuts wiring). Feeding digest mode
// through this function is what silently did nothing, because every branch below
// tests feed state that the digest view has already cleared.
function markAllReadInCurrentView() {
  if (feedStore.isMultiView) {
    feedStore.markAllRead(feedStore.activeCategory ?? undefined)
  } else if (feedStore.activeFeedId) {
    requestMarkFeedRead(feedStore.activeFeedId)
  }
}

// Mark-all-read for the digest list. `A` and DigestResultList's own button must
// agree on scope, so both end up here: the single config on screen, or every
// digest in the "All Digests" section.
function markAllReadInDigestView() {
  if (digestStore.activeConfigId != null) return digestStore.markAllRead(digestStore.activeConfigId)
  if (digestStore.showingAllDigests) return digestStore.markAllRead()
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
  // The saved view clears every feed/category/digest selection, so without this
  // branch it returns '' — and formatTitle() short-circuits on an empty name,
  // which also swallowed the item title. Naming the view is what makes both
  // halves of the title render.
  if (showingSaved.value) return 'Saved'
  return digestStore.activeConfig?.name ?? ''
})
const activeFeedUnread = computed(() => {
  // Saved view: the count that belongs to this view is the saved total, not an
  // unread tally (a saved item can be read or unread).
  if (showingSaved.value) return feedStore.savedCount
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
      <template v-if="(showContentPane && !isDesktop) || contentMaximized">
        <Button variant="ghost" size="sm" @click="onHeaderBack">
          <ArrowLeft class="size-4" /><span class="text-xs">Back</span>
        </Button>
      </template>
      <template v-else>
        <Newspaper class="size-5 text-accent shrink-0" />
        <span data-app-logo class="font-semibold shrink-0 hidden sm:inline">Digesterr</span>
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
        <!-- Icon shows the CURRENT layout; the tooltip states the action. -->
        <Button
          variant="ghost" size="icon"
          data-layout-toggle
          :disabled="!isDesktop"
          :title="isDesktop
            ? (layoutStore.mode === 'panes' ? 'Switch to stacked layout' : 'Switch to 3-pane layout')
            : 'Stacked layout is available at desktop width'"
          :aria-label="layoutStore.mode === 'panes' ? 'Switch to stacked layout' : 'Switch to 3-pane layout'"
          @click="layoutStore.toggle()"
        >
          <Columns3 v-if="layoutStore.mode === 'panes'" class="size-4" />
          <Rows2 v-else class="size-4" />
        </Button>
        <ThemeMenu />
        <!-- Cogwheel settings (LLM model) sits immediately before the profile button. -->
        <Button
          variant="ghost"
          size="icon"
          title="Settings"
          aria-label="Settings"
          data-settings-button
          @click="showLlmSettings = true"
        >
          <Settings class="size-4" />
        </Button>
        <!-- Change password / Google Reader API dialogs hook into the events emitted here. -->
        <AccountMenu @change-password="showChangePassword = true" @google-reader-api="readerApiOpen = true" />
      </div>
    </header>

    <ChangePasswordDialog :open="showChangePassword" @update:open="showChangePassword = $event" />
    <SettingsDialog :open="showLlmSettings" @update:open="showLlmSettings = $event" />
    <GoogleReaderApiDialog :open="readerApiOpen" @update:open="readerApiOpen = $event" />
    <ShortcutsDialog v-model:open="showShortcuts" />

    <!-- Body -->
    <div class="flex flex-1 min-h-0 overflow-x-hidden">
      <!-- Desktop: 3 resizable panes -->
      <template v-if="isDesktop">
        <SplitterGroup v-if="layoutStore.mode === 'panes'" direction="horizontal" auto-save-id="digesterr" class="flex-1" data-layout="panes">
          <SplitterPanel
            :default-size="18" :min-size="14" :max-size="28"
            :style="sidebarCollapsed ? collapsedPanelStyle : undefined"
            :inert="sidebarCollapsed || undefined"
            :aria-hidden="sidebarCollapsed || undefined"
          >
            <div class="h-full flex flex-col">
              <aside class="flex-1 min-h-0 bg-sidebar overflow-y-auto p-3" :class="sidebarCollapsed ? '' : 'border-r'">
                <FeedSidebar ref="sidebar" :showing-saving="showingSaved" @select-feed="selectFeed" @select-digest-config="selectDigestConfig" @select-category="selectCategory" @select-section="selectSection" @show-saved="showSaved" />
              </aside>
              <div class="shrink-0 flex justify-center bg-sidebar py-1.5" :class="sidebarCollapsed ? '' : 'border-r'">
                <VersionBadge />
              </div>
            </div>
          </SplitterPanel>
          <SplitterResizeHandle class="w-1 hover:w-1.5 bg-border hover:bg-accent transition-all cursor-col-resize" :class="{ hidden: contentMaximized }" />

          <SplitterPanel
            :default-size="30" :min-size="20" :max-size="50"
            :style="listCollapsed ? collapsedPanelStyle : undefined"
            :inert="listCollapsed || undefined"
            :aria-hidden="listCollapsed || undefined"
          >
            <DigestResultList v-if="digestMode" :showing-all="digestStore.showingAllDigests" :class="listCollapsed ? '' : 'border-r'" @select="onSelectDigestResult" @mark-all-read="digestStore.markAllRead(digestStore.activeConfigId ?? undefined)" />
            <ItemList v-else :saved-items="savedItems" :showing-saved="showingSaved" @select-item="selectItem" @mark-all-read="markAllReadInCurrentView" />
          </SplitterPanel>
          <SplitterResizeHandle class="w-1 hover:w-1.5 bg-border hover:bg-accent transition-all cursor-col-resize" :class="{ hidden: contentMaximized }" />

          <SplitterPanel :default-size="52" :min-size="30">
            <DigestContent v-if="digestMode" />
            <ItemContent v-else :unsave-item="unsaveItem" />
          </SplitterPanel>
        </SplitterGroup>

        <!-- Desktop: stacked — sidebar, then the item selector above the content
             in the right column. Its own auto-save-ids so each layout keeps its
             own split sizes. x collapses only the sidebar here: the list is
             already above the content, and it is the only way to pick an item. -->
        <SplitterGroup v-else direction="horizontal" auto-save-id="digesterr-stacked" class="flex-1" data-layout="stacked">
          <SplitterPanel
            :default-size="18" :min-size="14" :max-size="28"
            :style="sidebarCollapsed ? collapsedPanelStyle : undefined"
            :inert="sidebarCollapsed || undefined"
            :aria-hidden="sidebarCollapsed || undefined"
          >
            <div class="h-full flex flex-col">
              <aside class="flex-1 min-h-0 bg-sidebar overflow-y-auto p-3" :class="sidebarCollapsed ? '' : 'border-r'">
                <FeedSidebar ref="sidebar" :showing-saving="showingSaved" @select-feed="selectFeed" @select-digest-config="selectDigestConfig" @select-category="selectCategory" @select-section="selectSection" @show-saved="showSaved" />
              </aside>
              <div class="shrink-0 flex justify-center bg-sidebar py-1.5" :class="sidebarCollapsed ? '' : 'border-r'">
                <VersionBadge />
              </div>
            </div>
          </SplitterPanel>
          <SplitterResizeHandle class="w-1 hover:w-1.5 bg-border hover:bg-accent transition-all cursor-col-resize" :class="{ hidden: contentMaximized }" />

          <SplitterPanel :default-size="82" :min-size="40">
            <SplitterGroup direction="vertical" auto-save-id="digesterr-stacked-v" class="h-full">
              <SplitterPanel
                :default-size="35" :min-size="15" :max-size="70"
                :style="listCollapsed ? collapsedPanelStyle : undefined"
                :inert="listCollapsed || undefined"
                :aria-hidden="listCollapsed || undefined"
              >
                <DigestResultList v-if="digestMode" compact :showing-all="digestStore.showingAllDigests" class="h-full" :class="listCollapsed ? '' : 'border-b'" @select="onSelectDigestResult" @mark-all-read="digestStore.markAllRead(digestStore.activeConfigId ?? undefined)" />
                <ItemList v-else compact :saved-items="savedItems" :showing-saved="showingSaved" @select-item="selectItem" @mark-all-read="markAllReadInCurrentView" />
              </SplitterPanel>
              <SplitterResizeHandle class="h-1 hover:h-1.5 bg-border hover:bg-accent transition-all cursor-row-resize" :class="{ hidden: contentMaximized }" />
              <SplitterPanel :default-size="65" :min-size="25">
                <DigestContent v-if="digestMode" />
                <ItemContent v-else :unsave-item="unsaveItem" />
              </SplitterPanel>
            </SplitterGroup>
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
              <DigestResultList v-if="digestMode" :showing-all="digestStore.showingAllDigests" class="flex-1 min-h-0" @select="onSelectDigestResult" @mark-all-read="digestStore.markAllRead(digestStore.activeConfigId ?? undefined)" />
              <ItemList v-else :saved-items="savedItems" :showing-saved="showingSaved" @select-item="selectItem" @mark-all-read="markAllReadInCurrentView" />
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
