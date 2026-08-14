<script setup lang="ts">
import { computed } from 'vue'
import { RouterLink } from 'vue-router'
import { useDigestStore } from '../stores/digest'
import { useFeedStore } from '../stores/feed'
import type { DigestContent, DigestContentItem, DigestSection } from '../types/api'
import Button from './ui/Button.vue'
import { ExternalLink, Clock, MessageCircle, Trash2, Check, ArrowUp, BookOpen } from 'lucide-vue-next'
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

// Prefer the explicit sections emitted by digest generation (category sections
// nest feed sections). Older results without sections fall back to grouping
// the flat highlights/items per feed, preserving the historical layout.
const displaySections = computed<DigestSection[]>(() => {
  const d = digestContent.value
  if (d?.sections?.length) return d.sections
  const byName = new Map<string, DigestSection>()
  const getSection = (item: DigestContentItem): DigestSection => {
    const key = item.feed_name || 'Unknown'
    let section = byName.get(key)
    if (!section) {
      section = { name: key, kind: 'feed', feed_id: item.feed_id, highlights: [], items: [] }
      byName.set(key, section)
    }
    return section
  }
  for (const item of d?.highlights || []) getSection(item).highlights!.push(item)
  for (const item of d?.items || []) getSection(item).items!.push(item)
  return [...byName.values()]
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

// Feed sections are rendered for top-level feed sections and for the nested
// feed sections inside category sections.
function feedSectionsOf(block: DigestSection): DigestSection[] {
  return block.kind === 'category' ? (block.sections || []) : [block]
}
</script>

<template>
  <div class="h-full overflow-y-auto p-6" data-digest-content>
    <div v-if="!store.selectedResult" class="flex h-full items-center justify-center text-muted-foreground">
      <BookOpen class="size-8 mr-2" /> <span>Select an item</span>
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

      <!-- Sections: category sections wrap feed subsections; feed sections
           render highlights first, then that feed's remaining items. -->
      <div v-if="displaySections.length" class="mb-6">
        <div
          v-for="block in displaySections"
          :key="block.kind + ':' + block.name"
          class="mb-6"
          :data-category-section="block.kind === 'category' ? '' : undefined"
        >
          <h3 v-if="block.kind === 'category'" class="text-sm font-semibold mb-3 uppercase tracking-wider text-foreground/70">
            {{ block.name }}
          </h3>

          <div
            v-for="section in feedSectionsOf(block)"
            :key="section.name"
            class="mb-6"
            data-feed-section
          >
            <h3 class="text-sm font-semibold mb-3 flex items-center gap-1 text-accent">
              <img v-if="iconForFeedId(section.feed_id || 0)" :src="iconForFeedId(section.feed_id || 0)" class="size-3.5 shrink-0 rounded" @error="($event.target as HTMLImageElement).style.display = 'none'" />
              <RouterLink :to="`/feeds/${section.feed_id}`" class="hover:underline">{{ section.name }}</RouterLink>
            </h3>

            <!-- Highlights for this feed -->
            <div v-if="section.highlights?.length" class="space-y-4">
              <div
                v-for="item in section.highlights"
                :key="item.item_id"
                class="p-4 rounded-lg border bg-card"
              >
                <div class="flex items-start gap-2 mb-2">
                  <a :href="item.url" target="_blank" class="text-sm font-medium hover:text-accent flex-1">
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
                    <span class="font-medium text-muted-foreground">{{ c.author }}</span>
                    <span v-if="c.points != null" class="ml-1 text-accent text-[10px]">[{{ c.points }}]</span>
                    <span class="mx-1 text-muted-foreground/40">–</span>
                    <a v-if="c.url" :href="c.url" target="_blank" class="text-foreground/80 hover:text-accent">{{ c.text }}</a>
                    <span v-else class="text-foreground/80">{{ c.text }}</span>
                  </div>
                </div>
              </div>
            </div>

            <!-- All items for this feed -->
            <div v-if="section.items?.length" class="mt-4">
              <p class="text-xs font-medium text-muted-foreground mb-2">All items</p>
              <div class="space-y-2">
                <a
                  v-for="item in section.items"
                  :key="item.item_id"
                  :href="item.url"
                  target="_blank"
                  class="group flex items-center justify-between p-2 rounded-md hover:bg-muted transition-colors border"
                >
                  <div class="flex-1 min-w-0">
                    <span class="text-sm font-medium truncate block group-hover:text-accent transition-colors">{{ item.title }}</span>
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
          </div>
        </div>
      </div>

      <div v-if="!digestContent?.highlights?.length && !digestContent?.items?.length" class="text-sm text-muted-foreground">
        No content available for this digest.
      </div>
    </template>
  </div>
</template>
