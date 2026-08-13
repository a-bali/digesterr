<script setup lang="ts">
import { computed } from 'vue'
import { useDigestStore } from '../stores/digest'
import { useFeedStore } from '../stores/feed'
import type { DigestContent, DigestContentItem } from '../types/api'
import Button from './ui/Button.vue'
import { MessageSquare, ExternalLink, Clock, MessageCircle, Trash2, Check, ArrowUp } from 'lucide-vue-next'
import { absoluteTime, feedIcon } from '../lib/format'

const store = useDigestStore()
const feedStore = useFeedStore()

const digestContent = computed<DigestContent | null>(() => {
  if (!store.selectedResult) return null
  try {
    return JSON.parse(store.selectedResult.content)
  } catch {
    return null
  }
})

const groupedHighlights = computed<Record<string, { feedId: number; items: DigestContentItem[] }>>(() => {
  const items = digestContent.value?.highlights || []
  const groups: Record<string, { feedId: number; items: DigestContentItem[] }> = {}
  for (const item of items) {
    const key = item.feed_name || 'Unknown'
    if (!groups[key]) groups[key] = { feedId: item.feed_id, items: [] }
    groups[key].items.push(item)
  }
  return groups
})

const highlightCount = computed(() => digestContent.value?.highlights?.length || 0)
const itemCount = computed(() => (digestContent.value?.items?.length || 0) + highlightCount.value)

async function deleteResult() {
  if (!store.selectedResult) return
  await store.deleteResult(store.selectedResult.id)
}

function toggleStatus() {
  if (!store.selectedResult) return
  const newStatus = store.selectedResult.status === 'read' ? 'unread' : 'read'
  store.updateResultStatus(store.selectedResult.id, newStatus)
}

function iconForFeedId(feedId: number): string {
  const feed = feedStore.feeds.find(f => f.id === feedId)
  return feed ? feedIcon(feed) : ''
}
</script>

