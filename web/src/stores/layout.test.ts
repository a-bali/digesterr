import { describe, it, expect, beforeEach } from 'vitest'
import { setActivePinia, createPinia } from 'pinia'
import { useLayoutStore } from './layout'

describe('useLayoutStore', () => {
  beforeEach(() => { setActivePinia(createPinia()); localStorage.clear() })

  it('defaults to the panes layout', () => {
    expect(useLayoutStore().mode).toBe('panes')
  })

  it('restores the persisted layout', () => {
    localStorage.setItem('digesterr-layout', 'stacked')
    expect(useLayoutStore().mode).toBe('stacked')
  })

  it('ignores a garbage stored value', () => {
    localStorage.setItem('digesterr-layout', 'nonsense')
    expect(useLayoutStore().mode).toBe('panes')
  })

  it('persists the layout on toggle', async () => {
    const store = useLayoutStore()
    store.toggle()
    await Promise.resolve()
    expect(store.mode).toBe('stacked')
    expect(localStorage.getItem('digesterr-layout')).toBe('stacked')
    store.toggle()
    await Promise.resolve()
    expect(store.mode).toBe('panes')
    expect(localStorage.getItem('digesterr-layout')).toBe('panes')
  })

  it('setMode persists an explicit choice', async () => {
    const store = useLayoutStore()
    store.setMode('stacked')
    await Promise.resolve()
    expect(localStorage.getItem('digesterr-layout')).toBe('stacked')
  })
})
