import { clsx, type ClassValue } from 'clsx'
import { twMerge } from 'tailwind-merge'

export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs))
}

// Per-content memo: stripContent runs once per rendered row per render, and
// a DOMParser per call made long lists jank on scroll. Same eviction policy
// as decodeHTMLEntities (drop rather than grow the unbounded key space).
const STRIP_CACHE_LIMIT = 1000
const stripCache = new Map<string, string>()

export function stripContent(content: string): string {
  if (!content) return ''
  const cached = stripCache.get(content)
  if (cached !== undefined) return cached
  const doc = new DOMParser().parseFromString(content, 'text/html')
  const out = (doc.body.textContent || '')
    .replace(/\s+/g, ' ')
    .trim()
  if (stripCache.size >= STRIP_CACHE_LIMIT) stripCache.clear()
  stripCache.set(content, out)
  return out
}

// Decode HTML entities in a string (e.g. "&amp;" → "&", "&lt;" → "<").
// Used for titles stored with raw HTML entities from feed APIs.
// decodeHTMLEntities is called once per rendered list row and title, so the
// DOM round-trip (createElement + innerHTML parse) was repeated for the same
// handful of strings on every scroll. Results are cached; the key space is
// unbounded across feeds, so the cache is dropped rather than grown.
const ENTITY_CACHE_LIMIT = 1000
const entityCache = new Map<string, string>()

export function decodeHTMLEntities(s: string): string {
  if (!s) return ''
  const cached = entityCache.get(s)
  if (cached !== undefined) return cached
  const el = document.createElement('textarea')
  el.innerHTML = s
  const decoded = el.value
  if (entityCache.size >= ENTITY_CACHE_LIMIT) entityCache.clear()
  entityCache.set(s, decoded)
  return decoded
}
