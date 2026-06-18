<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { useRouter } from 'vue-router'
import Button from '../components/ui/Button'
import Input from '../components/ui/Input'
import Dialog from '../components/ui/Dialog'
import { useAuthStore } from '../stores/auth'
import { useCategoryStore } from '../stores/category'
import { useDigestStore } from '../stores/digest'
import { feedApi } from '../services/api'
import type { Feed, DigestConfig } from '../types/api'
import Separator from '../components/ui/Separator'
import { ArrowLeft, Plus, Trash2, Rss, Settings2, MessageSquare } from 'lucide-vue-next'

const router = useRouter()
const auth = useAuthStore()
const categoryStore = useCategoryStore()
const digestStore = useDigestStore()

const feeds = ref<Feed[]>([])
const showAddFeed = ref(false)
const showEditFeed = ref(false)
const editingFeed = ref<Feed | null>(null)
const editExtractContent = ref(0)
const newFeed = ref({ category_id: 0, type: 'rss', name: '', url: '' })
const newCategoryName = ref('')
const showAddCategory = ref(false)

const showAddDigest = ref(false)
const showEditDigest = ref(false)
const editingDigest = ref<{ id: number; schedule: string; categoryIds: number[]; feedIds: number[]; highlights: number; comments: number; prompt: string } | null>(null)
const newDigest = ref({
  schedule: '0 8 * * *',
  categoryIds: [] as number[],
  feedIds: [] as number[],
  highlights: 5,
  comments: 10,
  prompt: '',
})

onMounted(async () => {
  await categoryStore.fetchCategories()
  feeds.value = await feedApi.list()
  await digestStore.fetchConfigs()
})

function makeConfigJson(categoryIds: number[], feedIds: number[], highlights: number, comments: number, prompt: string): string {
  const config: any = {
    default_highlights: highlights,
    default_comments: comments,
    prompt: prompt || undefined,
    categories: categoryIds.map(id => ({ id, highlights, comments })),
    feeds: feedIds.map(id => ({ id, highlights, comments })),
  }
  return JSON.stringify(config)
}

async function addDigest() {
  const json = makeConfigJson(newDigest.value.categoryIds, newDigest.value.feedIds, newDigest.value.highlights, newDigest.value.comments, newDigest.value.prompt)
  await digestStore.createConfig(newDigest.value.schedule, json)
  showAddDigest.value = false
  newDigest.value = { schedule: '0 8 * * *', categoryIds: [], feedIds: [], highlights: 5, comments: 10, prompt: '' }
}

async function deleteDigest(id: number) {
  await digestStore.deleteConfig(id)
}

function startEditDigest(cfg: DigestConfig) {
  let catIds: number[] = []
  let feedIds: number[] = []
  let highlights = 5
  let comments = 10
  let prompt = ''
  try {
    const parsed = JSON.parse(cfg.config)
    if (parsed.default_highlights) highlights = parsed.default_highlights
    if (parsed.default_comments) comments = parsed.default_comments
    if (parsed.prompt) prompt = parsed.prompt
    if (Array.isArray(parsed.categories)) catIds = parsed.categories.map((c: any) => c.id)
    if (Array.isArray(parsed.feeds)) feedIds = parsed.feeds.map((f: any) => f.id)
  } catch { /* use defaults */ }
  editingDigest.value = { id: cfg.id, schedule: cfg.schedule, categoryIds: catIds, feedIds, highlights, comments, prompt }
  showEditDigest.value = true
}

async function saveEditDigest() {
  if (!editingDigest.value) return
  const json = makeConfigJson(editingDigest.value.categoryIds, editingDigest.value.feedIds, editingDigest.value.highlights, editingDigest.value.comments, editingDigest.value.prompt)
  await digestStore.updateConfig(editingDigest.value.id, editingDigest.value.schedule, json)
  showEditDigest.value = false
  editingDigest.value = null
}

function toggleCategory(list: number[], catId: number) {
  const idx = list.indexOf(catId)
  if (idx !== -1) { list.splice(idx, 1) } else { list.push(catId) }
}

