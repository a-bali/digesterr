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

  let fetchReq = 0

  async function fetchFeeds() {
    try {
      feeds.value = await feedApi.list()
      unreadCounts.value = await feedApi.unreadCounts()
    } catch (e) {
      toast.error('Failed to load feeds', { description: e instanceof ApiError ? e.message : undefined })
    }
  }

  async function setActiveFeed(id: number) {
    activeFeedId.value = id
    selectedItem.value = null
    fetchReq++
    loading.value = true
    items.value = []
    hasMore.value = false
    try {
      const rid = fetchReq
      const resp = await feedApi.getItems(id, {
        status: filter.value === 'all' ? undefined : filter.value,
        limit: PAGE_SIZE,
        offset: 0,
      })
      if (rid !== fetchReq) return
      items.value = resp.items
      hasMore.value = resp.has_more
    } catch (e) {
      toast.error('Failed to load items', { description: e instanceof ApiError ? e.message : undefined })
    } finally {
      loading.value = false
    }
  }

  async function fetchFirstPage() {
    if (!activeFeedId.value) return
    fetchReq++
    loading.value = true
    items.value = []
    hasMore.value = false
    try {
      const rid = fetchReq
      const resp = await feedApi.getItems(activeFeedId.value, {
        status: filter.value === 'all' ? undefined : filter.value,
        limit: PAGE_SIZE,
        offset: 0,
      })
      if (rid !== fetchReq) return
      items.value = resp.items
      hasMore.value = resp.has_more
    } catch (e) {
      toast.error('Failed to load items', { description: e instanceof ApiError ? e.message : undefined })
    } finally {
      loading.value = false
    }
  }

  async function fetchMoreItems() {
    if (!activeFeedId.value || !hasMore.value || loadingMore.value || loading.value) return
    loadingMore.value = true
    try {
      const resp = await feedApi.getItems(activeFeedId.value, {
        status: filter.value === 'all' ? undefined : filter.value,
        limit: PAGE_SIZE,
        offset: items.value.length,
      })
      items.value.push(...resp.items)
      hasMore.value = resp.has_more
    } catch (e) {
      toast.error('Failed to load more items', { description: e instanceof ApiError ? e.message : undefined })
    } finally {
      loadingMore.value = false
    }
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
    } catch (e) {
      toast.error('Failed to update item', { description: e instanceof ApiError ? e.message : undefined })
    }
  }

  async function fetchItems() {
    if (!activeFeedId.value) return
    fetchReq++
    loading.value = true
    items.value = []
    hasMore.value = false
    try {
      const rid = fetchReq
      const resp = await feedApi.getItems(activeFeedId.value, {
        status: filter.value === 'all' ? undefined : filter.value,
        limit: PAGE_SIZE,
        offset: 0,
      })
      if (rid !== fetchReq) return
      items.value = resp.items
      hasMore.value = resp.has_more
    } catch (e) {
      toast.error('Failed to load items', { description: e instanceof ApiError ? e.message : undefined })
    } finally {
      loading.value = false
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
    fetchFeeds, setActiveFeed, fetchFirstPage, fetchMoreItems, fetchItems,
    selectItem, updateItemStatus, changeFilter, fetchUnreadCounts,
  }
})
