<script setup lang="ts">
import { reactive, computed, ref, watch } from 'vue'
import { useFeedStore } from '../stores/feed'
import Dialog from './ui/Dialog.vue'
import Input from './ui/Input.vue'
import Select from './ui/Select.vue'
import Button from './ui/Button.vue'
import { SCORE_FEED_TYPES, feedUrlFor, isValidEmailSuffix, emailAddressFor, emailPrefixOf, emailDomainOf, emailSuffixOf, emailNameFor } from '../lib/feeds'
import { feedApi } from '../services/api'
import type { Feed } from '../types/api'

const props = defineProps<{ feed: Feed | null }>()
const emit = defineEmits<{
  (e: 'update:open', open: boolean): void
  (e: 'save', data: { type: string; name: string; url: string; category: string; extract_content: number; min_score: number }): void
  (e: 'delete', id: number): void
}>()

const feedStore = useFeedStore()

// Feeds whose items carry a score (points) that can be filtered on.
const isScoreType = computed(() => SCORE_FEED_TYPES.has(form.type))

// Deleting a feed removes all its items — confirm first (same pattern as
// mark-all-read in FeedSidebar).
const confirmDelete = ref(false)

// Category suggestions come from the categories currently in use.
const categoryNames = computed(() => {
  const names = new Set<string>()
  for (const f of feedStore.feeds) {
    if (f.category) names.add(f.category)
  }
  return [...names].sort((a, b) => a.localeCompare(b))
})

const form = reactive({ type: 'rss', name: '', url: '', category: '', extract_content: 0, min_score: 0 })
const typeLabel = (t: string) => ({ rss: 'RSS / Atom', hn: 'Hacker News', lobsters: 'Lobste.rs', reddit: 'Reddit', twitter: 'Twitter / X', email: 'Email newsletter' }[t] || t)

// Content extraction fetches the article behind item.url with readability;
// x.com serves a login-wall to crawlers, so it can't work for tweets —
// tweets already carry their full content from the API. A newsletter's first
// link is not "the article" either, so extraction would replace a good body
// with unrelated page chrome.
const extractionSupported = computed(() => form.type !== 'twitter' && form.type !== 'email')

// ---- Email feeds ----------------------------------------------------------
//
// One AgentMail inbox serves the whole instance, so the address is
// <inbox-local-part>+<suffix>@<domain>: the prefix and domain are fixed server
// state and only the suffix is the user's. Showing them as non-editable context
// (below) is what makes that obvious, and composing the full address in one
// helper keeps the dialog, the sidebar and the tests in agreement.
const emailInbox = computed(() => feedStore.emailInbox ?? '')
const emailAvailable = computed(() => emailInbox.value !== '')
const emailPrefix = computed(() => emailPrefixOf(emailInbox.value))
const emailDomain = computed(() => emailDomainOf(emailInbox.value))

// The full address for the current suffix — what the user must hand to the
// newsletter publisher. This is the single most important string in the form:
// AgentMail has no catch-all, so a wrong address receives nothing at all.
const resolvedEmailAddress = computed(() => {
  const suffix = form.url.trim()
  if (!emailAvailable.value || !isValidEmailSuffix(suffix)) return ''
  return emailAddressFor(emailInbox.value, suffix)
})

// Suffix validity is checked here so the user finds out before a round trip.
// Empty is not shown as an error while the field is untouched.
const emailSuffixError = computed(() => {
  const suffix = form.url.trim()
  if (suffix === '') return ''
  return isValidEmailSuffix(suffix)
    ? ''
    : 'Use letters, digits, dots, dashes or underscores (no spaces).'
})

// True while the name in the field came from the lookup rather than the user.
// It lets a new URL replace an auto-filled name, while a typed name is never
// overwritten.
const nameFromLookup = ref(false)

function onTypeChange(type: string) {
  form.type = type
  form.url = ''
  // A name that came from the lookup belongs to the old type's URL, so it is
  // dropped with it; a name the user typed is left alone.
  if (nameFromLookup.value) {
    form.name = ''
    nameFromLookup.value = false
  }
  // The minimum-score field is disabled for non-score feed types, so the
  // value is reset when switching away from a score-capable type.
  if (!SCORE_FEED_TYPES.has(type)) form.min_score = 0
  // Extraction is unsupported for twitter; never leave a stale flag on.
  if (!extractionSupported.value) form.extract_content = 0
  // Fixed URLs for the built-in score feed types (shared with FeedSidebar).
  if (type === 'hn' || type === 'lobsters') {
    form.url = feedUrlFor(type, '')
    // The URL is known the moment the type is picked, so there is no field to
    // leave — look the name up right away.
    void prefillName()
  }
}

