import { defineStore } from 'pinia'
import { ref, computed } from 'vue'
import { toast } from 'vue-sonner'
import { digestApi } from '../services/api'
import type { DigestConfig, DigestResult, DigestResultItemWithData } from '../types/api'
import { toastError } from '../lib/toast'

const PAGE_SIZE = 50

export const useDigestStore = defineStore('digest', () => {
  const configs = ref<DigestConfig[]>([])
  const activeConfigId = ref<number | null>(null)
  const results = ref<DigestResult[]>([])
  const selectedResult = ref<DigestResult | null>(null)
  const resultItems = ref<DigestResultItemWithData[]>([])
  const loading = ref(false)
  // True while the 10s poll / visibility-change background refresh runs;
  // drives the corner pulse indicator, not any list UI.
  const backgroundRefreshing = ref(false)
  const generating = ref(false)
  const loadingMore = ref(false)
  const hasMore = ref(false)
  const unreadCounts = ref<Record<number, number>>({})

  const activeConfig = computed(() => configs.value.find(c => c.id === activeConfigId.value) || null)

  // Monotonic request id to drop stale responses (config switches, refreshes).
  let resultsReq = 0

  function normalize(r: DigestResult): DigestResult {
    if (r.status === 'new') r.status = 'unread'
    return r
  }

  async function fetchConfigs() {
    try {
      configs.value = await digestApi.listConfigs()
    } catch (e) {
      toastError(e, 'Failed to load digest configs')
    }
  }

  async function createConfig(name: string, schedule: string, config: string) {
    try {
      const cfg = await digestApi.createConfig({ name, schedule, config })
      configs.value.push(cfg)
      return cfg
    } catch (e) {
      toastError(e, 'Failed to create digest config')
      return null
    }
  }

  async function updateConfig(id: number, name: string, schedule: string, config: string) {
    try {
      const updated = await digestApi.updateConfig(id, { name, schedule, config })
      const idx = configs.value.findIndex(c => c.id === id)
      if (idx !== -1) configs.value[idx] = updated
    } catch (e) {
      toastError(e, 'Failed to update digest config')
    }
  }

  async function deleteConfig(id: number) {
    try {
      await digestApi.deleteConfig(id)
      configs.value = configs.value.filter(c => c.id !== id)
      if (activeConfigId.value === id) {
        // Invalidate any in-flight results request for the deleted config.
        resultsReq++
        activeConfigId.value = null
        results.value = []
        selectedResult.value = null
        resultItems.value = []
      }
    } catch (e) {
      toastError(e, 'Failed to delete digest config')
    }
  }

  async function setActiveConfig(id: number) {
    const rid = ++resultsReq
    activeConfigId.value = id
    selectedResult.value = null
    resultItems.value = []
    loading.value = true
    hasMore.value = false
    try {
      const resp = await digestApi.listResults(id, { limit: PAGE_SIZE, offset: 0 })
      if (rid !== resultsReq) return
      results.value = resp.results.map(normalize)
      hasMore.value = resp.has_more
    } catch (e) {
      if (rid !== resultsReq) return
      toastError(e, 'Failed to load digest results')
    } finally {
      if (rid === resultsReq) loading.value = false
    }
  }

  async function selectResult(result: DigestResult) {
    selectedResult.value = result
    loading.value = true
    try {
      resultItems.value = await digestApi.listResultItems(result.id)
    } catch (e) {
      toastError(e, 'Failed to load result items')
    } finally {
      loading.value = false
    }
  }

  async function generateNow(configId: number) {
    generating.value = true
    try {
      const result = normalize(await digestApi.generate(configId))
      if (activeConfigId.value === configId) {
        results.value.unshift(result)
      }
      toast.success('Digest generated')
      return result
    } catch (e) {
      toastError(e, 'Digest generation failed')
      return null
    } finally {
      generating.value = false
    }
  }

  async function markResultRead(resultId: number) {
    try {
      const updated = normalize(await digestApi.updateResultStatus(resultId, 'read'))
      const idx = results.value.findIndex(r => r.id === resultId)
      if (idx !== -1) results.value[idx] = updated
      if (selectedResult.value?.id === resultId) selectedResult.value = updated
      fetchUnreadCounts()
    } catch (e) {
      toastError(e, 'Failed to mark digest as read')
    }
  }

  async function deleteResult(resultId: number) {
    try {
      await digestApi.deleteResult(resultId)
      results.value = results.value.filter(r => r.id !== resultId)
      if (selectedResult.value?.id === resultId) {
        selectedResult.value = null
        resultItems.value = []
      }
    } catch (e) {
      toastError(e, 'Failed to delete digest')
    }
  }

  async function fetchMoreResults() {
    const cfgId = activeConfigId.value
    if (!cfgId || !hasMore.value || loadingMore.value || loading.value) return
    const rid = ++resultsReq
    // Capture the offset at request time so a newer request can't shift the
    // list out from under this page append.
    const offset = results.value.length
    loadingMore.value = true
    try {
      const resp = await digestApi.listResults(cfgId, { limit: PAGE_SIZE, offset })
      if (rid !== resultsReq) return
      results.value.push(...resp.results.map(normalize))
      hasMore.value = resp.has_more
    } catch {
      // silent
    } finally {
      // Only one loadMore can be in flight (guarded above), so always clear.
      loadingMore.value = false
    }
  }

  async function silentRefresh() {
    backgroundRefreshing.value = true
    try {
      configs.value = await digestApi.listConfigs()
      const cfgId = activeConfigId.value
      if (cfgId) {
        const rid = ++resultsReq
        const resp = await digestApi.listResults(cfgId, { limit: PAGE_SIZE, offset: 0 })
        if (rid !== resultsReq) return
        results.value = resp.results.map(normalize)
        hasMore.value = resp.has_more
      }
    } catch {
      // silent
    } finally {
      backgroundRefreshing.value = false
    }
  }

  async function fetchUnreadCounts() {
    try {
      unreadCounts.value = await digestApi.unreadCounts()
    } catch {
      // silent
    }
  }

  async function updateResultStatus(resultId: number, status: string) {
    try {
      const updated = normalize(await digestApi.updateResultStatus(resultId, status))
      const idx = results.value.findIndex(r => r.id === resultId)
      if (idx !== -1) results.value[idx] = updated
      if (selectedResult.value?.id === resultId) selectedResult.value = updated
      fetchUnreadCounts()
    } catch (e) {
      toastError(e, 'Failed to update digest status')
    }
  }

  return {
    configs, activeConfigId, results, selectedResult, resultItems,
    loading, generating, loadingMore, hasMore, activeConfig, unreadCounts, backgroundRefreshing,
    fetchConfigs, createConfig, updateConfig, deleteConfig,
    setActiveConfig, selectResult, generateNow, markResultRead, deleteResult,
    updateResultStatus, silentRefresh, fetchUnreadCounts, fetchMoreResults,
  }
})
