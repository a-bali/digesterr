import { defineStore } from 'pinia'
import { ref, computed } from 'vue'
import { toast } from 'vue-sonner'
import { feedApi, itemApi, ApiError } from '../services/api'
import type { Feed, Item } from '../types/api'

const PAGE_SIZE = 50
const FILTER_KEY = 'digesterr-filter'

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
  const loading = ref(false)
  const loadingMore = ref(false)
  const hasMore = ref(false)

  const activeFeed = computed(() => feeds.value.find(f => f.id === activeFeedId.value) || null)

  const unreadCounts = ref<Record<number, number>>({})

  // Monotonic request id to drop stale responses (feed switches, filter changes).
  let fetchReq = 0

  function toastError(msg: string, e: unknown) {
    toast.error(msg, { description: e instanceof ApiError ? e.message : undefined })
  }

  // Loads a single page of items for a feed, replacing or appending to `items`.
  async function loadPage(feedId: number, replace: boolean) {
    const rid = ++fetchReq
    if (replace) {
      loading.value = true
      items.value = []
      hasMore.value = false
    } else {
      loadingMore.value = true
    }
    try {
      const resp = await feedApi.getItems(feedId, {
        status: filter.value === 'all' ? undefined : filter.value,
        limit: PAGE_SIZE,
        offset: replace ? 0 : items.value.length,
      })
      if (rid !== fetchReq) return
      if (replace) items.value = resp.items
      else items.value.push(...resp.items)
      hasMore.value = resp.has_more
    } catch (e) {
      toastError('Failed to load items', e)
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
      toastError('Failed to load feeds', e)
    }
  }

  async function setActiveFeed(id: number) {
    activeFeedId.value = id
    selectedItem.value = null
    await loadPage(id, true)
  }

  async function fetchFirstPage() {
    if (!activeFeedId.value) return
    await loadPage(activeFeedId.value, true)
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

  async function updateItemStatus(item: Item, status: string) {
    try {
      const updated = await itemApi.updateStatus(item.id, status)
      const idx = items.value.findIndex(i => i.id === item.id)
      if (idx !== -1) items.value[idx] = updated
      if (selectedItem.value?.id === item.id) selectedItem.value = updated
      fetchUnreadCounts()
    } catch (e) {
      toastError('Failed to update item', e)
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
    feeds, activeFeedId, items, selectedItem, filter, loading, loadingMore, hasMore,
    activeFeed, unreadCounts,
    fetchFeeds, setActiveFeed, fetchFirstPage, fetchMoreItems,
    selectItem, updateItemStatus, changeFilter, fetchUnreadCounts,
  }
})
