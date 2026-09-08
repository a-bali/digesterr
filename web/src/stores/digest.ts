import { defineStore } from 'pinia'
import { ref, computed } from 'vue'
import { toast } from 'vue-sonner'
import { digestApi } from '../services/api'
import type { DigestConfig, DigestResult, DigestResultItemWithData, DigestResultWithConfigName } from '../types/api'
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
  const loadingContent = ref(false)
  const hasMore = ref(false)
  const unreadCounts = ref<Record<number, number>>({})
  const allResults = ref<DigestResultWithConfigName[]>([])
  const showingAllDigests = ref(false)

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
    // Already selected with content loaded: nothing to fetch.
    if (selectedResult.value?.id === result.id && selectedResult.value?.content && resultItems.value.length > 0) return
    // Set preview immediately so the content pane renders while we fetch.
    selectedResult.value = result
    // List endpoints omit the heavy content field. Fetch the full result
    // so DigestContent can parse and render it.
    loadingContent.value = true
    try {
      const full = await digestApi.getResult(result.id)
      if (selectedResult.value?.id !== result.id) { loadingContent.value = false; return }
      selectedResult.value = full
    } catch {
      // Preview stays — already set above.
    } finally {
      loadingContent.value = false
    }
    try {
      const items = await digestApi.listResultItems(result.id)
      if (selectedResult.value?.id !== result.id) return // switched away mid-flight
      resultItems.value = items
    } catch (e) {
      toastError(e, 'Failed to load result items')
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

  async function loadAllResults(replace: boolean) {
    const rid = ++resultsReq
    if (replace) {
      loading.value = true
      hasMore.value = false
    } else {
      loadingMore.value = true
    }
    try {
      const resp = await digestApi.listAllResults({
        limit: PAGE_SIZE,
        offset: replace ? 0 : allResults.value.length,
      })
      if (rid !== resultsReq) return
      if (replace) {
        allResults.value = resp.results
      } else {
        allResults.value.push(...resp.results)
      }
      hasMore.value = resp.has_more
    } catch (e) {
      toastError(e, 'Failed to load digest results')
    } finally {
      if (replace) loading.value = false
      else loadingMore.value = false
    }
  }

  function enterAllDigestsMode() {
    showingAllDigests.value = true
    activeConfigId.value = null
    selectedResult.value = null
    resultItems.value = []
    allResults.value = []
    loadAllResults(true)
  }

  function exitAllDigestsMode() {
    showingAllDigests.value = false
    allResults.value = []
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

  async function markAllRead(configId?: number) {
    await digestApi.markAllRead(configId)
    await fetchUnreadCounts()
    // Refresh results if we're in the affected view.
    if (configId != null && activeConfigId.value === configId) {
      await setActiveConfig(configId)
    } else if (configId == null && showingAllDigests.value) {
      await loadAllResults(true)
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
    loading, generating, loadingMore, loadingContent, hasMore, activeConfig, unreadCounts, backgroundRefreshing,
    allResults, showingAllDigests,
    fetchConfigs, createConfig, updateConfig, deleteConfig,
    setActiveConfig, selectResult, generateNow, markResultRead, deleteResult,
    updateResultStatus, silentRefresh, fetchUnreadCounts, fetchMoreResults,
    loadAllResults, enterAllDigestsMode, exitAllDigestsMode, markAllRead,
  }
})
