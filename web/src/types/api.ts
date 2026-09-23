export interface User {
  id: number
  email: string
  created_at: string
  updated_at: string
}

export interface Feed {
  id: number
  user_id: number
  category: string
  type: string
  name: string
  url: string
  config: string
  min_score: number
  last_fetched_at: string | null
  status: string
  extract_content: number
  /**
   * Clean the body the feed already delivered with go-readability at ingest, and
   * store the result in the item's extracted content. An alternative to
   * `extract_content` (which fetches the page behind the link): the server rejects
   * both being set, because both write the same field.
   */
  readability: number
  last_error: string
  last_error_at: string | null
  last_error_severity: string
  /** Consecutive fetch failures; drives the retry backoff (next_poll_at). */
  fail_count: number
  /** Earliest time the feed may be polled again; null means due now. */
  next_poll_at: string | null
  created_at: string
  updated_at: string
}

export interface Item {
  id: number
  feed_id?: number
  hash?: string
  title: string
  url?: string
  content: string
  author?: string
  comment_url: string
  points: number
  comments_count: number
  published_at: string | null
  status: 'unread' | 'read' | 'digested'
  /** Saved/starred is a FLAG, orthogonal to read state: 1 when starred. */
  is_saved?: number
  fetched_at?: string
  created_at?: string
  updated_at?: string
  last_enriched_at?: string | null
  extract_content: string
  extract_status: string
  /**
   * Plain text for the list row, flattened server-side from the item's stored
   * HTML bodies (preferring the extracted text when one exists).
   *
   * The row must NOT flatten the bodies itself: a list response carries a
   * bounded prefix of each body, and a character cap over raw HTML can land
   * inside a leading run of markup — leaving the row with no excerpt at all
   * until the full item is fetched by opening it.
   */
  excerpt?: string
  extract_retries?: number
  extract_date?: string | null
}

export interface ItemComment {
  author: string
  text: string
  points: number
  valid_points: boolean
  url: string
}

export interface ItemCommentsResponse {
  comments: ItemComment[]
  cached: boolean
}

export interface ItemSummaryResponse {
  content_summary: string
  comments_summary: string
}

/** Ordering keys accepted by the item list. Must match the server's. */
export type ItemSort = 'date_desc' | 'date_asc' | 'score_desc' | 'score_asc'

/**
 * Scope plus view ordering for a relative mark-read request.
 *
 * `sort` and `min_score` mirror the middle pane as rendered: the server
 * resolves "above"/"below" in SQL against the same ordering and filter, so
 * rows that are filtered out or not yet lazily loaded still land on the
 * correct side of the boundary.
 */
export interface MarkRelativeOptions {
  /** Restricts the update to one category. */
  category?: string
  /** Marks the "Feeds" section as the scope; the value itself is not parsed. */
  feed_ids?: string
  /** Middle-pane sort key. */
  sort?: ItemSort
  /** Middle-pane minimum-score filter; 0 means no filter. */
  min_score?: number
}

export interface DigestConfig {
  id: number
  user_id: number
  name: string
  schedule: string
  config: string
  last_error: string
  last_error_at: string | null
  created_at: string
  updated_at: string
}

export interface DigestResult {
  id: number
  user_id: number
  config_id: number
  config_snapshot: string
  content: string
  status: string
  created_at: string
  updated_at: string
}

export interface DigestResultWithConfigName extends DigestResult {
  config_name: string
  title: string
  item_count: number
}

export interface AllDigestResultsResponse {
  results: DigestResultWithConfigName[]
  has_more: boolean
  total: number
}

export interface MetricsGroup {
  reason: string
  feed: string
  host: string
  status: string
  /** Recorded operations, not HTTP requests — see http_requests. */
  count: number
  bytes: number
  /** Outbound HTTP requests behind these operations (a poll can fan out). */
  http_requests: number
  errors: number
  avg_ms: number
  pct_total: number
}

export interface MetricsBucket {
  time: string
  count: number
  bytes: number
  http_requests: number
  errors: number
}

/** Last observed rate-limit state for one provider that advertises a budget. */
export interface ProviderQuota {
  /** Feed type the budget belongs to, e.g. "twitter" or "reddit". */
  provider: string
  /** False until a response carrying rate-limit headers has been seen, so a
   * zero remaining is not mistaken for an exhausted budget. */
  measured: boolean
  limit: number
  remaining: number
  reset?: string
  checked_at?: string
  /** Narrows the budget to the call that governs feed updates, for providers
   * that meter each endpoint separately. */
  endpoint?: string
  blocked: boolean
  blocked_until?: string
  /** Headroom kept for ingestion; budget_low is true when below it. */
  reserve: number
  budget_low: boolean
}

export interface MetricsSummary {
  /** Recorded operations, not HTTP requests — see total_http_requests. */
  total_requests: number
  total_http_requests: number
  total_bytes: number
  success_count: number
  error_count: number
  avg_duration_ms: number
  groups: MetricsGroup[]
  buckets: MetricsBucket[]
  /** One entry per provider with a registered client; providers that
   * publish no rate-limit headers never appear. */
  quotas?: ProviderQuota[]
}

export interface DigestResultItemWithData {
  id: number
  result_id: number
  item_id: number
  is_highlight: number
  result_item_created_at: string
  feed_id: number
  hash: string
  title: string
  url: string
  content: string
  author: string
  comment_url: string
  points: number
  comments_count: number
  published_at: string | null
  status: string
  fetched_at: string
  created_at: string
  updated_at: string
  last_enriched_at: string | null
  extract_content: string
  extract_status: string
  extract_retries: number
  extract_date: string | null
}

export interface DigestContent {
  title?: string
  summary: string
  sections?: DigestSection[]
  highlights: DigestContentItem[]
  items: DigestContentItem[]
}

export interface DigestSection {
  name: string
  kind: 'category' | 'feed'
  feed_id?: number
  sections?: DigestSection[]
  highlights?: DigestContentItem[]
  items?: DigestContentItem[]
}

export interface DigestContentItem {
  item_id: number
  feed_id: number
  feed_name: string
  title: string
  url: string
  comment_url: string
  points: number
  comments_count: number
  published_at: string
  extracted_content?: string
  llm_summary?: string
  top_comments?: DigestComment[]
}

export interface DigestComment {
  author: string
  text: string
  points?: number
  url?: string
}

export interface LogEntry {
  time: string
  level: string
  type: string
  message: string
  attrs?: Record<string, string>
}

export interface LogsResponse {
  entries: LogEntry[]
  total: number
  has_more: boolean
}

export interface AuthResponse {
  user: User
  access_token: string
}

export interface MultiItem extends Item {
  feed_name: string
  feed_type: string
}

export interface FeedItemsResponse {
  items: Item[]
  has_more: boolean
  total: number
}

export interface MultiFeedItemsResponse {
  items: MultiItem[]
  has_more: boolean
  total: number
}

// The Saved view's list: a saved item with its feed metadata, including
// archive feeds ("Saved -- <name>") holding orphans of deleted feeds.
export interface SavedItemsResponse {
  items: MultiItem[]
  has_more: boolean
  total: number
}

// HealthInfo is the readiness payload. It intentionally carries no feature
// state: it is byte-identical authenticated and anonymous, so a probe response
// cannot vary with credentials. Email feed availability is per-account and comes
// from settingsApi.getEmail().
export interface HealthInfo {
  status: string
}
