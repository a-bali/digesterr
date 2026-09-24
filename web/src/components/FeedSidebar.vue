<script setup lang="ts">
import { ref, reactive, computed, onMounted } from 'vue'
import { toastSuccess, toastError } from '../lib/toast'
import { scopeActionLabel, scopeEmptyLabel } from '../lib/markAllRead'
import { useFeedStore } from '../stores/feed'
import { useDigestStore } from '../stores/digest'
import { feedApi } from '../services/api'
import { feedUrlFor, emailAddressFor } from '../lib/feeds'
import { absoluteTime } from '../lib/format'
import type { DigestConfig, Feed } from '../types/api'
import { useRoute } from 'vue-router'
import { BookmarkCheck, Plus, MessageSquare, Rss, Settings, Sparkles, ChevronDown, Check } from 'lucide-vue-next'
import FeedRow from './FeedRow.vue'
import FeedDialog from './FeedDialog.vue'
import DigestDialog from './DigestDialog.vue'
import Tooltip from './ui/Tooltip.vue'

withDefaults(defineProps<{ showingSaved?: boolean }>(), { showingSaved: false })

const emit = defineEmits<{
  (e: 'selectFeed', id: number): void
  (e: 'selectDigestConfig', id: number): void
  (e: 'selectCategory', name: string): void
  (e: 'selectSection', section: 'feeds' | 'digests'): void
  (e: 'showSaved'): void
  // Mark-all-read asks the parent to run the shared confirmation. The sidebar
  // never marks a scope read itself, so every entry point gets the same dialog.
  (e: 'markAllRead', scope: { kind: 'feed'; id: number }
    | { kind: 'category'; name: string }
    | { kind: 'feeds-section' }
    | { kind: 'digest-config'; id: number; name: string }
    | { kind: 'all-digests' }): void
}>()

const feedStore = useFeedStore()
const digestStore = useDigestStore()
const route = useRoute()

const refreshingFeeds = ref(new Set<number>())
const generatingDigests = ref(new Set<number>())

// One rule for every action group in the sidebar and its rows: the SELECTED
// scope pins its actions visible, everything else reveals them on hover (pointer)
// or focus (keyboard) only. Nothing is revealed unconditionally — the old
// `pointer-coarse:opacity-100` painted four controls on every row on a touch
// device, which is the clutter being removed. The `+` buttons are exempt on
// purpose: they are the touch user's only way in, so they are always visible.
//
// THE CLASS STRINGS ARE WRITTEN OUT IN FULL, deliberately. An earlier version
// composed them: `opacity-0 ${group}:opacity-100`. Tailwind scans source as
// TEXT, so it never saw the literal named-group class and generated NO rule for
// it — the section and category rows could not be revealed at all, while the
// digest rows (which use the plain `group`) worked. A composed utility class name
// is a class that does not exist. `scripts/verify-classes.mjs` guards this.
//
// The mark-all-read controls are NEVER disabled. A disabled button cannot be
// pressed, so the user loses the answer the action gives on an empty scope —
// `requestMarkAllRead` already toasts "nothing to mark as read", and the tooltip
// distinguishes the cases via scopeActionLabel / scopeEmptyLabel. Disabling them
// also made the control permanently PALE and visible: the button carries
// `opacity-0`, but `disabled:opacity-30` is emitted later in the compiled sheet,
// so a disabled control resolved to opacity .3 instead of hidden. That stale
// class is gone too, so nothing depends on stylesheet order.
const REVEAL_GROUP = 'opacity-0 group-hover:opacity-100 group-focus-within:opacity-100'
const REVEAL_GROUP_SECTION = 'opacity-0 group-hover/section:opacity-100 group-focus-within:opacity-100'
const REVEAL_GROUP_CAT = 'opacity-0 group-hover/cat:opacity-100 group-focus-within:opacity-100'

const sectionActionClass = (selected: boolean) => (selected ? 'opacity-100' : REVEAL_GROUP_SECTION)
const categoryActionClass = (selected: boolean) => (selected ? 'opacity-100' : REVEAL_GROUP_CAT)
const digestBlockActionClass = (selected: boolean) => (selected ? 'opacity-100' : REVEAL_GROUP)

const showAddFeed = ref(false)
const showEditFeed = ref(false)
const editingFeed = ref<Feed | null>(null)

const showAddDigest = ref(false)
const showEditDigest = ref(false)
const editingDigest = ref<DigestConfig | null>(null)