<template>
  <div class="h-full overflow-y-auto p-6" data-digest-content>
    <div v-if="!store.selectedResult" class="flex h-full items-center justify-center text-muted-foreground text-sm">
      Select a digest result to view
    </div>
    <template v-else>
      <h1 class="text-xl font-semibold mb-2">
        {{ new Date(store.selectedResult.created_at).toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit' }) }}
      </h1>

      <div class="flex flex-wrap items-center gap-3 text-sm text-muted-foreground mb-4">
        <span class="text-xs text-muted-foreground">{{ new Date(store.selectedResult.created_at).toLocaleDateString(undefined, { month: 'short', day: 'numeric', year: 'numeric' }) }}</span>
        <span>{{ highlightCount }} highlights</span>
        <span>{{ itemCount }} items</span>
      </div>

      <div class="flex gap-2 mb-4 flex-wrap">
        <Button size="sm" variant="outline" @click="toggleStatus">
          <Check /> {{ store.selectedResult.status === 'read' ? 'Mark Unread' : 'Mark Read' }}
        </Button>
        <Button size="sm" variant="outline" @click="deleteResult">
          <Trash2 /> Delete
        </Button>
      </div>

      <!-- Summary -->
      <div v-if="digestContent?.summary" class="mb-6 p-4 rounded-lg border bg-muted/30">
        <h3 class="text-sm font-semibold mb-2">Summary</h3>
        <p class="text-sm leading-relaxed text-foreground/85">{{ digestContent.summary }}</p>
      </div>

      <!-- Highlights grouped by feed -->
      <div v-if="Object.keys(groupedHighlights).length" class="mb-6">
        <div v-for="(group, feedName) in groupedHighlights" :key="feedName" class="mb-6">
          <h3 class="text-sm font-semibold mb-3 flex items-center gap-1 text-accent">
            <img v-if="iconForFeedId(group.feedId)" :src="iconForFeedId(group.feedId)" class="size-3.5 shrink-0 rounded" @error="($event.target as HTMLImageElement).style.display = 'none'" />
            {{ feedName }}
          </h3>
          <div class="space-y-4">
            <div
              v-for="item in group.items"
              :key="item.item_id"
              class="p-4 rounded-lg border bg-card"
            >
              <div class="flex items-start gap-2 mb-2">
                <a :href="item.url" target="_blank" class="text-sm font-medium hover:underline flex-1">
                  {{ item.title }}
                  <ExternalLink class="size-3 inline opacity-50" />
                </a>
              </div>
              <div class="flex flex-wrap items-center gap-3 text-xs text-muted-foreground mb-3">
                <span class="flex items-center gap-1"><Clock class="size-3" /> {{ absoluteTime(item.published_at) }}</span>
                <span class="flex items-center gap-1"><ArrowUp class="size-3" /> {{ item.points }}</span>
                <a v-if="item.comment_url" :href="item.comment_url" target="_blank"
                  class="flex items-center gap-1 hover:text-accent">
                  <MessageCircle class="size-3" /> {{ item.comments_count }}
                </a>
              </div>
              <p v-if="item.llm_summary" class="text-sm leading-relaxed mb-3">
                {{ item.llm_summary }}
              </p>
              <div v-if="item.top_comments?.length" class="mb-3 border-t pt-2">
                <p class="text-xs font-medium text-muted-foreground mb-1">Top comments</p>
                <div v-for="c in item.top_comments" :key="c.url || c.text.substring(0, 20)"
                  class="text-xs mb-1 pl-2 border-l-2 border-border">
                  <template v-if="c.url">
                    <a :href="c.url" target="_blank" class="font-medium text-muted-foreground hover:text-accent">{{ c.author }}</a>
                    <span v-if="c.points != null" class="ml-1 text-accent text-[10px]">[{{ c.points }}]</span>
                    <span class="mx-1 text-muted-foreground/40">–</span>
                    <a :href="c.url" target="_blank" class="text-foreground/80 hover:text-accent">{{ c.text }}</a>
                  </template>
                  <template v-else>
                    <span class="font-medium text-muted-foreground">{{ c.author }}</span>
                    <span v-if="c.points != null" class="ml-1 text-accent text-[10px]">[{{ c.points }}]</span>
                    <span class="mx-1 text-muted-foreground/40">–</span>
                    <span class="text-foreground/80">{{ c.text }}</span>
                  </template>
                </div>
              </div>
            </div>
          </div>
        </div>
      </div>

      <!-- All Items -->
      <div v-if="digestContent?.items?.length">
        <h3 class="text-sm font-semibold mb-3 flex items-center gap-1">
          <MessageSquare class="size-3.5" /> All Items
        </h3>
        <div class="space-y-2">
          <a
            v-for="item in digestContent.items"
            :key="item.item_id"
            :href="item.url"
            target="_blank"
            class="flex items-center justify-between p-2 rounded-md hover:bg-muted transition-colors border"
          >
            <div class="flex-1 min-w-0">
              <span class="text-sm font-medium truncate block">{{ item.title }}</span>
              <div class="flex items-center gap-2 text-xs text-muted-foreground mt-0.5">
                <span class="flex items-center gap-1"><Clock class="size-3" /> {{ absoluteTime(item.published_at) }}</span>
                <span class="flex items-center gap-1"><ArrowUp class="size-3" /> {{ item.points }}</span>
                <a v-if="item.comment_url" :href="item.comment_url" target="_blank" class="flex items-center gap-1 hover:text-accent">
                  <MessageCircle class="size-3" /> {{ item.comments_count }}
                </a>
              </div>
            </div>
            <ExternalLink class="size-3 shrink-0 ml-2 opacity-50" />
          </a>
        </div>
      </div>

      <div v-if="!digestContent?.highlights?.length && !digestContent?.items?.length" class="text-sm text-muted-foreground">
        No content available for this digest.
      </div>
    </template>
  </div>
</template>
