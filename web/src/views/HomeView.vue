<script setup lang="ts">
import { ref, watch, onMounted, onUnmounted, computed, useTemplateRef, nextTick, type Ref } from 'vue'
import { useRouter, useRoute } from 'vue-router'
import { SplitterGroup, SplitterPanel, SplitterResizeHandle } from 'reka-ui'
import Button from '../components/ui/Button.vue'
import Dialog from '../components/ui/Dialog.vue'
import FeedSidebar from '../components/FeedSidebar.vue'
import ItemList from '../components/ItemList.vue'
import ItemContent from '../components/ItemContent.vue'
import DigestContent from '../components/DigestContent.vue'
import DigestResultList from '../components/DigestResultList.vue'
import ErrorBoundary from '../components/ErrorBoundary.vue'
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
import { useAuthStore } from '../stores/auth'
import { useThemeStore } from '../stores/theme'
import { useLayoutStore } from '../stores/layout'
import { itemApi } from '../services/api'
import { toastSuccess } from '../lib/toast'
import {
  type MarkAllScope, scopeLabel, scopeCount,
  MARK_EMPTY_TITLE, MARK_DIALOG_TITLE,
} from '../lib/markAllRead'
import type { Item } from '../types/api'
import { useBreakpoints } from '../composables/useMediaQuery'
import { useKeyboardShortcuts } from '../composables/useKeyboardShortcuts'
import { useSwipeGesture } from '../composables/useSwipeGesture'
import { usePageTitle, digestResultTitle } from '../composables/usePageTitle'
import { scrollToItemComments } from '../lib/itemComments'
import { Newspaper, Menu, ArrowLeft, CircleHelp, Columns3, Rows2, Settings } from 'lucide-vue-next'

const router = useRouter()
const route = useRoute()
const authStore = useAuthStore()
const feedStore = useFeedStore()
const digestStore = useDigestStore()
const themeStore = useThemeStore()
const layoutStore = useLayoutStore()

const { isDesktop, isTablet } = useBreakpoints()

