import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import LogsDialog from './LogsDialog.vue'
import { logsApi } from '../services/api'

vi.mock('../services/api', () => ({
  logsApi: {
    list: vi.fn(),
  },
}))

const mockedList = vi.mocked(logsApi.list)

function sampleEntries() {
  return {
    entries: [
      { time: '2026-08-13T12:00:01Z', level: 'INFO', type: 'http', message: 'request ok', attrs: { path: '/api/feeds', status: '200', duration: '12ms' } },
      { time: '2026-08-13T12:00:02Z', level: 'ERROR', type: 'digest', message: 'gen failed' },
      { time: '2026-08-13T12:00:03Z', level: 'WARN', type: 'feed', message: 'fetch slow' },
      { time: '2026-08-13T12:00:04Z', level: 'INFO', type: 'http', message: 'extract item', attrs: { category: 'item', item_id: '42', url: 'https://example.com/a', feed_id: '7', feed_name: 'Example Feed', items: '12', content_size: '3456' } },
    ],
    total: 4,
    has_more: false,
  }
}

function manyEntries(n: number, start = 0) {
  return Array.from({ length: n }, (_, i) => ({
    time: `2026-08-13T12:00:${String(i + start).padStart(2, '0')}Z`,
    level: 'INFO',
    type: 'http',
    message: `entry ${i + start}`,
  }))
}

// The dialog content renders inside a reka-ui DialogPortal (teleported to
// document.body), so read the live DOM, not the wrapper.
function bodyText(): string {
  return document.body.textContent || ''
}

function scroller(): HTMLElement {
  return document.body.querySelector('[data-logs-dialog] .overflow-y-auto') as HTMLElement
}

// Log rows are the direct children of the scroller (one div per entry).
function rows(): HTMLElement[] {
  return Array.from(document.body.querySelectorAll('[data-logs-dialog] .overflow-y-auto > div')) as HTMLElement[]
}

async function openDialog(wrapper: ReturnType<typeof mount>) {
  await wrapper.find('button[title="Logs"]').trigger('click')
  await flushPromises()
}

