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
}

export interface DigestConfig {
  id: number
  user_id: number
  schedule: string
  config: string
  created_at: string
  updated_at: string
}

export interface AuthResponse {
  user: User
  access_token: string
}

export interface FeedScale {
  points_max: number
  points_avg: number
  comments_max: number
  comments_avg: number
}

export interface FeedItemsResponse {
  items: Item[]
  scale: FeedScale
}
