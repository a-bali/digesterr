import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { setActivePinia, createPinia } from 'pinia'
import { createRouter, createMemoryHistory } from 'vue-router'
import FeedSidebar from './FeedSidebar.vue'

vi.mock('../services/api', () => ({
  feedApi: {
    list: vi.fn().mockResolvedValue([]),
    create: vi.fn(),
    update: vi.fn(),
    delete: vi.fn(),
    getItems: vi.fn().mockResolvedValue({ items: [] }),
    fetch: vi.fn(),
    markRead: vi.fn(),
    markReadAll: vi.fn(),
    unreadCounts: vi.fn().mockResolvedValue({}),
    savedCount: vi.fn().mockResolvedValue({ count: 0 }),
  },
  digestApi: {
    listConfigs: vi.fn().mockResolvedValue([]),
    createConfig: vi.fn(),
    updateConfig: vi.fn(),
    deleteConfig: vi.fn(),
    getConfig: vi.fn(),
    generate: vi.fn(),
    listResults: vi.fn().mockResolvedValue({ results: [], has_more: false }),
    getResult: vi.fn(),
    updateResultStatus: vi.fn(),
    deleteResult: vi.fn(),
    listResultItems: vi.fn().mockResolvedValue([]),
    unreadCounts: vi.fn().mockResolvedValue({}),
  },
  itemApi: { updateStatus: vi.fn(), getItem: vi.fn(), extract: vi.fn() },
  logsApi: { list: vi.fn().mockResolvedValue({ entries: [], total: 0, has_more: false }) },
  setAuthToken: vi.fn(),
  getAuthToken: vi.fn(() => null),
  setOnUnauthorized: vi.fn(),
  ApiError: class extends Error {},
}))

import { digestApi } from '../services/api'
import type { DigestConfig } from '../types/api'

window.matchMedia = window.matchMedia || ((query: string) => ({
  matches: false,
  media: query,
  onchange: null,
  addListener: () => {},
  removeListener: () => {},
  addEventListener: () => {},
  removeEventListener: () => {},
  dispatchEvent: () => false,
})) as MediaQueryList

class ResizeObserverStub {
  observe() {}
  unobserve() {}
  disconnect() {}
}
vi.stubGlobal('ResizeObserver', ResizeObserverStub)

const digestConfig: DigestConfig = {
  id: 7, user_id: 1, name: 'Morning Brief', schedule: '0 8 * * *', config: '{}',
  last_error: '', last_error_at: null,
  created_at: '', updated_at: '',
}

function router() {
  return createRouter({
    history: createMemoryHistory(),
    routes: [{ path: '/', component: FeedSidebar }],
  })
}

async function mountSidebar() {
  const r = router()
  const wrapper = mount(FeedSidebar, { global: { plugins: [r] } })
  await r.isReady()
  await flushPromises()
  return wrapper
}

// The digest dialogs live inside FeedSidebar, so "is the form still on screen
// with the user's input" is asserted through the component's exposed state plus
// the rendered dialog. Both matter: a form that is still mounted but reset is
// just as much data loss as one that closed.
describe('digest config dialog survives a failed save', () => {
  beforeEach(() => {
    document.body.innerHTML = ''
    setActivePinia(createPinia())
    vi.clearAllMocks()
    vi.mocked(digestApi.listConfigs).mockResolvedValue([])
  })

  it('keeps the edit dialog open when updating a digest config fails', async () => {
    vi.mocked(digestApi.listConfigs).mockResolvedValue([digestConfig])
    vi.mocked(digestApi.updateConfig).mockRejectedValue(new Error('boom'))

    const wrapper = await mountSidebar()
    const vm = wrapper.vm as any

    vm.editingDigest = digestConfig
    vm.showEditDigest = true
    await flushPromises()
    expect(vm.showEditDigest).toBe(true)

    await vm.saveEditDigest({ name: 'Renamed', schedule: '0 9 * * *', config: '{}' })
    await flushPromises()

    expect(digestApi.updateConfig).toHaveBeenCalled()
    expect(vm.showEditDigest).toBe(true)
    expect(vm.editingDigest).toEqual(digestConfig)
  })

  it('closes the edit dialog when updating succeeds', async () => {
    vi.mocked(digestApi.listConfigs).mockResolvedValue([digestConfig])
    vi.mocked(digestApi.updateConfig).mockResolvedValue({ ...digestConfig, name: 'Renamed' })

    const wrapper = await mountSidebar()
    const vm = wrapper.vm as any

    vm.editingDigest = digestConfig
    vm.showEditDigest = true
    await flushPromises()

    await vm.saveEditDigest({ name: 'Renamed', schedule: '0 9 * * *', config: '{}' })
    await flushPromises()

    expect(vm.showEditDigest).toBe(false)
    expect(vm.editingDigest).toBeNull()
  })

  it('keeps the add dialog open when creating a digest config fails', async () => {
    vi.mocked(digestApi.createConfig).mockRejectedValue(new Error('boom'))

    const wrapper = await mountSidebar()
    const vm = wrapper.vm as any

    vm.showAddDigest = true
    await flushPromises()

    await vm.addDigest({ name: 'New', schedule: '0 9 * * *', config: '{}' })
    await flushPromises()

    expect(vm.showAddDigest).toBe(true)
  })

  it('closes the add dialog when creating succeeds', async () => {
    vi.mocked(digestApi.createConfig).mockResolvedValue({
      id: 9, user_id: 1, name: 'New', schedule: '0 9 * * *', config: '{}',
      last_error: '', last_error_at: null, created_at: '', updated_at: '',
    })

    const wrapper = await mountSidebar()
    const vm = wrapper.vm as any

    vm.showAddDigest = true
    await flushPromises()

    await vm.addDigest({ name: 'New', schedule: '0 9 * * *', config: '{}' })
    await flushPromises()

    expect(vm.showAddDigest).toBe(false)
  })

  it('keeps the edit dialog open when deleting a digest config fails', async () => {
    vi.mocked(digestApi.listConfigs).mockResolvedValue([digestConfig])
    vi.mocked(digestApi.deleteConfig).mockRejectedValue(new Error('boom'))

    const wrapper = await mountSidebar()
    const vm = wrapper.vm as any

    vm.editingDigest = digestConfig
    vm.showEditDigest = true
    await flushPromises()

    await vm.deleteDigest(digestConfig.id)
    await flushPromises()

    expect(vm.showEditDigest).toBe(true)
  })

  it('closes the edit dialog when deleting succeeds', async () => {
    vi.mocked(digestApi.listConfigs).mockResolvedValue([digestConfig])
    vi.mocked(digestApi.deleteConfig).mockResolvedValue(undefined as any)

    const wrapper = await mountSidebar()
    const vm = wrapper.vm as any

    vm.editingDigest = digestConfig
    vm.showEditDigest = true
    await flushPromises()

    await vm.deleteDigest(digestConfig.id)
    await flushPromises()

    expect(vm.showEditDigest).toBe(false)
    expect(vm.editingDigest).toBeNull()
  })
})
