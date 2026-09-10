import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import MetricsDialog from './MetricsDialog.vue'
import { metricsApi } from '../services/api'
import type { MetricsSummary } from '../types/api'

vi.mock('../services/api', () => ({
  metricsApi: {
    get: vi.fn(),
    reset: vi.fn(),
  },
}))

const mockedGet = vi.mocked(metricsApi.get)

function summary(overrides: Partial<MetricsSummary> = {}): MetricsSummary {
  return {
    total_requests: 10,
    total_http_requests: 410,
    total_bytes: 2048,
    success_count: 8,
    error_count: 2,
    avg_duration_ms: 120,
    groups: [
      { reason: 'fetch', feed: 'OpenCode', host: 'x.com', status: '2xx', count: 4, bytes: 1024, http_requests: 4, errors: 0, avg_ms: 21000, pct_total: 40 },
      { reason: 'enrichment', feed: 'OpenCode', host: 'x.com', status: '4xx', count: 3, bytes: 512, http_requests: 322, errors: 3, avg_ms: 1500, pct_total: 30 },
      { reason: 'fetch', feed: 'OpenRouter', host: 'x.com', status: '5xx', count: 3, bytes: 512, http_requests: 84, errors: 3, avg_ms: 9000, pct_total: 30 },
    ],
    buckets: [
      { time: '2026-09-10T09:00:00Z', count: 5, bytes: 1024, http_requests: 205, errors: 1 },
      { time: '2026-09-10T09:15:00Z', count: 5, bytes: 1024, http_requests: 205, errors: 1 },
    ],
    twitter_quota: {
      configured: true,
      measured: true,
      limit: 50,
      remaining: 3,
      reset: new Date(Date.now() + 600000).toISOString(),
      blocked: false,
      reserve: 10,
      budget_low: true,
    },
    ...overrides,
  }
}

// The dialog renders inside a reka-ui DialogPortal (teleported to document.body).
function bodyText(): string {
  return document.body.textContent || ''
}

function chip(label: string): HTMLElement | undefined {
  return Array.from(document.body.querySelectorAll('span'))
    .find(s => s.textContent?.trim() === label) as HTMLElement | undefined
}

async function openDialog(wrapper: ReturnType<typeof mount>) {
  await wrapper.find('button[data-metrics-trigger]').trigger('click')
  await flushPromises()
  await flushPromises()
}

describe('MetricsDialog', () => {
  beforeEach(() => {
    document.body.innerHTML = ''
    mockedGet.mockReset()
    mockedGet.mockResolvedValue(summary())
  })

  it('shows real HTTP requests, with the operation count as context', async () => {
    const wrapper = mount(MetricsDialog)
    await openDialog(wrapper)

    expect(bodyText()).toContain('HTTP Requests')
    expect(bodyText()).toContain('410')
    // total_requests (operations) is still surfaced, but as secondary text.
    expect(bodyText()).toContain('10 ops')
  })

  it('renders the per-group request column', async () => {
    const wrapper = mount(MetricsDialog)
    await openDialog(wrapper)

    expect(bodyText()).toContain('reqs')
    // 322 enrichment HTTP requests must be visible even though the record
    // count for that group is only 3.
    expect(bodyText()).toContain('322')
  })

  it('colours 4xx amber and 5xx red, so a rate limit reads differently from an outage', async () => {
    const wrapper = mount(MetricsDialog)
    await openDialog(wrapper)

    const amber = chip('4xx')
    expect(amber, '4xx chip not rendered').toBeTruthy()
    expect(amber!.className).toContain('text-amber-400')
    expect(amber!.className).not.toContain('text-red-400')

    const red = chip('5xx')
    expect(red, '5xx chip not rendered').toBeTruthy()
    expect(red!.className).toContain('text-red-400')

    const green = chip('2xx')
    expect(green, '2xx chip not rendered').toBeTruthy()
    expect(green!.className).toContain('text-green-400')
  })

  it('surfaces twitter quota headroom, flagging a low budget', async () => {
    const wrapper = mount(MetricsDialog)
    await openDialog(wrapper)

    const banner = document.body.querySelector('[data-twitter-quota]') as HTMLElement
    expect(banner, 'quota banner not rendered').toBeTruthy()
    expect(banner.textContent).toContain('X / Twitter quota')
    expect(banner.textContent).toContain('remaining')
    expect(banner.textContent).toContain('3')
    expect(banner.textContent).toContain('reserve 10')
    // remaining 3 < reserve 10 → headroom warning, amber rather than red
    // because nothing is blocked yet.
    expect(banner.className).toContain('text-amber-400')
  })

  it('shows an exhausted quota as blocked with its reset time', async () => {
    mockedGet.mockResolvedValue(summary({
      twitter_quota: {
        configured: true,
        measured: true,
        limit: 50,
        remaining: 0,
        reset: new Date(Date.now() + 300000).toISOString(),
        blocked: true,
        blocked_until: new Date(Date.now() + 300000).toISOString(),
        reserve: 10,
        budget_low: true,
      },
    }))
    const wrapper = mount(MetricsDialog)
    await openDialog(wrapper)

    const banner = document.body.querySelector('[data-twitter-quota]') as HTMLElement
    expect(banner.textContent).toContain('blocked until')
    expect(banner.className).toContain('text-red-400')
  })

  it('does not claim a quota problem before any response is seen', async () => {
    mockedGet.mockResolvedValue(summary({
      twitter_quota: {
        configured: true,
        measured: false,
        limit: 0,
        remaining: 0,
        blocked: false,
        reserve: 10,
        budget_low: false,
      },
    }))
    const wrapper = mount(MetricsDialog)
    await openDialog(wrapper)

    const banner = document.body.querySelector('[data-twitter-quota]') as HTMLElement
    expect(banner.textContent).toContain('no response observed yet')
    expect(banner.className).not.toContain('text-red-400')
    expect(banner.className).not.toContain('text-amber-400')
  })
})
