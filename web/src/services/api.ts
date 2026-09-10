import type { AuthResponse, DigestConfig, DigestResult, DigestResultItemWithData, Feed, FeedItemsResponse, Item, LogsResponse, MarkRelativeOptions, MultiFeedItemsResponse, AllDigestResultsResponse, MetricsSummary } from '../types/api'

const TOKEN_KEY = 'digesterr-token'

let token: string | null = null
let onUnauthorized: (() => void) | null = null

export function setAuthToken(t: string | null) {
  token = t
  if (t) {
    localStorage.setItem(TOKEN_KEY, t)
  } else {
    localStorage.removeItem(TOKEN_KEY)
  }
}

export function getAuthToken() {
  if (token) return token
  const saved = localStorage.getItem(TOKEN_KEY)
  if (saved) {
    token = saved
    return token
  }
  return null
}

export function setOnUnauthorized(cb: () => void) {
  onUnauthorized = cb
}

export class ApiError extends Error {
  constructor(public status: number, body: any) {
    super(body?.error || `HTTP ${status}`)
  }
}

let refreshPromise: Promise<boolean> | null = null

async function tryRefresh(): Promise<boolean> {
  if (refreshPromise) return refreshPromise
  refreshPromise = (async () => {
    try {
      const res = await fetch('/api/auth/refresh', { method: 'POST', credentials: 'same-origin', headers: { 'Content-Type': 'application/json' } })
      if (res.ok) {
        const body = await res.json()
        setAuthToken(body.access_token)
        return true
      }
      return false
    } catch {
      return false
    } finally {
      refreshPromise = null
    }
  })()
  return refreshPromise
}

// parseBody reads the response body defensively: 204/empty bodies become null,
// valid JSON is parsed, and non-JSON payloads (e.g. HTML error pages from a
// reverse proxy) are converted into an error-shaped body instead of throwing
// a raw SyntaxError.
async function parseBody(res: Response): Promise<any> {
  if (res.status === 204) return null
  const text = await res.text()
  try {
    return JSON.parse(text)
  } catch {
    if (!text && res.ok) return null
    if (res.ok) throw new ApiError(res.status, { error: 'invalid response from server' })
    return { error: text.slice(0, 200) || res.statusText }
  }
}

export async function api<T>(path: string, options: RequestInit = {}): Promise<T> {
  const headers: Record<string, string> = {
    'Content-Type': 'application/json',
    ...(options.headers as Record<string, string>),
  }
  if (token) {
    headers['Authorization'] = `Bearer ${token}`
  }
  let res = await fetch(`/api${path}`, { ...options, headers })
  if (res.status === 401) {
    if (await tryRefresh()) {
      headers['Authorization'] = `Bearer ${token}`
      res = await fetch(`/api${path}`, { ...options, headers })
    }
  }
  if (res.status === 401) {
    setAuthToken(null)
    onUnauthorized?.()
    throw new ApiError(401, { error: 'unauthorized' })
  }
  const body = await parseBody(res)
  if (!res.ok) throw new ApiError(res.status, body)
  return body as T
}

function qs(params: Record<string, string | number | undefined>): string {
  const parts: string[] = []
  for (const [k, v] of Object.entries(params)) {
    if (v !== undefined && v !== '') parts.push(`${encodeURIComponent(k)}=${encodeURIComponent(v)}`)
  }
  return parts.length > 0 ? '?' + parts.join('&') : ''
}

export const authApi = {
  signup: (email: string, password: string) =>
    api<AuthResponse>('/auth/signup', { method: 'POST', body: JSON.stringify({ email, password }) }),
  signin: (email: string, password: string) =>
    api<AuthResponse>('/auth/signin', { method: 'POST', body: JSON.stringify({ email, password }) }),
  signout: () => api<void>('/auth/signout', { method: 'POST' }),
  changePassword: (oldPassword: string, newPassword: string) =>
    api<void>('/auth/change-password', { method: 'POST', body: JSON.stringify({ old_password: oldPassword, new_password: newPassword }) }),
  getApiKey: () => api<{ api_key: string }>('/auth/api-key'),
  rotateApiKey: () => api<{ api_key: string }>('/auth/api-key/rotate', { method: 'POST' }),
}

export const feedApi = {
  list: () => api<Feed[]>('/feeds'),
  create: (data: { category?: string; type: string; name: string; url: string; extract_content?: number; min_score?: number }) =>
    api<Feed>('/feeds', { method: 'POST', body: JSON.stringify(data) }),
  update: (id: number, data: Partial<Feed>) =>
    api<Feed>(`/feeds/${id}`, { method: 'PATCH', body: JSON.stringify(data) }),
  delete: (id: number) => api<void>(`/feeds/${id}`, { method: 'DELETE' }),
  getItems: (id: number, opts?: { status?: string; limit?: number; offset?: number; min_score?: number; sort?: string }) =>
    api<FeedItemsResponse>(`/feeds/${id}/items${qs({ status: opts?.status, limit: opts?.limit, offset: opts?.offset, min_score: opts?.min_score, sort: opts?.sort })}`),
  fetch: (id: number) => api<{ message: string }>(`/feeds/${id}/fetch`, { method: 'POST' }),
  markRead: (id: number) => api<{ message: string }>(`/feeds/${id}/mark-read`, { method: 'POST' }),
  markReadAll: (category?: string) => api<{ message: string }>('/mark-read', {
    method: 'POST',
    body: JSON.stringify(category ? { category } : {}),
  }),
  unreadCounts: () => api<Record<number, number>>('/unread-counts'),
  multiItems: (opts: { feed_ids?: number[]; category?: string; status?: string; limit?: number; offset?: number; min_score?: number; sort?: string }) => {
    const params: Record<string, string | number | undefined> = {
      status: opts.status, limit: opts.limit, offset: opts.offset,
      min_score: opts.min_score, sort: opts.sort,
    }
    if (opts.feed_ids) params.feed_ids = opts.feed_ids.join(',')
    if (opts.category) params.category = opts.category
    return api<MultiFeedItemsResponse>(`/items/multi${qs(params)}`)
  },
}

