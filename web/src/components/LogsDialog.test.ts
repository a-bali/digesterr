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
      { time: '2026-08-13T12:00:01Z', level: 'INFO', type: 'http', message: 'request ok', attrs: { path: '/api/feeds', status: '200' } },
      { time: '2026-08-13T12:00:02Z', level: 'ERROR', type: 'digest', message: 'gen failed' },
      { time: '2026-08-13T12:00:03Z', level: 'WARN', type: 'feed', message: 'fetch slow' },
    ],
    total: 3,
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

  it('loads logs when opened and renders entries', async () => {
    const wrapper = mount(LogsDialog)
    await openDialog(wrapper)

    expect(mockedList).toHaveBeenCalledWith({ limit: 200 })
    expect(bodyText()).toContain('request ok')
    expect(bodyText()).toContain('gen failed')
    expect(bodyText()).toContain('fetch slow')
    expect(bodyText()).toContain('INFO')
    expect(bodyText()).toContain('ERROR')
    expect(bodyText()).toContain('path=/api/feeds')
  })

  it('passes filters to the API when changed', async () => {
    const wrapper = mount(LogsDialog)
    await openDialog(wrapper)

    const selects = document.body.querySelectorAll('select')
    const severity = selects[0] as HTMLSelectElement
    severity.value = 'error'
    severity.dispatchEvent(new Event('change'))
    await flushPromises()

    expect(mockedList).toHaveBeenLastCalledWith({ limit: 200, level: 'error' })
  })

  it('derives the type dropdown from loaded entries', async () => {
    const wrapper = mount(LogsDialog)
    await openDialog(wrapper)

    const typeSelect = document.body.querySelectorAll('select')[1]
    const options = Array.from(typeSelect.querySelectorAll('option')).map(o => o.textContent)
    expect(options).toContain('http')
    expect(options).toContain('digest')
    expect(options).toContain('feed')
  })

  it('applies the wide content class to the dialog', async () => {
    const wrapper = mount(LogsDialog)
    await openDialog(wrapper)

    const dialog = document.body.querySelector('[role="dialog"]')
    expect(dialog).not.toBeNull()
    expect(dialog!.className).toContain('max-w-[min(66vw,1100px)]')
  })

  it('lazy-loads older entries when scrolling to the bottom', async () => {
    mockedList
      .mockResolvedValueOnce({ entries: manyEntries(200), total: 250, has_more: true })
      .mockResolvedValueOnce({ entries: manyEntries(50, 200), total: 250, has_more: false })

    const wrapper = mount(LogsDialog)
    await openDialog(wrapper)
    expect(mockedList).toHaveBeenLastCalledWith({ limit: 200 })
    expect(bodyText()).toContain('showing 200 of 250')

    const scroller = document.body.querySelector('[data-logs-dialog] .overflow-y-auto') as HTMLElement
    // Scroll near the bottom to trigger loading older entries.
    Object.defineProperty(scroller, 'scrollTop', { value: 100000, configurable: true })
    Object.defineProperty(scroller, 'clientHeight', { value: 400, configurable: true })
    Object.defineProperty(scroller, 'scrollHeight', { value: 400, configurable: true })
    scroller.dispatchEvent(new Event('scroll'))
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
    const scroller = document.body.querySelector('[data-logs-dialog] .overflow-y-auto') as HTMLElement
    Object.defineProperty(scroller, 'scrollTop', { value: 100000, configurable: true })
    Object.defineProperty(scroller, 'clientHeight', { value: 400, configurable: true })
    Object.defineProperty(scroller, 'scrollHeight', { value: 400, configurable: true })
    scroller.dispatchEvent(new Event('scroll'))
    await flushPromises()

    // 'entry 2' appears only once despite overlapping page boundary; new
    // entries 3 and 4 were appended.
    const matches = bodyText().match(/entry 2/g)
    expect(matches).toHaveLength(1)
    expect(bodyText()).toContain('entry 3')
    expect(bodyText()).toContain('entry 4')
  })
})
