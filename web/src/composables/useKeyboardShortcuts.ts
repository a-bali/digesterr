import { onMounted, onUnmounted } from 'vue'

// Keyboard shortcuts for the feed reader (Vim-like).
// ArrowLeft/Right: prev/next item; ArrowUp/Down: scroll content;
// PageUp/Down: prev/next feed; Space: toggle read; S: toggle saved; V: open URL.
export function useKeyboardShortcuts(ctx: {
  items: () => any[]
  selectedItem: () => any | null
  feeds: () => any[]
  activeFeedId: () => number | null
  selectItem: (item: any) => void
  selectFeed: (id: number) => void
  toggleRead: (item: any) => void
  toggleSaved: (item: any) => void
}) {
  function onKeyDown(e: KeyboardEvent) {
    const tag = (e.target as HTMLElement)?.tagName
    if (tag === 'INPUT' || tag === 'TEXTAREA' || tag === 'SELECT') return
    const item = ctx.selectedItem()
    switch (e.key) {
      case 'ArrowLeft':
      case 'ArrowRight': {
        const items = ctx.items()
        if (items.length === 0) return
        const idx = item ? items.findIndex(i => i.id === item.id) : -1
        const next = e.key === 'ArrowRight' ? idx + 1 : idx - 1
        if (next >= 0 && next < items.length) ctx.selectItem(items[next])
        break
      }
      case 'ArrowUp':
      case 'ArrowDown': {
        const el = document.querySelector<HTMLElement>('[data-item-content]')
        if (el) el.scrollBy(0, e.key === 'ArrowUp' ? -80 : 80)
        break
      }
      case 'PageUp':
      case 'PageDown': {
        const feeds = ctx.feeds()
        if (feeds.length === 0) return
        const cur = ctx.activeFeedId()
        const idx = cur ? feeds.findIndex(f => f.id === cur) : -1
        const next = e.key === 'PageDown' ? idx + 1 : idx - 1
        if (next >= 0 && next < feeds.length) ctx.selectFeed(feeds[next].id)
        break
      }
      case ' ':
        e.preventDefault()
        if (item) ctx.toggleRead(item)
        break
      case 's':
      case 'S':
        if (item) ctx.toggleSaved(item)
        break
      case 'v':
      case 'V':
        if (item?.url) window.open(item.url, '_blank')
        break
    }
  }

  onMounted(() => window.addEventListener('keydown', onKeyDown))
  onUnmounted(() => window.removeEventListener('keydown', onKeyDown))
}
