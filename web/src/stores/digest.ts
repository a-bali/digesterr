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
  // How many rows the SERVER has already served for the current view. This is
  // the paging offset, not the local array length: the two diverge because the
  // background refresh merges page one into a list that may reach further, and
  // because a row can drop out of the served window when its status changes.
  // Deriving the offset from results.value.length skipped real rows after
  // either happened.
  const nextOffset = ref(0)
  const unreadCounts = ref<Record<number, number>>({})
  const allResults = ref<DigestResultWithConfigName[]>([])
  const showingAllDigests = ref(false)
  // Status filter for the digest result list. It lives in the store, not in
  // DigestResultList, because the 'u' shortcut has to reach it: a local ref was
  // only reachable from that one component, so pressing 'u' in a digest view
  // silently flipped the ITEM filter and left the digest list unchanged.
  const resultFilter = ref<'all' | 'unread' | 'read'>('all')
  // Sort order for the digest result list. In the store for the same reason as
  // resultFilter, and because both are now sent to the SERVER: filtering and
  // sorting only the loaded page made "Oldest" the reverse of page one and left
  // older matches unreachable. A watcher refetches page one when either changes.
  const resultSort = ref<'newest' | 'oldest'>('newest')

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

  // createConfig/updateConfig/deleteConfig each report whether the mutation
  // actually happened. The caller needs that to decide whether to close its
  // dialog: closing on failure loses everything the user typed, and the toast
  // that explains the failure disappears with the form.
  async function createConfig(name: string, schedule: string, config: string): Promise<boolean> {
    try {
      const cfg = await digestApi.createConfig({ name, schedule, config })
      configs.value.push(cfg)
      toastSuccess('Digest config created', cfg.name || name)
      return true
    } catch (e) {
      toastError(e, 'Failed to create digest config', name)
      return false
    }
  }

  async function updateConfig(id: number, name: string, schedule: string, config: string): Promise<boolean> {
    try {
      const updated = await digestApi.updateConfig(id, { name, schedule, config })
      const idx = configs.value.findIndex(c => c.id === id)
      if (idx !== -1) configs.value[idx] = updated
      toastSuccess('Digest config updated', name)
      return true
    } catch (e) {
      toastError(e, 'Failed to update digest config', name)
      return false
    }
  }

  async function deleteConfig(id: number): Promise<boolean> {
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
      return true
    } catch (e) {
      toastError(e, 'Failed to delete digest config', name)
      return false
    }
  }

  // listQueryOpts are the filter/sort options both list endpoints take. Sent to
  // the server so the controls apply to the whole table rather than the loaded
  // page; see resultSort's comment.
  function listQueryOpts() {
    return {
      status: resultFilter.value === 'all' ? undefined : resultFilter.value,
      sort: resultSort.value === 'oldest' ? 'oldest' : undefined,
    }
  }

  async function setActiveConfig(id: number) {
    const rid = ++resultsReq
    activeConfigId.value = id
    selectedResult.value = null
    resultItems.value = []
    loading.value = true
    hasMore.value = false
    nextOffset.value = 0
    try {
      const resp = await digestApi.listResults(id, { limit: PAGE_SIZE, offset: 0, ...listQueryOpts() })
      if (rid !== resultsReq) return
      results.value = resp.results.map(normalize)
      // Advance the served offset past page one, or the first fetchMoreResults
      // would ask for offset 0 again and pagination would stall.
      nextOffset.value = resp.results.length
      hasMore.value = resp.has_more
    } catch (e) {
      if (rid !== resultsReq) return
      toastError(e, 'Failed to load digest results')
    } finally {
      if (rid === resultsReq) loading.value = false
    }
  }

  async function selectResult(result: DigestResult, prefetched?: DigestResult) {
    // Already selected with content loaded: nothing to fetch.
    if (selectedResult.value?.id === result.id && selectedResult.value?.content && resultItems.value.length > 0) return
    // Set preview immediately so the content pane renders while we fetch.
    selectedResult.value = result
    loadingContent.value = true
    try {
      // prefetched is the full row when the caller already resolved it by id
      // (see selectResultById); otherwise fetch it — list endpoints omit the
      // heavy content field, which DigestContent needs.
      const full = prefetched ?? await digestApi.getResult(result.id)
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

  // selectResultById loads a result the list does not hold — a deep link to a
  // result beyond the loaded page, or outside the active status filter.
  //
  // selectResult() takes a DigestResult it can already see, so a caller that
  // only has an ID had no way in: the route handler searched the loaded page,
  // found nothing, and left the content pane empty. Fetching by id is the one
  // resolution that does not depend on which page the client happens to have.
  async function selectResultById(resultId: number) {
    if (selectedResult.value?.id === resultId && selectedResult.value?.content) return
    try {
      const full = normalize(await digestApi.getResult(resultId))
      // Pass the fetched row through so selectResult does not refetch it.
      await selectResult(full, full)
    } catch {
      // A stale or foreign id is not an error worth a toast: the route simply
      // points at a result this user cannot see.
      selectedResult.value = null
      resultItems.value = []
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
    // The paging target depends on which list is on screen. In All-Digests mode
    // there is no active config, so this used to return immediately — and since
    // the list's infinite-scroll handler is wired to this function, "All
    // Digests" silently stopped at the first page even when the server reported
    // more. Delegating rather than duplicating keeps one pagination path.
    if (!activeConfigId.value) {
      if (showingAllDigests.value && hasMore.value && !loadingMore.value && !loading.value) {
        await loadAllResults(false)
      }
      return
    }
    const cfgId = activeConfigId.value
    if (!hasMore.value || loadingMore.value || loading.value) return
    const rid = ++resultsReq
    // The offset counts rows the SERVER has served, not rows on screen. Using
    // results.value.length sent the next request past rows that the refresh had
    // dropped, silently skipping results the user never saw.
    const offset = nextOffset.value
    loadingMore.value = true
    try {
      const resp = await digestApi.listResults(cfgId, { limit: PAGE_SIZE, offset, ...listQueryOpts() })
      if (rid !== resultsReq) return
      const page = resp.results.map(normalize)
      // Advance by what was SERVED, so the next page starts where this one
      // ended even if some of it was filtered out of the visible list.
      nextOffset.value = offset + page.length
      const have = new Set(results.value.map(r => r.id))
      const fresh = page.filter(r => !have.has(r.id))
      if (listQueryOpts().status != null) {
        results.value.push(...fresh.filter(r => r.status === listQueryOpts().status))
      } else {
        results.value.push(...fresh)
      }
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
      const offset = replace ? 0 : nextOffset.value
      const resp = await digestApi.listAllResults({
        limit: PAGE_SIZE,
        offset,
        ...listQueryOpts(),
      })
      if (rid !== resultsReq) return
      // normalize() maps the API's "new" to "unread". Without it the All-Digests
      // view rendered unread results as read (grey, no dot) and the Unread
      // filter matched nothing, because the status never equalled 'unread'.
      const page = resp.results.map(normalize)
      nextOffset.value = offset + page.length
      if (replace) {
        allResults.value = page
      } else {
        const have = new Set(allResults.value.map(r => r.id))
        allResults.value.push(...page.filter(r => !have.has(r.id)))
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
    nextOffset.value = 0
    loadAllResults(true)
  }

  function exitAllDigestsMode() {
    showingAllDigests.value = false
    allResults.value = []
  }

  // silentRefresh is the 10-second background poll. It must MERGE what it
  // fetches into the loaded list, never assign over it.
  //
  // Assigning the first page replaced the whole list, so a user who had
  // scrolled to page three watched pages two and three disappear every ten
  // seconds. It also only handled the single-config branch, so All-Digests was
  // never refreshed at all.
  //
  // mergeById refreshes the rows that came back (status, item count, title) and
  // appends ones the list does not have yet, leaving already-loaded rows in
  // place. hasMore is intentionally NOT taken from the refresh: it answers "is
  // there another page after the ones I have", and a first-page response cannot
  // know that — adopting its value would re-enable infinite scroll on a fully
  // loaded list, or stop it early.
  //
  // Three corrections on top of that merge, all reported after it shipped:
  //
  //   - The sort is the SERVER's, in the user's selected direction. mergeById
  //     used to hardcode newest-first, so a refresh dropped a refreshed row at
  //     the top of a list the user had set to "Oldest", contradicting the order
  //     on screen.
  //   - Rows that no longer belong are DROPPED. The merge only added or
  //     updated, so a result marked read from another client stayed visible
  //     under the unread filter that excludes it.
  //   - The paging offset counts rows the SERVER served, tracked in nextOffset,
  //     not the local array length. Rows retained from an earlier page that the
  //     server no longer returns inflated the length, so the next request
  //     skipped real rows.
  //
  // Visibility is reconciled against what the server currently serves:
  //
  //   - Rows failing the active status filter are dropped, so a result marked
  //     read elsewhere disappears from an unread view instead of lingering.
  //   - When the response reports NO further rows, it describes the server's
  //     entire set, so any loaded row it does not return is gone (deleted, or
  //     filtered out server-side) and is removed too. When further rows DO
  //     exist the client may legitimately hold rows from a deeper page that
  //     this page-one refresh knows nothing about, so those are kept -- that is
  //     what stops the refresh truncating a scrolled list.
  function mergeById<T extends DigestResult>(loaded: T[], fetched: T[], hasMore: boolean): T[] {
    // An EMPTY page is authoritative when the server reports no further rows: it
    // then describes the server's entire set, so every loaded row is gone (read
    // elsewhere, deleted, or filtered out) and retaining them left results on
    // screen that the server no longer serves. With further rows the response
    // says nothing about a loaded deeper page, so those stay.
    if (fetched.length === 0) return hasMore ? loaded : []
    const byId = new Map<number, T>()
    for (const r of fetched) byId.set(r.id, r)

    const opts = listQueryOpts()
    const matchesFilter = (r: T) => opts.status == null || normalize({ ...r }).status === opts.status

    const seen = new Set<number>()
    const out = loaded.map(r => {
      const fresh = byId.get(r.id)
      if (!fresh) return r
      seen.add(r.id)
      // Keep any field the preview omits that we already had (content is only
      // fetched by selectResult, so a refresh must not drop it).
      return { ...r, ...fresh }
    })
    for (const r of fetched) {
      if (!seen.has(r.id)) out.push(r)
    }

    // The client's paging offset counts rows the SERVER served, so it is a
    // high-water mark that only grows. results.value.length is not usable here:
    // it diverges as soon as the refresh merges page one into a longer list, or
    // a row drops out of the served window.
    nextOffset.value = Math.max(nextOffset.value, fetched.length)

    // Order in the direction the user selected, so a refreshed row lands where
    // the server puts it rather than jumping to the top of an "Oldest" list.
    const dir = opts.sort === 'oldest' ? 1 : -1

    const kept = hasMore ? out : out.filter(r => byId.has(r.id))
    const filtered = kept.filter(matchesFilter)
    filtered.sort((a, b) => dir * (new Date(a.created_at).getTime() - new Date(b.created_at).getTime()))
    return filtered
  }

  async function silentRefresh() {
    backgroundRefreshing.value = true
    try {
      configs.value = await digestApi.listConfigs()
      // One generation counter guards BOTH branches. The All-Digests branch used
      // to assign allResults.value with no check, so a background refresh that
      // resolved after a user-initiated load clobbered it with stale page-one
      // data. Claiming the generation before the request is what makes the check
      // meaningful: the newer call bumps resultsReq, so this one detects that it
      // was superseded even though it started first.
      const rid = ++resultsReq
      if (showingAllDigests.value) {
        const resp = await digestApi.listAllResults({ limit: PAGE_SIZE, offset: 0, ...listQueryOpts() })
        if (rid !== resultsReq) return
        allResults.value = mergeById(allResults.value, resp.results.map(normalize), resp.has_more)
        return
      }
      const cfgId = activeConfigId.value
      if (cfgId) {
        const resp = await digestApi.listResults(cfgId, { limit: PAGE_SIZE, offset: 0, ...listQueryOpts() })
        if (rid !== resultsReq) return
        results.value = mergeById(results.value, resp.results.map(normalize), resp.has_more)
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

  // refetchList reloads page one with the current filter/sort. The filter and
  // sort are server-side now, so changing either has to re-query rather than
  // re-derive from the loaded rows: the rows matching a new filter may not be
  // loaded at all.
  async function refetchList() {
    if (showingAllDigests.value || !activeConfigId.value) {
      await loadAllResults(true)
      return
    }
    // Reset paging: the loaded rows answered the OLD query.
    hasMore.value = false
    nextOffset.value = 0
    await setActiveConfig(activeConfigId.value)
  }

  // toggleResultFilter is the single funnel for changing the digest status
  // filter: it sets the filter AND refetches.
  //
  // The 'u' shortcut used to assign resultFilter directly, while the list's own
  // dropdown went through a computed setter that also called refetchList. Both
  // mutate the same state, so the shortcut left the PREVIOUS query's rows on
  // screen: the filter control showed "Unread" while the list still held the
  // unfiltered page. One funnel, so the two entry points cannot drift again.
  function toggleResultFilter() {
    resultFilter.value = resultFilter.value === 'unread' ? 'all' : 'unread'
    void refetchList()
  }

  // setResultFilter is the explicit-value twin of toggleResultFilter, for the
  // dropdown. Both entry points go through one path so neither can change the
  // filter without re-querying.
  function setResultFilter(v: 'all' | 'unread' | 'read') {
    resultFilter.value = v
    void refetchList()
  }

  // setResultSort is the same funnel for the sort direction: the order is
  // applied by the SERVER, so changing it must re-query rather than re-sort the
  // loaded page.
  function setResultSort(v: 'newest' | 'oldest') {
    resultSort.value = v
    void refetchList()
  }

  return {
    configs, activeConfigId, results, selectedResult, resultItems,
    loading, generating, loadingMore, loadingContent, hasMore, nextOffset, activeConfig, unreadCounts, backgroundRefreshing,
    allResults, showingAllDigests, resultFilter, resultSort,
    fetchConfigs, createConfig, updateConfig, deleteConfig,
    setActiveConfig, selectResult, selectResultById, generateNow, markResultRead, deleteResult,
    updateResultStatus, silentRefresh, fetchUnreadCounts, fetchMoreResults,
    loadAllResults, enterAllDigestsMode, exitAllDigestsMode, markAllRead, markReadAbove, markReadBelow,
    refetchList, toggleResultFilter, setResultFilter, setResultSort,
  }
})
