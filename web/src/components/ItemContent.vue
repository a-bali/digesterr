<script setup lang="ts">
import { ref, computed, watch, onUnmounted, nextTick } from 'vue'
import { useFeedStore } from '../stores/feed'
import { itemApi } from '../services/api'
import { toastError } from '../lib/toast'
import Button from './ui/Button.vue'
import Separator from './ui/Separator.vue'
import DropdownMenu from './ui/DropdownMenu.vue'
import Skeleton from './ui/Skeleton.vue'
import { BookOpen, Check, Bookmark, BookmarkCheck, MessageSquare, Download, RefreshCw, Files, ChevronDown, Sparkles, ChevronRight } from 'lucide-vue-next'
import { absoluteTime } from '../lib/format'
import { decodeHTMLEntities } from '../lib/utils'
import { setItemCommentsScroller, clearItemCommentsScroller } from '../lib/itemComments'
import type { ItemComment, ItemSummaryResponse } from '../types/api'

// Prose styling for raw HTML content.
const proseClass = 'prose prose-sm dark:prose-invert max-w-none [&_a]:text-accent [&_a]:hover:underline'

defineProps<{
  unsaveItem: (item: any) => Promise<void>
}>()

const feedStore = useFeedStore()

// Source feed for the selected item, resolved from the feed list by feed_id.
const selectedFeed = computed(() =>
  feedStore.feeds.find(f => f.id === feedStore.selectedItem?.feed_id) || null,
)

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
const extractedCollapsed = ref(false)
const originalCollapsed = ref(false)

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

// --- LLM Summary ---
const summaryLoading = ref(false)
const summaryData = ref<ItemSummaryResponse | null>(null)
const summaryCollapsed = ref(false)
let summaryToken = 0

