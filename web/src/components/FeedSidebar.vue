<script setup lang="ts">
import { ref, reactive, computed, onMounted } from 'vue'
import { toast } from 'vue-sonner'
import { useCategoryStore } from '../stores/category'
import { useFeedStore } from '../stores/feed'
import { useDigestStore } from '../stores/digest'
import { feedApi, digestApi } from '../services/api'
import type { DigestConfig, Feed } from '../types/api'
import Button from './ui/Button'
import Input from './ui/Input'
import Dialog from './ui/Dialog'
import TagInput from './TagInput.vue'
import Tooltip from './ui/Tooltip'
import { BookmarkCheck, Rss, RefreshCw, Check, Plus, Sparkles, Settings, Trash2, MessageSquare, ChevronDown, X } from 'lucide-vue-next'

const props = withDefaults(defineProps<{
  showingSaved?: boolean
}>(), { showingSaved: false })

const emit = defineEmits<{
  (e: 'selectFeed', id: number): void
  (e: 'selectDigestConfig', id: number): void
  (e: 'showSaved'): void
}>()

const feedStore = useFeedStore()
const categoryStore = useCategoryStore()
const digestStore = useDigestStore()
const refreshingFeeds = ref(new Set<number>())
const generatingDigests = ref(new Set<number>())

const showAddFeed = ref(false)
const showEditFeed = ref(false)
const editingFeed = ref<Feed | null>(null)
const newFeed = ref({ category_ids: [] as number[], createCategoryName: '', type: 'rss', name: '', url: '', extract_content: 0 })
const editingFeedData = ref({ category_ids: [] as number[], createCategoryName: '', type: 'rss', name: '', url: '', extract_content: 0 })
const newCategoryName = ref('')

const showAddDigest = ref(false)
const showEditDigest = ref(false)
const editingDigest = ref<DigestConfig | null>(null)
const defaultPrompt = 'You are a research assistant digesting news articles. For each article provided below, produce a 2-3 sentence highlight giving the concrete takeaway and why it matters — skip introductory phrases like "The article discusses" or "The author explains". Use the comments and extracted content to enrich your analysis.'

const digestSourceMode = ref<'categories' | 'feeds'>('categories')
const newDigest = ref({
  name: '',
  schedule: '0 8 * * *',
  sourceIds: [] as number[],
  highlights: 5,
  comments: 10,
  prompt: defaultPrompt,
})

function feedIcon(feed: Feed): string {
  if (feed.type === 'hn') return 'https://news.ycombinator.com/favicon.ico'
  if (feed.type === 'lobsters') return 'https://lobste.rs/favicon.ico'
  try {
    const domain = new URL(feed.url).hostname
    return `https://icons.duckduckgo.com/ip3/${domain}.ico`
  } catch {
    return ''
  }
}

