import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { nextTick } from 'vue'
import { mount, type VueWrapper } from '@vue/test-utils'
import { createPinia, setActivePinia, type Pinia } from 'pinia'
import { createRouter, createMemoryHistory } from 'vue-router'
import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import App from './App.vue'
import { useThemeStore } from './stores/theme'

let wrapper: VueWrapper | undefined
let pinia: Pinia

function mountApp() {
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [{ path: '/', component: { template: '<div />' } }],
  })
  // attachTo: the toaster renders inline in App's tree; without attaching, the
  // whole tree stays detached and document-level queries see nothing.
  // The active pinia is passed in so the store the test drives is the same one
  // App.vue resolves — a second createPinia() here would give it its own store.
  wrapper = mount(App, {
    attachTo: document.body,
    global: { plugins: [pinia, router] },
  })
  return wrapper
}

const themeAttr = () =>
  document.querySelector('[data-sonner-toaster]')?.getAttribute('data-sonner-theme')

beforeEach(() => {
  localStorage.clear()
  document.documentElement.classList.remove('dark')
  document.documentElement.removeAttribute('style')
  document.body.innerHTML = ''
  pinia = createPinia()
  setActivePinia(pinia)
  window.matchMedia = vi.fn().mockReturnValue({ matches: false }) as any
})

afterEach(() => {
  wrapper?.unmount()
  wrapper = undefined
})

describe('App toaster', () => {
  // The toaster's positioning lives in a stylesheet vue-sonner ships SEPARATELY
  // from its component; nothing bundles it implicitly. Without the import,
  // [data-sonner-toaster] has no `position: fixed` and no z-index, so it drops
  // into normal document flow as a full-width block — the "feed marked read"
  // toast then paints under the left pane instead of over the layout. jsdom does
  // not load CSS, so a static check on the import is the only way to pin this.
  it('imports the vue-sonner stylesheet that makes the toaster a fixed overlay', () => {
    const src = readFileSync(resolve(process.cwd(), 'src/App.vue'), 'utf8')
    expect(src).toMatch(/import\s+['"]vue-sonner\/style\.css['"]/)
  })

  it('mounts exactly one toaster, as the last element in the app tree', async () => {
    mountApp()
    await nextTick()
    expect(document.querySelectorAll('[data-sonner-toaster]').length).toBe(1)
    // Rendered last in App's template so it paints above the panes. The toaster's
    // own root is a <section> (the "Notifications" region) that wraps the <ol>.
    expect(wrapper!.element.lastElementChild?.querySelector('[data-sonner-toaster]')).not.toBeNull()
  })

  it('uses light toasts when the app is in light mode', async () => {
    useThemeStore().setMode('light')
    mountApp()
    await nextTick()
    expect(themeAttr()).toBe('light')
  })

  // The Toaster paints its own palette and does not inherit the `.dark` class,
  // so it must be told the resolved theme; otherwise dark mode gets a white card.
  it('switches to dark toasts when the app enters dark mode', async () => {
    const store = useThemeStore()
    mountApp()
    store.setMode('dark')
    await nextTick()
    expect(document.documentElement.classList.contains('dark')).toBe(true)
    expect(themeAttr()).toBe('dark')
  })

  it('follows the OS preference in system mode', async () => {
    window.matchMedia = vi.fn().mockReturnValue({ matches: true }) as any
    mountApp()
    await nextTick()
    expect(useThemeStore().isDark).toBe(true)
    expect(themeAttr()).toBe('dark')
  })
})
