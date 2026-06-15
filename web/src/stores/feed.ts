import { defineStore } from 'pinia'
import { ref, computed } from 'vue'
import { toast } from 'vue-sonner'
import { feedApi, itemApi, ApiError } from '../services/api'
import type { Feed, Item } from '../types/api'

export const useFeedStore = defineStore('feed', () => {
  const feeds = ref<Feed[]>([])
  const activeFeedId = ref<number | null>(null)
  const items = ref<Item[]>([])
  const selectedItem = ref<Item | null>(null)
  const filter = ref<'all' | 'unread' | 'read' | 'digested' | 'saved'>('all')
  const loading = ref(false)

  const activeFeed = computed(() => feeds.value.find(f => f.id === activeFeedId.value) || null)

  const filteredItems = computed(() => {
    if (filter.value === 'all') return items.value
    return items.value.filter(i => i.status === filter.value)
  })

  async function fetchFeeds() {
    try {
      feeds.value = await feedApi.list()
    } catch (e) {
      toast.error('Failed to load feeds', { description: e instanceof ApiError ? e.message : undefined })
    }
  }

  async function setActiveFeed(id: number) {
    activeFeedId.value = id
    selectedItem.value = null
    filter.value = 'all'
    await fetchItems()
  }

  async function fetchItems() {
    if (!activeFeedId.value) return
    loading.value = true
    try {
      items.value = await feedApi.getItems(activeFeedId.value)
    } catch (e) {
      toast.error('Failed to load items', { description: e instanceof ApiError ? e.message : undefined })
    } finally {
      loading.value = false
    }
  }

  function selectItem(item: Item) {
    selectedItem.value = item
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

  return {
    feeds, activeFeedId, items, selectedItem, filter, loading,
    activeFeed, filteredItems,
    fetchFeeds, setActiveFeed, fetchItems, selectItem, updateItemStatus,
  }
})
