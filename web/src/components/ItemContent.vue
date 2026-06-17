<script setup lang="ts">
import { useFeedStore } from '../stores/feed'
import Button from './ui/Button.vue'
import Separator from './ui/Separator.vue'
import { BookOpen, ExternalLink, Check, Bookmark, BookmarkCheck, MessageSquare } from 'lucide-vue-next'

defineProps<{
  absoluteTime: (val: any) => string
  unsaveItem: (item: any) => Promise<void>
}>()

const feedStore = useFeedStore()
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
          <Check class="size-3" /> {{ feedStore.selectedItem?.status === 'read' ? 'Mark Unread' : 'Mark Read' }}
        </Button>
        <Button v-if="feedStore.selectedItem!.status === 'saved'" size="sm" variant="outline" @click="unsaveItem(feedStore.selectedItem!)">
          <BookmarkCheck class="size-3" /> Unsave
        </Button>
        <Button v-else size="sm" variant="outline" @click="feedStore.updateItemStatus(feedStore.selectedItem!, 'saved')">
          <Bookmark class="size-3" /> Save
        </Button>
      </div>
      <Separator class="mb-4" />
      <div class="prose prose-sm dark:prose-invert max-w-none [&_a]:text-accent [&_a]:hover:underline" v-html="feedStore.selectedItem.content" />
    </div>
  </div>
</template>
