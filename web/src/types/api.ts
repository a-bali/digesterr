export interface User {
  id: number
  email: string
  created_at: string
  updated_at: string
}

export interface Category {
  id: number
  user_id: number
  name: string
  created_at: string
  updated_at: string
}

export interface Feed {
  id: number
  user_id: number
  category_id: number
  type: string
  name: string
  url: string
  config: string
  last_fetched_at: string | null
  status: string
  extract_content: number
  created_at: string
  updated_at: string
}

export interface Item {
  id: number
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
  status: 'unread' | 'read' | 'digested' | 'saved'
  fetched_at: string
  created_at: string
  updated_at: string
  last_enriched_at: string | null
  extract_content: string
  extract_status: string
  extract_retries: number
  extract_date: string | null
}

export interface DigestConfig {
  id: number
  user_id: number
  schedule: string
  config: string
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
  summary: string
  highlights: DigestContentItem[]
  items: DigestContentItem[]
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
  points: number
  url?: string
}

export interface AuthResponse {
  user: User
  access_token: string
}

export interface FeedItemsResponse {
  items: Item[]
  has_more: boolean
}
