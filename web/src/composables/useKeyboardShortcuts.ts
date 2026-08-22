import { onMounted, onUnmounted } from 'vue'

// Keyboard shortcuts for the feed reader, following Reminiflux bindings
// (github.com/reminiflux/reminiflux) wherever an equivalent action exists:
//   j / n / ArrowRight : next item          k / p / ArrowLeft : previous item
//   Home / End         : first / last item  m : toggle read/unread
//   s                  : toggle saved       v : open article URL
//   ArrowUp / ArrowDown: previous / next feed
//   PageUp / PageDown  : scroll content pane by one page
//   u                  : toggle unread-only filter
//   Shift+A            : mark active feed read
//   r                  : refresh unread counts
//   Shift+D            : toggle dark/light theme
//   ? or h             : shortcuts help overlay
// All shortcuts are suppressed while typing in inputs, textareas, select
// elements or contentEditable fields, and while modifier combos are held.
export function useKeyboardShortcuts(ctx: {
  items: () => any[]
  selectedItem: () => any | null
  feeds: () => any[]
  activeFeedId: () => number | null
  selectItem: (item: any) => void
  selectFeed: (id: number) => void
  toggleRead: (item: any) => void
  toggleSaved: (item: any) => void
  markAllRead: () => void
  toggleShowRead: () => void
  refreshCounts: () => void
  toggleTheme: () => void
  openHelp: () => void
}) {
  function scrollSelectedItemIntoView() {
    requestAnimationFrame(() => {
      document.querySelector<HTMLElement>('[data-selected-item]')?.scrollIntoView({ block: 'nearest' })
    })
  }

  function selectItemByIndex(idx: number) {
    const items = ctx.items()
    if (items.length === 0) return
    const clamped = Math.max(0, Math.min(idx, items.length - 1))
    ctx.selectItem(items[clamped])
    scrollSelectedItemIntoView()
  }

  function selectFeedByIndex(offset: number) {
    const feeds = ctx.feeds()
    if (feeds.length === 0) return
    const cur = ctx.activeFeedId()
    const idx = cur != null ? feeds.findIndex(f => f.id === cur) : -1
    const next = idx === -1 ? 0 : Math.max(0, Math.min(idx + offset, feeds.length - 1))
    if (next !== idx) ctx.selectFeed(feeds[next].id)
  }

  function onKeyDown(e: KeyboardEvent) {
    // Never fire while typing in editable fields.
    const target = e.target as HTMLElement | null
    const tag = target?.tagName
    if (tag === 'INPUT' || tag === 'TEXTAREA' || tag === 'SELECT') return
    if (target?.isContentEditable) return
    // Buttons swallow shortcuts too (e.g. focus restored to a header button
    // after its dialog closes, or focus left on a clicked feed row). Blur the
    // button and proceed instead of silently ignoring every keypress.
    if (tag === 'BUTTON' || typeof target?.closest === 'function' && target.closest('button')) {
      ;(document.activeElement as HTMLElement | null)?.blur?.()
    }
    // Never hijack browser/modifier combos (except our Shift+ bindings).
    if (e.ctrlKey || e.metaKey || e.altKey) return

    const item = ctx.selectedItem()

    switch (e.key) {
      // --- Item navigation ---
      case 'ArrowRight':
      case 'n':
      case 'j':
        e.preventDefault()
        selectItemByIndex(item ? ctx.items().findIndex(i => i.id === item.id) + 1 : 0)
        break
      case 'ArrowLeft':
      case 'p':
      case 'k':
        e.preventDefault()
        selectItemByIndex(item ? ctx.items().findIndex(i => i.id === item.id) - 1 : -1)
        break
      case 'Home':
        e.preventDefault()
        selectItemByIndex(0)
        break
      case 'End':
        e.preventDefault()
        selectItemByIndex(ctx.items().length - 1)
        break

      // --- Item actions ---
      case 'm':
        e.preventDefault()
        if (item) ctx.toggleRead(item)
        break
      case ' ':
        // Space only toggles read when an item is selected; otherwise let the
        // browser handle it (page scroll).
        if (!item) break
        e.preventDefault()
        ctx.toggleRead(item)
        break
      case 's':
      case 'S':
        if (item) ctx.toggleSaved(item)
        break
      case 'v':
      case 'V':
        if (item?.url) window.open(item.url, '_blank')
        break

      // --- Feed navigation ---
      case 'ArrowUp':
        e.preventDefault()
        selectFeedByIndex(-1)
        break
      case 'ArrowDown':
        e.preventDefault()
        selectFeedByIndex(1)
        break
      case 'PageUp':
      case 'PageDown': {
        const el = document.querySelector<HTMLElement>('[data-item-content]')
        if (el) {
          e.preventDefault()
          el.scrollBy(0, e.key === 'PageUp' ? -el.clientHeight : el.clientHeight)
        }
        break
      }

      // --- List / app actions ---
      case 'u':
      case 'U':
        ctx.toggleShowRead()
        break
      case 'A':
        ctx.markAllRead()
        break
      case 'r':
      case 'R':
        ctx.refreshCounts()
        break
      case 'D':
        ctx.toggleTheme()
        break
      case '?':
        e.preventDefault()
        ctx.openHelp()
        break
      case 'h':
        if (!item) ctx.openHelp()
        break
    }
  }

  onMounted(() => window.addEventListener('keydown', onKeyDown))
  onUnmounted(() => window.removeEventListener('keydown', onKeyDown))
}