function formatDate(val: any): string {
  if (!val) return ''
  if (typeof val === 'object') { if (!val.Valid) return ''; val = val.Time }
  if (typeof val !== 'string') return ''
  const d = new Date(val)
  return isNaN(d.getTime()) ? '' : d.toLocaleString(undefined, { year: 'numeric', month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' })
}

async function refreshFeed(id: number) {
  refreshingFeeds.value = new Set(refreshingFeeds.value).add(id)
  try {
    await feedApi.fetch(id)
    await feedStore.fetchItems()
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
    if (feedStore.activeFeedId === id) await feedStore.fetchItems()
    toast.success('Feed marked read')
  } catch (e: any) {
    toast.error('Failed to mark feed read', { description: e.message })
  }
}

function startEditFeed(feed: Feed) {
  editingFeed.value = feed
  const url = feed.type === 'reddit' ? feed.url.replace('https://www.reddit.com/r/', '').replace('.rss', '') : feed.url
  editingFeedData.value = {
    category_ids: feed.category_id ? [feed.category_id] : [],
    createCategoryName: '',
    type: feed.type,
    name: feed.name,
    url,
    extract_content: feed.extract_content,
  }
  showEditFeed.value = true
}

async function saveEditFeed() {
  if (!editingFeed.value) return
  const data: any = { extract_content: editingFeedData.value.extract_content }
  if (editingFeedData.value.category_ids.length > 0) {
    data.category_id = editingFeedData.value.category_ids[0]
  } else if (editingFeedData.value.createCategoryName) {
    data.category_name = editingFeedData.value.createCategoryName
  }
  if (editingFeedData.value.type === 'reddit') {
    data.url = 'https://www.reddit.com/r/' + editingFeedData.value.url.replace(/\.rss$/, '') + '.rss'
  } else if (editingFeedData.value.type === 'hn') {
    data.url = 'https://news.ycombinator.com/rss'
  } else if (editingFeedData.value.type === 'lobsters') {
    data.url = 'https://lobste.rs/rss'
  } else {
    data.url = editingFeedData.value.url
  }
  await feedApi.update(editingFeed.value.id, data)
  showEditFeed.value = false; editingFeed.value = null
  resetEditFeed()
  await feedStore.fetchFeeds()
}

async function deleteFeed(id: number) {
  await feedApi.delete(id)
  showEditFeed.value = false; editingFeed.value = null
  await feedStore.fetchFeeds()
}

function handleTypeChange(type: string) {
  ;[newFeed, editingFeedData].forEach(ref => {
    ref.value.type = type
    ref.value.url = ''
  })
  if (type === 'hn') { newFeed.value.url = 'https://news.ycombinator.com/rss'; editingFeedData.value.url = 'https://news.ycombinator.com/rss' }
  else if (type === 'lobsters') { newFeed.value.url = 'https://lobste.rs/rss'; editingFeedData.value.url = 'https://lobste.rs/rss' }
}

async function addFeed() {
  const data: any = { type: newFeed.value.type, name: newFeed.value.name, extract_content: newFeed.value.extract_content }
  if (newFeed.value.category_ids.length > 0) {
    data.category_id = newFeed.value.category_ids[0]
  } else if (newFeed.value.createCategoryName) {
    data.category_name = newFeed.value.createCategoryName
  }
  if (newFeed.value.type === 'reddit') {
    data.url = 'https://www.reddit.com/r/' + newFeed.value.url.replace(/\.rss$/, '') + '.rss'
  } else if (newFeed.value.type === 'hn') {
    data.url = 'https://news.ycombinator.com/rss'
  } else if (newFeed.value.type === 'lobsters') {
    data.url = 'https://lobste.rs/rss'
  } else {
    data.url = newFeed.value.url
  }
  await feedApi.create(data)
  await feedStore.fetchFeeds()
  showAddFeed.value = false
  resetNewFeed()
}

function resetNewFeed() {
  newFeed.value = { category_id: 0, category_ids: [] as number[], createCategoryName: '', type: 'rss', name: '', url: '', extract_content: 0 }
}

function resetEditFeed() {
  editingFeedData.value = { category_ids: [] as number[], createCategoryName: '', type: 'rss', name: '', url: '', extract_content: 0 }
}

function makeConfigJson(sourceIds: number[], highlights: number, comments: number, prompt: string): string {
  const mode = digestSourceMode.value
  const config: any = {
    default_highlights: highlights, default_comments: comments, prompt: prompt || undefined,
  }
  if (mode === 'categories') {
    config.categories = sourceIds.map(id => ({ id, highlights, comments }))
  } else {
    config.feeds = sourceIds.map(id => ({ id, highlights, comments }))
  }
  return JSON.stringify(config)
}

async function addDigest() {
  const json = makeConfigJson(newDigest.value.sourceIds, newDigest.value.highlights, newDigest.value.comments, newDigest.value.prompt)
  await digestStore.createConfig(newDigest.value.name, newDigest.value.schedule, json)
  showAddDigest.value = false
  newDigest.value = { name: '', schedule: '0 8 * * *', sourceIds: [], highlights: 5, comments: 10, prompt: defaultPrompt }
}

function startEditDigest(cfg: DigestConfig) {
  let ids: number[] = []; let highlights = 5; let comments = 10; let prompt = ''
  let mode: 'categories' | 'feeds' = 'categories'
  try {
    const parsed = JSON.parse(cfg.config)
    if (parsed.default_highlights) highlights = parsed.default_highlights
    if (parsed.default_comments) comments = parsed.default_comments
    if (parsed.prompt) prompt = parsed.prompt
    if (Array.isArray(parsed.categories) && parsed.categories.length) {
      mode = 'categories'; ids = parsed.categories.map((c: any) => c.id)
    } else if (Array.isArray(parsed.feeds) && parsed.feeds.length) {
      mode = 'feeds'; ids = parsed.feeds.map((f: any) => f.id)
    }
  } catch { /* defaults */ }
  editingDigest.value = cfg
  digestSourceMode.value = mode
  newDigest.value = { name: cfg.name, schedule: cfg.schedule, sourceIds: ids, highlights, comments, prompt }
  showEditDigest.value = true
}

async function saveEditDigest() {
  if (!editingDigest.value) return
  const json = makeConfigJson(newDigest.value.sourceIds, newDigest.value.highlights, newDigest.value.comments, newDigest.value.prompt)
  await digestStore.updateConfig(editingDigest.value.id, newDigest.value.name, newDigest.value.schedule, json)
  showEditDigest.value = false; editingDigest.value = null
}

async function deleteDigest(id: number) {
  await digestStore.deleteConfig(id)
  showEditDigest.value = false; editingDigest.value = null
}

function generateNow(e: Event, cfgId: number) {
  e.stopPropagation()
  generatingDigests.value = new Set(generatingDigests.value).add(cfgId)
  digestStore.generateNow(cfgId).finally(() => {
    const next = new Set(generatingDigests.value); next.delete(cfgId); generatingDigests.value = next
  })
}

function onDigestCategoryCreate(name: string) {
  // Will be created on save if category doesn't exist
  // For now just emit
}

function isRedditType(t: string) { return t === 'reddit' }

function setFeedUrlForType(type: string) {
  if (type === 'hn') {
    newFeed.value.url = 'https://news.ycombinator.com/rss'
  } else if (type === 'lobsters') {
    newFeed.value.url = 'https://lobste.rs/rss'
  } else if (type === 'reddit') {
    newFeed.value.url = 'https://www.reddit.com/r/'
  } else if (newFeed.value.type !== type && !isPresetType(type)) {
    newFeed.value.url = ''
  }
}

function isPresetType(t: string) { return t === 'hn' || t === 'lobsters' || t === 'reddit' }

onMounted(() => {
  digestStore.fetchConfigs()
})

const collapsedCategories = reactive(new Set<number>())
const sortedCategoryIds = computed(() => {
  const cats = [...categoryStore.categories].filter(c => feedStore.feeds.some(f => f.category_id === c.id) || c.name === 'FEEDS')
  const feedsCatIdx = cats.findIndex(c => c.name === 'FEEDS')
  if (feedsCatIdx > 0) {
    const [feedsCat] = cats.splice(feedsCatIdx, 1)
    cats.unshift(feedsCat)
  }
  return cats.map(c => c.id)
})

function toggleCategory(catId: number) {
  if (collapsedCategories.has(catId)) {
    collapsedCategories.delete(catId)
  } else {
    collapsedCategories.add(catId)
  }
}
</script>

<template>
  <div>
    <!-- Saved -->
    <button class="flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-sm hover:bg-accent/10 transition-colors mb-3" :class="{ 'bg-accent/10 font-medium text-accent': props.showingSaved }" @click="emit('showSaved')">
      <BookmarkCheck class="size-3.5 shrink-0 text-accent" /> <span class="truncate">Saved</span>
    </button>

    <!-- DIGESTS section -->
    <div class="flex items-center justify-between mb-2 px-2">
      <span class="text-xs font-semibold text-muted-foreground uppercase">Digests</span>
      <button class="p-1 rounded hover:bg-accent/20" title="Create digest config" @click="showAddDigest = true">
        <Plus class="size-3.5" />
      </button>
    </div>
    <div class="space-y-0.5 mb-3">
      <div
        v-for="cfg in digestStore.configs" :key="cfg.id"
        class="group flex items-center w-full rounded-md px-2 py-1.5 text-sm hover:bg-accent/10 transition-colors cursor-pointer"
        :class="digestStore.activeConfigId === cfg.id ? 'bg-accent/10 font-medium text-accent' : ''"
        @click="emit('selectDigestConfig', cfg.id)"
      >
        <div class="flex items-center gap-2 flex-1 min-w-0">
          <MessageSquare class="size-3.5 shrink-0 text-accent" />
          <span class="truncate">{{ cfg.name || cfg.schedule }}</span>
        </div>
        <div class="flex items-center gap-0.5 shrink-0 opacity-0 group-hover:opacity-100 transition-opacity">
          <button class="p-1 rounded hover:bg-accent/20" title="Settings" @click.stop="startEditDigest(cfg)">
            <Settings class="size-3" />
          </button>
          <button class="p-1 rounded hover:bg-accent/20" title="Generate now" @click.stop="generateNow($event, cfg.id)">
            <Sparkles class="size-3" :class="generatingDigests.has(cfg.id) ? 'animate-spin' : ''" />
          </button>
        </div>
      </div>
      <div v-if="digestStore.configs.length === 0" class="text-xs text-muted-foreground px-2 py-1">No digests yet.</div>
    </div>

    <!-- Feeds -->
    <div class="flex items-center justify-between mb-2 px-2">
      <span class="text-xs font-semibold text-muted-foreground uppercase">Feeds</span>
      <button class="p-1 rounded hover:bg-accent/20" title="Add feed" @click="showAddFeed = true">
        <Plus class="size-3.5" />
      </button>
    </div>
    <div v-for="catId in sortedCategoryIds" :key="catId" class="mb-3">
      <button class="flex w-full items-center justify-between text-xs font-semibold text-muted-foreground uppercase mb-1 hover:text-foreground transition-colors px-2" @click="toggleCategory(catId)">
        <span>{{ categoryStore.categories.find(c => c.id === catId)?.name || '' }}</span>
        <span class="inline-flex items-center justify-end h-9 w-9 shrink-0">
          <ChevronDown class="size-4 transition-transform" :class="collapsedCategories.has(catId) ? '-rotate-90' : ''" />
        </span>
      </button>
      <div v-if="!collapsedCategories.has(catId)" v-for="feed in feedStore.feeds.filter(f => f.category_id === catId)" :key="feed.id"
        class="group flex items-center w-full rounded-md px-2 py-1.5 text-sm hover:bg-accent/10 transition-colors"
        :class="{ 'bg-accent/10 font-medium text-accent': feed.id === feedStore.activeFeedId }"
      >
        <button class="flex items-center gap-2 flex-1 min-w-0" @click="emit('selectFeed', feed.id)">
          <img v-if="feedIcon(feed)" :src="feedIcon(feed)" class="size-3.5 shrink-0 rounded" @error="($event.target as HTMLImageElement).style.display = 'none'" />
          <Rss v-else class="size-3.5 shrink-0 text-accent" />
          <Tooltip>
            <template #trigger>
              <span class="truncate cursor-help">{{ feed.name }}</span>
            </template>
            <p>Last fetched: {{ formatDate(feed.last_fetched_at) || 'never' }}</p>
            <p v-if="feed.status === 'error'" class="text-red-500">Failed</p>
          </Tooltip>
          <span v-if="(feedStore.unreadCounts[feed.id] ?? 0) > 0" class="text-[10px] font-medium px-1.5 py-0.5 rounded-full bg-accent text-accent-foreground ml-1.5">{{ feedStore.unreadCounts[feed.id] }}</span>
        </button>
        <button class="shrink-0 opacity-0 group-hover:opacity-100 transition-opacity p-1 rounded hover:bg-accent/20" @click.stop="startEditFeed(feed)">
          <Settings class="size-3" />
        </button>
        <button class="shrink-0 opacity-0 group-hover:opacity-100 transition-opacity p-1 rounded hover:bg-accent/20" @click.stop="markFeedRead(feed.id)">
          <Check class="size-3" />
        </button>
        <button class="shrink-0 opacity-0 group-hover:opacity-100 transition-opacity p-1 rounded hover:bg-accent/20" @click.stop="refreshFeed(feed.id)">
          <RefreshCw v-if="refreshingFeeds.has(feed.id)" class="size-3 animate-spin" />
          <RefreshCw v-else class="size-3" />
        </button>
      </div>
    </div>

    <!-- Add Feed Dialog -->
    <Dialog v-if="showAddFeed" v-model:open="showAddFeed" title="Add Feed">
      <form @submit.prevent="addFeed" class="space-y-4">
        <div class="space-y-1">
          <label class="text-xs font-medium">Category</label>
          <TagInput
            :items="categoryStore.categories.map(c => ({ label: c.name, value: c.id }))"
            v-model="newFeed.category_ids"
            placeholder="Type to search or create…"
            :allowCreate="true"
            @create="(name: string) => { newFeed.createCategoryName = name; newFeed.category_ids = [] }"
          />
        </div>
        <div class="space-y-1">
          <label class="text-xs font-medium">Type</label>
          <select v-model="newFeed.type" @change="handleTypeChange(($event.target as HTMLSelectElement).value)" class="flex h-9 w-full rounded-md border border-border bg-background px-3 text-sm">
            <option value="rss">RSS / Atom</option>
            <option value="hn">Hacker News</option>
            <option value="lobsters">Lobste.rs</option>
            <option value="reddit">Reddit</option>
          </select>
        </div>
        <div class="space-y-1">
          <label class="text-xs font-medium">Name</label>
          <Input v-model="newFeed.name" placeholder="Feed name" required />
        </div>
        <div v-if="newFeed.type === 'hn' || newFeed.type === 'lobsters'" class="space-y-1">
          <label class="text-xs font-medium">URL</label>
          <p class="text-sm text-muted-foreground bg-muted rounded px-2 py-1.5">{{ newFeed.url }}</p>
        </div>
        <div v-else-if="newFeed.type === 'reddit'" class="space-y-1">
          <label class="text-xs font-medium">URL</label>
          <div class="flex items-center gap-0">
            <span class="text-xs text-muted-foreground bg-muted rounded-l-md px-2 py-1.5 border border-border border-r-0 whitespace-nowrap">https://www.reddit.com/r/</span>
            <Input v-model="newFeed.url" placeholder="subreddit" required class="rounded-none" />
            <span class="text-xs text-muted-foreground bg-muted rounded-r-md px-2 py-1.5 border border-border border-l-0 whitespace-nowrap">.rss</span>
          </div>
        </div>
        <div v-else class="space-y-1">
          <label class="text-xs font-medium">URL</label>
          <Input v-model="newFeed.url" placeholder="Feed URL" required />
        </div>
        <label class="flex items-center gap-2 text-sm cursor-pointer">
          <input type="checkbox" v-model.number="newFeed.extract_content" :true-value="1" :false-value="0" class="toggle" />
          Auto-extract full article content
        </label>
        <Button type="submit">Add</Button>
      </form>
    </Dialog>

    <!-- Edit Feed Dialog -->
    <Dialog v-if="showEditFeed && editingFeed" v-model:open="showEditFeed" title="Edit Feed">
      <form @submit.prevent="saveEditFeed" class="space-y-4">
        <div class="space-y-1">
          <label class="text-xs font-medium">Category</label>
          <TagInput
            :items="categoryStore.categories.map(c => ({ label: c.name, value: c.id }))"
            v-model="editingFeedData.category_ids"
            placeholder="Search…"
            :allowCreate="true"
            @create="(name: string) => { editingFeedData.createCategoryName = name; editingFeedData.category_ids = [] }"
          />
        </div>
        <div class="space-y-1">
          <label class="text-xs font-medium">Type</label>
          <select v-model="editingFeedData.type" @change="handleTypeChange(($event.target as HTMLSelectElement).value)" class="flex h-9 w-full rounded-md border border-border bg-background px-3 text-sm">
            <option value="rss">RSS / Atom</option>
            <option value="hn">Hacker News</option>
            <option value="lobsters">Lobste.rs</option>
            <option value="reddit">Reddit</option>
          </select>
        </div>
        <div class="space-y-1">
          <label class="text-xs font-medium">Name</label>
          <Input v-model="editingFeedData.name" placeholder="Feed name" required />
        </div>
        <div v-if="editingFeedData.type === 'hn' || editingFeedData.type === 'lobsters'" class="space-y-1">
          <label class="text-xs font-medium">URL</label>
          <p class="text-sm text-muted-foreground bg-muted rounded px-2 py-1.5">{{ editingFeedData.url }}</p>
        </div>
        <div v-else-if="editingFeedData.type === 'reddit'" class="space-y-1">
          <label class="text-xs font-medium">URL</label>
          <div class="flex items-center gap-0">
            <span class="text-xs text-muted-foreground bg-muted rounded-l-md px-2 py-1.5 border border-border border-r-0 whitespace-nowrap">https://www.reddit.com/r/</span>
            <Input v-model="editingFeedData.url" placeholder="subreddit" required class="rounded-none" />
            <span class="text-xs text-muted-foreground bg-muted rounded-r-md px-2 py-1.5 border border-border border-l-0 whitespace-nowrap">.rss</span>
          </div>
        </div>
        <div v-else class="space-y-1">
          <label class="text-xs font-medium">URL</label>
          <Input v-model="editingFeedData.url" placeholder="Feed URL" required />
        </div>
        <label class="flex items-center gap-2 text-sm cursor-pointer">
          <input type="checkbox" v-model.number="editingFeedData.extract_content" :true-value="1" :false-value="0" class="toggle" />
          Auto-extract full article content
        </label>
        <div class="flex gap-2">
          <Button type="submit">Save</Button>
          <Button variant="outline" class="text-red-500" @click="deleteFeed(editingFeed!.id)">Delete</Button>
        </div>
      </form>
    </Dialog>

    <!-- Add Digest Config Dialog -->
    <Dialog v-if="showAddDigest" v-model:open="showAddDigest" title="Create Digest Config">
      <form @submit.prevent="addDigest" class="space-y-4">
        <div class="space-y-1">
          <label class="text-xs font-medium">Name</label>
          <Input v-model="newDigest.name" placeholder="Digest name (e.g. Daily Tech)" required />
        </div>
        <div class="space-y-1">
          <label class="text-xs font-medium">Schedule (cron syntax)</label>
          <Input v-model="newDigest.schedule" placeholder="0 8 * * *" required />
          <p class="text-xs text-muted-foreground mt-0.5">0 8 * * * (daily 8am), 0 */6 * * * (every 6h)</p>
        </div>
        <div class="space-y-1">
          <label class="text-xs font-medium">Source</label>
          <div class="flex rounded-md border border-border overflow-hidden text-xs w-fit">
            <button type="button" class="px-2 py-1 transition-colors" :class="digestSourceMode === 'categories' ? 'bg-accent text-accent-foreground' : 'hover:bg-muted'" @click="digestSourceMode = 'categories'">Categories</button>
            <button type="button" class="px-2 py-1 border-x border-border transition-colors" :class="digestSourceMode === 'feeds' ? 'bg-accent text-accent-foreground' : 'hover:bg-muted'" @click="digestSourceMode = 'feeds'">Feeds</button>
          </div>
          <div class="mt-1">
            <TagInput
              v-if="digestSourceMode === 'categories'"
              :items="categoryStore.categories.map(c => ({ label: c.name, value: c.id }))"
              v-model="newDigest.sourceIds"
              placeholder="Search categories…"
            />
            <TagInput
              v-else
              :items="feedStore.feeds.map(f => ({ label: f.name + ' (' + f.type + ')', value: f.id }))"
              v-model="newDigest.sourceIds"
              placeholder="Search feeds…"
            />
          </div>
        </div>
        <div class="grid grid-cols-2 gap-3">
          <div class="space-y-1"><label class="text-xs font-medium">Max highlights</label><Input v-model.number="newDigest.highlights" type="number" min="1" max="10" /></div>
          <div class="space-y-1"><label class="text-xs font-medium">Max comments</label><Input v-model.number="newDigest.comments" type="number" min="1" max="50" /></div>
        </div>
        <div class="space-y-1">
          <label class="text-xs font-medium">Prompt (optional)</label>
          <textarea v-model="newDigest.prompt" rows="3" class="flex w-full rounded-md border border-border bg-background px-3 py-1.5 text-xs" />
        </div>
        <Button type="submit">Create</Button>
      </form>
    </Dialog>

    <!-- Edit Digest Config Dialog -->
    <Dialog v-if="showEditDigest" v-model:open="showEditDigest" title="Edit Digest Config">
      <form @submit.prevent="saveEditDigest" class="space-y-4">
        <div class="space-y-1">
          <label class="text-xs font-medium">Name</label>
          <Input v-model="newDigest.name" placeholder="Digest name" required />
        </div>
        <div class="space-y-1">
          <label class="text-xs font-medium">Schedule</label>
          <Input v-model="newDigest.schedule" placeholder="0 8 * * *" required />
        </div>
        <div class="space-y-1">
          <label class="text-xs font-medium">Source</label>
          <div class="flex rounded-md border border-border overflow-hidden text-xs w-fit">
            <button type="button" class="px-2 py-1 transition-colors" :class="digestSourceMode === 'categories' ? 'bg-accent text-accent-foreground' : 'hover:bg-muted'" @click="digestSourceMode = 'categories'">Categories</button>
            <button type="button" class="px-2 py-1 border-x border-border transition-colors" :class="digestSourceMode === 'feeds' ? 'bg-accent text-accent-foreground' : 'hover:bg-muted'" @click="digestSourceMode = 'feeds'">Feeds</button>
          </div>
          <div class="mt-1">
            <TagInput
              v-if="digestSourceMode === 'categories'"
              :items="categoryStore.categories.map(c => ({ label: c.name, value: c.id }))"
              v-model="newDigest.sourceIds"
              placeholder="Search categories…"
            />
            <TagInput
              v-else
              :items="feedStore.feeds.map(f => ({ label: f.name + ' (' + f.type + ')', value: f.id }))"
              v-model="newDigest.sourceIds"
              placeholder="Search feeds…"
            />
          </div>
        </div>
        <div class="grid grid-cols-2 gap-3">
          <div class="space-y-1"><label class="text-xs font-medium">Max highlights</label><Input v-model.number="newDigest.highlights" type="number" min="1" max="10" /></div>
          <div class="space-y-1"><label class="text-xs font-medium">Max comments</label><Input v-model.number="newDigest.comments" type="number" min="1" max="50" /></div>
        </div>
        <div class="space-y-1">
          <label class="text-xs font-medium">Prompt</label>
          <textarea v-model="newDigest.prompt" rows="3" class="flex w-full rounded-md border border-border bg-background px-3 py-1.5 text-xs" />
        </div>
        <div class="flex gap-2">
          <Button type="submit">Save</Button>
          <Button v-if="editingDigest" variant="outline" class="text-red-500" @click="deleteDigest(editingDigest.id)">Delete</Button>
        </div>
      </form>
    </Dialog>
  </div>
</template>
