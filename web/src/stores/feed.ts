import { defineStore } from 'pinia'
import { ref, computed } from 'vue'
import { feedApi, itemApi, healthApi } from '../services/api'
import type { Feed, Item, MultiItem, ItemSort, MarkRelativeOptions } from '../types/api'
import { toastError } from '../lib/toast'
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
  // The instance's AgentMail inbox, fetched from /api/health on first use.
  // The inbox is server-wide (one mailbox serves every email feed), so it is
  // server-reported state rather than something the user types: the dialog
  // shows it as fixed context and only the suffix is editable.
  //
  // null = not fetched yet, '' = server has the feature off.
  const emailInbox = ref<string | null>(null)

  // ensureEmailConfig fetches the inbox once per session. Failures leave it as
  // '' rather than retrying on every dialog open; the email option is simply
  // unavailable, which is the honest outcome when the server cannot say.
  async function ensureEmailConfig() {
    if (emailInbox.value !== null) return
    try {
      const health = await healthApi.get()
      emailInbox.value = health.email_inbox ?? ''
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
        // "Feeds" section: get all feed IDs from the feeds list.
        opts.feed_ids = feeds.value.map(f => f.id)
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
        else if (activeSection.value === 'feeds') refreshOpts.feed_ids = feeds.value.map(f => f.id)
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

  async function updateItemStatus(item: Item, status: string) {
    try {
      const updated = await itemApi.updateStatus(item.id, status)
      // Update in both items and multiItems arrays.
      const idx = items.value.findIndex(i => i.id === item.id)
      if (idx !== -1) items.value[idx] = updated
      const midx = multiItems.value.findIndex(i => i.id === item.id)
      if (midx !== -1) {
        const old = multiItems.value[midx]
        ;(multiItems.value[midx] as any) = { ...updated, feed_name: old.feed_name, feed_type: old.feed_type }
      }
      if (selectedItem.value?.id === item.id) selectedItem.value = updated
      fetchUnreadCounts()
    } catch (e) {
      toastError(e, 'Failed to update item')
    }
  }

  async function fetchUnreadCounts() {
    try {
      unreadCounts.value = await feedApi.unreadCounts()
    } catch {
      // silent
    }
  }

  async function markAllRead(category?: string) {
    await feedApi.markReadAll(category)
    // Refresh items and unread counts after marking read.
    await fetchFirstPage()
    await fetchUnreadCounts()
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
    await itemApi.markReadAbove(item.id, markRelativeOptions())
    await fetchFirstPage()
    await fetchUnreadCounts()
  }

  async function markReadBelow(item: any) {
    await itemApi.markReadBelow(item.id, markRelativeOptions())
    await fetchFirstPage()
    await fetchUnreadCounts()
  }

  return {
    feeds, activeFeedId, activeCategory, activeSection, items, multiItems, selectedItem, filter, minScore, sort, loading, loadingMore, hasMore, totalItems,
    activeFeed, isMultiView, supportsScore, unreadCounts, backgroundRefreshing,
    fetchFeeds, setActiveFeed, setActiveCategory, setActiveSection, clearActiveView, fetchFirstPage, refreshItems, fetchMoreItems,
    selectItem, updateItemStatus, changeFilter, changeSort, changeMinScore, fetchUnreadCounts, markAllRead, markReadAbove, markReadBelow,
    emailInbox, ensureEmailConfig,
  }
})