// prefillName asks the backend for the name the feed declares for itself and
// fills the name field with it — but only when the field is still empty (or
// holds a previous lookup result). Any failure is silent: the user types the
// name themselves, which is the pre-existing behaviour.
async function prefillName() {
  if (props.feed) return // editing keeps the existing name
  if (form.name.trim() !== '' && !nameFromLookup.value) return
  const type = form.type
  const raw = form.url.trim()
  if (!raw) return
  // An email feed's name comes from its own suffix, with no lookup: the server
  // has no endpoint that could name an address, and a round trip that could
  // time out would look like a broken form.
  if (type === 'email') {
    if (!emailAvailable.value || !isValidEmailSuffix(raw)) return
    form.name = emailNameFor(raw)
    nameFromLookup.value = true
    return
  }
  const url = feedUrlFor(type, raw)
  if (!url) return
  try {
    const { name } = await feedApi.discover(type, url)
    // Drop a stale answer: the user may have changed the type or URL while the
    // lookup was in flight, or started typing a name.
    if (!name) return
    if (form.type !== type || form.url.trim() !== raw) return
    if (form.name.trim() !== '' && !nameFromLookup.value) return
    form.name = name
    nameFromLookup.value = true
  } catch {
    /* lookup failed — leave the name for the user to type */
  }
}

watch(
  () => props.feed,
  () => {
    confirmDelete.value = false
    nameFromLookup.value = false
    if (props.feed) {
      const feed = props.feed
      form.type = feed.type
      form.name = feed.name
      form.url = feed.type === 'reddit'
        ? feed.url.replace('https://www.reddit.com/r/', '').replace('.rss', '')
        : feed.type === 'twitter'
          ? feed.url.replace('https://x.com/', '').replace('https://twitter.com/', '').replace(/^@/, '')
          : feed.type === 'email'
            // Only the suffix is editable, so an edit must prefill just that —
            // round-tripping the full address through the field would let a
            // stray keystroke corrupt it.
            ? emailSuffixOf(feed.url)
            : feed.url
      form.category = feed.category || ''
      form.extract_content = extractionSupported.value ? feed.extract_content : 0
      form.min_score = feed.min_score
    } else {
      form.type = 'rss'
      form.name = ''
      form.url = ''
      form.category = ''
      form.extract_content = 0
      form.min_score = 0
    }
  },
  { immediate: true },
)

// The email option needs the instance's inbox, which is only fetched when a
// dialog first needs it (the store caches it for the session).
watch(
  () => form.type,
  (t) => { if (t === 'email') void feedStore.ensureEmailConfig() },
  { immediate: true },
)

function submit() {
  emit('save', { ...form })
}

const dialogId = Math.random().toString(36).slice(2)
</script>

