<script setup lang="ts">
import { ref, watch } from 'vue'
import Dialog from './ui/Dialog.vue'
import Button from './ui/Button.vue'
import Input from './ui/Input.vue'
import { authApi } from '../services/api'
import { Check, Copy, RefreshCw, TriangleAlert } from 'lucide-vue-next'

const props = defineProps<{
  open?: boolean
}>()

const emit = defineEmits<{
  (e: 'update:open', v: boolean): void
}>()

const apiKey = ref('')
const loading = ref(false)
const rotating = ref(false)
const confirming = ref(false)
const error = ref('')
const success = ref('')
const copied = ref(false)

let copyTimer: ReturnType<typeof setTimeout> | null = null

// Fetch the current key every time the dialog opens so the displayed value is
// never stale (e.g. after a rotation in another tab).
watch(
  () => props.open,
  (open) => {
    if (open) loadKey()
  },
  { immediate: true },
)

async function loadKey() {
  loading.value = true
  error.value = ''
  success.value = ''
  confirming.value = false
  copied.value = false
  try {
    const res = await authApi.getApiKey()
    apiKey.value = res.api_key
  } catch (e) {
    error.value = e instanceof Error ? e.message : 'Failed to load API key'
  } finally {
    loading.value = false
  }
}

function requestRotate() {
  confirming.value = true
}

function cancelRotate() {
  confirming.value = false
}

async function confirmRotate() {
  rotating.value = true
  error.value = ''
  success.value = ''
  try {
    const res = await authApi.rotateApiKey()
    apiKey.value = res.api_key
    confirming.value = false
    success.value = 'API key rotated. The previous key no longer works — update your Google Reader clients.'
  } catch (e) {
    error.value = e instanceof Error ? e.message : 'Failed to rotate API key'
  } finally {
    rotating.value = false
  }
}

async function copyKey() {
  if (!apiKey.value || !navigator.clipboard) return
  try {
    await navigator.clipboard.writeText(apiKey.value)
    copied.value = true
    if (copyTimer) clearTimeout(copyTimer)
    copyTimer = setTimeout(() => (copied.value = false), 2000)
  } catch {
    // Clipboard unavailable; the field is still selectable for manual copy.
  }
}
</script>

<template>
  <Dialog
    :open="props.open"
    @update:open="emit('update:open', $event)"
    title="Google Reader API"
    description="This key authenticates Google Reader clients (Read You, FeedMe, Reeder, …) against your account."
  >
    <div class="flex flex-col gap-4" data-reader-api-dialog>
      <div v-if="loading" class="text-sm text-muted-foreground">Loading API key…</div>
      <template v-else>
        <div class="flex flex-col gap-1.5">
          <label for="reader-api-key" class="text-xs font-medium text-muted-foreground">API key</label>
          <div class="flex gap-2">
            <Input
              id="reader-api-key"
              :model-value="apiKey"
              readonly
              class="font-mono text-xs"
              aria-label="Google Reader API key"
            />
            <Button variant="outline" size="icon" title="Copy API key" :disabled="!apiKey" @click="copyKey">
              <Check v-if="copied" class="size-4" />
              <Copy v-else class="size-4" />
            </Button>
          </div>
          <p class="text-xs text-muted-foreground">
            Point your client at
            <span class="font-mono">https://&lt;host&gt;/reader/</span> and authenticate with
            <span class="font-mono">Authorization: GoogleLogin auth=&lt;key&gt;</span>.
          </p>
        </div>

        <div class="border-t border-border pt-4">
          <template v-if="confirming">
            <div class="flex items-start gap-2 rounded-md border border-amber-500/30 bg-amber-500/10 p-3">
              <TriangleAlert class="mt-0.5 size-4 shrink-0 text-amber-600 dark:text-amber-400" />
              <div class="text-sm">
                <p class="font-medium">Rotate API key?</p>
                <p class="text-muted-foreground">
                  The current key stops working immediately. Any Google Reader client using it must be updated.
                </p>
              </div>
            </div>
            <div class="mt-3 flex justify-end gap-2">
              <Button variant="ghost" :disabled="rotating" @click="cancelRotate">Cancel</Button>
              <Button variant="destructive" :disabled="rotating" @click="confirmRotate">
                <RefreshCw class="size-4" :class="rotating ? 'animate-spin' : ''" />
                {{ rotating ? 'Rotating…' : 'Confirm rotation' }}
              </Button>
            </div>
          </template>
          <template v-else>
            <Button variant="destructive" :disabled="rotating" @click="requestRotate">
              <RefreshCw class="size-4" />
              Rotate API key
            </Button>
          </template>
        </div>

        <p v-if="success" class="text-sm text-green-600 dark:text-green-400" data-feedback="success">{{ success }}</p>
        <p v-else-if="error" class="text-sm text-red-500" data-feedback="error">{{ error }}</p>
      </template>
    </div>
  </Dialog>
</template>
