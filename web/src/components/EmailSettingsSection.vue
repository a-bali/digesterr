<script setup lang="ts">
// The Email section of the Settings dialog: the account's AgentMail inbox and
// API key, which is what the email feed type needs.
//
// No Save button of its own — the dialog has a single footer Save that calls
// save() on each section. The section reports (via `dirty`) whether it has
// something to save so the footer can enable itself, and save() returns whether
// the write succeeded so the dialog knows whether to close.
//
// A stored API key is never sent to the browser, so the field shows a FIXED MASK
// rather than the real secret:
//
//   mask untouched      -> keep the stored key
//   field emptied       -> clear the stored key (the inbox stays)
//   anything typed      -> replace the stored key
//
// The mask is the field's VALUE, not a placeholder, so edits must never append to
// it — "•••• plus my characters" would be stored as the new key. Rather than
// intercepting keystrokes (which misses IME, autofill, password managers and
// paste, all of which insert text with no keydown), any new value derived from
// the mask is normalised in `keepMaskMeaningful`: text appended to the mask keeps
// only the appended part, and a chopped-down mask clears the key.
import { computed, ref, watch } from 'vue'
import Input from './ui/Input.vue'
import { settingsApi } from '../services/api'
import { useFeedStore } from '../stores/feed'

// MASK marks the field as "a key is stored". A constant, not a length-revealing
// one: the real length is never sent to the browser, so any length would be a
// fiction and the honest mask is a fixed one.
const MASK_CHAR = '•'
const MASK = MASK_CHAR.repeat(16)

const props = defineProps<{ open?: boolean }>()

const emit = defineEmits<{
  (e: 'dirty', v: boolean): void
  // Enter in either field asks the dialog to run the footer Save, so the section
  // needs no Save button to make Enter work.
  (e: 'submit'): void
}>()

const feedStore = useFeedStore()

const inbox = ref('')
// The values as the server has them, so "unchanged" can be recognised as
// nothing-to-save rather than written back as a redundant request.
const savedInbox = ref('')
const apiKey = ref('')
const tokenSet = ref(false)
const loading = ref(false)
const saving = ref(false)
const error = ref('')

// True while the field still shows the mask the user has not edited.
const maskUntouched = ref(false)

// Reload on every open: the value is per-account state that another tab can
// change, and the dialog stays mounted between opens. This is also what makes
// Cancel a plain close — nothing is written until Save, so there is nothing to
// roll back.
watch(
  () => props.open,
  (open) => {
    if (open) load()
  },
  { immediate: true },
)

// showMask restores the resting state: mask when a key is stored, empty otherwise.
function showMask() {
  maskUntouched.value = tokenSet.value
  apiKey.value = tokenSet.value ? MASK : ''
}

async function load() {
  loading.value = true
  error.value = ''
  try {
    const res = await settingsApi.getEmail()
    inbox.value = res.inbox
    savedInbox.value = res.inbox
    tokenSet.value = res.token_set
    showMask()
  } catch (e) {
    error.value = e instanceof Error ? e.message : 'Failed to load email settings'
  } finally {
    loading.value = false
  }
}

// keepMaskMeaningful turns any value derived from the mask into a real key
// intention. Fires for EVERY way the value can change (typing, IME, autofill,
// paste, programmatic), which keydown/paste handlers cannot cover.
//
// All bullets are stripped rather than a prefix/suffix: the browser inserts at the
// CARET, so a user who clicks at the start of the field produces "a••••••••••",
// which a prefix check would miss and then store as the key. Nothing legitimate in
// an API key is a bullet, so removing every one is safe and position-independent.
watch(apiKey, (v) => {
  if (!maskUntouched.value || v === MASK) return
  maskUntouched.value = false
  const stripped = v.replaceAll(MASK_CHAR, '')
  // '' is meaningful here: deleting bullets is how the user says "remove the key".
  if (stripped !== v) apiKey.value = stripped
})

const trimmedInbox = computed(() => inbox.value.trim())
const trimmedKey = computed(() => apiKey.value.trim())

// The untouched mask is the one value that means "keep the stored key".
const keyUnchanged = computed(() => maskUntouched.value && apiKey.value === MASK)