async function refreshFeed(id: number) {
  // The name is needed for the toast, and it may disappear mid-flight if the feed
  // is deleted, so capture it before the request.
  const name = feedStore.feeds.find(f => f.id === id)?.name
  refreshingFeeds.value = new Set(refreshingFeeds.value).add(id)
  try {
    await feedApi.fetch(id)
    await feedStore.fetchFirstPage()
    await feedStore.fetchFeeds()
    toastSuccess('Feed refreshed', name)
  } catch (e: any) {
    toastError(e, 'Failed to refresh feed', name)
  } finally {
    const next = new Set(refreshingFeeds.value); next.delete(id); refreshingFeeds.value = next
  }
}

// Marking a feed read marks ALL of its items read, so it confirms first. The
// dialog lives in HomeView alongside the digest and multi-view confirmations:
// one dialog owns every mark-all-read scope, and the sidebar only declares WHICH
// scope it is asking about. Duplicating the dialog here is what let the sidebar
// controls mark scopes read with no confirmation while the key and the mid-pane
// button did confirm.
// Resolve category name -> payload field and build final URL.
function buildFeedPayload(form: { type: string; name: string; url: string; category: string; extract_content: number; readability: number; min_score: number }): any {
  const data: any = { extract_content: form.extract_content, readability: form.readability, min_score: form.min_score, type: form.type, name: form.name }
  data.category = form.category.trim()
  if (form.type === 'reddit') {
    // Reddit form field holds just the subreddit name.
    data.url = feedUrlFor('reddit', form.url)
  } else if (form.type === 'twitter') {
    data.url = feedUrlFor('twitter', form.url)
  } else if (form.type === 'hn' || form.type === 'lobsters') {
    data.url = feedUrlFor(form.type, '')
  } else if (form.type === 'email') {
    // The form field holds only the part after '+'; the account's inbox comes
    // from the server, so the full address is composed here. The address
    // namespace is global, so the suffix must be unique across ALL users: two
    // feeds on one address would both read the same mail.
    data.url = emailAddressFor(feedStore.emailInbox ?? '', form.url)
  } else data.url = form.url
  return data
}

async function addFeed(form: { type: string; name: string; url: string; category: string; extract_content: number; readability: number; min_score: number }) {
  try {
    const newFeed = await feedApi.create(buildFeedPayload(form))
    await feedStore.fetchFeeds()
    showAddFeed.value = false
    toastSuccess('Feed added', newFeed?.name)
    // Auto-fetch the new feed immediately with spinner animation
    refreshingFeeds.value = new Set(refreshingFeeds.value).add(newFeed.id)
    try {
      await feedApi.fetch(newFeed.id)
      await feedStore.fetchFeeds()
    } catch { /* fire-and-forget errors are ok */ } finally {
      const next = new Set(refreshingFeeds.value); next.delete(newFeed.id); refreshingFeeds.value = next
    }
  } catch (e) {
    toastError(e, 'Failed to add feed')
  }
}

async function saveEditFeed(form: { type: string; name: string; url: string; category: string; extract_content: number; readability: number; min_score: number }) {
  if (!editingFeed.value) return
  // The form holds the name being SAVED, which is what the user recognises (it may
  // differ from the stored one when they just renamed the feed).
  const name = form.name || editingFeed.value.name
  try {
    await feedApi.update(editingFeed.value.id, buildFeedPayload(form))
    showEditFeed.value = false; editingFeed.value = null
    await feedStore.fetchFeeds()
    toastSuccess('Feed updated', name)
  } catch (e) {
    toastError(e, 'Failed to update feed', name)
  }
}

async function deleteFeed(id: number) {
  // Capture the name before the delete: after it succeeds the feed is gone from
  // the store and the toast could not name it.
  const name = feedStore.feeds.find(f => f.id === id)?.name
  try {
    await feedApi.delete(id)
    showEditFeed.value = false; editingFeed.value = null
    // Clear middle/right panes if the deleted feed was active
    if (feedStore.activeFeedId === id) {
      feedStore.activeFeedId = null
      feedStore.items = []
      feedStore.selectedItem = null
    }
    await feedStore.fetchFeeds()
    toastSuccess('Feed deleted', name)
  } catch (e) {
    toastError(e, 'Failed to delete feed', name)
  }
}

async function addDigest(data: { name: string; schedule: string; config: string }) {
  await digestStore.createConfig(data.name, data.schedule, data.config)
  showAddDigest.value = false
}

async function saveEditDigest(data: { name: string; schedule: string; config: string }) {
  if (!editingDigest.value) return
  await digestStore.updateConfig(editingDigest.value.id, data.name, data.schedule, data.config)
  showEditDigest.value = false; editingDigest.value = null
}

async function deleteDigest(id: number) {
  await digestStore.deleteConfig(id)
  showEditDigest.value = false; editingDigest.value = null
}

