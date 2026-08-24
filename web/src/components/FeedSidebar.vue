<script setup lang="ts">
import { ref, reactive, computed, onMounted } from 'vue'
import { toast } from 'vue-sonner'
import { useFeedStore } from '../stores/feed'
import { useDigestStore } from '../stores/digest'
import { feedApi } from '../services/api'
import { feedUrlFor } from '../lib/feeds'
import { toastError } from '../lib/toast'
import type { DigestConfig, Feed } from '../types/api'
import { useRoute } from 'vue-router'
import { BookmarkCheck, Plus, MessageSquare, Rss, Settings, Sparkles, ChevronDown } from 'lucide-vue-next'
import FeedRow from './FeedRow.vue'
import FeedDialog from './FeedDialog.vue'
import DigestDialog from './DigestDialog.vue'
import Dialog from './ui/Dialog.vue'
import Button from './ui/Button.vue'

withDefaults(defineProps<{ showingSaved?: boolean }>(), { showingSaved: false })

const emit = defineEmits<{
  (e: 'selectFeed', id: number): void
  (e: 'selectDigestConfig', id: number): void
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

// Marking a feed read marks ALL of its items read — confirm first.
const feedToMarkRead = ref<Feed | null>(null)

function requestMarkFeedRead(id: number) {
  const feed = feedStore.feeds.find(f => f.id === id)
  if (feed) feedToMarkRead.value = feed
}

defineExpose({ requestMarkFeedRead })

function closeMarkReadDialog() {
  feedToMarkRead.value = null
}

async function confirmMarkFeedRead() {
  const feed = feedToMarkRead.value
  feedToMarkRead.value = null
  if (feed) await markFeedRead(feed.id)
}

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
  await feedApi.create(buildFeedPayload(form))
  await feedStore.fetchFeeds()
  showAddFeed.value = false
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
</script>

<template>
  <div>
    <!-- Saved -->
    <button class="flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-sm hover:bg-accent/10 transition-colors mb-3" :class="{ 'bg-accent/10 font-medium text-accent': route.path === '/saved' }" @click="emit('showSaved')">
      <BookmarkCheck class="size-3.5 shrink-0 text-accent" /> <span class="truncate">Saved items</span>
    </button>

    <!-- Digests -->
    <div class="flex items-center justify-between mb-2 px-2">
      <span class="flex items-center gap-2 text-xs font-semibold text-muted-foreground uppercase">
        <MessageSquare class="size-3.5 shrink-0 text-accent" />
        Digests
      </span>
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
          <span class="truncate">{{ cfg.name || cfg.schedule }}</span>
          <span v-if="(digestStore.unreadCounts[cfg.id] ?? 0) > 0" class="text-[10px] font-medium px-1.5 py-0.5 rounded-full bg-accent text-accent-foreground ml-auto">{{ digestStore.unreadCounts[cfg.id] }}</span>
        </div>
        <div class="flex items-center gap-0.5 shrink-0 opacity-0 group-hover:opacity-100 group-focus-within:opacity-100 pointer-coarse:opacity-100 transition-opacity">
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
    <div class="flex items-center justify-between mb-2 px-2">
      <span class="flex items-center gap-2 text-xs font-semibold text-muted-foreground uppercase">
        <Rss class="size-3.5 shrink-0 text-accent" />
        Feeds
      </span>
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
      <button class="flex w-full items-center justify-between text-[10px] font-semibold text-muted-foreground uppercase mb-1 hover:text-foreground transition-colors px-2" @click="toggleCategory(catName)">
        <span>{{ catName }}</span>
        <span class="inline-flex items-center justify-end h-4 w-4 shrink-0">
          <ChevronDown class="size-4 transition-transform" :class="collapsedCategories.has(catName) ? '-rotate-90' : ''" />
        </span>
      </button>
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
        <Button data-mark-all-read-confirm @click="confirmMarkFeedRead">Mark read</Button>
      </div>
    </Dialog>
  </div>
</template>
