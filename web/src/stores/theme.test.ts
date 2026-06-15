import { describe, it, expect, beforeEach, vi } from 'vitest'
import { nextTick } from 'vue'
import { setActivePinia, createPinia } from 'pinia'
import { useThemeStore } from './theme'

beforeEach(() => {
  localStorage.clear()
  document.documentElement.classList.remove('dark')
  document.documentElement.removeAttribute('style')
  setActivePinia(createPinia())
  window.matchMedia = vi.fn().mockReturnValue({ matches: false }) as any
})

describe('theme store', () => {
  it('initializes with system mode and blue accent', () => {
    const store = useThemeStore()
    expect(['system', 'light', 'dark']).toContain(store.mode)
    expect(store.accent).toBe('blue')
  })

  it('persists to localStorage', async () => {
    const store = useThemeStore()
    store.setMode('dark')
    store.setAccent('green')
    await nextTick()
    const saved = JSON.parse(localStorage.getItem('digesterr-theme') || '{}')
    expect(saved.mode).toBe('dark')
    expect(saved.accent).toBe('green')
  })

  it('restores from localStorage', () => {
    localStorage.setItem('digesterr-theme', JSON.stringify({ mode: 'light', accent: 'purple' }))
    const store = useThemeStore()
    expect(store.mode).toBe('light')
    expect(store.accent).toBe('purple')
  })

  it('sets CSS variables on the root element', async () => {
    const store = useThemeStore()
    store.setAccent('teal')
    await nextTick()
    const accent = document.documentElement.style.getPropertyValue('--accent')
    expect(accent).toBe('173 80% 36%')
  })

  it('toggles dark mode', async () => {
    const store = useThemeStore()
    store.setMode('dark')
    await nextTick()
    expect(document.documentElement.classList.contains('dark')).toBe(true)
    store.setMode('light')
    await nextTick()
    expect(document.documentElement.classList.contains('dark')).toBe(false)
  })
})