<template>
  <Dialog :open="true" :title="feed ? 'Edit Feed' : 'Add Feed'" @update:open="emit('update:open', $event)">
    <form @submit.prevent="submit" class="space-y-4">
      <div class="space-y-1">
        <label class="text-sm font-medium">Category</label>
        <Input
          v-model="form.category"
          :list="`${dialogId}-cats`"
          placeholder="Select existing or type new name…"
        />
        <datalist :id="`${dialogId}-cats`">
          <option v-for="name in categoryNames" :key="name" :value="name" />
        </datalist>
      </div>
      <div class="space-y-1">
        <label class="text-sm font-medium">Type</label>
        <Select v-model="form.type" @change="onTypeChange(($event.target as HTMLSelectElement).value)">
          <option v-for="t in ['rss','hn','lobsters','reddit','twitter','email']" :key="t" :value="t" :disabled="t === 'email' && !emailAvailable">{{ typeLabel(t) }}{{ t === 'email' && !emailAvailable ? ' (not configured)' : '' }}</option>
        </Select>
      </div>
      <div class="space-y-1">
        <label class="text-sm font-medium">Name</label>
        <Input v-model="form.name" placeholder="Feed name" required @input="nameFromLookup = false" />
      </div>
      <div v-if="form.type === 'hn' || form.type === 'lobsters'" class="space-y-1">
        <label class="text-sm font-medium">URL</label>
        <p class="text-sm text-muted-foreground bg-muted rounded px-2 py-1.5">{{ form.url }}</p>
      </div>
      <div v-else-if="form.type === 'reddit'" class="space-y-1">
        <label class="text-sm font-medium">URL</label>
        <div class="flex items-center gap-0">
          <span class="text-xs text-muted-foreground bg-muted rounded-l-md px-2 py-1.5 border border-border border-r-0 whitespace-nowrap">https://www.reddit.com/r/</span>
          <Input v-model="form.url" placeholder="subreddit" required @blur="prefillName" />
        </div>
      </div>
      <div v-else-if="form.type === 'twitter'" class="space-y-1">
        <label class="text-sm font-medium">URL</label>
        <div class="flex items-center gap-0">
          <span class="text-xs text-muted-foreground bg-muted rounded-l-md px-2 py-1.5 border border-border border-r-0 whitespace-nowrap">https://x.com/</span>
          <Input v-model="form.url" placeholder="username" required class="rounded-none" @blur="prefillName" />
        </div>
      </div>
      <!--
        Email feeds: one AgentMail inbox serves the whole instance, so the
        address is <prefix>+<suffix>@<domain>. The prefix and domain are
        server-side state shown as fixed context; only the suffix is editable.
        The resolved full address is displayed because that exact string is what
        the user has to give the newsletter publisher — AgentMail has no
        catch-all, so a wrong suffix silently receives nothing at all.
      -->
      <div v-else-if="form.type === 'email'" class="space-y-1" data-email-fields>
        <label class="text-sm font-medium">Subscription address</label>
        <div class="flex items-center gap-0">
          <span class="text-xs text-muted-foreground bg-muted rounded-l-md px-2 py-1.5 border border-border border-r-0 whitespace-nowrap" data-email-prefix>{{ emailPrefix }}</span>
          <Input
            v-model="form.url"
            placeholder="sport"
            required
            class="rounded-none"
            data-email-suffix
            @blur="prefillName"
          />
          <span class="text-xs text-muted-foreground bg-muted rounded-r-md px-2 py-1.5 border border-border border-l-0 whitespace-nowrap" data-email-domain>{{ emailDomain }}</span>
        </div>
        <p v-if="emailSuffixError" class="text-xs text-destructive">{{ emailSuffixError }}</p>
        <template v-else-if="resolvedEmailAddress">
          <p class="text-xs text-muted-foreground">Give this address to the newsletter publisher:</p>
          <p class="text-xs font-mono bg-muted rounded px-2 py-1 select-all break-all" data-email-resolved>{{ resolvedEmailAddress }}</p>
        </template>
        <p v-else class="text-xs text-muted-foreground">
          One mailbox serves this server, so only the part after “+” is yours to choose.
        </p>
      </div>
      <div v-else class="space-y-1">
        <label class="text-sm font-medium">URL</label>
        <Input v-model="form.url" placeholder="Feed URL" required @blur="prefillName" />
      </div>
      <label class="flex items-center gap-2 text-sm" :class="extractionSupported ? 'cursor-pointer' : 'cursor-not-allowed opacity-50'">
        <input
          type="checkbox"
          v-model.number="form.extract_content"
          :true-value="1"
          :false-value="0"
          :disabled="!extractionSupported"
          class="toggle"
        />
        Auto-extract full article content
      </label>
      <p v-if="form.type === 'twitter'" class="text-xs text-muted-foreground -mt-2">
        Not available for Twitter/X feeds — tweets already carry their full content.
      </p>
      <div class="space-y-1">
        <label class="text-sm font-medium" :class="{ 'text-muted-foreground': !isScoreType }">Minimum score</label>
        <Input
          v-model.number="form.min_score"
          type="number"
          min="0"
          :disabled="!isScoreType"
          :class="{ 'opacity-50': !isScoreType }"
        />
        <p v-if="isScoreType" class="text-xs text-muted-foreground">
          Only items with at least this many points are kept. Set 0 to keep all items.
        </p>
        <p v-else class="text-xs text-muted-foreground">
          Scores are only available for Hacker News, Lobste.rs and Reddit feeds.
        </p>
      </div>
      <div class="flex gap-2">
        <Button type="submit">{{ feed ? 'Save' : 'Add' }}</Button>
        <Button v-if="feed" variant="outline" class="text-destructive" data-delete-feed @click="confirmDelete = true">Delete</Button>
      </div>
    </form>
    <Dialog
      v-if="confirmDelete"
      :open="true"
      title="Delete feed?"
      :description="`Delete feed “${feed?.name}”? All of its items will be removed. This cannot be undone.`"
      @update:open="v => { if (!v) confirmDelete = false }"
    >
      <div class="flex justify-end gap-2">
        <Button variant="outline" @click="confirmDelete = false">Cancel</Button>
        <Button variant="outline" class="text-destructive" data-delete-feed-confirm @click="emit('delete', feed!.id)">Delete</Button>
      </div>
    </Dialog>
  </Dialog>
</template>