// The Saved view's list lives in the store so a status change reaches its rows
// (see savedItems in stores/feed.ts); this is just a readable alias.
const savedItems = computed(() => feedStore.savedItems)
// The Saved view's active state lives in the store too: the store's page loader
// has to know whether fetchMoreItems should page the saved list or a feed, and
// changeFilter refetches the saved list only when it is showing. A local ref
// would leave the store blind to it.
const showingSaved = computed({
  get: () => feedStore.showingSaved,
  set: (v: boolean) => { feedStore.showingSaved = v },
})
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
    if (item.is_saved === 1) unsaveItem(item)
    else feedStore.updateItemStatus(item, undefined, true)
  },
  // 'A' and the mid-pane button now share requestMarkAllRead with every sidebar
  // control, so the key cannot disagree with the button about confirming.
  markAllRead: requestMarkAllRead,
  // A function, not the computed ref: useKeyboardShortcuts CALLS this, and a
  // ref object is always truthy, so passing it made the guard dead and 'A'
  // re-opened the dialog while it was already open.
  markAllReadDialogOpen: () => markAllDialogOpen.value,
  toggleShowRead: () => {
    // 'u' toggles unread-only for whichever list is on screen. In a digest view
    // the item filter is not what the user is looking at, and flipping it had
    // no visible effect while silently overwriting the persisted item filter.
    if (digestMode.value) {
      // Goes through the store so the filter change also REFETCHES. Assigning
      // resultFilter here left the previous query's rows on screen: the menu
      // showed "Unread" while the list still held the unfiltered page, and only
      // the menu's own setter knew to re-query.
      digestStore.toggleResultFilter()
      return
    }
    // The Saved view always shows every saved item, so there is nothing here for
    // 'u' to toggle. The one filter it could reach is the persisted item
    // filter, which this view ignores: flipping that would be invisible AND
    // would overwrite the filter the user set for their feeds.
    //
    // This is a deliberate no-op, not a copy of the digest case above. A digest
    // view can redirect 'u' to the digest's OWN filter (results carry their own
    // read state), so the keypress still does something visible there. Saved
    // items have no equivalent view-local filter — adding one would contradict
    // the rule that Saved always shows everything — so there is nowhere to
    // redirect to and doing nothing is the only honest behaviour. The only
    // property shared with the digest case is that neither overwrites the
    // persisted item filter.
    if (feedStore.showingSaved) return
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
  // Rehydrate the profile for reloads: the access token is memory-only, so
  // auth.user is null even though the router guard restored the session.
  void authStore.loadUser()
  feedStore.fetchFeeds()
  // The saved badge is part of the initial paint, not just a poll refresh: the
  // sidebar renders before any item is touched.
  feedStore.fetchSavedCount()
  pollInterval = setInterval(() => {
    // A backgrounded tab has nothing to show: skip the tick and let the
    // visibilitychange handler refresh on return instead.
    if (document.hidden) return
    feedStore.fetchUnreadCounts()
    digestStore.fetchUnreadCounts()
    feedStore.fetchSavedCount()
    // Feed rows carry last_error/last_error_severity, and a poll on the server
    // clears a feed's error as soon as it recovers. Nothing else refetches the
    // feed list on a timer, so without this the sidebar kept showing the red
    // (or amber) mark after the condition had already gone — the error only
    // disappeared on a reload or some unrelated feed action. Digest configs
    // refresh their own list in the same tick (silentRefresh), which is why
    // digest errors cleared but feed errors did not.
    feedStore.fetchFeeds({ silent: true })
    digestStore.silentRefresh()
    // The item LIST is refreshed too, not just the counts: a new item raised the
    // sidebar badge while the open middle pane stayed frozen until the feed was
    // reselected or a filter changed. silentRefresh merges into the loaded list
    // (in the active sort order), so it injects the arrival without discarding
    // pages the reader has scrolled to.
    feedStore.silentRefresh()
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
  if (showingSaved.value) feedStore.fetchSavedItems()
  // Refresh the item list on return, but MERGE rather than replace: a clean
  // replace would make read items vanish from a filtered (Unread) view whenever
  // the response no longer includes them, which is why this used to be skipped
  // entirely. silentRefresh keeps the loaded rows and only adds/updates, so the
  // list stays exactly as the user left it while still picking up arrivals and
  // status changes made elsewhere (e.g. NetNewsWire via the Reader API).
  else feedStore.silentRefresh()
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
    } else if (itemId && path.startsWith('/feeds/item/')) {
      // All-Feeds item: /feeds/item/:itemId. The section mixes feeds, so there is
      // no feed id to resolve against -- the item id is the only handle.
      showingSaved.value = false
      digestStore.exitAllDigestsMode()
      digestStore.activeConfigId = null
      digestStore.selectedResult = null
      feedStore.setActiveSection('feeds')
      await resolveItemById(itemId, feedStore.multiItems)
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
        // Resolve from the loaded list when possible, otherwise fetch the
        // result by id. Searching only the loaded page meant a deep link to a
        // result past page one silently selected nothing; the id is enough to
        // ask the server directly.
        const loaded = digestStore.results.find(r => r.id === resultId)
        if (loaded) {
          await digestStore.selectResult(loaded)
        } else {
          await digestStore.selectResultById(resultId)
        }
      } else { digestStore.selectedResult = null; digestStore.resultItems = [] }
    } else if (path === '/saved' || path.startsWith('/saved/')) {
      showingSaved.value = true; sidebarOpen.value = false
      feedStore.clearActiveView()
      digestStore.exitAllDigestsMode()
      digestStore.activeConfigId = null; digestStore.selectedResult = null
      // Wait for the page before resolving, so the item is found in the loaded
      // rows when it is there (and fetched by id when it is not).
      await feedStore.fetchSavedItems()
      if (itemId) await resolveItemById(itemId, feedStore.savedItems)
      else feedStore.selectedItem = null
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
  // Saved and All-Feeds items now carry the id in the URL, so returning to the
  // list has to drop it -- otherwise the route still names an item and the
  // content pane reopens.
  else if (showingSaved.value) router.push({ path: '/saved' })
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
  } else if (showingSaved.value) {
    // Saved spans feeds and categories, so the item needs its own segment for
    // the URL to name it. Without this the pane updated while the route stayed
    // at /saved, making the open item unlinkable and lost on reload.
    router.push({ path: `/saved/${item.id}` })
  } else if (feedStore.activeSection === 'feeds') {
    router.push({ path: `/feeds/item/${item.id}` })
  }
  if (item.status === 'unread') {
    // updateItemStatus -> applyUpdatedItem now reaches savedItems too, so no
    // local patch is needed here.
    await feedStore.updateItemStatus(item, 'read')
  }
}