const hasInbox = computed(() => trimmedInbox.value !== '')
// The untouched mask counts as a key, so an inbox-only edit is still saveable.
const hasKey = computed(() => trimmedKey.value !== '' || tokenSet.value)
const changed = computed(() => trimmedInbox.value !== savedInbox.value || !keyUnchanged.value)

const hasConfig = computed(() => savedInbox.value !== '' || tokenSet.value)

// Emptying the key field removes the stored key but keeps the inbox.
const clearingKey = computed(() => tokenSet.value && trimmedKey.value === '')

// Both fields blanked = remove the configuration entirely. Requires deliberately
// clearing the inbox too, so a stray empty field cannot delete credentials.
// `hasConfig` guards the unconfigured case: on a fresh account both fields are
// already blank, and offering to remove nothing would be a false alarm.
const removingAll = computed(
  () =>
    hasConfig.value && trimmedInbox.value === '' && apiKey.value !== MASK && trimmedKey.value === '',
)

const canSave = computed(() => {
  if (saving.value || loading.value) return false
  if (removingAll.value) return hasConfig.value
  return hasInbox.value && hasKey.value && changed.value
})

// The footer Save is enabled by ANY section with changes, so the section has to
// report its own state rather than the dialog reaching into it.
watch(canSave, (v) => emit('dirty', v), { immediate: true })

// apiKeyArg maps the field onto the request: undefined keeps the stored token,
// '' clears it, anything else replaces it.
function apiKeyArg(): string | undefined {
  if (keyUnchanged.value) return undefined
  return trimmedKey.value
}

// save returns whether the settings were stored, so the dialog can keep itself
// open on a failure (a rejected key must not also discard the LLM model edit).
async function save(): Promise<boolean> {
  if (!canSave.value) return true
  saving.value = true
  error.value = ''
  try {
    const res = removingAll.value
      ? await settingsApi.resetEmail()
      : await settingsApi.updateEmail(trimmedInbox.value, apiKeyArg())

    inbox.value = res.inbox
    savedInbox.value = res.inbox
    tokenSet.value = res.token_set
    showMask()
    // The Add Feed dialog reads the inbox from the store, which caches it for the
    // session. Without this the email option stays "(not configured)" until a
    // reload, even though the account is now configured.
    await feedStore.refreshEmailConfig()
    return true
  } catch (e) {
    error.value = e instanceof Error ? e.message : 'Failed to save email settings'
    return false
  } finally {
    saving.value = false
  }
}

defineExpose({ save, canSave })
</script>

<template>
  <div class="flex flex-col gap-4 border-t border-border pt-4" data-email-settings-section>
    <div class="flex flex-col gap-1">
      <h3 class="text-sm font-medium">Email newsletter feeds</h3>
      <p class="text-xs text-muted-foreground">
        Agentmail inbox name and API key for receiving emails for email-type feeds.
      </p>
    </div>

    <div class="flex flex-col gap-1.5">
      <label for="email-inbox" class="text-sm font-medium">AgentMail inbox</label>
      <Input
        id="email-inbox"
        v-model="inbox"
        placeholder="name@agentmail.to"
        autocomplete="off"
        spellcheck="false"
        class="font-mono text-xs"
        aria-label="AgentMail inbox"
        data-email-settings-inbox
        @keyup.enter="emit('submit')"
      />
    </div>

    <div class="flex flex-col gap-1.5">
      <label for="email-key" class="text-sm font-medium">AgentMail API key</label>
      <Input
        id="email-key"
        v-model="apiKey"
        type="password"
        autocomplete="new-password"
        spellcheck="false"
        class="font-mono text-xs"
        placeholder="AgentMail API key"
        aria-label="AgentMail API key"
        data-email-settings-key
        @keyup.enter="emit('submit')"
      />
    </div>

    <!-- Shown only while a removal is pending: clearing a password field is easy
         to do by accident, and this is the only destructive outcome in the dialog. -->
    <p v-if="removingAll" class="text-xs text-amber-600 dark:text-amber-400" data-email-settings-removing>
      Saving now removes your AgentMail credentials. Email feeds stop receiving mail.
    </p>
    <p v-else-if="clearingKey" class="text-xs text-amber-600 dark:text-amber-400" data-email-settings-clearing>
      Saving now removes the stored key. Email feeds stop receiving mail.
    </p>

    <p v-if="error" class="text-sm text-red-500" data-email-settings-error>{{ error }}</p>
  </div>
</template>
