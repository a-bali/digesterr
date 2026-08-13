import { ref, onMounted, onUnmounted, computed, type Ref } from 'vue'

// Standard Tailwind breakpoints.
export const BREAKPOINTS = {
  md: 768,
  lg: 1024,
} as const

// Reactive media query. Returns a ref that updates on window resize.
export function useMediaQuery(query: string): Ref<boolean> {
  const matches = ref(false)
  let mql: MediaQueryList | null = null

  const update = () => { matches.value = mql?.matches ?? false }

  onMounted(() => {
    mql = window.matchMedia(query)
    update()
    mql.addEventListener('change', update)
  })

  onUnmounted(() => {
    mql?.removeEventListener('change', update)
  })

  return matches
}

export function useBreakpoints() {
  const isDesktop = useMediaQuery(`(min-width: ${BREAKPOINTS.lg}px)`)
  const isTablet = useMediaQuery(`(min-width: ${BREAKPOINTS.md}px)`)
  const isMobile = computed(() => !isTablet.value)
  return { isDesktop, isTablet, isMobile }
}
