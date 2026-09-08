<script setup lang="ts">
import { ref, reactive, computed, watch, nextTick, onMounted, useTemplateRef } from 'vue'
import { toast } from 'vue-sonner'
import { useFeedStore } from '../stores/feed'
import { useDigestStore } from '../stores/digest'
import { feedApi } from '../services/api'
import { feedUrlFor } from '../lib/feeds'
import { toastError } from '../lib/toast'
import { absoluteTime } from '../lib/format'
import type { DigestConfig, Feed } from '../types/api'
import { useRoute } from 'vue-router'
import { BookmarkCheck, Plus, MessageSquare, Rss, Settings, Sparkles, ChevronDown, Check } from 'lucide-vue-next'
import FeedRow from './FeedRow.vue'
import FeedDialog from './FeedDialog.vue'
import DigestDialog from './DigestDialog.vue'
import Dialog from './ui/Dialog.vue'
import Button from './ui/Button.vue'
import Tooltip from './ui/Tooltip.vue'

withDefaults(defineProps<{ showingSaved?: boolean }>(), { showingSaved: false })

const emit = defineEmits<{
  (e: 'selectFeed', id: number): void
  (e: 'selectDigestConfig', id: number): void
  (e: 'selectCategory', name: string): void
  (e: 'selectSection', section: 'feeds' | 'digests'): void
  (e: 'showSaved'): void
}>()

const feedStore = useFeedStore()
const digestStore = useDigestStore()
const route = useRoute()

const refreshingFeeds = ref(new Set<number>())
const generatingDigests = ref(new Set<number>())

const showAddFeed = ref(false)
const showEditFeed = ref(false)
const editingFeed = ref<Feed | null>(null)

const showAddDigest = ref(false)
const showEditDigest = ref(false)
const editingDigest = ref<DigestConfig | null>(null)

async function refreshFeed(id: number) {
  refreshingFeeds.value = new Set(refreshingFeeds.value).add(id)
  try {
    await feedApi.fetch(id)
    await feedStore.fetchFirstPage()
    await feedStore.fetchFeeds()
    toast.success('Feed refreshed')
  } catch (e: any) {
    toast.error('Failed to refresh feed', { description: e.message })
  } finally {
    const next = new Set(refreshingFeeds.value); next.delete(id); refreshingFeeds.value = next
  }
}

async function markFeedRead(id: number) {
  try {
    await feedApi.markRead(id)
    await feedStore.fetchFeeds()
    if (feedStore.activeFeedId === id) await feedStore.fetchFirstPage()
    toast.success('Feed marked read')
  } catch (e: any) {
    toastError(e, 'Failed to mark feed read')
  }
}

async function markSectionRead(category?: string) {
  try {
    await feedStore.markAllRead(category)
    await feedStore.fetchFeeds()
    toast.success(category ? `"${category}" marked read` : 'All feeds marked read')
  } catch (e: any) {
    toastError(e, 'Failed to mark read')
  }
}

// Marking a feed read marks ALL of its items read — confirm first.
const feedToMarkRead = ref<Feed | null>(null)

function requestMarkFeedRead(id: number) {
  const feed = feedStore.feeds.find(f => f.id === id)
  if (feed) feedToMarkRead.value = feed
}

const markAllReadDialogOpen = computed(() => feedToMarkRead.value !== null)
defineExpose({ requestMarkFeedRead, markAllReadDialogOpen })

function closeMarkReadDialog() {
  feedToMarkRead.value = null
}

async function confirmMarkFeedRead() {
  const feed = feedToMarkRead.value
  feedToMarkRead.value = null
  if (feed) await markFeedRead(feed.id)
}

// When the confirm button is focused, Enter fires both the keydown handler
// below and the native button click. Use a microtask guard so only one path
// executes confirmMarkFeedRead.
let confirmKeyHandled = false
function onConfirmKeyDown(e: KeyboardEvent) {
  if (e.key !== 'Enter') return
  if (confirmKeyHandled) { e.preventDefault(); return }
  confirmKeyHandled = true
  queueMicrotask(() => { confirmKeyHandled = false })
  e.preventDefault()
  confirmMarkFeedRead()
}

