<script setup lang="ts">
import { ref, computed, watch, onUnmounted, nextTick } from 'vue'
import { useFeedStore } from '../stores/feed'
import { itemApi } from '../services/api'
import { toastSuccess, toastError } from '../lib/toast'
import Separator from './ui/Separator.vue'
import DropdownMenu from './ui/DropdownMenu.vue'
import Skeleton from './ui/Skeleton.vue'
import { BookOpen, Check, Bookmark, BookmarkCheck, MessageSquare, Download, RefreshCw, Files, ChevronDown, Sparkles, Trash2, X, ArrowDownToLine } from 'lucide-vue-next'
import { absoluteTime, relativeTimeAgo } from '../lib/format'
import { decodeHTMLEntities } from '../lib/utils'
import { setItemCommentsScroller, clearItemCommentsScroller } from '../lib/itemComments'
import type { ItemComment, ItemSummaryResponse } from '../types/api'

// Prose styling for raw HTML content.
const proseClass = 'prose prose-sm dark:prose-invert max-w-none [&_a]:text-accent [&_a]:hover:underline'

// Icon-only action buttons in the header. The actions are a compact icon row
// because six labelled buttons do not fit beside the title; each keeps its label
// as title/aria-label so nothing is discoverable only by shape.
const actionBtn =
  'inline-flex size-7 shrink-0 items-center justify-center rounded-md text-muted-foreground ' +
  'transition-colors hover:bg-muted hover:text-foreground focus-visible:outline-none ' +
  'focus-visible:ring-2 focus-visible:ring-ring disabled:pointer-events-none disabled:opacity-50'

// An action button that is lit while its state holds — read/unread and
// digested/undigested are TOGGLES, so the control reports the item's state at a
// glance instead of only naming the action that a click would perform. The accent
// fill carries "unread"/"undigested" (the states worth surfacing); the muted fill
// is the resting, nothing-to-do state.
const toggleBtnOn =
  'inline-flex size-7 shrink-0 items-center justify-center rounded-md ' +
  'bg-accent text-accent-foreground transition-colors hover:bg-accent/90 ' +
  'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring'
const toggleBtnOff = actionBtn

function toggleBtnClass(on: boolean) {
  return on ? toggleBtnOn : toggleBtnOff
}

defineProps<{
  unsaveItem: (item: any) => Promise<void>
}>()

const feedStore = useFeedStore()

// Source feed for the selected item, resolved from the feed list by feed_id.
const selectedFeed = computed(() =>
  feedStore.feeds.find(f => f.id === feedStore.selectedItem?.feed_id) || null,
)

// The header's metadata. Points and comments are the strongest signals for a
// ranked feed, so they are bold and lead the row; author and feed follow as
// context. Phrased as fragments because they are joined by separators.
const itemMeta = computed(() => {
  const item = feedStore.selectedItem
  if (!item) return { points: '', comments: '', author: '', feed: '', time: '' }
  return {
    points: item.points ? String(item.points) : '',
    comments: item.comments_count > 0 ? String(item.comments_count) : '',
    author: item.author ? `By ${item.author}` : '',
    feed: selectedFeed.value?.name ?? '',
    time: relativeTimeAgo(item.published_at),
  }
})

// Whether the metadata line has anything to show — an item with no points,
// comments, author, feed or date renders no empty row.
const hasItemMeta = computed(() => Object.values(itemMeta.value).some(Boolean))