async function unsaveItem(item: Item) {
  // Unsave clears the saved FLAG; it does not touch read state. The store owns
  // the saved list, so it is the thing that drops the row.
  await feedStore.unsaveItem(item)
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
// The sidebar declares the scope it wants marked; the dialog and the action both
// live here, so a sidebar control cannot mark a scope read without the same
// confirmation the key and the mid-pane button use.
function onSidebarMarkAllRead(scope: MarkAllScope) {
  requestMarkAllRead(scope)
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
// Mark-all-read confirmation. Every mark-all-read entry point funnels through
// here — the `A` shortcut, the mid-pane end-of-list button, and all four sidebar
// controls — so they cannot disagree about whether to confirm or what scope they
// cover.
//
// The rule the user asked for: ANY mark-all-read that can destroy more than one
// unread item confirms first. That means every multi-view (category, "Feeds"
// section, "All Digests" section) plus a single feed and a single digest config.
// Marking a single item stays immediate, and marking an already-empty scope does
// not confirm at all (see requestMarkAllRead).
//
// The confirmation names its scope and, for a scoped mark, how many items it will
// touch, so "Mark all as read?" is never ambiguous about what it covers.
interface MarkAllTarget {
  scope: MarkAllScope
  /**
   * Unread items the action will mark. `null` means the count is not known yet
   * (unread counts arrive separately from the lists). 0 means provably nothing to
   * mark, which is the only case that skips the confirmation.
   */
  unread: number | null
  title: string
  description: string
}

const markAllTarget = ref<MarkAllTarget | null>(null)
const markAllDialogOpen = computed(() => markAllTarget.value !== null)
const markAllBusy = ref(false)

// Unread items in the scope on screen. Returns null when the count cannot be
// determined (digest unread counts arrive from a separate request, so a missing
// key means "not loaded yet", not zero) — in that case the action proceeds
// without a confirmation rather than blocking on data that may never arrive.
function feedUnreadInScope(category?: string): number | null {
  const feeds = category
    ? feedStore.feeds.filter(f => f.category === category)
    : feedStore.feeds
  if (feeds.length === 0) return null
  let total = 0
  let known = false
  for (const f of feeds) {
    const n = feedStore.unreadCounts[f.id]
    if (typeof n === 'number') { total += n; known = true }
  }
  return known ? total : null
}

function digestUnreadInScope(configId?: number): number | null {
  const cfgId = configId ?? null
  if (cfgId != null) {
    const n = digestStore.unreadCounts[cfgId]
    return typeof n === 'number' ? n : null
  }
  const ids = digestStore.configs.map(c => c.id)
  if (ids.length === 0) return null
  let total = 0
  let known = false
  for (const id of ids) {
    const n = digestStore.unreadCounts[id]
    if (typeof n === 'number') { total += n; known = true }
  }
  return known ? total : null
}

// Single funnel for every mark-all-read request. Decides the scope from the view
// on screen, skips the dialog when there is provably nothing to mark, and opens
// the confirmation otherwise.
function requestMarkAllRead(explicit?: MarkAllScope) {
  if (markAllTarget.value) return
  const scope = explicit ?? resolveMarkAllScope()
  if (!scope) return
  const target = targetForScope(scope)
  if (!target) return
  // Provably empty scope, and the FIRST unread could be counted: the action would
  // be a no-op, so do not ask and do not claim success. The earlier code toasted
  // "marked read" for a category with nothing unread in it.
  if (target.unread === 0) {
    // Same shape as every other toast: title = outcome, subtitle = the SCOPE.
    // Using the full sentence here ("Nothing unread in X") repeated the title and
    // broke the pattern the success/failure toasts follow.
    toastSuccess(MARK_EMPTY_TITLE, scopeLabel(target.scope))
    return
  }
  markAllTarget.value = target
}

// Builds the confirmation for a scope. This is the SINGLE source of the wording:
// the dialog title, the dialog body, the "nothing to mark" toast and the
// control's tooltip all derive from the scope, so they cannot drift apart. An
// earlier version built the same target twice (once per entry point) and the copy
// diverged — the scope ended up in the toast TITLE for digests but in the SUBTITLE
// for feeds.
function targetForScope(scope: MarkAllScope): MarkAllTarget | null {
  const unread = unreadInScope(scope)
  // A scope whose entity cannot be resolved (feed deleted, configs not loaded) has
  // no confirmation to offer.
  if (unread === undefined) return null
  return {
    scope,
    unread,
    title: MARK_DIALOG_TITLE,
    description: `Mark ${scopeCount(scope, unread)} in ${scopeLabel(scope)} as read? This cannot be undone.`,
  }
}

// Unread count for a scope, or undefined when the entity itself is unknown.
// `null` (inside the returned object) already means "count not loaded yet".
function unreadInScope(scope: MarkAllScope): number | null | undefined {
  switch (scope.kind) {
    case 'feed': {
      const feed = feedStore.feeds.find(f => f.id === scope.id)
      if (!feed) return undefined
      const n = feedStore.unreadCounts[feed.id]
      return typeof n === 'number' ? n : null
    }
    case 'category': return feedUnreadInScope(scope.name)
    case 'feeds-section': return feedUnreadInScope()
    case 'digest-config':
      return digestStore.configs.some(c => c.id === scope.id) ? digestUnreadInScope(scope.id) : undefined
    case 'all-digests': return digestUnreadInScope()
  }
}

// The scope the CURRENT view points at. Deliberately only decides WHICH scope;
// every word comes from targetForScope, so the two entry points (key/button vs
// sidebar control) cannot produce different copy for the same scope.
function resolveMarkAllScope(): MarkAllScope | null {
  if (digestMode.value) {
    if (digestStore.activeConfigId != null) {
      const cfg = digestStore.activeConfig
      if (!cfg) return null
      return { kind: 'digest-config', id: cfg.id, name: cfg.name }
    }
    if (digestStore.showingAllDigests) return { kind: 'all-digests' }
    return null
  }
  if (feedStore.activeCategory) return { kind: 'category', name: feedStore.activeCategory }
  if (feedStore.activeSection === 'feeds') return { kind: 'feeds-section' }
  if (feedStore.activeFeedId) {
    const feed = feedStore.activeFeed
    if (!feed) return null
    return { kind: 'feed', id: feed.id, name: feed.name }
  }
  return null
}

async function confirmMarkAllRead() {
  const target = markAllTarget.value
  markAllTarget.value = null
  if (!target || markAllBusy.value) return
  markAllBusy.value = true
  try {
    await runMarkAllScope(target.scope)
  } finally {
    markAllBusy.value = false
  }
}

async function runMarkAllScope(scope: MarkAllScope) {
  switch (scope.kind) {
    case 'feed':
      await feedStore.markFeedRead(scope.id)
      break
    case 'category':
      await feedStore.markAllRead(scope.name)
      await feedStore.fetchFeeds()
      break
    case 'feeds-section':
      await feedStore.markAllRead()
      await feedStore.fetchFeeds()
      break
    case 'digest-config':
      await digestStore.markAllRead(scope.id)
      break
    case 'all-digests':
      await digestStore.markAllRead()
      break
  }
}

function closeMarkAllReadDialog() {
  markAllTarget.value = null
}

// Focus the confirm button when the dialog opens, so the keyboard flow is
// 'A' then Enter. The dialog was moved here from FeedSidebar and must keep the
// focus behaviour it had there.
const markAllConfirmRef = useTemplateRef<InstanceType<typeof Button>>('markAllConfirm')
watch(markAllTarget, async (target) => {
  if (!target) return
  await nextTick()
  ;(markAllConfirmRef.value?.$el as HTMLElement | undefined)?.focus()
})

// Enter must confirm from the keyboard. It cannot be left to the native
// activate-focused-button behaviour: useKeyboardShortcuts blurs any focused
// BUTTON on keydown (so shortcuts keep working after a click), and that blur
// cancels the default action. Handle Enter here, and guard the microtask so the
// keydown and the click it would otherwise trigger do not both run the action.
let markAllConfirmKeyHandled = false
function onMarkAllConfirmKeyDown(e: KeyboardEvent) {
  if (e.key !== 'Enter') return
  e.preventDefault()
  if (markAllConfirmKeyHandled) return
  markAllConfirmKeyHandled = true
  queueMicrotask(() => { markAllConfirmKeyHandled = false })
  void confirmMarkAllRead()
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
    <!-- Skip link for keyboard and screen-reader users: jumps past the
         sidebar and list straight to the content pane. -->
    <a href="#main-content" class="sr-only focus:not-sr-only focus:absolute focus:z-50 focus:bg-background focus:px-3 focus:py-2 focus:text-sm">Skip to content</a>
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
                <FeedSidebar ref="sidebar" :showing-saving="showingSaved" @select-feed="selectFeed" @select-digest-config="selectDigestConfig" @select-category="selectCategory" @select-section="selectSection" @show-saved="showSaved" @mark-all-read="onSidebarMarkAllRead" />
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
            <DigestResultList v-if="digestMode" :showing-all="digestStore.showingAllDigests" :class="listCollapsed ? '' : 'border-r'" @select="onSelectDigestResult" @mark-all-read="requestMarkAllRead" />
            <ItemList v-else :saved-items="savedItems" :showing-saved="showingSaved" @select-item="selectItem" @mark-all-read="requestMarkAllRead" />
          </SplitterPanel>
          <SplitterResizeHandle class="w-1 hover:w-1.5 bg-border hover:bg-accent transition-all cursor-col-resize" :class="{ hidden: contentMaximized }" />

          <SplitterPanel :default-size="52" :min-size="30">
            <main id="main-content" class="h-full min-h-0">
              <ErrorBoundary>
                <DigestContent v-if="digestMode" />
                <ItemContent v-else :unsave-item="unsaveItem" />
              </ErrorBoundary>
            </main>
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
                <FeedSidebar ref="sidebar" :showing-saving="showingSaved" @select-feed="selectFeed" @select-digest-config="selectDigestConfig" @select-category="selectCategory" @select-section="selectSection" @show-saved="showSaved" @mark-all-read="onSidebarMarkAllRead" />
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
                <DigestResultList v-if="digestMode" compact :showing-all="digestStore.showingAllDigests" class="h-full" :class="listCollapsed ? '' : 'border-b'" @select="onSelectDigestResult" @mark-all-read="requestMarkAllRead" />
                <ItemList v-else compact :saved-items="savedItems" :showing-saved="showingSaved" @select-item="selectItem" @mark-all-read="requestMarkAllRead" />
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
        <!-- A closed drawer must not be reachable by keyboard or assistive
             tech. Sliding it off-screen with -translate-x-full keeps it in the
             accessibility tree and in the tab order, so tabbing from the top of
             the page landed on the hidden sidebar's controls. inert removes the
             whole subtree from interaction; the transition still animates.
             Bound as `cond ? true : undefined`: a false value renders the
             attribute inert="false", and PRESENCE of a boolean attribute means
             inert — so the open drawer would have been inerted too. -->
        <aside
          class="h-drawer fixed left-0 top-12 z-40 w-64 bg-sidebar border-r shadow-xl transition-transform duration-200 flex flex-col"
          :class="sidebarOpen ? 'translate-x-0' : '-translate-x-full'"
          :inert="!sidebarOpen ? true : undefined"
          :aria-hidden="!sidebarOpen ? 'true' : undefined"
        >
          <div class="flex-1 min-h-0 overflow-y-auto p-3">
            <FeedSidebar ref="sidebar" :showing-saving="showingSaved" @select-feed="selectFeed" @select-digest-config="selectDigestConfig" @select-category="selectCategory" @select-section="selectSection" @show-saved="showSaved" @mark-all-read="onSidebarMarkAllRead" />
          </div>
          <div class="shrink-0 flex justify-center border-t border-border/50 py-1.5">
            <VersionBadge />
          </div>
        </aside>

        <!-- List pane. Never unmounted on selection change (remounting resets
             scroll). On mobile, when content is shown, the wrapper leaves the
             flex flow (zero-width + absolute overlay off-screen) so the content
             pane takes the full width — display:none would also zero scrollTop.
             pointer-events-none stops the MOUSE but not the KEYBOARD: the
             off-screen list stayed in the tab order and the accessibility tree,
             so tabbing from the content pane walked into rows the user cannot
             see. inert is the same fix the drawer got above.

             Bound as `cond ? true : undefined`, not `:inert="cond"`: with a
             false value Vue writes the attribute inert="false", and inert is a
             boolean attribute, so PRESENCE means inert. Undefined removes it
             outright, which is unambiguous in jsdom (no `inert` property, so
             every binding becomes an attribute) and in browsers alike. -->
        <div
          class="min-w-0"
          :class="!isTablet && showContentPane ? 'relative w-0 flex-none' : 'flex-1'"
          :inert="!isTablet && showContentPane ? true : undefined"
          :aria-hidden="!isTablet && showContentPane ? 'true' : undefined"
        >
          <div
            class="absolute inset-0 flex flex-col transition-transform duration-200"
            :class="!isTablet && showContentPane ? '-translate-x-full pointer-events-none' : 'static w-full h-full'"
          >
              <DigestResultList v-if="digestMode" :showing-all="digestStore.showingAllDigests" class="flex-1 min-h-0" @select="onSelectDigestResult" @mark-all-read="requestMarkAllRead" />
              <ItemList v-else :saved-items="savedItems" :showing-saved="showingSaved" @select-item="selectItem" @mark-all-read="requestMarkAllRead" />
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

    <!-- The one mark-all-read confirmation. Every trigger — the 'A' shortcut, the
         mid-pane end-of-list button, and all four sidebar controls (feed, category,
         "Feeds" section, digest config, "All Digests") — routes through
         requestMarkAllRead, so scope and confirmation cannot drift apart. -->
    <Dialog
      v-if="markAllTarget"
      :open="true"
      :title="markAllTarget.title"
      :description="markAllTarget.description"
      @update:open="v => { if (!v) closeMarkAllReadDialog() }"
    >
      <div class="flex justify-end gap-2">
        <Button variant="outline" @click="closeMarkAllReadDialog">Cancel</Button>
        <Button ref="markAllConfirm" data-mark-all-read-confirm :disabled="markAllBusy" @click="confirmMarkAllRead" @keydown="onMarkAllConfirmKeyDown">Mark as read</Button>
      </div>
    </Dialog>
  </div>
</template>