const confirmBtnRef = useTemplateRef<InstanceType<typeof Button>>('confirmBtn')
watch(feedToMarkRead, async (feed) => {
  if (feed) {
    await nextTick()
    const el = (confirmBtnRef.value?.$el as HTMLElement | undefined)
    el?.focus()
  }
})

// Resolve category name -> payload field and build final URL.
function buildFeedPayload(form: { type: string; name: string; url: string; category: string; extract_content: number; min_score: number }): any {
  const data: any = { extract_content: form.extract_content, min_score: form.min_score, type: form.type, name: form.name }
  data.category = form.category.trim()
  if (form.type === 'reddit') {
    // Reddit form field holds just the subreddit name.
    data.url = feedUrlFor('reddit', form.url)
  } else if (form.type === 'twitter') {
    data.url = feedUrlFor('twitter', form.url)
  } else if (form.type === 'hn' || form.type === 'lobsters') {
    data.url = feedUrlFor(form.type, '')
  } else data.url = form.url
  return data
}

async function addFeed(form: { type: string; name: string; url: string; category: string; extract_content: number; min_score: number }) {
  const newFeed = await feedApi.create(buildFeedPayload(form))
  await feedStore.fetchFeeds()
  showAddFeed.value = false
  // Auto-fetch the new feed immediately with spinner animation
  refreshingFeeds.value = new Set(refreshingFeeds.value).add(newFeed.id)
  try {
    await feedApi.fetch(newFeed.id)
    await feedStore.fetchFeeds()
  } catch { /* fire-and-forget errors are ok */ } finally {
    const next = new Set(refreshingFeeds.value); next.delete(newFeed.id); refreshingFeeds.value = next
  }
}

async function saveEditFeed(form: { type: string; name: string; url: string; category: string; extract_content: number; min_score: number }) {
  if (!editingFeed.value) return
  await feedApi.update(editingFeed.value.id, buildFeedPayload(form))
  showEditFeed.value = false; editingFeed.value = null
  await feedStore.fetchFeeds()
}

