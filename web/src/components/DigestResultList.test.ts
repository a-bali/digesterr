import { describe, it, expect, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import DigestResultList from './DigestResultList.vue'
import { useDigestStore } from '../stores/digest'
import { absoluteTime } from '../lib/format'
import type { DigestResult } from '../types/api'

// jsdom lacks ResizeObserver; FilterBar uses it to collapse to a dropdown.
class ResizeObserverStub {
  observe() {}
  unobserve() {}
  disconnect() {}
}
vi.stubGlobal('ResizeObserver', ResizeObserverStub)

function makeResult(overrides: Partial<DigestResult> = {}): DigestResult {
  return {
    id: 1,
    user_id: 1,
    config_id: 1,
    config_snapshot: '{}',
    content: JSON.stringify({ summary: 's', items: [{ item_id: 2 }] }),
    status: 'unread',
    created_at: '2026-08-13T08:00:00Z',
    updated_at: '2026-08-13T08:00:00Z',
    ...overrides,
  }
}

function mountList() {
  const pinia = createPinia()
  setActivePinia(pinia)
  const store = useDigestStore()
  store.loading = false
  store.results = [
    makeResult(),
    makeResult({ id: 2, created_at: '2026-08-12T09:30:00Z' }),
  ]
  return mount(DigestResultList, { global: { plugins: [pinia] } })
}

describe('DigestResultList', () => {
  it('shows the full date and time for each digest', () => {
    const wrapper = mountList()
    const text = wrapper.text()
    expect(text).toContain(absoluteTime('2026-08-13T08:00:00Z'))
    expect(text).toContain(absoluteTime('2026-08-12T09:30:00Z'))
  })

  it('counts items from the digest content', () => {
    const wrapper = mountList()
    expect(wrapper.text()).toContain('1 items')
  })
})
