import { defineStore } from 'pinia'
import { ref, computed } from 'vue'
import { toast } from 'vue-sonner'
import { digestApi, ApiError } from '../services/api'
import type { DigestConfig, DigestResult, DigestResultItemWithData } from '../types/api'

export const useDigestStore = defineStore('digest', () => {
  const configs = ref<DigestConfig[]>([])
  const activeConfigId = ref<number | null>(null)
  const results = ref<DigestResult[]>([])
  const selectedResult = ref<DigestResult | null>(null)
  const resultItems = ref<DigestResultItemWithData[]>([])
  const loading = ref(false)
  const generating = ref(false)
  const unreadCounts = ref<Record<number, number>>({})

  const activeConfig = computed(() => configs.value.find(c => c.id === activeConfigId.value) || null)

  function normalize(r: DigestResult): DigestResult {
    if (r.status === 'new') r.status = 'unread'
    return r
  }

  async function fetchConfigs() {
    try {
      configs.value = await digestApi.listConfigs()
    } catch (e) {
      toast.error('Failed to load digest configs', { description: e instanceof ApiError ? e.message : undefined })
    }
  }

  async function createConfig(name: string, schedule: string, config: string) {
    try {
      const cfg = await digestApi.createConfig({ name, schedule, config })
      configs.value.push(cfg)
      return cfg
    } catch (e) {
      toast.error('Failed to create digest config', { description: e instanceof ApiError ? e.message : undefined })
      return null
    }
  }

  async function updateConfig(id: number, name: string, schedule: string, config: string) {
    try {
      const updated = await digestApi.updateConfig(id, { name, schedule, config })
      const idx = configs.value.findIndex(c => c.id === id)
      if (idx !== -1) configs.value[idx] = updated
    } catch (e) {
      toast.error('Failed to update digest config', { description: e instanceof ApiError ? e.message : undefined })
    }
  }

  async function deleteConfig(id: number) {
    try {
      await digestApi.deleteConfig(id)
      configs.value = configs.value.filter(c => c.id !== id)
      if (activeConfigId.value === id) {
        activeConfigId.value = null
        results.value = []
        selectedResult.value = null
        resultItems.value = []
      }
    } catch (e) {
      toast.error('Failed to delete digest config', { description: e instanceof ApiError ? e.message : undefined })
    }
  }

  async function setActiveConfig(id: number) {
    activeConfigId.value = id
    selectedResult.value = null
    resultItems.value = []
    loading.value = true
    try {
      results.value = (await digestApi.listResults(id)).map(normalize)
    } catch (e) {
      toast.error('Failed to load digest results', { description: e instanceof ApiError ? e.message : undefined })
    } finally {
      loading.value = false
    }
  }

  async function selectResult(result: DigestResult) {
    selectedResult.value = result
    loading.value = true
    try {
      resultItems.value = await digestApi.listResultItems(result.id)
    } catch (e) {
      toast.error('Failed to load result items', { description: e instanceof ApiError ? e.message : undefined })
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
      toast.error('Digest generation failed', { description: e instanceof ApiError ? e.message : undefined })
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
    } catch (e) {
      toast.error('Failed to mark digest as read', { description: e instanceof ApiError ? e.message : undefined })
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
      toast.error('Failed to delete digest', { description: e instanceof ApiError ? e.message : undefined })
    }
  }

  async function silentRefresh() {
    try {
      configs.value = await digestApi.listConfigs()
      if (activeConfigId.value) {
        results.value = (await digestApi.listResults(activeConfigId.value)).map(normalize)
      }
    } catch {
      // silent
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
    } catch (e) {
      toast.error('Failed to update digest status', { description: e instanceof ApiError ? e.message : undefined })
    }
  }

  return {
    configs, activeConfigId, results, selectedResult, resultItems,
    loading, generating, activeConfig, unreadCounts,
    fetchConfigs, createConfig, updateConfig, deleteConfig,
    setActiveConfig, selectResult, generateNow, markResultRead, deleteResult,
    updateResultStatus, silentRefresh, fetchUnreadCounts,
  }
})
