<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import { toast } from 'vue-sonner'
import { useCategoryStore } from '../stores/category'
import { useFeedStore } from '../stores/feed'
import { useDigestStore } from '../stores/digest'
import { feedApi, digestApi } from '../services/api'
import type { DigestConfig, Feed } from '../types/api'
import Button from './ui/Button'
import Input from './ui/Input'
import Dialog from './ui/Dialog'
import Tooltip from './ui/Tooltip'
import { BookmarkCheck, Rss, RefreshCw, Check, Plus, Sparkles, Settings, Trash2, MessageSquare } from 'lucide-vue-next'

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

const showAddFeed = ref(false)
const showEditFeed = ref(false)
const editingFeed = ref<Feed | null>(null)
const editExtractContent = ref(0)
const newFeed = ref({ category_id: 0, category_name: '', type: 'rss', name: '', url: '', createNewCategory: false })
const newCategoryName = ref('')

const showAddDigest = ref(false)
const showEditDigest = ref(false)
const editingDigest = ref<DigestConfig | null>(null)
const newDigest = ref({
  name: '',
  schedule: '0 8 * * *',
  categoryIds: [] as number[],
  feedIds: [] as number[],
  highlights: 5,
  comments: 10,
  prompt: '',
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
  editExtractContent.value = feed.extract_content
  showEditFeed.value = true
}

async function saveEditFeed() {
  if (!editingFeed.value) return
  await feedApi.update(editingFeed.value.id, { extract_content: editExtractContent.value })
  const f = feedStore.feeds.find(f => f.id === editingFeed.value!.id)
  if (f) f.extract_content = editExtractContent.value
  showEditFeed.value = false; editingFeed.value = null
  await feedStore.fetchFeeds()
}

async function deleteFeed(id: number) {
  await feedApi.delete(id)
  showEditFeed.value = false; editingFeed.value = null
  await feedStore.fetchFeeds()
}

function handleTypeChange(type: string) {
  newFeed.value.type = type
  switch (type) {
    case 'hn': newFeed.value.url = 'https://news.ycombinator.com/rss'; break
    case 'lobsters': newFeed.value.url = 'https://lobste.rs/rss'; break
    case 'reddit': newFeed.value.url = 'https://www.reddit.com/r/'; break
    default: newFeed.value.url = ''
  }
}

async function addFeed() {
  const data: any = { type: newFeed.value.type, name: newFeed.value.name, url: newFeed.value.url }
  if (newFeed.value.createNewCategory && newFeed.value.category_name) {
    data.category_name = newFeed.value.category_name
  } else if (newFeed.value.category_id > 0) {
    data.category_id = newFeed.value.category_id
  }
  await feedApi.create(data)
  await feedStore.fetchFeeds()
  showAddFeed.value = false
  newFeed.value = { category_id: 0, category_name: '', type: 'rss', name: '', url: '', createNewCategory: false }
}

function makeConfigJson(categoryIds: number[], feedIds: number[], highlights: number, comments: number, prompt: string): string {
  const config: any = {
    default_highlights: highlights, default_comments: comments, prompt: prompt || undefined,
    categories: categoryIds.map(id => ({ id, highlights, comments })),
    feeds: feedIds.map(id => ({ id, highlights, comments })),
  }
  return JSON.stringify(config)
}

async function addDigest() {
  const json = makeConfigJson(newDigest.value.categoryIds, newDigest.value.feedIds, newDigest.value.highlights, newDigest.value.comments, newDigest.value.prompt)
  await digestStore.createConfig(newDigest.value.name, newDigest.value.schedule, json)
  showAddDigest.value = false
  newDigest.value = { name: '', schedule: '0 8 * * *', categoryIds: [], feedIds: [], highlights: 5, comments: 10, prompt: '' }
}

function startEditDigest(cfg: DigestConfig) {
  let catIds: number[] = []; let feedIds: number[] = []; let highlights = 5; let comments = 10; let prompt = ''
  try {
    const parsed = JSON.parse(cfg.config)
    if (parsed.default_highlights) highlights = parsed.default_highlights
    if (parsed.default_comments) comments = parsed.default_comments
    if (parsed.prompt) prompt = parsed.prompt
    if (Array.isArray(parsed.categories)) catIds = parsed.categories.map((c: any) => c.id)
    if (Array.isArray(parsed.feeds)) feedIds = parsed.feeds.map((f: any) => f.id)
  } catch { /* defaults */ }
  editingDigest.value = cfg
  newDigest.value = { name: cfg.name, schedule: cfg.schedule, categoryIds: catIds, feedIds, highlights, comments, prompt }
  showEditDigest.value = true
}

async function saveEditDigest() {
  if (!editingDigest.value) return
  const json = makeConfigJson(newDigest.value.categoryIds, newDigest.value.feedIds, newDigest.value.highlights, newDigest.value.comments, newDigest.value.prompt)
  await digestStore.updateConfig(editingDigest.value.id, newDigest.value.name, newDigest.value.schedule, json)
  showEditDigest.value = false; editingDigest.value = null
}

async function deleteDigest(id: number) {
  await digestStore.deleteConfig(id)
  showEditDigest.value = false; editingDigest.value = null
}

function generateNow(e: Event, cfgId: number) {
  e.stopPropagation()
  digestStore.generateNow(cfgId)
}

function toggleCategory(list: number[], catId: number) {
  const idx = list.indexOf(catId)
  if (idx !== -1) list.splice(idx, 1); else list.push(catId)
}

function toggleFeed(list: number[], feedId: number) {
  const idx = list.indexOf(feedId)
  if (idx !== -1) list.splice(idx, 1); else list.push(feedId)
}

onMounted(() => {
  digestStore.fetchConfigs()
})

const sortedCategoryIds = computed(() => {
  const cats = [...categoryStore.categories]
  const feedsCatIdx = cats.findIndex(c => c.name === 'FEEDS')
  if (feedsCatIdx > 0) {
    const [feedsCat] = cats.splice(feedsCatIdx, 1)
    cats.unshift(feedsCat)
  }
  return cats.map(c => c.id)
})
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
      <Button variant="ghost" size="icon" class="size-5" title="Create digest config" @click="showAddDigest = true">
        <Plus class="size-3.5" />
      </Button>
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
          <button class="p-1 rounded hover:bg-accent/20" title="Generate now" :disabled="digestStore.generating" @click.stop="generateNow($event, cfg.id)">
            <Sparkles class="size-3" />
          </button>
          <button class="p-1 rounded hover:bg-accent/20" title="Settings" @click.stop="startEditDigest(cfg)">
            <Settings class="size-3" />
          </button>
        </div>
      </div>
      <div v-if="digestStore.configs.length === 0" class="text-xs text-muted-foreground px-2 py-1">No digests yet.</div>
    </div>

    <!-- Feeds -->
    <div class="flex items-center justify-between mb-2 px-2">
      <span class="text-xs font-semibold text-muted-foreground uppercase">Feeds</span>
      <Button variant="ghost" size="icon" class="size-5" title="Add feed" @click="showAddFeed = true">
        <Plus class="size-3.5" />
      </Button>
    </div>
    <div v-for="catId in sortedCategoryIds" :key="catId" class="mb-3">
      <p class="text-xs font-semibold text-muted-foreground uppercase mb-1 px-2">{{ categoryStore.categories.find(c => c.id === catId)?.name || '' }}</p>
      <div v-for="feed in feedStore.feeds.filter(f => f.category_id === catId)" :key="feed.id"
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
      <form @submit.prevent="addFeed" class="space-y-3">
        <div>
          <select v-model="newFeed.category_id" class="flex h-9 w-full rounded-md border border-border bg-background px-3 text-sm">
            <option value="0">FEEDS (no category)</option>
            <option v-for="cat in categoryStore.categories.filter(c => c.name !== 'FEEDS')" :key="cat.id" :value="cat.id">{{ cat.name }}</option>
            <option value="-1">Create new category…</option>
          </select>
          <Input v-if="newFeed.category_id === -1" v-model="newFeed.category_name" placeholder="New category name" class="mt-1" @update:modelValue="() => { newFeed.createNewCategory = true }" />
        </div>
        <select :value="newFeed.type" @change="handleTypeChange(($event.target as HTMLSelectElement).value)" class="flex h-9 w-full rounded-md border border-border bg-background px-3 text-sm">
          <option value="rss">RSS / Atom</option>
          <option value="hn">Hacker News</option>
          <option value="lobsters">Lobste.rs</option>
          <option value="reddit">Reddit</option>
        </select>
        <Input v-model="newFeed.name" placeholder="Feed name" required />
        <div v-if="newFeed.type === 'rss' || newFeed.type === 'atom' || newFeed.type === 'reddit'" class="space-y-1">
          <Input v-model="newFeed.url" placeholder="Feed URL" required />
          <p v-if="newFeed.type === 'reddit'" class="text-xs text-muted-foreground">Append subreddit name and .rss</p>
        </div>
        <p v-if="newFeed.type === 'hn' || newFeed.type === 'lobsters'" class="text-xs text-muted-foreground">URL set to {{ newFeed.url }}</p>
        <Button type="submit">Add</Button>
      </form>
    </Dialog>

    <!-- Edit Feed Dialog -->
    <Dialog v-if="showEditFeed && editingFeed" v-model:open="showEditFeed" title="Edit Feed">
      <form @submit.prevent="saveEditFeed" class="space-y-3">
        <p class="text-sm font-medium">{{ editingFeed.name }}</p>
        <label class="flex items-center gap-2 text-sm cursor-pointer">
          <input type="checkbox" v-model.number="editExtractContent" :true-value="1" :false-value="0" class="toggle" />
          Auto-extract full article content
        </label>
        <div class="flex gap-2">
          <Button type="submit">Save</Button>
          <Button variant="outline" class="text-red-500" @click="deleteFeed(editingFeed.id)">Delete</Button>
        </div>
      </form>
    </Dialog>

    <!-- Add Digest Config Dialog -->
    <Dialog v-if="showAddDigest" v-model:open="showAddDigest" title="Create Digest Config">
      <form @submit.prevent="addDigest" class="space-y-3">
        <Input v-model="newDigest.name" placeholder="Digest name (e.g. Daily Tech)" required />
        <div>
          <label class="text-xs font-medium">Schedule (cron syntax)</label>
          <Input v-model="newDigest.schedule" placeholder="0 8 * * *" required />
          <p class="text-xs text-muted-foreground mt-0.5">0 8 * * * (daily 8am), 0 */6 * * * (every 6h)</p>
        </div>
        <div>
          <label class="text-xs font-medium">Include categories</label>
          <div class="space-y-1 mt-1 max-h-24 overflow-y-auto">
            <label v-for="cat in categoryStore.categories" :key="cat.id" class="flex items-center gap-2 text-xs cursor-pointer">
              <input type="checkbox" :checked="newDigest.categoryIds.includes(cat.id)" @change="toggleCategory(newDigest.categoryIds, cat.id)" />
              {{ cat.name }}
            </label>
          </div>
        </div>
        <div>
          <label class="text-xs font-medium">Include specific feeds</label>
          <div class="space-y-1 mt-1 max-h-24 overflow-y-auto">
            <label v-for="feed in feedStore.feeds" :key="feed.id" class="flex items-center gap-2 text-xs cursor-pointer">
              <input type="checkbox" :checked="newDigest.feedIds.includes(feed.id)" @change="toggleFeed(newDigest.feedIds, feed.id)" />
              {{ feed.name }} ({{ feed.type }})
            </label>
          </div>
        </div>
        <div class="grid grid-cols-2 gap-2">
          <div><label class="text-xs font-medium">Max highlights</label><Input v-model.number="newDigest.highlights" type="number" min="1" max="10" /></div>
          <div><label class="text-xs font-medium">Max comments</label><Input v-model.number="newDigest.comments" type="number" min="1" max="50" /></div>
        </div>
        <div>
          <label class="text-xs font-medium">Prompt (optional)</label>
          <textarea v-model="newDigest.prompt" rows="3" class="flex w-full rounded-md border border-border bg-background px-3 py-1.5 text-xs" />
        </div>
        <Button type="submit">Create</Button>
      </form>
    </Dialog>

    <!-- Edit Digest Config Dialog -->
    <Dialog v-if="showEditDigest" v-model:open="showEditDigest" title="Edit Digest Config">
      <form @submit.prevent="saveEditDigest" class="space-y-3">
        <Input v-model="newDigest.name" placeholder="Digest name" required />
        <div>
          <label class="text-xs font-medium">Schedule</label>
          <Input v-model="newDigest.schedule" placeholder="0 8 * * *" required />
        </div>
        <div>
          <label class="text-xs font-medium">Include categories</label>
          <div class="space-y-1 mt-1 max-h-24 overflow-y-auto">
            <label v-for="cat in categoryStore.categories" :key="cat.id" class="flex items-center gap-2 text-xs cursor-pointer">
              <input type="checkbox" :checked="newDigest.categoryIds.includes(cat.id)" @change="toggleCategory(newDigest.categoryIds, cat.id)" />
              {{ cat.name }}
            </label>
          </div>
        </div>
        <div>
          <label class="text-xs font-medium">Include specific feeds</label>
          <div class="space-y-1 mt-1 max-h-24 overflow-y-auto">
            <label v-for="feed in feedStore.feeds" :key="feed.id" class="flex items-center gap-2 text-xs cursor-pointer">
              <input type="checkbox" :checked="newDigest.feedIds.includes(feed.id)" @change="toggleFeed(newDigest.feedIds, feed.id)" />
              {{ feed.name }} ({{ feed.type }})
            </label>
          </div>
        </div>
        <div class="grid grid-cols-2 gap-2">
          <div><label class="text-xs font-medium">Max highlights</label><Input v-model.number="newDigest.highlights" type="number" min="1" max="10" /></div>
          <div><label class="text-xs font-medium">Max comments</label><Input v-model.number="newDigest.comments" type="number" min="1" max="50" /></div>
        </div>
        <div>
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