async function generateNow(e: Event, cfgId: number) {
  e.stopPropagation()
  generatingDigests.value = new Set(generatingDigests.value).add(cfgId)
  try {
    const result = await digestStore.generateNow(cfgId)
    if (result && digestStore.activeConfigId !== cfgId) {
      emit('selectDigestConfig', cfgId)
    }
    feedStore.fetchUnreadCounts()
    digestStore.fetchUnreadCounts()
  } finally {
    const next = new Set(generatingDigests.value); next.delete(cfgId); generatingDigests.value = next
  }
}

onMounted(() => {
  digestStore.fetchConfigs()
  digestStore.fetchUnreadCounts()
})

const collapsedCategories = reactive(new Set<string>())
// ---- Section totals ----
const totalDigestUnread = computed(() => {
  let sum = 0
  for (const cfg of digestStore.configs) sum += digestStore.unreadCounts[cfg.id] ?? 0
  return sum
})
const totalFeedUnread = computed(() => {
  let sum = 0
  for (const feed of feedStore.feeds) sum += feedStore.unreadCounts[feed.id] ?? 0
  return sum
})

// Categories are derived from feeds: only names still in use, sorted
// alphabetically. Feeds without a category are listed under "Feeds".
const uncategorizedFeeds = computed(() => feedStore.feeds.filter(f => !f.category))
const sortedCategoryNames = computed(() => {
  const names = new Set<string>()
  for (const f of feedStore.feeds) {
    if (f.category) names.add(f.category)
  }
  return [...names].sort((a, b) => a.localeCompare(b))
})

function toggleCategory(name: string) {
  if (collapsedCategories.has(name)) collapsedCategories.delete(name)
  else collapsedCategories.add(name)
}

// Digest unread counts arrive from a separate request than the configs, so a
// missing key means "not loaded yet" rather than zero — only an explicit 0 dims.
// (Feed rows apply the same rule in FeedRow.vue.) Dimming the row wrapper instead
// of the icon/name would compound with the hover-revealed action buttons.
function digestHasNoUnread(id: number) {
  return digestStore.unreadCounts[id] === 0
}

// Per-category unread counts.
const categoryUnreadCounts = computed(() => {
  const counts: Record<string, number> = {}
  for (const feed of feedStore.feeds) {
    if (feed.category) {
      counts[feed.category] = (counts[feed.category] ?? 0) + (feedStore.unreadCounts[feed.id] ?? 0)
    }
  }
  return counts
})
</script>

