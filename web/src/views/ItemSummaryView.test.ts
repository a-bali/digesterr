import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createMemoryHistory, createRouter } from 'vue-router'
import type { Item } from '../types/api'

const mocks = vi.hoisted(() => ({ getItem: vi.fn(), summarize: vi.fn() }))
vi.mock('../services/api', () => ({
  itemApi: {
    getItem: (...args: unknown[]) => mocks.getItem(...args),
    summarize: (...args: unknown[]) => mocks.summarize(...args),
  },
}))

import ItemSummaryView from './ItemSummaryView.vue'

class ResizeObserverStub {
  observe() {}
  unobserve() {}
  disconnect() {}
}
vi.stubGlobal('ResizeObserver', ResizeObserverStub)

const item: Item = {
  id: 42,
  title: 'A longer article',
  url: 'https://example.com/article',
  content: '<p>Article text</p>',
  comment_url: '',
  points: 0,
  comments_count: 0,
  published_at: null,
  status: 'unread',
  extract_content: '',
  extract_status: '',
  extract_retries: 0,
  extract_date: null,
}

async function mountSummaryPage() {
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [{ path: '/items/:itemId/summary', component: ItemSummaryView }],
  })
  await router.push('/items/42/summary')
  await router.isReady()
  const wrapper = mount(ItemSummaryView, { global: { plugins: [router] } })
  await flushPromises()
  return wrapper
}

describe('ItemSummaryView', () => {
  beforeEach(() => {
    document.body.innerHTML = ''
    mocks.getItem.mockReset().mockResolvedValue(item)
    mocks.summarize.mockReset().mockResolvedValue({ content_summary: 'Main points.', comments_summary: 'Discussion themes.' })
  })

  it('fetches the item and auto-runs the medium summary on open', async () => {
    const wrapper = await mountSummaryPage()
    expect(mocks.getItem).toHaveBeenCalledWith(42)
    expect(mocks.summarize).toHaveBeenCalledWith(42, {
      sentence_count: 7, min_sentences: 5, max_sentences: 7, detail: 'balanced',
    })
    expect(wrapper.text()).toContain('A longer article')
    expect(wrapper.text()).toContain('Main points.')
    expect(wrapper.text()).toContain('Discussion themes.')
    expect(wrapper.find('[data-summary-regenerate]').exists()).toBe(true)
    expect(wrapper.find('[data-summary-length-opener]').text()).toContain('Medium')
    wrapper.unmount()
  })

  it('offers the web UI length presets and re-runs when another is selected', async () => {
    const wrapper = await mountSummaryPage()
    await wrapper.find('[data-summary-length-opener]').trigger('click')
    await flushPromises()
    const menuItems = Array.from(document.body.querySelectorAll('[role="menuitem"]'))
    expect(menuItems.map(el => el.textContent?.replace(/\s+/g, ' ').trim())).toEqual([
      expect.stringContaining('Short'),
      expect.stringContaining('Medium'),
      expect.stringContaining('Long'),
    ])
    const long = menuItems.find(el => el.textContent?.includes('Long')) as HTMLElement | undefined
    expect(long).toBeTruthy()
    long!.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    await flushPromises()
    expect(mocks.summarize).toHaveBeenLastCalledWith(42, {
      sentence_count: 18, min_sentences: 12, max_sentences: 18, detail: 'detailed',
    })
    expect(wrapper.find('[data-summary-length-opener]').text()).toContain('Long')
    wrapper.unmount()
  })

  it('shows an explicit empty state when the model has no usable summary', async () => {
    mocks.summarize.mockResolvedValue({ content_summary: '', comments_summary: '' })
    const wrapper = await mountSummaryPage()
    expect(wrapper.find('[data-summary-empty]').exists()).toBe(true)
    wrapper.unmount()
  })
})
