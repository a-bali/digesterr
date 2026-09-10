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
  last_error: string
  last_error_at: string | null
  last_error_severity: string
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
  status: 'unread' | 'read' | 'digested' | 'saved'
  fetched_at?: string
  created_at?: string
  updated_at?: string
  last_enriched_at?: string | null
  extract_content: string
  extract_status: string
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
  count: number
  bytes: number
  errors: number
  avg_ms: number
  pct_total: number
}

export interface MetricsBucket {
  time: string
  count: number
  bytes: number
  errors: number
}

export interface MetricsSummary {
  total_requests: number
  total_bytes: number
  success_count: number
  error_count: number
  avg_duration_ms: number
  groups: MetricsGroup[]
  buckets: MetricsBucket[]
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