<template>
  <nav aria-label="Feeds">
  <div>
    <!-- Saved -->
    <button class="flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-sm hover:bg-accent/10 transition-colors mb-3" :class="{ 'bg-accent/10 font-medium text-accent': route.path === '/saved' }" @click="emit('showSaved')">
      <BookmarkCheck class="size-3.5 shrink-0 text-accent" />
      <span class="truncate">Saved items</span>
      <!-- Solid like the unread pills, at a dimmed accent — same shape and size,
           so it stays one family while a saved count is not mistaken for an
           unread tally. A hollow outline was tried first and was barely visible.

           The text colour is mode-aware because the dimmed fill inverts: white
           on accent/60 measures 6.27:1 in dark but only 2.18:1 in light (worse
           than the full pill's 3.64:1, so white would defeat the dim); the
           foreground colour gives 8.18:1 light / 2.85:1 dark. Pairing each mode
           with the text that stays legible on its own fill keeps both above the
           unread pill's baseline. -->
      <span
        v-if="feedStore.savedCount > 0"
        data-testid="saved-count"
        class="text-[10px] font-medium px-1.5 py-0.5 rounded-full bg-accent/60 text-foreground dark:text-accent-foreground ml-1.5"
        :title="`${feedStore.savedCount} saved item${feedStore.savedCount === 1 ? '' : 's'}`"
      >{{ feedStore.savedCount }}</span>
    </button>

    <!-- Digests. The wrapper carries no horizontal padding: the header button's
         own px-2 is what puts the icon at x=20 and the label at x=42, the same
         line as the Saved row above and the Feeds row below. All three icons
         are size-3.5 (14px) with gap-2, so the labels share one text column. -->
    <div class="group/section flex items-center justify-between mb-2">
      <button
        class="flex items-center gap-2 text-xs font-semibold text-muted-foreground uppercase rounded-md px-2 py-1.5 hover:bg-accent/10 transition-colors cursor-pointer w-full"
        :class="{ 'text-accent bg-accent/10': digestStore.showingAllDigests }"
        @click="emit('selectSection', 'digests')"
      >
        <MessageSquare class="size-3.5 shrink-0 text-accent" />
        Digests
        <span
          v-if="totalDigestUnread > 0"
          data-testid="digests-section-total"
          class="text-[10px] font-medium px-1.5 py-0.5 rounded-full bg-accent text-accent-foreground"
          :title="`${totalDigestUnread} unread across ${digestStore.configs.length} digest config${digestStore.configs.length === 1 ? '' : 's'}`"
        >{{ totalDigestUnread }}</span>
      </button>
      <button
        class="shrink-0 p-1 rounded hover:bg-accent/20 transition-opacity"
        :class="sectionActionClass(feedStore.activeSection === 'digests')"
        :title="totalDigestUnread > 0
          ? scopeActionLabel({ kind: 'all-digests' })
          : scopeEmptyLabel({ kind: 'all-digests' })"
        data-mark-all-digests
        @click="emit('markAllRead', { kind: 'all-digests' })"
      >
        <Check class="size-3" />
      </button>
      <button class="p-1 rounded hover:bg-accent/20" title="Create digest config" @click="showAddDigest = true">
        <Plus class="size-3.5" />
      </button>
    </div>
    <div class="space-y-0.5 mb-3 pl-3">
      <div
        v-for="cfg in digestStore.configs" :key="cfg.id"
        class="group flex items-center w-full rounded-md px-2 py-1.5 text-sm hover:bg-accent/10 transition-colors cursor-pointer"
        :class="digestStore.activeConfigId === cfg.id ? 'bg-accent/10 font-medium text-accent' : ''"
        @click="emit('selectDigestConfig', cfg.id)"
      >
        <div class="flex items-center gap-2 flex-1 min-w-0">
          <MessageSquare class="size-3.5 shrink-0 text-accent" :class="{ 'opacity-60': digestHasNoUnread(cfg.id) }" />
          <span class="truncate" :class="{ 'opacity-60': digestHasNoUnread(cfg.id) }">{{ cfg.name || cfg.schedule }}</span>
          <Tooltip v-if="cfg.last_error">
            <template #trigger>
              <svg data-testid="digest-error" viewBox="0 0 24 24" class="size-4 text-red-500 shrink-0" fill="none"><rect x="2" y="2" width="20" height="20" rx="4" fill="currentColor"/><path d="M12 7v6" stroke="white" stroke-width="2.5" stroke-linecap="round"/><circle cx="12" cy="16" r="1.3" fill="white"/></svg>
            </template>
            <p>{{ cfg.last_error }}</p>
            <p v-if="cfg.last_error_at">at {{ absoluteTime(cfg.last_error_at) }}</p>
          </Tooltip>
          <span
            v-if="(digestStore.unreadCounts[cfg.id] ?? 0) > 0"
            data-testid="digest-unread"
            class="text-[10px] font-medium px-1.5 py-0.5 rounded-full bg-accent text-accent-foreground ml-1.5"
          >{{ digestStore.unreadCounts[cfg.id] }}</span>
        </div>
        <div
          class="flex items-center gap-0.5 shrink-0 transition-opacity"
          :class="digestBlockActionClass(digestStore.activeConfigId === cfg.id)"
          data-digest-actions
        >
          <button
            class="p-1 rounded hover:bg-accent/20"
            :title="(digestStore.unreadCounts[cfg.id] ?? 0) > 0
              ? scopeActionLabel({ kind: 'digest-config', id: cfg.id, name: cfg.name })
              : scopeEmptyLabel({ kind: 'digest-config', id: cfg.id, name: cfg.name })"
            :data-mark-digest-read="cfg.id"
            @click.stop="emit('markAllRead', { kind: 'digest-config', id: cfg.id, name: cfg.name })"
          >
            <Check class="size-3" />
          </button>
          <button class="p-1 rounded hover:bg-accent/20" title="Settings" @click.stop="editingDigest = cfg; showEditDigest = true">
            <Settings class="size-3" />
          </button>
          <button class="p-1 rounded hover:bg-accent/20" title="Generate now" @click.stop="generateNow($event, cfg.id)">
            <Sparkles class="size-3" :class="generatingDigests.has(cfg.id) ? 'animate-spin' : ''" />
          </button>
        </div>
      </div>
    </div>

    <!-- Feeds. Same wrapper treatment as Digests above — see that comment. -->
    <div class="group/section flex items-center justify-between mb-2">
      <button
        class="flex items-center gap-2 text-xs font-semibold text-muted-foreground uppercase rounded-md px-2 py-1.5 hover:bg-accent/10 transition-colors cursor-pointer w-full"
        :class="{ 'text-accent bg-accent/10': feedStore.activeSection === 'feeds' }"
        @click="emit('selectSection', 'feeds')"
      >
        <Rss class="size-3.5 shrink-0 text-accent" />
        Feeds
        <span
          v-if="totalFeedUnread > 0"
          data-testid="feeds-section-total"
          class="text-[10px] font-medium px-1.5 py-0.5 rounded-full bg-accent text-accent-foreground"
          :title="`${totalFeedUnread} unread across ${feedStore.feeds.length} feed${feedStore.feeds.length === 1 ? '' : 's'}`"
        >{{ totalFeedUnread }}</span>
      </button>
      <button
        class="shrink-0 p-1 rounded hover:bg-accent/20 transition-opacity"
        :class="sectionActionClass(feedStore.activeSection === 'feeds')"
        :title="totalFeedUnread > 0
          ? scopeActionLabel({ kind: 'feeds-section' })
          : scopeEmptyLabel({ kind: 'feeds-section' })"
        data-mark-feeds-section-read
        @click="emit('markAllRead', { kind: 'feeds-section' })"
      >
        <Check class="size-3" />
      </button>
      <button class="p-1 rounded hover:bg-accent/20" title="Add feed" @click="showAddFeed = true">
        <Plus class="size-3.5" />
      </button>
    </div>

    <div v-if="uncategorizedFeeds.length" class="mb-3 pl-3">
      <FeedRow
        v-for="feed in uncategorizedFeeds" :key="feed.id"
        :feed="feed" :refreshing="refreshingFeeds.has(feed.id)"
        @select="emit('selectFeed', $event)"
        @edit="editingFeed = $event; showEditFeed = true"
        @mark-read="id => emit('markAllRead', { kind: 'feed', id })"
        @refresh="refreshFeed"
      />
    </div>

    <div v-for="catName in sortedCategoryNames" :key="catName" class="mb-3 pl-3">
      <div class="group/cat flex items-center justify-between">
        <button
          class="flex w-full items-center gap-2 text-[10px] font-semibold uppercase mb-1 transition-colors px-2 py-1.5 rounded-md hover:bg-accent/10 cursor-pointer"
          :class="feedStore.activeCategory === catName ? 'text-accent bg-accent/10' : 'text-muted-foreground hover:text-foreground'"
          @click="emit('selectCategory', catName)"
        >
          <span class="flex items-center gap-1.5 min-w-0">
            <ChevronDown class="size-3 shrink-0 transition-transform" :class="collapsedCategories.has(catName) ? '-rotate-90' : ''" @click.stop="toggleCategory(catName)" />
            <span class="truncate">{{ catName }}</span>
          </span>
          <span
            v-if="(categoryUnreadCounts[catName] ?? 0) > 0"
            data-testid="category-unread"
            class="text-[10px] font-medium px-1.5 py-0.5 rounded-full bg-accent text-accent-foreground"
          >{{ categoryUnreadCounts[catName] }}</span>
        </button>
        <button
          class="shrink-0 p-1 rounded hover:bg-accent/20 transition-opacity"
          :class="categoryActionClass(feedStore.activeCategory === catName)"
          :title="(categoryUnreadCounts[catName] ?? 0) > 0
            ? scopeActionLabel({ kind: 'category', name: catName })
            : scopeEmptyLabel({ kind: 'category', name: catName })"
          :data-mark-category-read="catName"
          @click="emit('markAllRead', { kind: 'category', name: catName })"
        >
          <Check class="size-3" />
        </button>
      </div>
      <template v-if="!collapsedCategories.has(catName)">
        <div class="pl-3">
          <FeedRow
            v-for="feed in feedStore.feeds.filter(f => f.category === catName)" :key="feed.id"
            :feed="feed" :refreshing="refreshingFeeds.has(feed.id)"
            @select="emit('selectFeed', $event)"
            @edit="editingFeed = $event; showEditFeed = true"
            @mark-read="id => emit('markAllRead', { kind: 'feed', id })"
            @refresh="refreshFeed"
          />
        </div>
      </template>
    </div>

    <FeedDialog v-if="showAddFeed" :feed="null" @update:open="showAddFeed = $event" @save="addFeed" />
    <FeedDialog v-if="showEditFeed && editingFeed" :feed="editingFeed" @update:open="showEditFeed = $event" @save="saveEditFeed" @delete="deleteFeed" />
    <DigestDialog v-if="showAddDigest" :config="null" @update:open="showAddDigest = $event" @save="addDigest" />
    <DigestDialog v-if="showEditDigest" :config="editingDigest" @update:open="showEditDigest = $event" @save="saveEditDigest" @delete="deleteDigest" />
  </div>
  </nav>
</template>
