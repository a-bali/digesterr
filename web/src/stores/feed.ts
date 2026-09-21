import { defineStore } from 'pinia'
import { ref, computed } from 'vue'
import { feedApi, itemApi, settingsApi } from '../services/api'
import type { Feed, Item, MultiItem, ItemSort, MarkRelativeOptions } from '../types/api'
import { toastError, toastSuccess } from '../lib/toast'
import { SCORE_FEED_TYPES } from '../lib/feeds'

const PAGE_SIZE = 50
const FILTER_KEY = 'digesterr-filter'

export type FeedSort = ItemSort
export type FeedStatusFilter = 'all' | 'unread' | 'read' | 'digested' | 'saved'

function loadFilter(): 'all' | 'unread' | 'read' | 'digested' | 'saved' {
  try {
    const saved = localStorage.getItem(FILTER_KEY)
    if (saved === 'all' || saved === 'unread' || saved === 'read' || saved === 'digested' || saved === 'saved') return saved
  } catch { /* ignore */ }
  return 'all'
}

function saveFilter(f: string) {
  try { localStorage.setItem(FILTER_KEY, f) } catch { /* ignore */ }
}

export const useFeedStore = defineStore('feed', () => {
  const feeds = ref<Feed[]>([])
  // The signed-in account's AgentMail inbox, from GET /api/settings/email.
  //
  // It is PER-ACCOUNT state, not server-fixed state: the same user can change it
  // in the settings dialog, so a once-per-session cache would go stale the moment
  // they do — the Add Feed dialog would keep saying "(not configured)" until a
  // reload. Hence refreshEmailConfig(), which the settings dialog calls after a
  // successful save.
  //
  // null = not fetched yet, '' = this account has no email configuration.
  const emailInbox = ref<string | null>(null)

  // ensureEmailConfig fetches the inbox once per session. Failures leave it as
  // '' rather than retrying on every dialog open; the email option is simply
  // unavailable, which is the honest outcome when the server cannot say.
  async function ensureEmailConfig() {
    if (emailInbox.value !== null) return
    await refreshEmailConfig()
  }

  // refreshEmailConfig refetches unconditionally. Call it after anything that can
  // change the value (saving or clearing it in the settings dialog).
  async function refreshEmailConfig() {
    try {
      const settings = await settingsApi.getEmail()
      emailInbox.value = settings.configured ? settings.inbox : ''
    } catch {
      emailInbox.value = ''
    }
  }
  const activeFeedId = ref<number | null>(null)
  const activeCategory = ref<string | null>(null) // null = single-feed view, string = category name
  const activeSection = ref<'feeds' | 'digests' | null>(null) // null = not viewing a section
  const items = ref<Item[]>([])
  const multiItems = ref<MultiItem[]>([]) // items with feed metadata for multi-feed views
  const selectedItem = ref<Item | null>(null)
  const filter = ref<'all' | 'unread' | 'read' | 'digested' | 'saved'>(loadFilter())
  const minScore = ref<number | null>(null)
  const sort = ref<FeedSort>('date_desc')
  const loading = ref(false)
  const loadingMore = ref(false)
  const backgroundRefreshing = ref(false)
  const hasMore = ref(false)
  const totalItems = ref(0)
  // Total saved items across all feeds, for the sidebar badge. Separate from
  // unreadCounts because "saved" is a status, not an unread tally, and it is
  // fetched separately so it stays correct while a filter hides items.
  const savedCount = ref(0)
  // The Saved view's item list. It lives in the store, not in HomeView, because
  // it is a fourth place an item can be held and it was the one `applyUpdatedItem`
  // could not reach: marking an item unread from the saved view updated the DB and
  // the content pane while the ROW kept its old status icon until a reload
  // ("mark unread is not showing as unread until I reload the view").
  const savedItems = ref<Item[]>([])

  const activeFeed = computed(() => feeds.value.find(f => f.id === activeFeedId.value) || null)

  // True when viewing a category or "Feeds" section (not a single feed or digest).
  const isMultiView = computed(() => activeCategory.value !== null || activeSection.value === 'feeds')

  // Score filter/sort are only meaningful for feeds whose items carry score
  // data (HN, Lobste.rs, Reddit). The Saved view mixes feed types, so the
  // component treats it as unsupported regardless of this computed.
  const supportsScore = computed(() => {
    if (isMultiView.value) return true // multi-view mixes feed types but some may have scores
    const type = activeFeed.value?.type
    return type != null && SCORE_FEED_TYPES.has(type)
  })

  const unreadCounts = ref<Record<number, number>>({})

  // Monotonic request id to drop stale responses (feed switches, filter changes).
  let fetchReq = 0

  // Loads a single page of items for a feed, replacing or appending to `items`.
  async function loadPage(feedId: number, replace: boolean) {
    const rid = ++fetchReq
    if (replace) {
      loading.value = true
      hasMore.value = false
    } else {
      loadingMore.value = true
    }
    try {
      const resp = await feedApi.getItems(feedId, {
        status: filter.value === 'all' ? undefined : filter.value,
        min_score: minScore.value || undefined,
        sort: sort.value,
        limit: PAGE_SIZE,
        offset: replace ? 0 : items.value.length,
      })
      if (rid !== fetchReq) return
      if (replace) {
        items.value = resp.items
        totalItems.value = resp.total ?? 0
      } else {
        items.value.push(...resp.items)
      }
      hasMore.value = resp.has_more
    } catch (e) {
      toastError(e, 'Failed to load items')
    } finally {
      if (replace) loading.value = false
      else loadingMore.value = false
    }
  }

  // The "Feeds" section is scoped by an explicit list of feed ids, which the
  // caller can only build once the feed list has loaded. Selecting the section
  // before that (cold deep link to /feeds, or a reload with the section
  // restored) used to send `feed_ids=[]` — the query-string helper drops empty
  // values, so the request arrived with NO scope at all and the API answered 400
  // "feed_ids or category is required". Load the feeds first, and never pass an
  // empty list.
  async function sectionFeedIDs(): Promise<number[] | null> {
    if (feeds.value.length === 0) {
      try {
        feeds.value = await feedApi.list()
      } catch {
        return null
      }
    }
    const ids = feeds.value.map(f => f.id)
    return ids.length > 0 ? ids : null
  }

  // Loads items for a multi-feed view (category or "Feeds" section).
  async function loadMultiPage(replace: boolean) {
    const rid = ++fetchReq
    if (replace) {
      loading.value = true
      hasMore.value = false
    } else {
      loadingMore.value = true
    }
    try {
      const opts: Parameters<typeof feedApi.multiItems>[0] = {
        status: filter.value === 'all' ? undefined : filter.value,
        min_score: minScore.value || undefined,
        sort: sort.value,
        limit: PAGE_SIZE,
        offset: replace ? 0 : multiItems.value.length,
      }
      if (activeCategory.value) {
        opts.category = activeCategory.value
      } else if (activeSection.value === 'feeds') {
        const ids = await sectionFeedIDs()
        // No feeds at all (or the list could not be loaded): send nothing rather
        // than an unscoped request the API would reject.
        if (ids === null) {
          if (replace) {
            multiItems.value = []
            totalItems.value = 0
            hasMore.value = false
          }
          return
        }
        opts.feed_ids = ids
      }
      const resp = await feedApi.multiItems(opts)
      if (rid !== fetchReq) return
      if (replace) {
        multiItems.value = resp.items
        totalItems.value = resp.total ?? 0
      } else {
        multiItems.value.push(...resp.items)
      }
      hasMore.value = resp.has_more
    } catch (e) {
      toastError(e, 'Failed to load items')
    } finally {
      if (replace) loading.value = false
      else loadingMore.value = false
    }
  }

  async function fetchFeeds() {
    try {
      feeds.value = await feedApi.list()
      unreadCounts.value = await feedApi.unreadCounts()
    } catch (e) {
      toastError(e, 'Failed to load feeds')
    }
  }

  async function setActiveFeed(id: number) {
    activeFeedId.value = id
    activeCategory.value = null
    activeSection.value = null
    selectedItem.value = null
    items.value = []
    multiItems.value = []
    totalItems.value = 0
    // Seed the score filter from the feed's own threshold so the FilterBar's
    // label tells the truth and the pane matches what the API returns by
    // default. A feed with no threshold clears the filter rather than keeping
    // the previous feed's value, which would hide most of its items.
    //
    // The feeds list may not have loaded yet on a cold deep link, so an
    // unknown feed leaves the control alone instead of guessing.
    const feed = feeds.value.find(f => f.id === id)
    if (feed) minScore.value = feed.min_score > 0 ? feed.min_score : null
    await loadPage(id, true)
  }

  async function setActiveCategory(name: string) {
    activeFeedId.value = null
    activeCategory.value = name
    activeSection.value = null
    selectedItem.value = null
    items.value = []
    multiItems.value = []
    totalItems.value = 0
    // Mixed thresholds live server-side per row here, so the FilterBar's
    // single value starts cleared rather than carrying the last feed's seed.
    minScore.value = null
    await loadMultiPage(true)
  }

  async function setActiveSection(section: 'feeds' | 'digests') {
    activeFeedId.value = null
    activeCategory.value = null
    activeSection.value = section
    selectedItem.value = null
    items.value = []
    multiItems.value = []
    totalItems.value = 0
    // Same as categories: mixed thresholds live server-side per row.
    minScore.value = null
    if (section === 'feeds') {
      await loadMultiPage(true)
    }
    // 'digests' section is handled by digestStore, not here.
  }

  function clearActiveView() {
    activeFeedId.value = null
    activeCategory.value = null
    activeSection.value = null
    selectedItem.value = null
    items.value = []
    multiItems.value = []
    totalItems.value = 0
  }

  async function fetchFirstPage() {
    if (isMultiView.value) {
      await loadMultiPage(true)
    } else if (activeFeedId.value) {
      await loadPage(activeFeedId.value, true)
    }
  }

  async function refreshItems(opts?: { silent?: boolean; replace?: boolean }) {
    if (isMultiView.value) {
      // Multi-view refresh.
      const rid = ++fetchReq
      if (opts?.silent) backgroundRefreshing.value = true
      else loading.value = true
      try {
        const refreshOpts: Parameters<typeof feedApi.multiItems>[0] = {
          status: filter.value === 'all' ? undefined : filter.value,
          min_score: minScore.value || undefined,
          sort: sort.value,
          limit: PAGE_SIZE,
          offset: 0,
        }
        if (activeCategory.value) refreshOpts.category = activeCategory.value
        else if (activeSection.value === 'feeds') {
          const ids = await sectionFeedIDs()
          if (ids === null) return
          refreshOpts.feed_ids = ids
        }
        const resp = await feedApi.multiItems(refreshOpts)
        if (rid !== fetchReq) return
        if (opts?.silent && !opts?.replace) {
          // Silent refresh: merge to preserve scroll.
          const localMap = new Map(multiItems.value.map(i => [i.id, i]))
          const merged: MultiItem[] = []
          const seenLocal = new Set<number>()
          for (const fresh of resp.items) {
            const old = localMap.get(fresh.id)
            if (old) {
              seenLocal.add(fresh.id)
              if (fresh.status !== old.status || fresh.points !== old.points ||
                  fresh.comments_count !== old.comments_count || fresh.title !== old.title) {
                merged.push(fresh)
              } else {
                merged.push(old)
              }
            } else {
              merged.push(fresh)
            }
          }
          for (const old of multiItems.value) {
            if (!seenLocal.has(old.id)) merged.push(old)
          }
          multiItems.value = merged
        } else {
          multiItems.value = resp.items
        }
        hasMore.value = resp.has_more
        totalItems.value = resp.total ?? 0
      } catch (e) {
        if (!opts?.silent) toastError(e, 'Failed to load items')
      } finally {
        if (rid === fetchReq) {
          loading.value = false
          backgroundRefreshing.value = false
        }
      }
      return
    }

    if (!activeFeedId.value) return
    const rid = ++fetchReq
    if (opts?.silent) backgroundRefreshing.value = true
    else loading.value = true
    try {
      const resp = await feedApi.getItems(activeFeedId.value, {
        status: filter.value === 'all' ? undefined : filter.value,
        min_score: minScore.value || undefined,
        sort: sort.value,
        limit: PAGE_SIZE,
        offset: 0,
      })
      if (rid !== fetchReq) return

      if (opts?.silent && !opts?.replace) {
        const localMap = new Map(items.value.map(i => [i.id, i]))
        const merged: Item[] = []
        const seenLocal = new Set<number>()
        for (const fresh of resp.items) {
          const old = localMap.get(fresh.id)
          if (old) {
            seenLocal.add(fresh.id)
            if (fresh.status !== old.status || fresh.points !== old.points ||
                fresh.comments_count !== old.comments_count || fresh.title !== old.title) {
              merged.push(fresh)
            } else {
              merged.push(old)
            }
          } else {
            merged.push(fresh)
          }
        }
        for (const old of items.value) {
          if (!seenLocal.has(old.id)) merged.push(old)
        }
        items.value = merged
      } else {
        items.value = resp.items
      }
      hasMore.value = resp.has_more
      totalItems.value = resp.total ?? 0
    } catch (e) {
      if (!opts?.silent) toastError(e, 'Failed to load items')
    } finally {
      if (rid === fetchReq) {
        loading.value = false
        backgroundRefreshing.value = false
      }
    }
  }

  async function fetchMoreItems() {
    if (isMultiView.value) {
      if (!hasMore.value || loadingMore.value || loading.value) return
      await loadMultiPage(false)
      return
    }
    if (!activeFeedId.value || !hasMore.value || loadingMore.value || loading.value) return
    await loadPage(activeFeedId.value, false)
  }

  async function selectItem(item: Item) {
    // Set preview immediately so the content pane renders while we fetch.
    selectedItem.value = item
    try {
      const full = await itemApi.getItem(item.id)
      selectedItem.value = full
    } catch {
      // Preview data stays — already set above.
    }
  }

  function changeFilter(f: typeof filter.value) {
    filter.value = f
    saveFilter(f)
    if (isMultiView.value || activeFeedId.value) {
      fetchFirstPage()
    }
  }

  function changeSort(s: FeedSort) {
    if (s === sort.value) return
    sort.value = s
    if (isMultiView.value || activeFeedId.value) {
      fetchFirstPage()
    }
  }

  function changeMinScore(n: number | null) {
    minScore.value = n
    if (isMultiView.value || activeFeedId.value) {
      fetchFirstPage()
    }
  }

  // applyUpdatedItem writes a server-returned item into every place that holds
  // it: the feed's item list, the multi-view list, and the selection.
  //
  // The multiItems row keeps its feed_name/feed_type: the item endpoints return
  // a plain Item, which carries neither, so replacing the row wholesale makes
  // the source-feed label vanish from the middle pane (see "MultiItem field
  // preservation on status update").
  function applyUpdatedItem(updated: Item) {
    const idx = items.value.findIndex(i => i.id === updated.id)
    if (idx !== -1) items.value[idx] = updated
    const midx = multiItems.value.findIndex(i => i.id === updated.id)
    if (midx !== -1) {
      const old = multiItems.value[midx]
      ;(multiItems.value[midx] as any) = { ...updated, feed_name: old.feed_name, feed_type: old.feed_type }
    }
    // The saved view is a separate list of the same items. Saved-ness and read
    // state are independent, so a status change does NOT remove the row: an item
    // marked unread is still saved and must stay, re-rendered with its new icon.
    // (Only unsaving removes it, and that path filters the array itself.)
    const sidx = savedItems.value.findIndex(i => i.id === updated.id)
    if (sidx !== -1) savedItems.value[sidx] = updated
    if (selectedItem.value?.id === updated.id) selectedItem.value = updated
  }

  // updateItemStatus writes read state and/or the saved flag. They are separate
  // fields on the API — `saved` is NOT a status — so each caller sends only what
  // it means to change: marking read must not alter savedness, and vice versa.
  //
  // Deliberately NO success toast: this is the highest-frequency action in the app
  // (every row click, every star toggle, every swipe) and the row's own badge and
  // dimming already report the outcome inline. A toast per click would bury the
  // messages that do need attention. Failures still toast, because a click that
  // silently does nothing is the case a user must hear about.
  async function updateItemStatus(item: Item, status?: string, saved?: boolean) {
    try {
      const body: { status?: string; saved?: boolean } = {}
      if (status !== undefined) body.status = status
      if (saved !== undefined) body.saved = saved
      const updated = await itemApi.updateStatus(item.id, body)
      applyUpdatedItem(updated)
      // Keep the saved badge in step without a round trip: the transition is
      // already known here. Clamped because the counter is a total maintained
      // across many sources, and a double-fire must not drive it negative.
      const wasSaved = item.is_saved === 1
      const nowSaved = saved ?? (updated.is_saved === 1)
      if (!wasSaved && nowSaved) savedCount.value++
      else if (wasSaved && !nowSaved) savedCount.value = Math.max(0, savedCount.value - 1)
      fetchUnreadCounts()
    } catch (e) {
      toastError(e, 'Failed to update item')
    }
  }

  // deleteExtraction drops an item's extracted body, so the panes fall back to
  // the original content from the feed. No list refetch: the response carries
  // the whole updated item, and a refetch would discard the reader's position.
  async function deleteExtraction(item: Item) {
    try {
      applyUpdatedItem(await itemApi.deleteExtracted(item.id))
      toastSuccess('Extracted content removed', itemTitle(item))
    } catch (e) {
      toastError(e, 'Failed to delete extracted content', itemTitle(item))
    }
  }

  // fetchSavedCount refreshes the sidebar's saved total. Failures are silent:
  // the badge is decoration, and a stale number beats an error toast.
  async function fetchSavedCount() {
    try {
      savedCount.value = (await feedApi.savedCount()).count
    } catch {
      // silent
    }
  }

  async function fetchUnreadCounts() {
    try {
      unreadCounts.value = await feedApi.unreadCounts()
    } catch {
      // silent
    }
  }

  // Mark a single feed read. Lives in the store rather than the sidebar because
  // the sidebar no longer owns the confirmation dialog — HomeView funnels every
  // mark-all-read scope (feed, category, section) through one place, so the store
  // has to expose the single-feed action too.
  async function markFeedRead(id: number) {
    const name = feeds.value.find(f => f.id === id)?.name
    try {
      await feedApi.markRead(id)
      await fetchFeeds()
      if (activeFeedId.value === id) await fetchFirstPage()
      await fetchUnreadCounts()
      toastSuccess('Feed marked read', name)
    } catch (e) {
      toastError(e, 'Failed to mark feed read', name)
    }
  }

  // The scope the reader is looking at, named for the toast. The category is the
  // useful label when one is active; otherwise the "All Feeds" section.
  function markAllReadScope(category?: string): string {
    return category ? `category “${category}”` : 'all feeds'
  }

  // Every mark-read path reports both outcomes. These used to have no error
  // handling at all, so a failure rejected into the caller's `await` and the user
  // saw the panes simply not change.
  async function markAllRead(category?: string) {
    try {
      await feedApi.markReadAll(category)
      // Refresh items and unread counts after marking read.
      await fetchFirstPage()
      await fetchUnreadCounts()
      toastSuccess('Marked all as read', markAllReadScope(category))
    } catch (e) {
      toastError(e, 'Failed to mark read', markAllReadScope(category))
    }
  }

  // The server resolves "above"/"below" in SQL, so it needs the ordering and
  // filter the middle pane is showing to include rows the client has not
  // lazily loaded yet.
  function markRelativeOptions(): MarkRelativeOptions {
    const opts: MarkRelativeOptions = { sort: sort.value, min_score: minScore.value ?? 0 }
    if (activeCategory.value) opts.category = activeCategory.value
    else if (activeSection.value === 'feeds') opts.feed_ids = feeds.value.map(f => f.id).join(',')
    return opts
  }

  async function markReadAbove(item: any) {
    const label = relativeScopeLabel(item)
    try {
      await itemApi.markReadAbove(item.id, markRelativeOptions())
      await fetchFirstPage()
      await fetchUnreadCounts()
      toastSuccess('Marked above as read', label)
    } catch (e) {
      toastError(e, 'Failed to mark above as read', label)
    }
  }

  async function markReadBelow(item: any) {
    const label = relativeScopeLabel(item)
    try {
      await itemApi.markReadBelow(item.id, markRelativeOptions())
      await fetchFirstPage()
      await fetchUnreadCounts()
      toastSuccess('Marked below as read', label)
    } catch (e) {
      toastError(e, 'Failed to mark below as read', label)
    }
  }

  // The item's title, for naming it in a toast. Undefined when the title is absentor
  // blank, so the helper renders no second line rather than an empty one.
  function itemTitle(item: any): string | undefined {
    const title = typeof item?.title === 'string' ? item.title.trim() : ''
    return title || undefined
  }

  // "from <item title>" — the range is anchored on the clicked row, so its title is
  // what tells the reader which boundary was used. Truncated because titles are
  // arbitrary length and this is one line of a toast.
  function relativeScopeLabel(item: any): string | undefined {
    const title = typeof item?.title === 'string' ? item.title.trim() : ''
    if (!title) return undefined
    return `from “${title.length > 40 ? title.slice(0, 40) + '…' : title}”`
  }

  // unsaveItem clears the saved flag and drops the row from the Saved view.
  //
  // It deliberately does NOT write a read status: unsaving is not a read-state
  // change, and the old code forced 'read' here, which silently un-read nothing
  // but also meant a saved-and-unread item could never stay unread. The item
  // keeps whatever read state it had.
  //
  // Like updateItemStatus, no success toast: the row leaves the list, which is
  // its own confirmation. (updateItemStatus's error toast still covers failure,
  // since it is the one that performs the write.)
  async function unsaveItem(item: Item) {
    await updateItemStatus(item, undefined, false)
    savedItems.value = savedItems.value.filter(i => i.id !== item.id)
  }

  // fetchSavedItems loads the Saved view's list in one server-side query.
  // Server-side per-feed query, so it is kept here rather than in the view
  // that happens to render it. Archive feeds are included, so items orphaned
  // by a feed deletion come back with their provenance.
  async function fetchSavedItems() {
    try {
      savedItems.value = (await feedApi.savedItems()).items as Item[]
    } catch {
      // Saved view keeps its previous list on failure; badge count is separate.
    }
  }

  return {
    feeds, activeFeedId, activeCategory, activeSection, items, multiItems, selectedItem, savedItems, savedCount, filter, minScore, sort, loading, loadingMore, hasMore, totalItems,
    activeFeed, isMultiView, supportsScore, unreadCounts, backgroundRefreshing,
    fetchFeeds, setActiveFeed, setActiveCategory, setActiveSection, clearActiveView, fetchFirstPage, refreshItems, fetchMoreItems,
    selectItem, updateItemStatus, unsaveItem, deleteExtraction, changeFilter, changeSort, changeMinScore, fetchUnreadCounts, fetchSavedItems, fetchSavedCount, markFeedRead, markAllRead, markReadAbove, markReadBelow,
    emailInbox, ensureEmailConfig, refreshEmailConfig,
  }
})
