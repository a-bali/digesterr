<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { toast } from 'vue-sonner'
import Dialog from './ui/Dialog.vue'
import Input from './ui/Input.vue'
import Button from './ui/Button.vue'
import { settingsApi } from '../services/api'
import { RotateCcw } from 'lucide-vue-next'

const props = withDefaults(defineProps<{ open?: boolean }>(), { open: false })

const emit = defineEmits<{
  (e: 'update:open', v: boolean): void
}>()

const model = ref('')
// defaultModel is the env-configured model: it prefills nothing (the field
// shows the effective setting) but it is what Reset falls back to, so it has to
// survive editing the field.
const defaultModel = ref('')
const overridden = ref(false)
const loading = ref(false)
const saving = ref(false)
const resetting = ref(false)
const error = ref('')

// Reload on every open: the value can change in another tab, and the dialog
// stays mounted between opens.
watch(
  () => props.open,
  (open) => {
    if (open) load()
  },
  { immediate: true },
)

async function load() {
  loading.value = true
  error.value = ''
  try {
    const res = await settingsApi.getLLM()
    model.value = res.model
    defaultModel.value = res.default_model
    overridden.value = res.overridden
  } catch (e) {
    error.value = e instanceof Error ? e.message : 'Failed to load LLM settings'
  } finally {
    loading.value = false
  }
}

const trimmed = computed(() => model.value.trim())
// Saving the env value explicitly is a no-op the backend treats as "use the
// default", so disable Save in that case rather than writing a redundant
// override that then shows as "custom".
const canSave = computed(() => !saving.value && !loading.value && trimmed.value !== '' && trimmed.value !== defaultModel.value)
const canReset = computed(() => !resetting.value && !loading.value && overridden.value)

async function save() {
  if (!canSave.value) return
  saving.value = true
  error.value = ''
  try {
    const res = await settingsApi.updateLLM(trimmed.value)
    model.value = res.model
    defaultModel.value = res.default_model
    overridden.value = res.overridden
    toast.success('LLM model saved')
    emit('update:open', false)
  } catch (e) {
    error.value = e instanceof Error ? e.message : 'Failed to save LLM model'
  } finally {
    saving.value = false
  }
}

async function reset() {
  if (!canReset.value) return
  resetting.value = true
  error.value = ''
  try {
    const res = await settingsApi.resetLLM()
    model.value = res.model
    defaultModel.value = res.default_model
    overridden.value = res.overridden
    toast.success('Reverted to the configured default model')
  } catch (e) {
    error.value = e instanceof Error ? e.message : 'Failed to reset LLM model'
  } finally {
    resetting.value = false
  }
}
</script>

<template>
  <Dialog
    :open="props.open"
    title="Settings"
    description="Model used for digest generation and article summaries. The default comes from the server configuration; an override applies to your account only."
    @update:open="emit('update:open', $event)"
  >
    <div class="flex flex-col gap-4" data-llm-settings-dialog>
      <div v-if="loading" class="text-sm text-muted-foreground">Loading settings…</div>
      <template v-else>
        <div class="flex flex-col gap-1.5">
          <label for="llm-model" class="text-sm font-medium">LLM model</label>
          <div class="flex gap-2">
            <Input
              id="llm-model"
              v-model="model"
              placeholder="Model name"
              autocomplete="off"
              spellcheck="false"
              class="font-mono text-xs"
              aria-label="LLM model"
              @keyup.enter="save"
            />
            <Button
              variant="outline"
              size="icon"
              title="Reset to the configured default model"
              :disabled="!canReset"
              data-llm-settings-reset
              @click="reset"
            >
              <RotateCcw class="size-4" :class="resetting ? 'animate-spin' : ''" />
            </Button>
          </div>
          <p class="text-xs text-muted-foreground">
            Default:
            <span class="font-mono">{{ defaultModel || 'not configured' }}</span>
            <span v-if="overridden" data-llm-settings-override class="ml-1">(overridden for your account)</span>
          </p>
        </div>

        <p v-if="error" class="text-sm text-red-500" data-llm-settings-error>{{ error }}</p>

        <div class="flex justify-end gap-2">
          <Button variant="ghost" :disabled="saving" @click="emit('update:open', false)">Cancel</Button>
          <Button type="button" :disabled="!canSave" data-llm-settings-save @click="save">
            {{ saving ? 'Saving…' : 'Save' }}
          </Button>
        </div>
      </template>
    </div>
  </Dialog>
</template>
