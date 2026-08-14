import type { AuthResponse, Category, DigestConfig, DigestResult, DigestResultItemWithData, Feed, FeedItemsResponse, Item, LogsResponse } from '../types/api'

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
  const body = res.status === 204 ? null : await res.json()
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

export const categoryApi = {
  list: () => api<Category[]>('/categories'),
  create: (name: string) => api<Category>('/categories', { method: 'POST', body: JSON.stringify({ name }) }),
  update: (id: number, name: string) => api<Category>(`/categories/${id}`, { method: 'PATCH', body: JSON.stringify({ name }) }),
  delete: (id: number) => api<void>(`/categories/${id}`, { method: 'DELETE' }),
}

export const feedApi = {
  list: () => api<Feed[]>('/feeds'),
  create: (data: { category_id?: number; category_name?: string; type: string; name: string; url: string; extract_content?: number }) =>
    api<Feed>('/feeds', { method: 'POST', body: JSON.stringify(data) }),
  update: (id: number, data: Partial<Feed>) =>
    api<Feed>(`/feeds/${id}`, { method: 'PATCH', body: JSON.stringify(data) }),
  delete: (id: number) => api<void>(`/feeds/${id}`, { method: 'DELETE' }),
  getItems: (id: number, opts?: { status?: string; limit?: number; offset?: number }) =>
    api<FeedItemsResponse>(`/feeds/${id}/items${qs({ status: opts?.status, limit: opts?.limit, offset: opts?.offset })}`),
  fetch: (id: number) => api<{ message: string }>(`/feeds/${id}/fetch`, { method: 'POST' }),
  markRead: (id: number) => api<{ message: string }>(`/feeds/${id}/mark-read`, { method: 'POST' }),
  unreadCounts: () => api<Record<number, number>>('/unread-counts'),
}

export const itemApi = {
  updateStatus: (id: number, status: string) =>
    api<Item>(`/items/${id}`, { method: 'PATCH', body: JSON.stringify({ status }) }),
  getItem: (id: number) => api<Item>(`/items/${id}`),
  extract: (id: number) => api<Item>(`/items/${id}/extract`, { method: 'POST' }),
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
}

export const logsApi = {
  list: (opts?: { limit?: number; offset?: number; level?: string; type?: string }) =>
    api<LogsResponse>(`/logs${qs({ limit: opts?.limit, offset: opts?.offset, level: opts?.level, type: opts?.type })}`),
}
