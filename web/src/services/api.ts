import type { AllDigestResultsResponse, AuthResponse, DigestConfig, DigestResult, DigestResultItemWithData, Feed, FeedItemsResponse, HealthInfo, Item, LogsResponse, MarkRelativeOptions, MetricsSummary, MultiFeedItemsResponse, SavedItemsResponse, User } from '../types/api'

// The access token lives in memory only — never localStorage. Any XSS that
// reaches the page (e.g. through feed HTML) could otherwise read it straight
// out of storage and impersonate the account; an in-memory token dies with
// the tab. Persistence across reloads comes from the HttpOnly refresh cookie
// (credentials: same-origin on tryRefresh), not from a readable token copy.
let token: string | null = null
type UnauthorizedCallback = () => void
let onUnauthorized: UnauthorizedCallback | null = null

export function setAuthToken(t: string | null) {
  token = t
  // Best-effort cleanup of tokens persisted by older versions: they are no
  // longer written, but a stale copy must not survive the upgrade.
  try {
    localStorage.removeItem('digesterr-token')
  } catch {
    // Storage unavailable (private mode) — nothing to clean.
  }
}

export function getAuthToken() {
  return token
}

// hasAuthToken reports whether an access token (or a refresh attempt worth
// making) exists. After reload the in-memory token is gone, but the refresh
// cookie may still hold a session — so "no token" means "try silent refresh",
// not "definitely logged out".
export function hasAuthToken() {
  return token != null
}

async function silentRefresh(): Promise<boolean> {
  // tryRefresh already serializes concurrent refresh attempts.
  return tryRefresh()
}

export { silentRefresh }

export function setOnUnauthorized(cb: UnauthorizedCallback) {
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
    // Keep the server's message instead of a hardcoded "unauthorized". The
    // signin endpoint answers 401 with "invalid email or password", and the
    // login form renders e.message — overwriting every 401 with a generic
    // string told the user nothing about why the credential was rejected.
    const body = await parseBody(res)
    throw new ApiError(401, body)
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
  // Rehydrates the profile after a reload: the access token is memory-only, so
  // the client must ask for the user rather than decode a stored one.
  me: () => api<User>('/auth/me'),
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
  // Resolve the name a feed declares for itself, used to prefill the name
  // field in the add-feed dialog when the user only supplies a URL.
  discover: (type: string, url: string) =>
    api<{ name: string }>('/feeds/discover', { method: 'POST', body: JSON.stringify({ type, url }) }),
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
  // Total saved items across all feeds. NOT an unread count — the sidebar shows
  // it on the Saved row, which is a status view, not an inbox.
  savedCount: () => api<{ count: number }>('/saved-count'),
  // The Saved view's list in one query. Includes archive feeds, so items
  // orphaned by a feed deletion keep their provenance.
  savedItems: (opts: { status?: string; limit?: number; offset?: number; sort?: string } = {}) =>
    api<SavedItemsResponse>(`/saved${qs(opts)}`),
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
  // A single PATCH carrying either field: `status` writes read state, `saved`
  // flips the saved FLAG. They are independent, so either can be sent alone.
  updateStatus: (id: number, body: { status?: string; saved?: boolean }) =>
    api<Item>(`/items/${id}`, { method: 'PATCH', body: JSON.stringify(body) }),
  getItem: (id: number) => api<Item>(`/items/${id}`),
  extract: (id: number) => api<Item>(`/items/${id}/extract`, { method: 'POST' }),
  // Drop the extracted body so the item renders its original feed content.
  deleteExtracted: (id: number) => api<Item>(`/items/${id}/extract`, { method: 'DELETE' }),
  summarize: (id: number, opts?: { sentence_count?: number; min_sentences?: number; max_sentences?: number; detail?: string; prompt?: string }) =>
    api<import('../types/api').ItemSummaryResponse>(`/items/${id}/summarize`, {
      method: 'POST',
      body: JSON.stringify({
        sentence_count: opts?.sentence_count,
        min_sentences: opts?.min_sentences,
        max_sentences: opts?.max_sentences,
        detail: opts?.detail,
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

export interface LlmSettings {
  model: string
  default_model: string
  overridden: boolean
  // The endpoint's offered model list, for the picker. Advisory only: empty when
  // the list could not be fetched, which is why the field stays editable.
  models: string[]
}

// EmailSettings is the per-account AgentMail configuration for the email feed
// type. `token_set` reports whether a credential is stored; the token itself is
// NEVER sent to the client, so the settings dialog edits it blind (an empty field
// means "leave it alone", not "clear it").
export interface EmailSettings {
  configured: boolean
  inbox: string
  token_set: boolean
}

export const settingsApi = {
  getLLM: () => api<LlmSettings>('/settings/llm'),
  updateLLM: (model: string) =>
    api<LlmSettings>('/settings/llm', { method: 'PUT', body: JSON.stringify({ model }) }),
  resetLLM: () => api<LlmSettings>('/settings/llm', { method: 'DELETE' }),

  getEmail: () => api<EmailSettings>('/settings/email'),
  // apiKey is omitted (rather than sent empty) when the field still holds the
  // stored-key mask: the backend reads a missing key as "keep the stored token".
  // An empty string is sent when the user emptied the field, which now means
  // "clear the stored key" (the inbox is kept).
  updateEmail: (inbox: string, apiKey?: string) =>
    api<EmailSettings>('/settings/email', {
      method: 'PUT',
      body: JSON.stringify(apiKey === undefined ? { inbox } : { inbox, api_key: apiKey }),
    }),
  resetEmail: () => api<EmailSettings>('/settings/email', { method: 'DELETE' }),
}

// healthApi.get returns the readiness payload: it carries no feature state.
// Email feed availability used to be reported here, but that is a per-account
// setting now and lives in settingsApi.getEmail().
export const healthApi = {
  get: () => api<HealthInfo>('/health'),
}
