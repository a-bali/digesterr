import { defineStore } from 'pinia'
import { ref, watch } from 'vue'

const STORAGE_KEY = 'digesterr-theme'

// Single source of truth for the accent palette. `[accent, accent-foreground]`
// are raw HSL triplets consumed both here and by ThemeMenu's swatches.
export const accents: Record<string, [string, string]> = {
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

    // Dark mode + all neutral colors come from the :root / .dark CSS blocks
    // in index.css; the store only flips the class and the accent.
    root.classList.toggle('dark', isDark)
    const [ac, fg] = accents[accent.value] || accents.blue
    root.style.setProperty('--accent', ac)
    root.style.setProperty('--accent-foreground', fg)

    localStorage.setItem(STORAGE_KEY, JSON.stringify({ mode: mode.value, accent: accent.value }))
  }

  watch([mode, accent], apply, { immediate: true })

  function setMode(m: string) { mode.value = m }
  function setAccent(a: string) { accent.value = a }

  return { mode, accent, setMode, setAccent, apply }
})
