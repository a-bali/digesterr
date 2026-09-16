import { defineStore } from 'pinia'
import { ref, watch } from 'vue'

const STORAGE_KEY = 'digesterr-layout'

// 'panes'   — the original 3 panes side by side (sidebar | list | content)
// 'stacked' — sidebar, then list on top and content below in the right column
export type LayoutMode = 'panes' | 'stacked'

function loadMode(): LayoutMode {
  try {
    const saved = localStorage.getItem(STORAGE_KEY)
    if (saved === 'panes' || saved === 'stacked') return saved
  } catch { /* ignore */ }
  return 'panes'
}

export const useLayoutStore = defineStore('layout', () => {
  const mode = ref<LayoutMode>(loadMode())

  watch(mode, (m) => {
    try { localStorage.setItem(STORAGE_KEY, m) } catch { /* ignore */ }
  })

  function setMode(m: LayoutMode) { mode.value = m }
  function toggle() { mode.value = mode.value === 'panes' ? 'stacked' : 'panes' }

  return { mode, setMode, toggle }
})
