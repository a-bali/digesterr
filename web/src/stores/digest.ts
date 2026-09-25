import { defineStore } from 'pinia'
import { ref, computed } from 'vue'
import { digestApi, ApiError } from '../services/api'
import type { DigestConfig, DigestResult, DigestResultItemWithData, DigestResultWithConfigName } from '../types/api'
import { toastSuccess, toastError, toastInfo } from '../lib/toast'
import { MARK_SUCCESS_TITLE, MARK_FAILURE_TITLE } from '../lib/markAllRead'

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
  // Status filter for the digest result list. It lives in the store, not in
  // DigestResultList, because the 'u' shortcut has to reach it: a local ref was
  // only reachable from that one component, so pressing 'u' in a digest view
  // silently flipped the ITEM filter and left the digest list unchanged.
  const resultFilter = ref<'all' | 'unread' | 'read'>('all')

  const activeConfig = computed(() => configs.value.find(c => c.id === activeConfigId.value) || null)

  // Monotonic request id to drop stale responses (config switches, refreshes).
  let resultsReq = 0

  // Names for toasts. Both resolve against what the store already holds, so a
  // toast can always say WHICH config or digest was acted on.
  //
  // configName falls back to undefined when the config is unknown (deleted, or
  // configs not loaded yet) — the toast helper treats that as "no second line"
  // rather than rendering "undefined".
  function configName(configId?: number | null): string | undefined {
    if (configId == null) return undefined
    return configs.value.find(c => c.id === configId)?.name || undefined
  }

  // The digest scope as a phrase: the config's name, or "all digests".
  function digestScopeLabel(configId?: number | null): string {
    if (configId == null) return 'all digests'
    return configName(configId) ?? 'this digest'
  }

  // A result's own title, with the config name as context: results often share a
  // generated title across runs, so "which config" is the useful disambiguator.
  // Falls back to the config name alone for an untitled result.
  function resultLabel(resultId: number): string | undefined {
    const result = results.value.find(r => r.id === resultId)
      ?? allResults.value.find(r => r.id === resultId)
    const config = configName(result?.config_id)
    const title = (result as { title?: string } | undefined)?.title || undefined
    if (title && config) return `${title} (${config})`
    return title || config
  }

  // Maps the API's "new" status to "unread". Generic over the result shape: the
  // per-config list and the All-Digests list are different interfaces, and both
  // need this mapping.
  function normalize<T extends DigestResult>(r: T): T {
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
      toastSuccess('Digest config created', cfg.name || name)
      return cfg
    } catch (e) {
      toastError(e, 'Failed to create digest config', name)
      return null
    }
  }

  async function updateConfig(id: number, name: string, schedule: string, config: string) {
    try {
      const updated = await digestApi.updateConfig(id, { name, schedule, config })
      const idx = configs.value.findIndex(c => c.id === id)
      if (idx !== -1) configs.value[idx] = updated
      toastSuccess('Digest config updated', name)
    } catch (e) {
      toastError(e, 'Failed to update digest config', name)
    }
  }

  async function deleteConfig(id: number) {
    // Capture the name first: after the delete it is gone from `configs`, and the
    // toast has to name what was deleted.
    const name = configs.value.find(c => c.id === id)?.name
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
      toastSuccess('Digest config deleted', name)
    } catch (e) {
      toastError(e, 'Failed to delete digest config', name)
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
      toastSuccess('Digest generated', configName(configId))
      return result
    } catch (e) {
      // 409 means a generation for this config is already running (a scheduled
      // run still on its LLM call, or a double click). Nothing failed — the
      // work the user asked for is happening — so it must not read as an error.
      if (e instanceof ApiError && e.status === 409) {
        // The same status covers "nothing to digest", which is also not a
        // failure: a quiet stretch is a normal outcome for a working config.
        const msg = e.message || ''
        if (/no unread items/i.test(msg)) {
          toastInfo('No unread items to digest', configName(configId))
        } else {
          toastInfo('Digest generation already in progress', configName(configId))
        }
      } else {
        toastError(e, 'Digest generation failed', configName(configId))
      }
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
      toastSuccess(MARK_SUCCESS_TITLE, resultLabel(resultId))
    } catch (e) {
      toastError(e, MARK_FAILURE_TITLE, resultLabel(resultId))
    }
  }

  async function deleteResult(resultId: number) {
    // Capture the label first: the result is removed from both lists below, and the
    // toast must still be able to name it.
    const label = resultLabel(resultId)
    try {
      await digestApi.deleteResult(resultId)
      results.value = results.value.filter(r => r.id !== resultId)
      if (selectedResult.value?.id === resultId) {
        selectedResult.value = null
        resultItems.value = []
      }
      toastSuccess('Digest deleted', label)
    } catch (e) {
      toastError(e, 'Failed to delete digest', label)
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
      // normalize() maps the API's "new" to "unread". Without it the All-Digests
      // view rendered unread results as read (grey, no dot) and the Unread
      // filter matched nothing, because the status never equalled 'unread'.
      if (replace) {
        allResults.value = resp.results.map(normalize)
      } else {
        allResults.value.push(...resp.results.map(normalize))
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
    try {
      await digestApi.markAllRead(configId)
      await fetchUnreadCounts()
      // Refresh results if we're in the affected view.
      if (configId != null && activeConfigId.value === configId) {
        await setActiveConfig(configId)
      } else if (configId == null && showingAllDigests.value) {
        await loadAllResults(true)
      }
      toastSuccess(MARK_SUCCESS_TITLE, digestScopeLabel(configId))
    } catch (e) {
      toastError(e, MARK_FAILURE_TITLE, digestScopeLabel(configId))
    }
  }

  async function markReadAbove(result: DigestResult) {
    try {
      const opts: { config_id?: number } = {}
      if (activeConfigId.value) opts.config_id = activeConfigId.value
      const label = resultLabel(result.id)
      await digestApi.markReadAbove(result.id, opts)
      await fetchUnreadCounts()
      if (activeConfigId.value) {
        await setActiveConfig(activeConfigId.value)
      } else if (showingAllDigests.value) {
        await loadAllResults(true)
      }
      toastSuccess(MARK_SUCCESS_TITLE, `above ${label ?? 'this digest'}`)
    } catch (e) {
      toastError(e, MARK_FAILURE_TITLE, `above ${label ?? 'this digest'}`)
    }
  }

  async function markReadBelow(result: DigestResult) {
    try {
      const opts: { config_id?: number } = {}
      if (activeConfigId.value) opts.config_id = activeConfigId.value
      const label = resultLabel(result.id)
      await digestApi.markReadBelow(result.id, opts)
      await fetchUnreadCounts()
      if (activeConfigId.value) {
        await setActiveConfig(activeConfigId.value)
      } else if (showingAllDigests.value) {
        await loadAllResults(true)
      }
      toastSuccess(MARK_SUCCESS_TITLE, `below ${label ?? 'this digest'}`)
    } catch (e) {
      toastError(e, MARK_FAILURE_TITLE, `below ${label ?? 'this digest'}`)
    }
  }

  async function updateResultStatus(resultId: number, status: string) {
    try {
      const updated = normalize(await digestApi.updateResultStatus(resultId, status))
      // PATCH returns full content but no title/item_count (those are
      // computed by the preview query). Derive them so the middle pane
      // keeps showing the title after status updates.
      try {
        const c = JSON.parse(updated.content)
        ;(updated as any).title = c?.title ?? ''
        ;(updated as any).item_count = (c?.highlights?.length ?? 0) + (c?.items?.length ?? 0)
      } catch { /* content unavailable or invalid */ }
      const idx = results.value.findIndex(r => r.id === resultId)
      if (idx !== -1) results.value[idx] = updated
      const aidx = allResults.value.findIndex(r => r.id === resultId)
      if (aidx !== -1) (allResults.value[aidx] as any) = { ...allResults.value[aidx], ...updated }
      if (selectedResult.value?.id === resultId) selectedResult.value = updated
      fetchUnreadCounts()
    } catch (e) {
      toastError(e, 'Failed to update digest status')
    }
  }

  return {
    configs, activeConfigId, results, selectedResult, resultItems,
    loading, generating, loadingMore, loadingContent, hasMore, activeConfig, unreadCounts, backgroundRefreshing,
    allResults, showingAllDigests, resultFilter,
    fetchConfigs, createConfig, updateConfig, deleteConfig,
    setActiveConfig, selectResult, generateNow, markResultRead, deleteResult,
    updateResultStatus, silentRefresh, fetchUnreadCounts, fetchMoreResults,
    loadAllResults, enterAllDigestsMode, exitAllDigestsMode, markAllRead, markReadAbove, markReadBelow,
  }
})
