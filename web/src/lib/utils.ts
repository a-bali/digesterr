import { clsx, type ClassValue } from 'clsx'
import { twMerge } from 'tailwind-merge'

export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs))
}

export function stripContent(content: string): string {
  if (!content) return ''
  const el = document.createElement('div')
  el.innerHTML = content
  return (el.textContent || '')
    .replace(/\s+/g, ' ')
    .trim()
}