async function deleteFeed(id: number) {
  await feedApi.delete(id)
  showEditFeed.value = false; editingFeed.value = null
  // Clear middle/right panes if the deleted feed was active
  if (feedStore.activeFeedId === id) {
    feedStore.activeFeedId = null
    feedStore.items = []
    feedStore.selectedItem = null
  }
  await feedStore.fetchFeeds()
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
  <div>
    <!-- Saved -->
    <button class="flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-sm hover:bg-accent/10 transition-colors mb-3" :class="{ 'bg-accent/10 font-medium text-accent': route.path === '/saved' }" @click="emit('showSaved')">
      <BookmarkCheck class="size-3.5 shrink-0 text-accent" /> <span class="truncate">Saved items</span>
    </button>

    <!-- Digests -->
    <div class="group/section flex items-center justify-between mb-2 px-2">
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
        v-if="totalDigestUnread > 0"
        class="shrink-0 opacity-0 group-hover/section:opacity-100 pointer-coarse:opacity-100 transition-opacity p-1 rounded hover:bg-accent/20"
        title="Mark all digests as read"
        @click="digestStore.markAllRead()"
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
          <MessageSquare class="size-3.5 shrink-0 text-accent" />
          <Tooltip v-if="cfg.last_error">
            <template #trigger>
              <svg viewBox="0 0 24 24" class="size-4 text-red-500 shrink-0" fill="none"><rect x="2" y="2" width="20" height="20" rx="4" fill="currentColor"/><path d="M12 7v6" stroke="white" stroke-width="2.5" stroke-linecap="round"/><circle cx="12" cy="16" r="1.3" fill="white"/></svg>
            </template>
            <p>{{ cfg.last_error }}</p>
            <p v-if="cfg.last_error_at">at {{ absoluteTime(cfg.last_error_at) }}</p>
          </Tooltip>
          <span class="truncate">{{ cfg.name || cfg.schedule }}</span>
          <span v-if="(digestStore.unreadCounts[cfg.id] ?? 0) > 0" class="text-[10px] font-medium px-1.5 py-0.5 rounded-full bg-accent text-accent-foreground ml-auto">{{ digestStore.unreadCounts[cfg.id] }}</span>
        </div>
        <div class="flex items-center gap-0.5 shrink-0 opacity-0 group-hover:opacity-100 group-focus-within:opacity-100 pointer-coarse:opacity-100 transition-opacity">
          <button
            v-if="(digestStore.unreadCounts[cfg.id] ?? 0) > 0"
            class="p-1 rounded hover:bg-accent/20"
            title="Mark digest as read"
            @click.stop="digestStore.markAllRead(cfg.id)"
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

    <!-- Feeds -->
    <div class="group/section flex items-center justify-between mb-2 px-2">
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
        v-if="totalFeedUnread > 0"
        class="shrink-0 opacity-0 group-hover/section:opacity-100 pointer-coarse:opacity-100 transition-opacity p-1 rounded hover:bg-accent/20"
        title="Mark all feeds as read"
        @click="markSectionRead()"
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
        @mark-read="requestMarkFeedRead"
        @refresh="refreshFeed"
      />
    </div>

    <div v-for="catName in sortedCategoryNames" :key="catName" class="mb-3 pl-3">
      <div class="group/cat flex items-center justify-between">
        <button
          class="flex w-full items-center justify-between text-[10px] font-semibold uppercase mb-1 transition-colors px-2 py-1.5 rounded-md hover:bg-accent/10 cursor-pointer"
          :class="feedStore.activeCategory === catName ? 'text-accent bg-accent/10' : 'text-muted-foreground hover:text-foreground'"
          @click="emit('selectCategory', catName)"
        >
          <span class="flex items-center gap-1.5">
            <ChevronDown class="size-3 transition-transform" :class="collapsedCategories.has(catName) ? '-rotate-90' : ''" @click.stop="toggleCategory(catName)" />
            {{ catName }}
          </span>
          <span
            v-if="(categoryUnreadCounts[catName] ?? 0) > 0"
            class="text-[10px] font-medium px-1.5 py-0.5 rounded-full bg-accent text-accent-foreground"
          >{{ categoryUnreadCounts[catName] }}</span>
        </button>
        <button
          v-if="(categoryUnreadCounts[catName] ?? 0) > 0"
          class="shrink-0 opacity-0 group-hover/cat:opacity-100 pointer-coarse:opacity-100 transition-opacity p-1 rounded hover:bg-accent/20"
          title="Mark category as read"
          @click="markSectionRead(catName)"
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
            @mark-read="requestMarkFeedRead"
            @refresh="refreshFeed"
          />
        </div>
      </template>
    </div>

    <FeedDialog v-if="showAddFeed" :feed="null" @update:open="showAddFeed = $event" @save="addFeed" />
    <FeedDialog v-if="showEditFeed && editingFeed" :feed="editingFeed" @update:open="showEditFeed = $event" @save="saveEditFeed" @delete="deleteFeed" />
    <DigestDialog v-if="showAddDigest" :config="null" @update:open="showAddDigest = $event" @save="addDigest" />
    <DigestDialog v-if="showEditDigest" :config="editingDigest" @update:open="showEditDigest = $event" @save="saveEditDigest" @delete="deleteDigest" />
    <Dialog
      v-if="feedToMarkRead"
      :open="true"
      title="Mark all as read?"
      :description="`Mark all items in “${feedToMarkRead.name}” as read? This cannot be undone.`"
      @update:open="v => { if (!v) closeMarkReadDialog() }"
    >
      <div class="flex justify-end gap-2">
        <Button variant="outline" @click="closeMarkReadDialog">Cancel</Button>
        <Button ref="confirmBtn" data-mark-all-read-confirm @click="confirmMarkFeedRead" @keydown="onConfirmKeyDown">Mark read</Button>
      </div>
    </Dialog>
  </div>
</template>
