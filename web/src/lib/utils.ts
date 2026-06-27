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
