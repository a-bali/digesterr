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

// "Whenever the item is hovered (background changes), a click must always change
// to the item."
//
// The hover surface is the ROW, so the select handler must be on the row too.
// It used to sit on an inner <button> that was only as tall as its own content
// inside a taller padded row, which left a band at the top and bottom that lit
// up on hover but swallowed the click.
describe('FeedRow click surface', () => {
  it('puts the select handler on the row, not on an inner button', async () => {
    // The structural assertion that makes the geometry true: the element that
    // carries hover:bg-accent/10 must be the element that emits 'select'.
    const wrapper = mountFeedRow(makeFeed())
    const row = wrapper.find('[role="button"]')
    expect(row.exists()).toBe(true)
    expect(row.classes()).toContain('hover:bg-accent/10')

    await row.trigger('click')
    expect(wrapper.emitted('select')).toBeTruthy()
    expect(wrapper.emitted('select')![0]).toEqual([1])
  })

  it('selects on a click anywhere in the row, including its padding', async () => {
    // Padding is part of the row's box precisely so that hovering the padding
    // (which paints the hover background) also selects on click.
    const wrapper = mountFeedRow(makeFeed())
    const row = wrapper.find('[role="button"]')
    expect(row.classes()).toContain('py-1.5')
    expect(row.classes()).toContain('px-2')

    await row.trigger('click')
    await row.trigger('click')
    expect(wrapper.emitted('select')).toHaveLength(2)
  })

  it('does not select when an action button is clicked', async () => {
    const wrapper = mountFeedRow(makeFeed())
    const markRead = wrapper.find('button[title="Mark “Test Feed” as read"]')
    expect(markRead.exists()).toBe(true)

    await markRead.trigger('click')
    expect(wrapper.emitted('select')).toBeFalsy()
    expect(wrapper.emitted('markRead')).toBeTruthy()
  })

  it('is keyboard reachable, matching the other two lists', async () => {
    // ItemList and DigestResultList rows are role=button + tabindex=0; a row
    // that only responds to the mouse would be a regression in reachability.
    const wrapper = mountFeedRow(makeFeed())
    const row = wrapper.find('[role="button"]')
    expect(row.attributes('tabindex')).toBe('0')

    await row.trigger('keydown', { key: 'Enter' })
    expect(wrapper.emitted('select')).toBeTruthy()
  })
})