export const itemApi = {
  updateStatus: (id: number, status: string) =>
    api<Item>(`/items/${id}`, { method: 'PATCH', body: JSON.stringify({ status }) }),
  getItem: (id: number) => api<Item>(`/items/${id}`),
  extract: (id: number) => api<Item>(`/items/${id}/extract`, { method: 'POST' }),
  summarize: (id: number, opts?: { sentence_count?: number; min_sentences?: number; max_sentences?: number; prompt?: string }) =>
    api<import('../types/api').ItemSummaryResponse>(`/items/${id}/summarize`, {
      method: 'POST',
      body: JSON.stringify({
        sentence_count: opts?.sentence_count,
        min_sentences: opts?.min_sentences,
        max_sentences: opts?.max_sentences,
        prompt: opts?.prompt,
      }),
    }),
  getComments: (id: number, opts?: { limit?: number; refresh?: boolean }) =>
    api<import('../types/api').ItemCommentsResponse>(`/items/${id}/comments${qs({ limit: opts?.limit, refresh: opts?.refresh ? 'true' : undefined })}`),
  markReadAbove: (id: number, opts?: MarkRelativeOptions) =>
    api<{ message: string }>(`/items/${id}/mark-read-above`, {
      method: 'POST',
      body: JSON.stringify(opts ?? {}),
    }),
  markReadBelow: (id: number, opts?: MarkRelativeOptions) =>
    api<{ message: string }>(`/items/${id}/mark-read-below`, {
      method: 'POST',
      body: JSON.stringify(opts ?? {}),
    }),
}

export const digestApi = {
  listConfigs: () => api<DigestConfig[]>('/digest-configs'),
  createConfig: (data: { name: string; schedule: string; config: string }) =>
    api<DigestConfig>('/digest-configs', { method: 'POST', body: JSON.stringify(data) }),
  updateConfig: (id: number, data: { name: string; schedule: string; config: string }) =>
    api<DigestConfig>(`/digest-configs/${id}`, { method: 'PATCH', body: JSON.stringify(data) }),
  deleteConfig: (id: number) => api<void>(`/digest-configs/${id}`, { method: 'DELETE' }),
  getConfig: (id: number) => api<DigestConfig>(`/digest-configs/${id}`),
  generate: (configId: number) =>
    api<DigestResult>(`/digest-configs/${configId}/generate`, { method: 'POST' }),
  listResults: (configId: number, opts?: { limit?: number; offset?: number }) =>
    api<{ results: DigestResult[]; has_more: boolean }>(`/digest-configs/${configId}/results${qs({ limit: opts?.limit, offset: opts?.offset })}`),
  getResult: (id: number) => api<DigestResult>(`/digest-results/${id}`),
  updateResultStatus: (id: number, status: string) =>
    api<DigestResult>(`/digest-results/${id}`, { method: 'PATCH', body: JSON.stringify({ status }) }),
  deleteResult: (id: number) => api<void>(`/digest-results/${id}`, { method: 'DELETE' }),
  listResultItems: (resultId: number) => api<DigestResultItemWithData[]>(`/digest-results/${resultId}/items`),
  unreadCounts: () => api<Record<number, number>>('/digest-unread-counts'),
  listAllResults: (opts?: { limit?: number; offset?: number }) =>
    api<AllDigestResultsResponse>(`/digest-results${qs({ limit: opts?.limit, offset: opts?.offset })}`),
  markAllRead: (configId?: number) => api<{ message: string }>('/digest-results/mark-read', {
    method: 'POST',
    body: JSON.stringify(configId ? { config_id: configId } : {}),
  }),
  markReadAbove: (id: number, opts?: { config_id?: number }) =>
    api<{ message: string }>(`/digest-results/${id}/mark-read-above`, {
      method: 'POST',
      body: JSON.stringify(opts ?? {}),
    }),
  markReadBelow: (id: number, opts?: { config_id?: number }) =>
    api<{ message: string }>(`/digest-results/${id}/mark-read-below`, {
      method: 'POST',
      body: JSON.stringify(opts ?? {}),
    }),
}

export const logsApi = {
  list: (opts?: { limit?: number; offset?: number; level?: string; type?: string }) =>
    api<LogsResponse>(`/logs${qs({ limit: opts?.limit, offset: opts?.offset, level: opts?.level, type: opts?.type })}`),
}

export interface VersionInfo {
  version: string
  go: string
  os: string
  arch: string
  uptime_seconds: number
}

export const metricsApi = {
  get: (opts?: { since?: string; group_by?: string; bucket?: string }) =>
    api<MetricsSummary>(`/metrics${qs({ since: opts?.since, group_by: opts?.group_by, bucket: opts?.bucket })}`),
  reset: () => api<{ message: string }>('/metrics/reset', { method: 'POST' }),
}

export const systemApi = {
  version: () => api<VersionInfo>('/version'),
}