// Content extraction is not useful for twitter (login wall) or reddit
// posts whose content is already rendered by the fetcher (self-text,
// reddit-hosted images). Reddit posts linking to external articles
// still benefit from extraction.
// When the feed type is unknown (not in store), allow extraction as a fallback.
const extractionSupported = computed(() => {
  const type = selectedFeed.value?.type
  if (type === 'twitter') return false
  if (type === 'reddit') {
    const url = feedStore.selectedItem?.url ?? ''
    // Hide extraction for reddit-hosted content that's already rendered
    if (/i\.redd\.it|preview\.redd\.it|reddit\.com\/r\//i.test(url)) return false
  }
  return true
})

const extracting = ref(false)

// Which body the single content section shows. One section replaced the two
// independently-collapsible Extracted/Original blocks: with both open the user
// read the same article twice, and folding one away left a header with no useful
// purpose. Exactly one body is shown, and when an extraction exists the button
// above it toggles between them.
//
// 'extracted' is deliberately NOT a remembered preference: extraction exists to
// improve on the original, so it is the view you want by default. An item with no
// extraction is always 'original', whatever was selected on the previous item.
const contentView = ref<'original' | 'extracted'>('original')

// Is there a third section to switch to at all?
const hasExtraction = computed(() => feedStore.selectedItem?.extract_status === 'done')

// What to actually render. `hasExtraction` is the single source of truth: the
// extraction poll and the Re-extract flow both dismiss an in-flight failure
// banner the moment a body lands, and gating on the status alone left that banner
// with nothing to clear on success.
const shownBody = computed(() => {
  const item = feedStore.selectedItem
  if (!item) return ''
  return contentView.value === 'extracted' && hasExtraction.value
    ? item.extract_content
    : item.content
})

// Only offer the toggle when both bodies are real and differ. Identical bodies
// would make it a no-op, and an empty original (HN/link posts carry no text)
// means selecting "Original" would reveal nothing.
const canToggleOriginal = computed(() => {
  const item = feedStore.selectedItem
  if (!item || !hasExtraction.value) return false
  return Boolean(item.content?.trim()) && item.content !== item.extract_content
})

// Two-position toggle: left is the original, right is the extracted body. It is a
// radiogroup rather than two independent buttons because exactly one body is on
// screen — mutually exclusive options with `aria-checked` saying which.
const viewOptions = [
  { value: 'original', label: 'Original' },
  { value: 'extracted', label: 'Extracted' },
] as const

function selectView(value: 'original' | 'extracted') {
  contentView.value = value
}

// Roving focus: only the checked segment is in the tab order, and the arrow keys
// move between the two — the standard radiogroup keyboard contract.
function onViewKeydown(e: KeyboardEvent) {
  if (e.key !== 'ArrowLeft' && e.key !== 'ArrowRight') return
  e.preventDefault()
  const idx = viewOptions.findIndex(o => o.value === contentView.value)
  const next = e.key === 'ArrowRight' ? idx + 1 : idx - 1
  // Wrap, so the toggle keeps working when held in one direction.
  selectView(viewOptions[(next + viewOptions.length) % viewOptions.length].value)
}

// Note on the other extract_status values, now that one body section renders
// whatever `hasExtraction` says:
//   'cleared' — the user deleted the extraction; the server keeps the status so a
//     later pass will not re-extract, but to the reader there is no extraction at
//     all, so the pane shows the original and the action button offers "Extract".
//   'skipped' — the server declined to produce a body (Reddit/HN-hosted URL, or
//     text it could not improve). Nothing to display: the old code needed an
//     explicit gate for this, because a header gated only on "status is set" drew
//     an "Extracted" header over an empty body for all 593 such items in the dev
//     database. `hasExtraction` only accepts 'done', so an empty extracted body can
//     no longer be shown.
//   'pending'/'failed' — no body yet; the section renders the in-progress or
//     failure banner instead (see the template).
// "Delete extracted" is offered exactly when there is an extracted body to
// delete — the same condition under which the action button reads "Re-extract".
// (`hasExtraction` above is that condition; it is also what the view toggle uses.)

// Read state for the read toggle. `digested` counts as read: the Reader API maps
// item `digested` → read, and only digesting treats it as a distinct state for
// filtering. Testing `status === 'read'` alone therefore reported a digested item
// as unread, showing "Mark Read" on an item that was already read.
const isRead = computed(() =>
  feedStore.selectedItem?.status === 'read' || feedStore.selectedItem?.status === 'digested',
)

// Icon-button state for the header actions. Each pair of values drives
// `aria-pressed` + the tooltip, so the button reports the item's current state
// rather than only naming the action.
const isDigested = computed(() => feedStore.selectedItem?.status === 'digested')
const isSaved = computed(() => feedStore.selectedItem?.is_saved === 1)
// "Delete extracted" is offered exactly when an extracted body exists — the same
// condition under which the extract button reads "Re-extract".
const canDeleteExtraction = computed(() => extractionSupported.value && hasExtraction.value)
const readTitle = computed(() => (isRead.value ? 'Mark unread' : 'Mark read'))
const digestedTitle = computed(() => (isDigested.value ? 'Mark undigested' : 'Mark digested'))
const saveTitle = computed(() => (isSaved.value ? 'Unsave' : 'Save'))
const extractTitle = computed(() => (hasExtraction.value ? 'Re-extract' : 'Extract'))

function toggleRead() {
  if (feedStore.selectedItem) {
    feedStore.updateItemStatus(feedStore.selectedItem, isRead.value ? 'unread' : 'read')
  }
}

function toggleDigested() {
  if (feedStore.selectedItem) {
    feedStore.updateItemStatus(feedStore.selectedItem, isDigested.value ? 'unread' : 'digested')
  }
}

function toggleSave() {
  const item = feedStore.selectedItem
  if (!item) return
  // Saving and unsaving are separate calls (unsaveItem is injected), and unsave
  // needs the post-save state, so branch on the current state rather than
  // flipping a flag.
  if (isSaved.value) unsaveItem(item)
  else feedStore.updateItemStatus(item, undefined, true)
}

// On-demand top comments (experimental).
const topComments = ref<ItemComment[]>([])
const commentsLoading = ref(false)
const commentsCollapsed = ref(false)
const commentsCached = ref(false)
let commentFetchToken = 0

// --- Scroll to comments ---
const commentsRef = ref<HTMLElement | null>(null)
function scrollToComments() {
  nextTick(() => {
    const el = commentsRef.value
    if (!el) return
    // Expand comments if collapsed so the user sees them after scrolling.
    if (commentsCollapsed.value) commentsCollapsed.value = false
    el.scrollIntoView({ behavior: 'smooth', block: 'start' })
  })
}

// Hand the scroller to the shared handle the HomeView keyboard shortcut reads.
setItemCommentsScroller(scrollToComments)

// Scroll-to-comments is offered only when there is something to scroll to. It is
// a header control now (top right) rather than a chevron beside the title.
const canScrollToComments = computed(() => topComments.value.length > 0 || commentsLoading.value)

// --- LLM Summary ---
const summaryLoading = ref(false)
const summaryData = ref<ItemSummaryResponse | null>(null)
// The summary is a dismissible box above the body, not a collapsible section:
// closing it hides the box for this item without discarding the text, so
// re-running summarize (or reopening the item) brings it back.
const summaryDismissed = ref(false)
let summaryToken = 0

// Summary length is offered as three presets rather than a free slider. Each
// one is described to the user as a sentence range and to the model as both a
// range and a character (`detail`) — the presets used to differ only in length,
// which made them read alike, just longer.
type SummaryLength = 'short' | 'medium' | 'long'
type SummaryDetail = 'gist' | 'balanced' | 'detailed'
const SUMMARY_LENGTHS: Record<SummaryLength, { min: number; max: number; detail: string; level: SummaryDetail }> = {
  short: { min: 1, max: 2, detail: '1-2 sentences', level: 'gist' },
  medium: { min: 5, max: 7, detail: '5-7 sentences', level: 'balanced' },
  long: { min: 12, max: 18, detail: '12-18 sentences', level: 'detailed' },
}
const summaryLength = ref<SummaryLength>('medium')

const summaryLengthItems = computed(() =>
  (Object.keys(SUMMARY_LENGTHS) as SummaryLength[]).map(key => ({
    label: key.charAt(0).toUpperCase() + key.slice(1),
    detail: SUMMARY_LENGTHS[key].detail,
    checked: summaryLength.value === key,
    onClick: () => selectSummaryLength(key),
  })),
)

// The menu IS the summarize control (one control, not a button plus a chevron),
// so picking a length runs the summary — including re-picking the current length,
// which is how you ask for another summary. Returning early on an unchanged pick
// would make the only control dead exactly when the user wants to re-run.
function selectSummaryLength(length: SummaryLength) {
  summaryLength.value = length
  void doSummarize()
}

// Item title for naming it in a toast; undefined when blank so no empty line renders.
function itemLabel(item: any): string | undefined {
  const title = typeof item?.title === 'string' ? item.title.trim() : ''
  return title || undefined
}

async function doSummarize() {
  const item = feedStore.selectedItem
  if (!item || summaryLoading.value) return
  summaryLoading.value = true
  const token = ++summaryToken
  try {
    const { min, max, level } = SUMMARY_LENGTHS[summaryLength.value]
    const resp = await itemApi.summarize(item.id, {
      sentence_count: max,
      min_sentences: min,
      max_sentences: max,
      detail: level,
    })
    if (token !== summaryToken) return
    summaryData.value = resp
    // There is only one body section now, so generating a summary no longer folds
    // anything away — it sits above the body as a dismissible box.
    toastSuccess('Summary generated', itemLabel(item))
  } catch (e) {
    if (token === summaryToken) {
      toastError(e, 'Failed to generate summary')
    }
  } finally {
    if (token === summaryToken) summaryLoading.value = false
  }
}

// Render comment text: escape HTML, then convert Reddit markdown images and
// bare image URLs to <img> tags with size constraints.
//
// src/alt are escaped and scheme-checked BEFORE interpolation: the result is
// inserted via v-html, so an unescaped `"` in either field breaks out of the
// attribute (e.g. `![a](x" onerror="...)`) and executes in the reader session.
function escapeAttr(s: string): string {
  return s.replace(/&/g, '&amp;').replace(/"/g, '&quot;').replace(/</g, '&lt;').replace(/>/g, '&gt;')
}

// Only http(s) image URLs are rendered as <img>. Anything else (javascript:,
// data:, protocol-relative, whitespace-padded schemes) falls back to plain
// escaped text instead of a tag, so no crafted URL can become an attribute.
function safeImgSrc(raw: string): string | null {
  const src = raw.trim()
  if (!/^https?:\/\//i.test(src)) return null
  if (/[\s"'<>]/.test(src)) return null
  return src
}

function renderCommentHtml(text: string): string {
  // Step 1: Extract markdown images and bare image URLs, replace with placeholders.
  const placeholders: string[] = []
  let work = text

  // Phase 0: Convert Reddit's internal giphy syntax ![alt](giphy|ID) to real URLs.
  // The ID is restricted to URL-safe chars so it cannot smuggle `"`, spaces or
  // a scheme break into the generated img tag.
  work = work.replace(/!\[([^\]]*)\]\(giphy\|([^)]+)\)/g, (_m: string, alt: string, id: string) => {
    if (!/^[A-Za-z0-9_-]+$/.test(id)) return _m
    return `![${alt}](https://media.giphy.com/media/${id}/giphy.gif)`
  })

  // Markdown images: ![alt](url)
  work = work.replace(/!\[([^\]]*)\]\(([^)]+)\)/g, (_m, alt: string, src: string) => {
    const safe = safeImgSrc(src)
    if (!safe) return escapeAttr(_m)
    const tag = `<img src="${escapeAttr(safe)}" alt="${escapeAttr(alt)}" style="max-width:400px;max-height:300px;width:auto;height:auto;border-radius:4px;margin:4px 0;display:block" loading="lazy" />`
    placeholders.push(tag)
    return `\x00IMG${placeholders.length - 1}\x00`
  })

  // Bare image URLs from common hosts
  work = work.replace(
    /https?:\/\/(?:media\d*\.giphy\.com\/\S+\.(?:gif|webp|png|jpg|jpeg)(?:\?\S*)?|giphy\.com\/gifs\/\S+|[^\s]*\.?(?:i\.redd\.it|preview\.redd\.it|imgur\.com|i\.imgur\.com|files\.catbox\.moe|cdn\.discordapp\.com)\/\S+\.(?:jpg|jpeg|png|gif|webp|svg)(?:\?\S*)?)/g,
    (url) => {
      const safe = safeImgSrc(url)
      if (!safe) return escapeAttr(url)
      const tag = `<img src="${escapeAttr(safe)}" alt="" style="max-width:400px;max-height:300px;width:auto;height:auto;border-radius:4px;margin:4px 0;display:block" loading="lazy" />`
      placeholders.push(tag)
      return `\x00IMG${placeholders.length - 1}\x00`
    },
  )

  // Step 2: Escape HTML in the remaining text.
  const escaped = work
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')

  // Step 2b: Decode Reddit's original HTML entities that got double-escaped.
  // e.g. Reddit's &gt; → &amp;gt; after escaping → decode back to >
  let decoded = escaped
    .replace(/&amp;gt;/g, '>')
    .replace(/&amp;lt;/g, '<')
    .replace(/&amp;quot;/g, '"')
    .replace(/&amp;#39;/g, "'")
    .replace(/&amp;amp;/g, '&')

  // Step 3: Restore image tags.
  const restored = decoded.replace(/\x00IMG(\d+)\x00/g, (_, i: string) => placeholders[parseInt(i)])

  // Step 3b: Convert bare URLs to clickable links. Skip URLs already inside
  // HTML attributes (preceded by ", ', >, or /) to avoid double-linking.
  // Both the href and the link text are escaped: the text sits in element
  // content (escape <>&), the href in an attribute (escape quotes too).
  const skipPrefixes = new Set(['"', "'", '>', '/'])
  const withLinks = restored.replace(
    /((?:https?):\/\/[^\s<>"]+)/g,
    (match: string, _capture: string, offset: number) => {
      // Use this match's own offset. indexOf(match) returned the FIRST
      // occurrence in the whole string, so a URL appearing twice was judged
      // twice by the prefix of its first copy — linking one that sits inside an
      // attribute, and skipping one that sits in plain text.
      if (offset > 0 && skipPrefixes.has(restored[offset - 1])) return match
      if (!safeImgSrc(match)) return match
      const href = escapeAttr(match)
      const label = match.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;')
      return '<a href="' + href + '" target="_blank" rel="noopener noreferrer">' + label + '</a>'
    },
  )

  // Step 4: Convert plain-text newlines to <br> for visible line breaks.
  return withLinks.replace(/\n/g, '<br>')
}

// Per-item scroll memory: reopening the SAME item restores its previous
// scroll position; opening a DIFFERENT item resets to top. The scroller is a
// single persistent DOM element, so without this logic positions either leak
// across items or get lost on back-navigation.
const scroller = ref<HTMLElement | null>(null)
// Module-level: HomeView's keyed <Transition> unmounts this component when
// navigating back to the list, so per-instance state would be lost.
const scrollMemory: Map<number, number> =
  ((globalThis as any).__itemScrollMemory ??= new Map())
// Continuous capture via @scroll (bound in template): the watcher approach can't
// save on back-navigation because Vue unmounts this component before the
// selected-item watcher job runs, leaving scroller.value null. Capped: a long
// session would otherwise grow the map without bound.
const SCROLL_MEMORY_LIMIT = 200
function onScroll() {
  const id = feedStore.selectedItem?.id
  if (id != null && scroller.value) {
    if (scrollMemory.size >= SCROLL_MEMORY_LIMIT) {
      const oldest = scrollMemory.keys().next()
      if (!oldest.done) scrollMemory.delete(oldest.value)
    }
    scrollMemory.set(id, scroller.value.scrollTop)
  }
}
watch(
  () => feedStore.selectedItem?.id,
  (newId) => {
    const saved = (newId != null && scrollMemory.get(newId)) || 0
    // Defer past render + null-check inside: with {immediate:true} this fires
    // during setup when scroller is still null, and a fresh mount's item body
    // isn't in the DOM until nextTick — setting scrollTop before either would
    // no-op or clamp to 0.
    nextTick(() => {
      if (!scroller.value) return
      scroller.value.scrollTop = saved
    })
  },
  { immediate: true },
)
let extractPoll: ReturnType<typeof setInterval> | null = null

// Token guarding the extraction poll: bumped when the selected item changes
// or polling is cancelled, so a stale interval can never touch the new item.
let extractToken = 0
// Cap poll attempts so a stuck backend can't spin the interval forever.
const MAX_EXTRACT_POLLS = 60

function stopExtractPoll() {
  extractToken++
  if (extractPoll) {
    clearInterval(extractPoll)
    extractPoll = null
  }
  extracting.value = false
}

async function doExtract() {
  const item = feedStore.selectedItem
  if (!item || extracting.value) return
  extracting.value = true
  // Asking for an extraction is asking to read it, so switch the view up front
  // rather than leaving the user on the body they just replaced — and rather than
  // making them move the toggle themselves once it lands.
  contentView.value = 'extracted'
  const token = ++extractToken
  try {
    await itemApi.extract(item.id)
    // Start polling for result
    if (extractPoll) clearInterval(extractPoll)
    let polls = 0
    extractPoll = setInterval(async () => {
      if (token !== extractToken) {
        // Selected item changed while this interval was in flight.
        if (extractPoll) clearInterval(extractPoll)
        extractPoll = null
        return
      }
      polls++
      if (polls > MAX_EXTRACT_POLLS) {
        toastError(new Error('Extraction did not complete'), 'Extraction timed out', itemLabel(item))
        stopExtractPoll()
        return
      }
      try {
        const updated = await itemApi.getItem(item.id)
        if (token !== extractToken) return
        if (updated.extract_status !== 'pending') {
          if (extractPoll) clearInterval(extractPoll)
          extractPoll = null
          extracting.value = false
          // Update store with the result
          const idx = feedStore.items.findIndex(i => i.id === updated.id)
          if (idx !== -1) feedStore.items[idx] = updated
          if (feedStore.selectedItem?.id === updated.id) feedStore.selectedItem = updated
          // Report the outcome the poll observed, so extraction is not silent.
          // A landed body means the previous attempt's failure banner is stale, so
          // drop it; on failure stay on the banner rather than flipping back.
          if (updated.extract_status === 'failed') {
            toastError(new Error('extraction failed'), 'Extraction failed', itemLabel(updated))
          } else {
            contentView.value = 'extracted'
            toastSuccess('Content extracted', itemLabel(updated))
          }
        }
      } catch {
        // poll failed, will retry
      }
    }, 2000)
  } catch (e) {
    if (token === extractToken) {
      extracting.value = false
      toastError(e, 'Failed to start extraction', itemLabel(item))
    }
  }
}

async function doDeleteExtraction() {
  const item = feedStore.selectedItem
  if (!item) return
  // An extraction may still be running (Re-extract leaves the poll live). Drop
  // it first: otherwise its next tick would write the freshly extracted body
  // back over the item we are about to clear.
  stopExtractPoll()
  // Deleting the extracted body removes the view it was showing, so fall back to
  // the original — otherwise the pane would render the deleted text from a
  // contentView that no longer has anything behind it.
  contentView.value = 'original'
  await feedStore.deleteExtraction(item)
}

// Cancel in-flight comment fetch when a different item is selected.
let commentFetchTimeout: ReturnType<typeof setTimeout> | null = null
function fetchTopComments(refresh = false) {
  const item = feedStore.selectedItem
  if (!item || !item.comment_url && item.comments_count <= 0) {
    // No comments to fetch: drop any request still in flight for the previous
    // item (its response would otherwise land here and render that item's
    // comments) and clear the loading flag, or the new item shows a skeleton
    // that never resolves.
    commentFetchToken++
    if (commentFetchTimeout) clearTimeout(commentFetchTimeout)
    commentFetchTimeout = null
    topComments.value = []
    commentsLoading.value = false
    return
  }
  const token = ++commentFetchToken
  commentsLoading.value = true
  // Debounce: wait 200ms before fetching to avoid firing during rapid navigation.
  // Skip debounce on explicit refresh.
  const delay = refresh ? 0 : 200
  if (commentFetchTimeout) clearTimeout(commentFetchTimeout)
  commentFetchTimeout = setTimeout(async () => {
    if (token !== commentFetchToken) return
    try {
      const resp = await itemApi.getComments(item.id, { limit: 5, refresh })
      if (token !== commentFetchToken) return
      topComments.value = resp.comments
      commentsCached.value = resp.cached
    } catch {
      if (token === commentFetchToken) topComments.value = []
    } finally {
      if (token === commentFetchToken) commentsLoading.value = false
    }
  }, delay)
}

// Reset per-item state and debounce-fetch comments for the newly selected item.
// immediate: below the lg breakpoint HomeView wraps this pane in a keyed
// <Transition>, so selecting an item remounts the component — the item arrives
// as the watcher's initial value rather than a change, and without this the
// comments were never fetched for the item the pane was mounted with.
watch(
  () => feedStore.selectedItem?.id,
  () => {
    if (extractPoll || extracting.value) stopExtractPoll()
    // Reset comment state and debounce-fetch for the new item.
    topComments.value = []
    commentsCollapsed.value = false
    commentsCached.value = false
    fetchTopComments()
    // Reset summary state. The dismissal is per-item UI state: leaving it set
    // would open the next item with its summary box already hidden.
    summaryData.value = null
    summaryLength.value = 'medium'
    summaryDismissed.value = false
    summaryLoading.value = false
    summaryToken++
    // The body view is per-item too. Reset to the item's natural default:
    // 'extracted' when there is an extraction (it exists to improve on the
    // original, so it is the view you want), otherwise 'original'. Carrying the
    // previous item's choice over would open a fresh item on the wrong body.
    contentView.value = hasExtraction.value ? 'extracted' : 'original'
  },
  { immediate: true },
)

onUnmounted(() => {
  stopExtractPoll()
  if (commentFetchTimeout) clearTimeout(commentFetchTimeout)
  // Only unregister if we are still the live pane — a layout switch can
  // unmount this instance after its replacement has already registered.
  clearItemCommentsScroller(scrollToComments)
})
</script>

<template>
  <div ref="scroller" class="h-full overflow-y-auto overflow-x-hidden min-w-0 px-3 py-3 sm:p-6" data-item-content @scroll.passive="onScroll">
    <div v-if="!feedStore.selectedItem" class="flex h-full items-center justify-center text-muted-foreground">
      <BookOpen class="size-8 mr-2" /> <span>Select an item</span>
    </div>
    <!-- Extraction in flight: skeleton only when item has no content yet. -->
    <div v-else-if="extracting && !feedStore.selectedItem.content" data-extracting-skeleton>
      <Skeleton class="h-7 w-3/4 mb-2" />
      <Skeleton class="h-4 w-1/2 mb-6" />
      <Separator class="mb-4" />
      <div class="space-y-2">
        <Skeleton v-for="i in 8" :key="i" class="h-4" :class="i % 4 === 0 ? 'w-5/6' : 'w-full'" />
      </div>
    </div>
    <div v-else class="@container">
      <!-- One content section. The header carries everything: title and metadata
           on the left, with the controls floated to the top right — five stacked
           bands (title / meta / actions / toggle / date) cost ~206px before the
           first word of content. The pane is a `@container`, not the viewport,
           because its width depends on the split layout: at 50% on a 2000px
           screen the pane is phone-narrow while the viewport is not, and
           viewport breakpoints would keep the desktop row. -->
      <!-- Sticky: the actions and the content toggle stay reachable while
           reading a long article, which is the whole reason this header is
           compact enough to be worth pinning. -->
      <div class="sticky top-0 z-10 -mx-3 mb-3 border-b border-border bg-background px-3 pt-1 pb-2.5
                  @max-[520px]:pb-2 sm:-mx-6 sm:px-6">
          <div class="flex items-center gap-3 @max-[520px]:flex-wrap @max-[520px]:gap-y-2">
            <div class="min-w-0 flex-1 @max-[520px]:basis-full">
              <!-- Title only. The scroll-to-comments control is no longer pinned
                   here — it lives with the other header controls, top right. -->
              <a v-if="feedStore.selectedItem.url" :href="feedStore.selectedItem.url" target="_blank"
                class="block mb-1 text-[15px] font-semibold leading-snug hover:text-accent transition-colors">
                {{ decodeHTMLEntities(feedStore.selectedItem.title) }}
              </a>
              <h1 v-else class="mb-1 text-[15px] font-semibold leading-snug">{{ decodeHTMLEntities(feedStore.selectedItem.title) }}</h1>
              <!-- Metadata: points and comments lead and are bold because they are
                   the signals this header exists to surface; author, feed and age
                   follow as context. Left-aligned at every width. -->
              <div v-if="hasItemMeta" class="flex flex-wrap items-center gap-x-2 gap-y-1 text-xs text-muted-foreground">
                <span v-if="itemMeta.points" class="flex items-center gap-1 font-semibold text-foreground" title="Points">
                  ▴ {{ itemMeta.points }}
                </span>
                <template v-if="itemMeta.points && (itemMeta.comments || itemMeta.author || itemMeta.feed || itemMeta.time)">
                  <span class="opacity-40">·</span>
                </template>
                <a v-if="itemMeta.comments && feedStore.selectedItem.comment_url"
                  :href="feedStore.selectedItem.comment_url" target="_blank"
                  class="flex items-center gap-1 font-semibold text-foreground hover:text-accent transition-colors" title="Comments">
                  <MessageSquare class="size-3" /> {{ itemMeta.comments }}
                </a>
                <span v-else-if="itemMeta.comments" class="flex items-center gap-1 font-semibold text-foreground" title="Comments">
                  <MessageSquare class="size-3" /> {{ itemMeta.comments }}
                </span>
                <template v-if="itemMeta.comments && (itemMeta.author || itemMeta.feed || itemMeta.time)">
                  <span class="opacity-40">·</span>
                </template>
                <span v-if="itemMeta.author">{{ itemMeta.author }}</span>
                <template v-if="itemMeta.author && (itemMeta.feed || itemMeta.time)">
                  <span class="opacity-40">·</span>
                </template>
                <RouterLink v-if="itemMeta.feed" :to="`/feeds/${selectedFeed!.id}`"
                  class="text-accent hover:underline" title="Open feed">
                  {{ itemMeta.feed }}
                </RouterLink>
                <template v-if="itemMeta.feed && itemMeta.time">
                  <span class="opacity-40">·</span>
                </template>
                <span v-if="itemMeta.time" :title="absoluteTime(feedStore.selectedItem.published_at)">{{ itemMeta.time }}</span>
                <!-- Extraction freshness, only while the extracted body is the one
                     on screen: that is when its age matters. -->
                <template v-if="contentView === 'extracted' && feedStore.selectedItem.extract_date">
                  <span class="opacity-40">·</span>
                  <span data-extract-date>Extracted {{ absoluteTime(feedStore.selectedItem.extract_date) }}</span>
                </template>
              </div>
            </div>
            <!-- Actions, floated to the top right of the header. Icon-only
                 because six controls do not fit beside a title; each keeps a
                 title/aria-label so nothing is discoverable by shape alone.
                 Read/unread and digested/undigested are TOGGLES that light up in
                 the unread/undigested state, so the header answers "have I read
                 this, have I digested it" without a click. -->
            <div class="flex shrink-0 items-center gap-0.5 self-start @max-[520px]:w-full @max-[520px]:justify-start">
              <!-- Content view toggle (Original/Extracted). Kept from the layout
                   you approved: first in the row, since it selects which body the
                   text below shows rather than acting on the item. -->
              <div
                v-if="canToggleOriginal"
                class="mr-1 inline-flex rounded-md border border-border bg-muted/40 p-0.5"
                role="radiogroup"
                aria-label="Content view"
                data-view-toggle
                @keydown="onViewKeydown"
              >
                <button
                  v-for="opt in viewOptions"
                  :key="opt.value"
                  type="button"
                  role="radio"
                  :aria-checked="contentView === opt.value"
                  :tabindex="contentView === opt.value ? 0 : -1"
                  :data-view-option="opt.value"
                  :class="[
                    'rounded px-2.5 py-0.5 text-xs font-medium transition-colors',
                    contentView === opt.value
                      ? 'bg-accent text-accent-foreground shadow-sm'
                      : 'text-muted-foreground hover:text-foreground',
                  ]"
                  @click="selectView(opt.value)"
                >
                  {{ opt.label }}
                </button>
              </div>
              <button type="button" :class="toggleBtnClass(!isRead)" :title="readTitle" :aria-label="readTitle" :aria-pressed="!isRead"
                data-action-read @click="toggleRead">
                <Check class="size-4" />
              </button>
              <button type="button" :class="toggleBtnClass(!isDigested)" :title="digestedTitle" :aria-label="digestedTitle" :aria-pressed="!isDigested"
                data-action-digest @click="toggleDigested">
                <Files class="size-4" />
              </button>
              <button type="button" :class="toggleBtnClass(isSaved)" :title="saveTitle" :aria-label="saveTitle" :aria-pressed="isSaved"
                data-action-save @click="toggleSave">
                <BookmarkCheck v-if="isSaved" class="size-4" />
                <Bookmark v-else class="size-4" />
              </button>
              <!-- Scroll to the comments block, beside the other header controls. -->
              <button v-if="canScrollToComments" type="button" :class="actionBtn"
                title="Scroll to comments (c)" aria-label="Scroll to comments" data-action-comments
                @click="scrollToComments">
                <ArrowDownToLine class="size-4" />
              </button>
              <button v-if="extractionSupported" type="button" :class="actionBtn" :disabled="extracting"
                :title="extracting ? 'Extracting…' : extractTitle" :aria-label="extracting ? 'Extracting' : extractTitle"
                data-action-extract @click="doExtract">
                <RefreshCw v-if="extracting" class="size-4 animate-spin" />
                <Download v-else class="size-4" />
              </button>
              <button v-if="canDeleteExtraction" type="button" :class="actionBtn" :disabled="extracting"
                title="Delete the extracted content and show the original one" aria-label="Delete extracted"
                data-action-delete-extracted @click="doDeleteExtraction">
                <Trash2 class="size-4" />
              </button>
              <!-- Summarize is ONE control: the button runs the summary, and its
                   menu both picks the length and is the only place the current
                   length is visible. A chevron beside a separate button made the
                   two read as unrelated controls. -->
              <DropdownMenu :items="summaryLengthItems" data-summary-length-menu>
                <template #trigger>
                  <button type="button" :class="actionBtn" :disabled="summaryLoading"
                    :title="summaryLoading ? 'Summarizing…' : 'Summarize'" :aria-label="summaryLoading ? 'Summarizing' : 'Summarize'"
                    data-action-summarize>
                    <RefreshCw v-if="summaryLoading" class="size-4 animate-spin" />
                    <Sparkles v-else class="size-4" />
                  </button>
                </template>
              </DropdownMenu>
            </div>
          </div>
      </div>

      <!-- LLM Summary: a tinted box above the body, dismissible with the close
           button. Not a collapsible section — the copy is short and either worth
           reading or worth hiding, so there is no half state to toggle into. -->
      <section v-if="(summaryData || summaryLoading) && !summaryDismissed" data-summary-box
        class="mb-4 rounded-md border border-accent/20 bg-accent/5 px-3 py-2.5">
        <div class="flex items-center gap-1.5 mb-1.5">
          <Sparkles class="size-3 shrink-0 text-accent" />
          <span class="flex-1 text-xs font-semibold text-muted-foreground uppercase">LLM Summary</span>
          <!-- Close. Hides the box for this item; the text is not discarded, so
               re-running Summarize brings it back with the new result. -->
          <button
            type="button"
            class="shrink-0 -mr-1 p-0.5 rounded text-muted-foreground/60 hover:text-foreground hover:bg-accent/10 transition-colors"
            title="Hide summary"
            aria-label="Hide summary"
            data-summary-close
            @click="summaryDismissed = true"
          >
            <X class="size-3.5" />
          </button>
        </div>
        <!-- Loading spinner -->
        <div v-if="summaryLoading" class="flex items-center gap-2 py-1 text-sm text-muted-foreground" role="status" aria-live="polite">
          <RefreshCw class="size-4 animate-spin" aria-hidden="true" />
          <span>Generating summary…</span>
        </div>
        <!-- Summary content -->
        <div v-else-if="summaryData" class="space-y-3">
          <p v-if="summaryData.content_summary" class="text-sm leading-relaxed">{{ summaryData.content_summary }}</p>
          <p v-if="summaryData.comments_summary" class="text-sm leading-relaxed">{{ summaryData.comments_summary }}</p>
          <!-- The reply was unusable (undecodable JSON is dropped rather than
               rendered), so say so instead of showing an empty section. -->
          <p v-if="!summaryData.content_summary && !summaryData.comments_summary" data-summary-empty class="text-sm text-muted-foreground">
            The model did not return a usable summary. Try again.
          </p>
        </div>
      </section>

      <!-- The content. One section, one body: the extracted text when one exists
           (extraction exists to improve on the original, so it is the default),
           otherwise the original. The view toggle lives in the header's action row;
           the extraction date sits on the metadata line, shown only while the
           extracted body is the one on screen — that is when its age matters. -->
      <!-- Extraction still running and nothing to show yet: hold the space rather
           than flashing an empty body. -->
      <div v-if="extracting && !shownBody" class="p-4 text-sm text-muted-foreground bg-muted/30 rounded-md">
        <RefreshCw class="size-3 inline animate-spin mr-1" /> Extraction in progress…
      </div>
      <!-- Failed extraction: the error text IS the body the server stored, so it
           renders here rather than in a separate section. -->
      <div v-else-if="feedStore.selectedItem.extract_status === 'failed' && contentView === 'extracted' && !hasExtraction"
        class="p-4 rounded-md bg-warning/10 dark:bg-warning/15 border border-warning/30">
        <p class="text-xs font-medium text-warning-foreground dark:text-warning mb-1">Content processing failed after {{ feedStore.selectedItem.extract_retries }} attempt{{ feedStore.selectedItem.extract_retries === 1 ? '' : 's' }}</p>
        <pre class="text-xs text-warning-foreground/80 dark:text-warning/90 whitespace-pre-wrap">{{ feedStore.selectedItem.extract_content }}</pre>
      </div>
      <div v-else :class="proseClass" v-html="shownBody" v-external-links />

      <!-- Top Comments (experimental) -->
      <div ref="commentsRef" data-comments-section>
        <template v-if="topComments.length > 0 || commentsLoading">
          <Separator class="my-4" />
          <div class="flex items-center gap-1.5 mb-2">
            <button
              type="button"
              class="flex flex-1 items-center gap-1.5 text-left text-xs font-semibold text-muted-foreground uppercase hover:text-foreground transition-colors"
              :aria-expanded="!commentsCollapsed"
              @click="commentsCollapsed = !commentsCollapsed"
            >
              <ChevronDown class="size-3.5 shrink-0 transition-transform" :class="commentsCollapsed ? '-rotate-90' : ''" />
              <MessageSquare class="size-3" />
              <span>Top Comments</span>
            </button>
            <button
              v-if="commentsCached && !commentsLoading"
              type="button"
              class="text-muted-foreground/50 hover:text-muted-foreground transition-colors"
              title="Refresh comments"
              @click="fetchTopComments(true)"
            >
              <RefreshCw class="size-3" />
            </button>
          </div>
          <div v-if="!commentsCollapsed">
            <div v-if="commentsLoading" class="space-y-2">
              <Skeleton v-for="i in 3" :key="i" class="h-10 w-full" />
            </div>
            <ul v-else class="space-y-3">
              <li v-for="(c, i) in topComments" :key="i" class="text-sm border-l-2 border-accent/30 pl-3 py-1">
                <div class="flex items-center gap-2 mb-0.5">
                  <a v-if="c.url" :href="c.url" target="_blank" rel="noopener noreferrer" class="font-medium text-accent hover:underline">{{ c.author }}</a>
                  <span v-else class="font-medium">{{ c.author }}</span>
                  <span v-if="c.valid_points && c.points > 0" class="text-xs text-muted-foreground">{{ c.points }} pts</span>
                </div>
                <p class="text-muted-foreground text-xs leading-relaxed" v-html="renderCommentHtml(c.text)" v-external-links />
              </li>
            </ul>
          </div>
        </template>
      </div>
    </div>
  </div>
</template>
