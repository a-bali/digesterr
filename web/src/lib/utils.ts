import { clsx, type ClassValue } from 'clsx'
import { twMerge } from 'tailwind-merge'

export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs))
}

export function stripContent(content: string): string {
  if (!content) return ''
  const doc = new DOMParser().parseFromString(content, 'text/html')
  return (doc.body.textContent || '')
    .replace(/\s+/g, ' ')
    .trim()
}

// Decode HTML entities in a string (e.g. "&amp;" → "&", "&lt;" → "<").
// Used for titles stored with raw HTML entities from feed APIs.
export function decodeHTMLEntities(s: string): string {
  if (!s) return ''
  const el = document.createElement('textarea')
  el.innerHTML = s
  return el.value
}
