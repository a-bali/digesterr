<script setup lang="ts">
import { ref, onUnmounted } from 'vue'
import { useFeedStore } from '../stores/feed'
import { itemApi } from '../services/api'
import Button from './ui/Button.vue'
import Separator from './ui/Separator.vue'
import { BookOpen, Check, Bookmark, BookmarkCheck, MessageSquare, Download, RefreshCw, Files } from 'lucide-vue-next'
import { absoluteTime } from '../lib/format'

defineProps<{
  unsaveItem: (item: any) => Promise<void>
}>()

const feedStore = useFeedStore()

const extracting = ref(false)
let extractPoll: ReturnType<typeof setInterval> | null = null

async function doExtract() {
  const item = feedStore.selectedItem
  if (!item || extracting.value) return
  extracting.value = true
  try {
    await itemApi.extract(item.id)
    // Start polling for result
    if (extractPoll) clearInterval(extractPoll)
    extractPoll = setInterval(async () => {
      try {
        const updated = await itemApi.getItem(item.id)
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
  } catch {
    extracting.value = false
  }
}

onUnmounted(() => {
  if (extractPoll) clearInterval(extractPoll)
})
</script>

<template>
  <div class="h-full overflow-y-auto p-6" data-item-content>
    <div v-if="!feedStore.selectedItem" class="flex h-full items-center justify-center text-muted-foreground">
      <BookOpen class="size-8 mr-2" /> <span>Select an item</span>
    </div>
    <div v-else>
      <a v-if="feedStore.selectedItem.url" :href="feedStore.selectedItem.url" target="_blank"
        class="text-xl font-semibold mb-2 block hover:text-accent transition-colors">
        {{ feedStore.selectedItem.title }}
      </a>
      <h1 v-else class="text-xl font-semibold mb-2">{{ feedStore.selectedItem.title }}</h1>
      <div class="flex flex-wrap items-center gap-3 text-sm text-muted-foreground mb-4">
        <span v-if="feedStore.selectedItem.author">By {{ feedStore.selectedItem.author }}</span>
        <span class="text-xs text-muted-foreground">{{ absoluteTime(feedStore.selectedItem.published_at) }}</span>
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
        <Button size="sm" variant="outline" :disabled="extracting" @click="doExtract">
          <template v-if="extracting"><RefreshCw class="animate-spin" /> Extracting</template>
          <template v-else><Download /> {{ feedStore.selectedItem.extract_status === 'done' ? 'Re-extract' : 'Extract' }}</template>
        </Button>
      </div>

      <!-- Extracted Content -->
      <template v-if="feedStore.selectedItem.extract_status">
        <Separator class="mb-4" />
        <p class="text-xs font-semibold text-muted-foreground uppercase mb-2">
          Extracted
          <span v-if="feedStore.selectedItem.extract_status === 'done' && feedStore.selectedItem.extract_date" class="font-normal text-muted-foreground/60 ml-1">{{ absoluteTime(feedStore.selectedItem.extract_date) }}</span>
        </p>
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
          class="prose prose-sm dark:prose-invert max-w-none [&_a]:text-accent [&_a]:hover:underline" v-html="feedStore.selectedItem.extract_content" />
      </template>

      <!-- Original Content -->
      <template v-if="feedStore.selectedItem.extract_status">
        <Separator class="mb-4" />
        <p class="text-xs font-semibold text-muted-foreground uppercase mb-2">Original</p>
      </template>
      <div class="prose prose-sm dark:prose-invert max-w-none [&_a]:text-accent [&_a]:hover:underline" v-html="feedStore.selectedItem.content" />
    </div>
  </div>
</template>
