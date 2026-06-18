<script setup lang="ts">
import { computed } from 'vue'
import { useDigestStore } from '../stores/digest'
import type { DigestContent, DigestContentItem } from '../types/api'
import { MessageSquare, ExternalLink, Star, Clock, MessageCircle, Trash2 } from 'lucide-vue-next'

const store = useDigestStore()

const digestContent = computed<DigestContent | null>(() => {
  if (!store.selectedResult) return null
  try {
    return JSON.parse(store.selectedResult.content)
  } catch {
    return null
  }
})

const groupedHighlights = computed<Record<string, DigestContentItem[]>>(() => {
  const items = digestContent.value?.highlights || []
  const groups: Record<string, DigestContentItem[]> = {}
  for (const item of items) {
    const key = item.feed_name || 'Unknown'
    if (!groups[key]) groups[key] = []
    groups[key].push(item)
  }
  return groups
})

function relativeTime(val: string): string {
  if (!val) return ''
  const d = new Date(val)
  if (isNaN(d.getTime())) return ''
  const diff = Date.now() - d.getTime()
  const sec = Math.floor(diff / 1000)
  if (sec < 60) return 'now'
  const min = Math.floor(sec / 60)
  if (min < 60) return `${min}m ago`
  const h = Math.floor(min / 60)
  if (h < 24) return `${h}h ago`
  const d2 = Math.floor(h / 24)
  if (d2 < 30) return `${d2}d ago`
  const m = Math.floor(d2 / 30)
  if (m < 12) return `${m}mo ago`
  return `${Math.floor(m / 12)}y ago`
}

async function deleteResult() {
  if (!store.selectedResult) return
  await store.deleteResult(store.selectedResult.id)
}
</script>

<template>
  <div class="h-full overflow-y-auto p-4" data-digest-content>
    <div v-if="!store.selectedResult" class="flex items-center justify-center h-full text-muted-foreground text-sm">
      Select a digest result to view
    </div>
    <template v-else>
      <div class="flex items-center justify-between mb-4">
        <div>
          <h2 class="text-lg font-semibold">Digest</h2>
          <p class="text-xs text-muted-foreground">
            Generated {{ new Date(store.selectedResult.created_at).toLocaleString() }}
            <span v-if="store.selectedResult.status === 'new'" class="ml-2 inline-flex items-center gap-0.5 text-accent bg-accent/10 rounded px-1.5 py-0.5 text-xs">
              <Star class="size-3" /> New
            </span>
          </p>
        </div>
        <button class="text-xs text-muted-foreground hover:text-red-500 transition-colors" @click="deleteResult" title="Delete digest">
          <Trash2 class="size-3.5" />
        </button>
      </div>

      <!-- Highlights grouped by feed -->
      <div v-if="Object.keys(groupedHighlights).length" class="mb-6">
        <div v-for="(items, feedName) in groupedHighlights" :key="feedName" class="mb-6">
          <h3 class="text-sm font-semibold mb-3 flex items-center gap-1 text-accent">
            <Star class="size-3.5" /> {{ feedName }}
          </h3>
          <div class="space-y-4">
            <div
              v-for="item in items"
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
                <span class="flex items-center gap-1"><Star class="size-3" /> {{ item.points }} points</span>
                <span class="flex items-center gap-1"><Clock class="size-3" /> {{ relativeTime(item.published_at) }}</span>
                <a v-if="item.comment_url" :href="item.comment_url" target="_blank"
                  class="flex items-center gap-1 hover:text-accent">
                  <MessageCircle class="size-3" /> {{ item.comments_count }} comments
                </a>
              </div>
              <p v-if="item.llm_summary" class="text-sm leading-relaxed mb-3">
                {{ item.llm_summary }}
              </p>
              <div v-if="item.top_comments?.length" class="mb-3 border-t pt-2">
                <p class="text-xs font-medium text-muted-foreground mb-1">Top comments</p>
                <div v-for="c in item.top_comments" :key="c.url || c.text.substring(0, 20)"
                  class="text-xs text-muted-foreground/80 mb-1 pl-2 border-l-2 border-border">
                  <span class="font-medium">{{ c.author }}</span>
                  <span class="ml-1 text-accent">[{{ c.points }}]</span>
                  <a v-if="c.url" :href="c.url" target="_blank" class="ml-1 hover:text-accent">{{ c.text }}</a>
                  <span v-else class="ml-1">{{ c.text }}</span>
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
                <span>{{ item.points }} points</span>
                <span>{{ relativeTime(item.published_at) }}</span>
                <a v-if="item.comment_url" :href="item.comment_url" target="_blank" class="hover:text-accent">
                  {{ item.comments_count }} comments
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