function toggleFeed(list: number[], feedId: number) {
  const idx = list.indexOf(feedId)
  if (idx !== -1) { list.splice(idx, 1) } else { list.push(feedId) }
}

async function addFeed() {
  await feedApi.create(newFeed.value)
  feeds.value = await feedApi.list()
  showAddFeed.value = false
  newFeed.value = { category_id: 0, type: 'rss', name: '', url: '' }
}

async function deleteFeed(id: number) {
  await feedApi.delete(id)
  feeds.value = feeds.value.filter(f => f.id !== id)
}

async function addCategory() {
  if (!newCategoryName.value) return
  await categoryStore.create(newCategoryName.value)
  newCategoryName.value = ''
  showAddCategory.value = false
}

async function deleteCategory(id: number) {
  await categoryStore.remove(id)
}

function startEdit(feed: Feed) {
  editingFeed.value = feed
  editExtractContent.value = feed.extract_content
  showEditFeed.value = true
}

async function saveEdit() {
  if (!editingFeed.value) return
  await feedApi.update(editingFeed.value.id, { extract_content: editExtractContent.value })
  const f = feeds.value.find(f => f.id === editingFeed.value!.id)
  if (f) f.extract_content = editExtractContent.value
  showEditFeed.value = false
  editingFeed.value = null
}

function handleTypeChange(type: string) {
  newFeed.value.type = type
  switch (type) {
    case 'hn':
      newFeed.value.url = 'https://news.ycombinator.com/rss'
      break
    case 'lobsters':
      newFeed.value.url = 'https://lobste.rs/rss'
      break
    case 'reddit':
      newFeed.value.url = 'https://www.reddit.com/r/'
      break
    default:
      newFeed.value.url = ''
  }
}
</script>

