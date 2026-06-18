<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { useRouter } from 'vue-router'
import Button from '../components/ui/Button'
import Input from '../components/ui/Input'
import Dialog from '../components/ui/Dialog'
import { useAuthStore } from '../stores/auth'
import { useCategoryStore } from '../stores/category'
import { feedApi } from '../services/api'
import type { Feed } from '../types/api'
import Separator from '../components/ui/Separator'
import { ArrowLeft, Plus, Trash2, Rss, Settings2 } from 'lucide-vue-next'

const router = useRouter()
const auth = useAuthStore()
const categoryStore = useCategoryStore()

const feeds = ref<Feed[]>([])
const showAddFeed = ref(false)
const showEditFeed = ref(false)
const editingFeed = ref<Feed | null>(null)
const editExtractContent = ref(0)
const newFeed = ref({ category_id: 0, type: 'rss', name: '', url: '' })
const newCategoryName = ref('')
const showAddCategory = ref(false)
onMounted(async () => {
  await categoryStore.fetchCategories()
  feeds.value = await feedApi.list()
})

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

    <Separator class="my-6" />
    <Button variant="ghost" @click="auth.signout()">Sign Out</Button>
  </div>
</template>
