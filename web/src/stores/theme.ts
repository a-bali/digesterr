import { defineStore } from 'pinia'
import { ref, watch } from 'vue'

const STORAGE_KEY = 'digesterr-theme'

const accents: Record<string, [string, string]> = {
  blue:   ['217 91% 60%', '0 0% 100%'],
  green:  ['142 71% 45%', '0 0% 100%'],
  purple: ['271 81% 56%', '0 0% 100%'],
  orange: ['24 95% 53%', '0 0% 100%'],
  rose:   ['346 77% 50%', '0 0% 100%'],
  teal:   ['173 80% 36%', '0 0% 100%'],
  amber:  ['38 92% 50%', '0 0% 100%'],
}

function loadTheme(): { mode: string; accent: string } {
  try {
    return JSON.parse(localStorage.getItem(STORAGE_KEY) || '{}')
  } catch {
    return { mode: 'system', accent: 'blue' }
  }
}

export const useThemeStore = defineStore('theme', () => {
  const saved = loadTheme()
  const mode = ref(saved.mode || 'system')
  const accent = ref(saved.accent || 'blue')

  function apply() {
    const root = document.documentElement
    const isDark = mode.value === 'dark' || (mode.value === 'system' && window.matchMedia('(prefers-color-scheme: dark)').matches)
    root.classList.toggle('dark', isDark)
    root.style.setProperty('--background', isDark ? '222 47% 11%' : '0 0% 100%')
    root.style.setProperty('--foreground', isDark ? '210 40% 98%' : '222 47% 11%')
    root.style.setProperty('--muted', isDark ? '217 33% 17%' : '210 40% 96%')
    root.style.setProperty('--muted-foreground', isDark ? '215 20% 65%' : '215 16% 47%')
    root.style.setProperty('--border', isDark ? '217 33% 17%' : '214 32% 91%')
    root.style.setProperty('--sidebar', isDark ? '217 33% 17%' : '210 40% 96%')
    root.style.setProperty('--sidebar-foreground', isDark ? '215 20% 65%' : '215 16% 47%')
    const [ac, fg] = accents[accent.value] || accents.blue
    root.style.setProperty('--accent', ac)
    root.style.setProperty('--accent-foreground', fg)
    // Tailwind v4 generates bg-accent, hover:bg-accent etc. from
    // --color-accent. Override the variable so they pick up the change.
    root.style.setProperty('--color-accent', `hsl(${ac})`)
    root.style.setProperty('--color-accent-foreground', `hsl(${fg})`)
    root.style.setProperty('--color-background', `hsl(${isDark ? '222 47% 11%' : '0 0% 100%'})`)
    root.style.setProperty('--color-foreground', `hsl(${isDark ? '210 40% 98%' : '222 47% 11%'})`)
    root.style.setProperty('--color-muted', `hsl(${isDark ? '217 33% 17%' : '210 40% 96%'})`)
    root.style.setProperty('--color-muted-foreground', `hsl(${isDark ? '215 20% 65%' : '215 16% 47%'})`)
    root.style.setProperty('--color-border', `hsl(${isDark ? '217 33% 17%' : '214 32% 91%'})`)
    root.style.setProperty('--color-sidebar', `hsl(${isDark ? '217 33% 17%' : '210 40% 96%'})`)
    root.style.setProperty('--color-sidebar-foreground', `hsl(${isDark ? '215 20% 65%' : '215 16% 47%'})`)
    localStorage.setItem(STORAGE_KEY, JSON.stringify({ mode: mode.value, accent: accent.value }))
  }

  watch([mode, accent], apply, { immediate: true })

  function setMode(m: string) { mode.value = m }
  function setAccent(a: string) { accent.value = a }

  return { mode, accent, setMode, setAccent, apply }
})
