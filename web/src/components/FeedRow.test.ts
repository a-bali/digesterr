import { describe, it, expect } from 'vitest'
import { mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createRouter, createMemoryHistory } from 'vue-router'
import FeedRow from './FeedRow.vue'
import type { Feed } from '../types/api'

function makeFeed(overrides: Partial<Feed> = {}): Feed {
  return {
    id: 1, user_id: 1, category: '', type: 'rss', name: 'Test Feed',
    url: 'https://example.com/rss', config: '', min_score: 0,
    last_fetched_at: null, status: 'ok', extract_content: 0,
    last_error: '', last_error_at: null, last_error_severity: '',
    created_at: '', updated_at: '', ...overrides,
  }
}

function mountFeedRow(feed: Feed) {
  const pinia = createPinia()
  setActivePinia(pinia)
  const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/', component: { template: '<div />' } }] })
  return mount(FeedRow, {
    props: { feed, refreshing: false },
    global: { plugins: [pinia, router] },
  })
}

describe('FeedRow error indicators', () => {
  it('shows error icon when last_error is set and severity is error', () => {
    const wrapper = mountFeedRow(makeFeed({
      last_error: 'connection refused',
      last_error_at: '2025-01-01T00:00:00Z',
      last_error_severity: 'error',
    }))
    // Inline SVG renders with class containing text-red-500
    const errorIcon = wrapper.find('.text-red-500')
    expect(errorIcon.exists()).toBe(true)
    // No warning icon
    expect(wrapper.find('.text-yellow-500').exists()).toBe(false)
  })

  it('shows error icon when last_error is set and severity is empty', () => {
    const wrapper = mountFeedRow(makeFeed({
      last_error: 'some error',
      last_error_at: '2025-01-01T00:00:00Z',
      last_error_severity: '',
    }))
    const errorIcon = wrapper.find('.text-red-500')
    expect(errorIcon.exists()).toBe(true)
    expect(wrapper.find('.text-yellow-500').exists()).toBe(false)
  })

  it('shows warning icon when severity is warning', () => {
    const wrapper = mountFeedRow(makeFeed({
      last_error: 'enrichment failed',
      last_error_at: '2025-01-01T00:00:00Z',
      last_error_severity: 'warning',
    }))
    const warningIcon = wrapper.find('.text-yellow-500')
    expect(warningIcon.exists()).toBe(true)
    expect(wrapper.find('.text-red-500').exists()).toBe(false)
  })

  it('shows no icon when feed is clean', () => {
    const wrapper = mountFeedRow(makeFeed({
      last_error: '',
      last_error_at: null,
      last_error_severity: '',
    }))
    expect(wrapper.find('.text-red-500').exists()).toBe(false)
    expect(wrapper.find('.text-yellow-500').exists()).toBe(false)
  })
})