describe('LogsDialog', () => {
  beforeEach(() => {
    document.body.innerHTML = ''
    mockedList.mockReset()
    mockedList.mockResolvedValue(sampleEntries())
  })

  it('renders the trigger button with a log icon', () => {
    const wrapper = mount(LogsDialog)
    const trigger = wrapper.find('button[title="Logs"]')
    expect(trigger.exists()).toBe(true)
    expect(trigger.find('svg').exists()).toBe(true)
  })

  it('loads logs when opened and renders entries with level and category badges', async () => {
    const wrapper = mount(LogsDialog)
    await openDialog(wrapper)

    expect(mockedList).toHaveBeenCalledWith({ limit: 200 })
    expect(bodyText()).toContain('request ok')
    expect(bodyText()).toContain('gen failed')
    expect(bodyText()).toContain('fetch slow')
    expect(bodyText()).toContain('INFO')
    expect(bodyText()).toContain('ERROR')
    // Category badge: attrs.category wins for item records, type otherwise.
    expect(bodyText()).toContain('item')
    expect(bodyText()).toContain('feed')
  })

  it('renders structured attrs inline on the same row as the message', async () => {
    const wrapper = mount(LogsDialog)
    await openDialog(wrapper)

    const reqRow = rows().find(r => r.textContent?.includes('request ok'))!
    expect(reqRow.textContent).toContain('request ok')
    expect(reqRow.textContent).toContain('path=')
    expect(reqRow.textContent).toContain('/api/feeds')
    expect(reqRow.textContent).toContain('duration=')
    expect(reqRow.textContent).toContain('12ms')

    // One row per entry — no separate expanded details section.
    const itemRow = rows().find(r => r.textContent?.includes('extract item'))!
    expect(itemRow.textContent).toContain('item_id="42"')
    expect(itemRow.textContent).toContain('https://example.com/a')
    // The category attr is already shown as the badge, not repeated inline.
    expect(itemRow.textContent).not.toContain('category=')
  })

  it('click toggles full-length wrapping of a truncated row', async () => {
    const wrapper = mount(LogsDialog)
    await openDialog(wrapper)

    const reqRow = rows().find(r => r.textContent?.includes('request ok'))!
    expect(reqRow.className).toContain('whitespace-nowrap')

    reqRow.dispatchEvent(new Event('click'))
    await flushPromises()

    const expandedRow = rows().find(r => r.textContent?.includes('request ok'))!
    expect(expandedRow.className).toContain('whitespace-pre-wrap')

    expandedRow.dispatchEvent(new Event('click'))
    await flushPromises()
    const collapsedAgain = rows().find(r => r.textContent?.includes('request ok'))!
    expect(collapsedAgain.className).toContain('whitespace-nowrap')
  })

  it('handles entries without attrs gracefully (no key=value tail, category falls back to type)', async () => {
    const wrapper = mount(LogsDialog)
    await openDialog(wrapper)

    const genFailed = rows().find(r => r.textContent?.includes('gen failed'))!
    expect(genFailed.textContent).not.toContain('=')
    expect(genFailed.textContent).toContain('digest') // category badge from type

    genFailed.dispatchEvent(new Event('click'))
    await flushPromises()
    // Toggling is harmless for rows without attrs.
    expect(rows().length).toBe(sampleEntries().entries.length)
  })

  it('applies the wide mobile-friendly content class to the dialog', async () => {
    const wrapper = mount(LogsDialog)
    await openDialog(wrapper)

    const dialog = document.body.querySelector('[role="dialog"]')
    expect(dialog).not.toBeNull()
    expect(dialog!.className).toContain('max-w-[min(95vw,1100px)]')
    expect(dialog!.className).toContain('w-[95vw]')
  })

  it('passes level and category filters to the API when changed', async () => {
    const wrapper = mount(LogsDialog)
    await openDialog(wrapper)

    const selects = document.body.querySelectorAll('select')
    const level = selects[0] as HTMLSelectElement
    level.value = 'error'
    level.dispatchEvent(new Event('change'))
    await flushPromises()

    expect(mockedList).toHaveBeenLastCalledWith({ limit: 200, level: 'error' })

    const category = selects[1] as HTMLSelectElement
    category.value = 'item'
    category.dispatchEvent(new Event('change'))
    await flushPromises()

    // Filters accumulate: the level filter is still active.
    expect(mockedList).toHaveBeenLastCalledWith({ limit: 200, level: 'error', type: 'item' })
  })

  it('derives the category dropdown from loaded entries', async () => {
    const wrapper = mount(LogsDialog)
    await openDialog(wrapper)

    const categorySelect = document.body.querySelectorAll('select')[1]
    const options = Array.from(categorySelect.querySelectorAll('option')).map(o => o.textContent)
    expect(options).toContain('http')
    expect(options).toContain('feed')
    expect(options).toContain('item')
    expect(options).toContain('digest')
  })

  it('lazy-loads older entries when scrolling to the bottom', async () => {
    mockedList
      .mockResolvedValueOnce({ entries: manyEntries(200), total: 250, has_more: true })
      .mockResolvedValueOnce({ entries: manyEntries(50, 200), total: 250, has_more: false })

    const wrapper = mount(LogsDialog)
    await openDialog(wrapper)
    expect(mockedList).toHaveBeenLastCalledWith({ limit: 200 })
    expect(bodyText()).toContain('showing 200 of 250')

    const el = scroller()
    // Scroll near the bottom to trigger loading older entries.
    Object.defineProperty(el, 'scrollTop', { value: 100000, configurable: true })
    Object.defineProperty(el, 'clientHeight', { value: 400, configurable: true })
    Object.defineProperty(el, 'scrollHeight', { value: 400, configurable: true })
    el.dispatchEvent(new Event('scroll'))
    await flushPromises()

    expect(mockedList).toHaveBeenLastCalledWith({ limit: 200, offset: 200 })
    expect(bodyText()).toContain('entry 249')
    expect(bodyText()).not.toContain('showing 200 of 250')
  })

  it('dedupes overlapping entries across pages', async () => {
    const firstPage = manyEntries(3)
    const older = [
      { time: '2026-08-13T12:10:00Z', level: 'INFO', type: 'http', message: 'entry 3' },
      { time: '2026-08-13T12:10:01Z', level: 'INFO', type: 'http', message: 'entry 4' },
    ]
    mockedList
      .mockResolvedValueOnce({ entries: firstPage, total: 5, has_more: true })
      .mockResolvedValueOnce({ entries: [firstPage[2], ...older], total: 5, has_more: false })

    const wrapper = mount(LogsDialog)
    await openDialog(wrapper)
    const el = scroller()
    Object.defineProperty(el, 'scrollTop', { value: 100000, configurable: true })
    Object.defineProperty(el, 'clientHeight', { value: 400, configurable: true })
    Object.defineProperty(el, 'scrollHeight', { value: 400, configurable: true })
    el.dispatchEvent(new Event('scroll'))
    await flushPromises()

    // 'entry 2' appears only once despite overlapping page boundary; new
    // entries 3 and 4 were appended.
    const matches = bodyText().match(/entry 2/g)
    expect(matches).toHaveLength(1)
    expect(bodyText()).toContain('entry 3')
    expect(bodyText()).toContain('entry 4')
  })
})