// Summary length is offered as three presets rather than a free slider. Each
// one is described to the user and to the model as a sentence range.
type SummaryLength = 'short' | 'medium' | 'long'
const SUMMARY_LENGTHS: Record<SummaryLength, { min: number; max: number; detail: string }> = {
  short: { min: 2, max: 3, detail: '2-3 sentences' },
  medium: { min: 4, max: 5, detail: '4-5 sentences' },
  long: { min: 10, max: 15, detail: '10-15 sentences' },
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

// Picking a different length re-runs the summary so the choice takes effect
// immediately; before the first summary it only sets what will be requested.
function selectSummaryLength(length: SummaryLength) {
  if (summaryLength.value === length) return
  summaryLength.value = length
  if (summaryData.value) void doSummarize()
}

async function doSummarize() {
  const item = feedStore.selectedItem
  if (!item || summaryLoading.value) return
  summaryLoading.value = true
  const token = ++summaryToken
  try {
    const { min, max } = SUMMARY_LENGTHS[summaryLength.value]
    const resp = await itemApi.summarize(item.id, {
      sentence_count: max,
      min_sentences: min,
      max_sentences: max,
    })
    if (token !== summaryToken) return
    summaryData.value = resp
    summaryCollapsed.value = false
    // The summary replaces the bodies it was built from, so fold them away.
    extractedCollapsed.value = true
    originalCollapsed.value = true
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
function renderCommentHtml(text: string): string {
  // Step 1: Extract markdown images and bare image URLs, replace with placeholders.
  const placeholders: string[] = []
  let work = text

  // Phase 0: Convert Reddit's internal giphy syntax ![alt](giphy|ID) to real URLs.
  work = work.replace(/!\[([^\]]*)\]\(giphy\|([^)]+)\)/g, (_m: string, alt: string, id: string) => {
    return `![${alt}](https://media.giphy.com/media/${id}/giphy.gif)`
  })

  // Markdown images: ![alt](url)
  work = work.replace(/!\[([^\]]*)\]\(([^)]+)\)/g, (_m, alt: string, src: string) => {
    const tag = `<img src="${src}" alt="${alt}" style="max-width:400px;max-height:300px;width:auto;height:auto;border-radius:4px;margin:4px 0;display:block" loading="lazy" />`
    placeholders.push(tag)
    return `\x00IMG${placeholders.length - 1}\x00`
  })

  // Bare image URLs from common hosts
  work = work.replace(
    /https?:\/\/(?:media\d*\.giphy\.com\/\S+\.(?:gif|webp|png|jpg|jpeg)(?:\?\S*)?|giphy\.com\/gifs\/\S+|[^\s]*\.?(?:i\.redd\.it|preview\.redd\.it|imgur\.com|i\.imgur\.com|files\.catbox\.moe|cdn\.discordapp\.com)\/\S+\.(?:jpg|jpeg|png|gif|webp|svg)(?:\?\S*)?)/g,
    (url) => {
      const tag = `<img src="${url}" alt="" style="max-width:400px;max-height:300px;width:auto;height:auto;border-radius:4px;margin:4px 0;display:block" loading="lazy" />`
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
  const skipPrefixes = new Set(['"', "'", '>', '/'])
  const withLinks = restored.replace(
    /((?:https?|ftp):\/\/[^\s<>"]+)/g,
    (match: string, _capture: string, offset: number) => {
      // Use this match's own offset. indexOf(match) returned the FIRST
      // occurrence in the whole string, so a URL appearing twice was judged
      // twice by the prefix of its first copy — linking one that sits inside an
      // attribute, and skipping one that sits in plain text.
      if (offset > 0 && skipPrefixes.has(restored[offset - 1])) return match
      return '<a href="' + match + '" target="_blank" rel="noopener noreferrer">' + match + '</a>'
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
// selected-item watcher job runs, leaving scroller.value null.
function onScroll() {
  const id = feedStore.selectedItem?.id
  if (id != null && scroller.value) scrollMemory.set(id, scroller.value.scrollTop)
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
        toastError(new Error('Extraction did not complete'), 'Extraction timed out')
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
        }
      } catch {
        // poll failed, will retry
      }
    }, 2000)
  } catch (e) {
    if (token === extractToken) {
      extracting.value = false
      toastError(e, 'Failed to start extraction')
    }
  }
}

// Cancel in-flight comment fetch when a different item is selected.
let commentFetchTimeout: ReturnType<typeof setTimeout> | null = null
function fetchTopComments(refresh = false) {
  const item = feedStore.selectedItem
  if (!item || !item.comment_url && item.comments_count <= 0) {
    topComments.value = []
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

// Cancel in-flight extraction poll when a different item is selected.
watch(
  () => feedStore.selectedItem?.id,
  () => {
    if (extractPoll || extracting.value) stopExtractPoll()
    // Reset comment state and debounce-fetch for the new item.
    topComments.value = []
    commentsCollapsed.value = false
    commentsCached.value = false
    fetchTopComments()
    // Reset summary state.
    summaryData.value = null
    summaryLength.value = 'medium'
    summaryLoading.value = false
    summaryToken++
  },
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
    <div v-else>
      <!-- Title row with scroll-to-comments arrow -->
      <div class="flex items-start justify-between gap-2 mb-2">
        <a v-if="feedStore.selectedItem.url" :href="feedStore.selectedItem.url" target="_blank"
          class="text-xl font-semibold hover:text-accent transition-colors">
          {{ decodeHTMLEntities(feedStore.selectedItem.title) }}
        </a>
        <h1 v-else class="text-xl font-semibold">{{ decodeHTMLEntities(feedStore.selectedItem.title) }}</h1>
        <button
          v-if="topComments.length > 0 || commentsLoading"
          type="button"
          class="shrink-0 mt-1 p-1 rounded text-muted-foreground hover:text-accent hover:bg-muted transition-colors"
          title="Scroll to comments (c)"
          @click="scrollToComments"
        >
          <ChevronDown class="size-4" />
        </button>
      </div>
      <div class="flex flex-wrap items-center gap-3 text-sm text-muted-foreground mb-4">
        <span v-if="feedStore.selectedItem.author">By {{ feedStore.selectedItem.author }}</span>
        <span class="text-xs text-muted-foreground">{{ absoluteTime(feedStore.selectedItem.published_at) }}</span>
        <RouterLink v-if="selectedFeed" :to="`/feeds/${selectedFeed.id}`"
          class="text-accent hover:underline" title="Open feed">
          {{ selectedFeed.name }}
        </RouterLink>
        <span v-if="feedStore.selectedItem.points">▴ {{ feedStore.selectedItem.points }}</span>
        <a v-if="feedStore.selectedItem.comment_url"
          :href="feedStore.selectedItem.comment_url" target="_blank"
          class="flex items-center gap-1 text-accent hover:underline">
          <MessageSquare class="size-3" />
          <span v-if="feedStore.selectedItem.comments_count > 0">{{ feedStore.selectedItem.comments_count }}</span>
        </a>
        <span v-else-if="feedStore.selectedItem.comments_count > 0" class="flex items-center gap-1 text-muted-foreground">
          <MessageSquare class="size-3" /> {{ feedStore.selectedItem.comments_count }}
        </span>
      </div>
      <div class="flex gap-2 mb-4 flex-wrap">
        <Button size="sm" variant="outline" @click="feedStore.updateItemStatus(feedStore.selectedItem!, feedStore.selectedItem!.status === 'read' ? 'unread' : 'read')">
          <Check /> {{ feedStore.selectedItem?.status === 'read' ? 'Mark Unread' : 'Mark Read' }}
        </Button>
        <Button v-if="feedStore.selectedItem?.status === 'digested'" size="sm" variant="outline" @click="feedStore.updateItemStatus(feedStore.selectedItem!, 'unread')">
          <Files /> Mark Undigested
        </Button>
        <Button v-else size="sm" variant="outline" @click="feedStore.updateItemStatus(feedStore.selectedItem!, 'digested')">
          <Files /> Mark Digested
        </Button>
        <Button v-if="feedStore.selectedItem!.status === 'saved'" size="sm" variant="outline" @click="unsaveItem(feedStore.selectedItem!)">
          <BookmarkCheck /> Unsave
        </Button>
        <Button v-else size="sm" variant="outline" @click="feedStore.updateItemStatus(feedStore.selectedItem!, 'saved')">
          <Bookmark /> Save
        </Button>
        <Button v-if="extractionSupported" size="sm" variant="outline" :disabled="extracting" @click="doExtract">
          <template v-if="extracting"><RefreshCw class="animate-spin" /> Extracting</template>
          <template v-else><Download /> {{ feedStore.selectedItem.extract_status === 'done' ? 'Re-extract' : 'Extract' }}</template>
        </Button>
        <!-- Split button: summarizing runs at the selected length, and the
             attached chevron opens the length picker. -->
        <div class="flex items-center">
          <Button size="sm" variant="outline" class="rounded-r-none" :disabled="summaryLoading" @click="doSummarize">
            <template v-if="summaryLoading"><RefreshCw class="animate-spin" /> Summarizing…</template>
            <template v-else><Sparkles /> Summarize</template>
          </Button>
          <DropdownMenu :items="summaryLengthItems" data-summary-length-menu>
            <template #trigger>
              <button
                type="button"
                :disabled="summaryLoading"
                aria-label="Summary length"
                class="inline-flex h-8 items-center rounded-r-md border border-l-0 border-border bg-background px-1.5 text-muted-foreground transition-colors hover:bg-accent hover:text-accent-foreground disabled:pointer-events-none disabled:opacity-50"
              >
                <ChevronDown class="size-3.5" />
              </button>
            </template>
          </DropdownMenu>
        </div>
      </div>

      <!-- LLM Summary -->
      <template v-if="summaryData || summaryLoading">
        <Separator class="mb-4" />
        <button
          type="button"
          class="flex w-full items-center gap-1.5 text-left text-xs font-semibold text-muted-foreground uppercase mb-2 hover:text-foreground transition-colors"
          :aria-expanded="!summaryCollapsed"
          @click="summaryCollapsed = !summaryCollapsed"
        >
          <ChevronDown class="size-3.5 shrink-0 transition-transform" :class="summaryCollapsed ? '-rotate-90' : ''" />
          <Sparkles class="size-3" />
          <span>LLM Summary</span>
        </button>
        <template v-if="!summaryCollapsed">
          <!-- Loading spinner -->
          <div v-if="summaryLoading" class="flex items-center gap-2 py-4 text-sm text-muted-foreground">
            <RefreshCw class="size-4 animate-spin" />
            <span>Generating summary…</span>
          </div>
          <!-- Summary content -->
          <div v-else-if="summaryData" class="space-y-3">
            <p v-if="summaryData.content_summary" class="text-sm leading-relaxed">{{ summaryData.content_summary }}</p>
            <p v-if="summaryData.comments_summary" class="text-sm leading-relaxed">{{ summaryData.comments_summary }}</p>
          </div>
        </template>
      </template>

      <!-- Extracted Content -->
      <template v-if="feedStore.selectedItem.extract_status">
        <Separator class="mb-4" />
        <button
          type="button"
          class="flex w-full items-center gap-1.5 text-left text-xs font-semibold text-muted-foreground uppercase mb-2 hover:text-foreground transition-colors"
          :aria-expanded="!extractedCollapsed"
          @click="extractedCollapsed = !extractedCollapsed"
        >
          <ChevronDown class="size-3.5 shrink-0 transition-transform" :class="extractedCollapsed ? '-rotate-90' : ''" />
          <span>Extracted</span>
          <span v-if="feedStore.selectedItem.extract_status === 'done' && feedStore.selectedItem.extract_date" class="font-normal text-muted-foreground/60 ml-1">{{ absoluteTime(feedStore.selectedItem.extract_date) }}</span>
        </button>
        <template v-if="!extractedCollapsed">
          <div v-if="feedStore.selectedItem.extract_status === 'pending'"
            class="p-4 text-sm text-muted-foreground bg-muted/30 rounded-md">
            <RefreshCw class="size-3 inline animate-spin mr-1" /> Extraction in progress…
          </div>
          <div v-else-if="feedStore.selectedItem.extract_status === 'failed'"
            class="p-4 rounded-md bg-warning/10 dark:bg-warning/15 border border-warning/30">
            <p class="text-xs font-medium text-warning-foreground dark:text-warning mb-1">Extraction failed after {{ feedStore.selectedItem.extract_retries }} attempt{{ feedStore.selectedItem.extract_retries === 1 ? '' : 's' }}</p>
            <pre class="text-xs text-warning-foreground/80 dark:text-warning/90 whitespace-pre-wrap">{{ feedStore.selectedItem.extract_content }}</pre>
          </div>
          <div v-else-if="feedStore.selectedItem.extract_status === 'done'"
            :class="proseClass" v-html="feedStore.selectedItem.extract_content" v-external-links />
        </template>
      </template>

      <!-- Original Content -->
      <template v-if="feedStore.selectedItem.extract_status">
        <Separator class="mb-4" />
        <button
          type="button"
          class="flex w-full items-center gap-1.5 text-left text-xs font-semibold text-muted-foreground uppercase mb-2 hover:text-foreground transition-colors"
          :aria-expanded="!originalCollapsed"
          @click="originalCollapsed = !originalCollapsed"
        >
          <ChevronDown class="size-3.5 shrink-0 transition-transform" :class="originalCollapsed ? '-rotate-90' : ''" />
          <span>Original</span>
        </button>
        <div v-if="!originalCollapsed" :class="proseClass" v-html="feedStore.selectedItem.content" v-external-links />
      </template>
      <div v-else :class="proseClass" v-html="feedStore.selectedItem.content" v-external-links />

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
                  <a v-if="c.url" :href="c.url" target="_blank" class="font-medium text-accent hover:underline">{{ c.author }}</a>
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