<template>
  <div class="mx-auto max-w-2xl p-6">
    <div class="flex items-center gap-4 mb-6">
      <Button variant="ghost" size="icon" @click="router.back()">
        <ArrowLeft class="size-4" />
      </Button>
      <h1 class="text-xl font-semibold">Settings</h1>
    </div>

    <!-- Categories -->
    <section class="mb-8">
      <div class="flex items-center justify-between mb-3">
        <h2 class="text-sm font-semibold">Categories</h2>
        <Button size="sm" variant="outline" @click="showAddCategory = true">
          <Plus class="size-3" /> Add
        </Button>
      </div>
      <div v-for="cat in categoryStore.categories" :key="cat.id" class="flex items-center justify-between py-2 border-b">
        <span class="text-sm">{{ cat.name }}</span>
        <Button variant="ghost" size="icon" @click="deleteCategory(cat.id)">
          <Trash2 class="size-3 text-red-500" />
        </Button>
      </div>
      <Dialog v-if="showAddCategory" v-model:open="showAddCategory" title="Add Category">
        <form @submit.prevent="addCategory" class="space-y-3">
          <Input v-model="newCategoryName" placeholder="Category name" required />
          <Button type="submit">Add</Button>
        </form>
      </Dialog>
    </section>

    <!-- Feeds -->
    <section class="mb-8">
      <div class="flex items-center justify-between mb-3">
        <h2 class="text-sm font-semibold">Feeds</h2>
        <Button size="sm" variant="outline" @click="showAddFeed = true">
          <Plus class="size-3" /> Add
        </Button>
      </div>
      <div v-for="feed in feeds" :key="feed.id" class="flex items-center justify-between py-2 border-b">
        <div class="flex items-center gap-2">
          <Rss class="size-3 text-accent" />
          <span class="text-sm">{{ feed.name }}</span>
          <span class="text-xs text-muted-foreground">({{ feed.type }})</span>
        </div>
        <div class="flex items-center gap-1">
          <Button variant="ghost" size="icon" @click="startEdit(feed)">
            <Settings2 class="size-3" />
          </Button>
          <Button variant="ghost" size="icon" @click="deleteFeed(feed.id)">
            <Trash2 class="size-3 text-red-500" />
          </Button>
        </div>
      </div>
      <Dialog v-if="showEditFeed" v-model:open="showEditFeed" title="Edit Feed">
        <form @submit.prevent="saveEdit" class="space-y-3">
          <p class="text-sm font-medium">{{ editingFeed?.name }}</p>
          <label class="flex items-center gap-2 text-sm cursor-pointer">
            <input type="checkbox" v-model.number="editExtractContent" :true-value="1" :false-value="0" class="toggle" />
            Auto-extract full article content
          </label>
          <Button type="submit">Save</Button>
        </form>
      </Dialog>
      <Dialog v-if="showAddFeed" v-model:open="showAddFeed" title="Add Feed">
        <form @submit.prevent="addFeed" class="space-y-3">
          <select v-model="newFeed.category_id" required class="flex h-9 w-full rounded-md border border-border bg-background px-3 text-sm">
            <option value="0" disabled>Select category</option>
            <option v-for="cat in categoryStore.categories" :key="cat.id" :value="cat.id">{{ cat.name }}</option>
          </select>
          <select :value="newFeed.type" @change="handleTypeChange(($event.target as HTMLSelectElement).value)" class="flex h-9 w-full rounded-md border border-border bg-background px-3 text-sm">
            <option value="rss">RSS / Atom</option>
            <option value="hn">Hacker News</option>
            <option value="lobsters">Lobste.rs</option>
            <option value="reddit">Reddit</option>
          </select>
          <Input v-model="newFeed.name" placeholder="Feed name" required />
          <div v-if="newFeed.type === 'rss' || newFeed.type === 'atom' || newFeed.type === 'reddit'" class="space-y-1">
            <Input v-model="newFeed.url" placeholder="Feed URL" required />
            <p v-if="newFeed.type === 'reddit'" class="text-xs text-muted-foreground">Append the subreddit name and .rss, e.g. https://www.reddit.com/r/programming/.rss</p>
          </div>
          <p v-if="newFeed.type === 'hn' || newFeed.type === 'lobsters'" class="text-xs text-muted-foreground">URL will be set automatically to {{ newFeed.url }}</p>
          <Button type="submit">Add</Button>
        </form>
      </Dialog>
    </section>

    <!-- Digests -->
    <section class="mb-8">
      <div class="flex items-center justify-between mb-3">
        <h2 class="text-sm font-semibold">Digests</h2>
        <Button size="sm" variant="outline" @click="showAddDigest = true">
          <Plus class="size-3" /> Add
        </Button>
      </div>
      <div v-for="cfg in digestStore.configs" :key="cfg.id" class="flex items-center justify-between py-2 border-b">
        <div class="flex items-center gap-2 min-w-0">
          <MessageSquare class="size-3 text-accent shrink-0" />
          <span class="text-sm truncate">{{ cfg.schedule }}</span>
          <span class="text-xs text-muted-foreground shrink-0">(created {{ new Date(cfg.created_at).toLocaleDateString() }})</span>
        </div>
        <div class="flex items-center gap-1 shrink-0">
          <Button variant="ghost" size="icon" @click="startEditDigest(cfg)">
            <Settings2 class="size-3" />
          </Button>
          <Button variant="ghost" size="icon" @click="deleteDigest(cfg.id)">
            <Trash2 class="size-3 text-red-500" />
          </Button>
        </div>
      </div>
      <div v-if="digestStore.configs.length === 0" class="text-xs text-muted-foreground py-2">
        No digest configs yet.
      </div>
      <Dialog v-if="showAddDigest" v-model:open="showAddDigest" title="Create Digest">
        <form @submit.prevent="addDigest" class="space-y-3">
          <div>
            <label class="text-xs font-medium">Schedule (cron syntax)</label>
            <Input v-model="newDigest.schedule" placeholder="0 8 * * *" required />
            <p class="text-xs text-muted-foreground mt-0.5">e.g. 0 8 * * * (daily 8am), 0 */6 * * * (every 6h)</p>
          </div>
          <div>
            <label class="text-xs font-medium">Include categories</label>
            <div class="space-y-1 mt-1 max-h-32 overflow-y-auto">
              <label v-for="cat in categoryStore.categories" :key="cat.id" class="flex items-center gap-2 text-xs cursor-pointer">
                <input type="checkbox" :checked="newDigest.categoryIds.includes(cat.id)" @change="toggleCategory(newDigest.categoryIds, cat.id)" />
                {{ cat.name }}
              </label>
            </div>
          </div>
          <div>
            <label class="text-xs font-medium">Include specific feeds</label>
            <div class="space-y-1 mt-1 max-h-32 overflow-y-auto">
              <label v-for="feed in feeds" :key="feed.id" class="flex items-center gap-2 text-xs cursor-pointer">
                <input type="checkbox" :checked="newDigest.feedIds.includes(feed.id)" @change="toggleFeed(newDigest.feedIds, feed.id)" />
                {{ feed.name }} ({{ feed.type }})
              </label>
            </div>
          </div>
          <div class="grid grid-cols-2 gap-2">
            <div>
              <label class="text-xs font-medium">Max highlights</label>
              <Input v-model.number="newDigest.highlights" type="number" min="1" max="10" />
            </div>
            <div>
              <label class="text-xs font-medium">Max comments</label>
              <Input v-model.number="newDigest.comments" type="number" min="1" max="50" />
            </div>
          </div>
          <div>
            <label class="text-xs font-medium">Prompt (optional, leave empty for default)</label>
            <textarea v-model="newDigest.prompt" rows="3" class="flex w-full rounded-md border border-border bg-background px-3 py-1.5 text-xs" placeholder="Custom LLM prompt..." />
          </div>
          <Button type="submit">Create</Button>
        </form>
      </Dialog>
      <Dialog v-if="showEditDigest && editingDigest" v-model:open="showEditDigest" title="Edit Digest">
        <form @submit.prevent="saveEditDigest" class="space-y-3">
          <div>
            <label class="text-xs font-medium">Schedule (cron syntax)</label>
            <Input v-model="editingDigest.schedule" placeholder="0 8 * * *" required />
            <p class="text-xs text-muted-foreground mt-0.5">e.g. 0 8 * * * (daily 8am), 0 */6 * * * (every 6h)</p>
          </div>
          <div>
            <label class="text-xs font-medium">Include categories</label>
            <div class="space-y-1 mt-1 max-h-32 overflow-y-auto">
              <label v-for="cat in categoryStore.categories" :key="cat.id" class="flex items-center gap-2 text-xs cursor-pointer">
                <input type="checkbox" :checked="editingDigest.categoryIds.includes(cat.id)" @change="toggleCategory(editingDigest.categoryIds, cat.id)" />
                {{ cat.name }}
              </label>
            </div>
          </div>
          <div>
            <label class="text-xs font-medium">Include specific feeds</label>
            <div class="space-y-1 mt-1 max-h-32 overflow-y-auto">
              <label v-for="feed in feeds" :key="feed.id" class="flex items-center gap-2 text-xs cursor-pointer">
                <input type="checkbox" :checked="editingDigest.feedIds.includes(feed.id)" @change="toggleFeed(editingDigest.feedIds, feed.id)" />
                {{ feed.name }} ({{ feed.type }})
              </label>
            </div>
          </div>
          <div class="grid grid-cols-2 gap-2">
            <div>
              <label class="text-xs font-medium">Max highlights</label>
              <Input v-model.number="editingDigest.highlights" type="number" min="1" max="10" />
            </div>
            <div>
              <label class="text-xs font-medium">Max comments</label>
              <Input v-model.number="editingDigest.comments" type="number" min="1" max="50" />
            </div>
          </div>
          <div>
            <label class="text-xs font-medium">Prompt (optional, leave empty for default)</label>
            <textarea v-model="editingDigest.prompt" rows="3" class="flex w-full rounded-md border border-border bg-background px-3 py-1.5 text-xs" placeholder="Custom LLM prompt..." />
          </div>
          <Button type="submit">Save</Button>
        </form>
      </Dialog>
    </section>

    <Separator class="my-6" />
    <Button variant="ghost" @click="auth.signout()">Sign Out</Button>
  </div>
</template>
