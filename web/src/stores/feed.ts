import { defineStore } from 'pinia'
import { ref, computed } from 'vue'
import { feedApi, itemApi } from '../services/api'
import type { Feed, Item } from '../types/api'
import { toastError } from '../lib/toast'
import { SCORE_FEED_TYPES } from '../lib/feeds'

const PAGE_SIZE = 50
const FILTER_KEY = 'digesterr-filter'

export type FeedSort = 'date_desc' | 'date_asc' | 'score_desc' | 'score_asc'
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
  const activeFeedId = ref<number | null>(null)
  const items = ref<Item[]>([])
  const selectedItem = ref<Item | null>(null)
  const filter = ref<'all' | 'unread' | 'read' | 'digested' | 'saved'>(loadFilter())
  const minScore = ref<number | null>(null)
  const sort = ref<FeedSort>('date_desc')
  const loading = ref(false)
  const loadingMore = ref(false)
  const hasMore = ref(false)
  const totalItems = ref(0)

  const activeFeed = computed(() => feeds.value.find(f => f.id === activeFeedId.value) || null)

  // Score filter/sort are only meaningful for feeds whose items carry score
  // data (HN, Lobste.rs, Reddit). The Saved view mixes feed types, so the
  // component treats it as unsupported regardless of this computed.
  const supportsScore = computed(() => {
    const type = activeFeed.value?.type
    return type != null && SCORE_FEED_TYPES.has(type)
  })

  const unreadCounts = ref<Record<number, number>>({})

  // Monotonic request id to drop stale responses (feed switches, filter changes).
  let fetchReq = 0

  // Loads a single page of items for a feed, replacing or appending to `items`.
  // Replace keeps existing rows visible while in flight (dimmed via `loading`
  // in ItemList) so the pane never flashes empty.
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
    selectedItem.value = null
    // Feed switch: empty the list immediately so the loading skeleton shows
    // instead of the previous feed's rows.
    items.value = []
    totalItems.value = 0
    await loadPage(id, true)
  }

  async function fetchFirstPage() {
    if (!activeFeedId.value) return
    await loadPage(activeFeedId.value, true)
  }

  async function refreshItems() {
    if (!activeFeedId.value) return
    const rid = ++fetchReq
    loading.value = true
    try {
      const resp = await feedApi.getItems(activeFeedId.value, {
        status: filter.value === 'all' ? undefined : filter.value,
        min_score: minScore.value || undefined,
        sort: sort.value,
        limit: PAGE_SIZE,
        offset: 0,
      })
      if (rid !== fetchReq) return
      // Replace in-place: avoid clearing the array first so the scroll
      // position is preserved when this is called from the visibility-change
      // handler on tab refocus.
      items.value = resp.items
      hasMore.value = resp.has_more
      totalItems.value = resp.total ?? 0
    } catch (e) {
      toastError(e, 'Failed to load items')
    } finally {
      if (rid === fetchReq) loading.value = false
    }
  }

  async function fetchMoreItems() {
    if (!activeFeedId.value || !hasMore.value || loadingMore.value || loading.value) return
    await loadPage(activeFeedId.value, false)
  }

  function selectItem(item: Item) {
    selectedItem.value = item
  }

  function changeFilter(f: typeof filter.value) {
    filter.value = f
    saveFilter(f)
    if (activeFeedId.value) {
      fetchFirstPage()
    }
  }

  function changeSort(s: FeedSort) {
    if (s === sort.value) return
    sort.value = s
    if (activeFeedId.value) {
      fetchFirstPage()
    }
  }

  // minScore: null (or 0) means "any score"; only positive values filter.
  function changeMinScore(n: number | null) {
    minScore.value = n
    if (activeFeedId.value) {
      fetchFirstPage()
    }
  }

  async function updateItemStatus(item: Item, status: string) {
    try {
      const updated = await itemApi.updateStatus(item.id, status)
      const idx = items.value.findIndex(i => i.id === item.id)
      if (idx !== -1) items.value[idx] = updated
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

  return {
    feeds, activeFeedId, items, selectedItem, filter, minScore, sort, loading, loadingMore, hasMore, totalItems,
    activeFeed, supportsScore, unreadCounts,
    fetchFeeds, setActiveFeed, fetchFirstPage, refreshItems, fetchMoreItems,
    selectItem, updateItemStatus, changeFilter, changeSort, changeMinScore, fetchUnreadCounts,
  }
})
