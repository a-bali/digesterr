<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { useRouter } from 'vue-router'
import Button from '../components/ui/Button'
import Input from '../components/ui/Input'
import Dialog from '../components/ui/Dialog'
import { useAuthStore } from '../stores/auth'
import { useCategoryStore } from '../stores/category'
import { useThemeStore } from '../stores/theme'
import { feedApi } from '../services/api'
import type { Feed } from '../types/api'
import Separator from '../components/ui/Separator'
import { ArrowLeft, Plus, Trash2, Rss } from 'lucide-vue-next'

const router = useRouter()
const auth = useAuthStore()
const categoryStore = useCategoryStore()
const theme = useThemeStore()

const feeds = ref<Feed[]>([])
const showAddFeed = ref(false)
const newFeed = ref({ category_id: 0, type: 'rss', name: '', url: '' })
const newCategoryName = ref('')
const showAddCategory = ref(false)
const selectedAccent = ref('')

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

const accents = ['blue', 'green', 'purple', 'orange', 'rose', 'teal', 'amber']

const accentColors: Record<string, string> = {
  blue: 'hsl(217 91% 60%)',
  green: 'hsl(142 71% 45%)',
  purple: 'hsl(271 81% 56%)',
  orange: 'hsl(24 95% 53%)',
  rose: 'hsl(346 77% 50%)',
  teal: 'hsl(173 80% 36%)',
  amber: 'hsl(38 92% 50%)',
}
</script>

<template>
  <div class="mx-auto max-w-2xl p-6">
    <div class="flex items-center gap-4 mb-6">
      <Button variant="ghost" size="icon" @click="router.push('/')">
        <ArrowLeft class="size-4" />
      </Button>
      <h1 class="text-xl font-semibold">Settings</h1>
    </div>

    <!-- Accent color -->
    <section class="mb-8">
      <h2 class="text-sm font-semibold mb-3">Accent Color</h2>
      <div class="flex gap-2">
        <button
          v-for="a in accents"
          :key="a"
          class="size-7 rounded-full border-2 transition-all"
          :class="theme.accent === a ? 'border-foreground scale-110' : 'border-transparent'"
          :style="{ backgroundColor: accentColors[a] }"
          @click="theme.setAccent(a)"
        />
      </div>
    </section>

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
        <Button variant="ghost" size="icon" @click="deleteFeed(feed.id)">
          <Trash2 class="size-3 text-red-500" />
        </Button>
      </div>
      <Dialog v-if="showAddFeed" v-model:open="showAddFeed" title="Add Feed">
        <form @submit.prevent="addFeed" class="space-y-3">
          <select v-model="newFeed.category_id" required class="flex h-9 w-full rounded-md border border-border bg-background px-3 text-sm">
            <option value="0" disabled>Select category</option>
            <option v-for="cat in categoryStore.categories" :key="cat.id" :value="cat.id">{{ cat.name }}</option>
          </select>
          <select v-model="newFeed.type" class="flex h-9 w-full rounded-md border border-border bg-background px-3 text-sm">
            <option value="rss">RSS / Atom</option>
            <option value="hn">Hacker News</option>
            <option value="lobsters">Lobste.rs</option>
            <option value="reddit">Reddit</option>
          </select>
          <Input v-model="newFeed.name" placeholder="Feed name" required />
          <Input v-model="newFeed.url" placeholder="Feed URL" required />
          <Button type="submit">Add</Button>
        </form>
      </Dialog>
    </section>

    <Separator class="my-6" />
    <Button variant="ghost" @click="auth.signout()">Sign Out</Button>
  </div>
</template>
