<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { toastSuccess, toastError } from '../lib/toast'
import Dialog from './ui/Dialog.vue'
import Input from './ui/Input.vue'
import Button from './ui/Button.vue'
import DropdownMenu from './ui/DropdownMenu.vue'
import { settingsApi } from '../services/api'
import EmailSettingsSection from './EmailSettingsSection.vue'
// Imported as ResetIcon rather than RotateCcw: leaving the component referenced
// under its own name makes a missing import render as an empty unknown element
// that looks like a blank icon button instead of failing.
import { RotateCcw as ResetIcon, ChevronDown } from 'lucide-vue-next'

const props = withDefaults(defineProps<{ open?: boolean }>(), { open: false })

const emit = defineEmits<{
  (e: 'update:open', v: boolean): void
}>()

// The model shown in the field: this account's override, or the env default when
// they have none.
const model = ref('')
// The effective model as STORED. The field is prefilled with a model, so without
// this the dialog cannot tell "untouched" from "edited back to the stored value"
// and Save would light up the moment an overridden account opened it.
const savedModel = ref('')
// defaultModel is the env-configured model: it is what Reset falls back to, and
// the row the picker marks as the default.
const defaultModel = ref('')
const overridden = ref(false)
// The endpoint's offered models. Empty is normal — the list is advisory and its
// fetch is allowed to fail — which is why the field stays a text input with the
// picker attached rather than a Select.
const models = ref<string[]>([])

const loading = ref(false)
const saving = ref(false)
const error = ref('')

// Whether the USER has typed in the model field. The field is prefilled with the
// current model, so filtering the list on the value alone would immediately
// narrow it to the one model already selected — the opposite of a picker's job.
const modelTouched = ref(false)

// onModelInput marks the field as user-edited so the list starts narrowing.
function onModelInput() {
  modelTouched.value = true
}

const emailSection = ref<InstanceType<typeof EmailSettingsSection> | null>(null)
// Whether the email section holds an unsaved change, reported by the section so
// the footer can enable itself without reaching into the child's state.
const emailDirty = ref(false)

// Reload on every open: the value can change in another tab, and the dialog
// stays mounted between opens.
watch(
  () => props.open,
  (open) => {
    if (open) load()
  },
  { immediate: true },
)

function apply(res: {
  model: string
  default_model: string
  overridden: boolean
  models?: string[]
}) {
  model.value = res.model
  savedModel.value = res.model
  defaultModel.value = res.default_model
  overridden.value = res.overridden
  // A freshly loaded value must not count as a filter: the picker should open
  // showing the whole list.
  modelTouched.value = false
  // Keep the last known list when a response omits it (PUT/DELETE do): the
  // picker must not empty itself just because a save answered.
  if (res.models) models.value = res.models
}

async function load() {
  loading.value = true
  error.value = ''
  try {
    apply(await settingsApi.getLLM())
  } catch (e) {
    error.value = e instanceof Error ? e.message : 'Failed to load LLM settings'
  } finally {
    loading.value = false
  }
}

const trimmed = computed(() => model.value.trim())

// canReset is about whether an override EXISTS, not about the field: it is the
// button that undoes an override.
const canReset = computed(() => !saving.value && !loading.value && overridden.value)

// The model list is advisory, so an empty list leaves a plain text field: there
// would be nothing to pick from, and a dropdown holding one "not configured" row
// is worse than no dropdown.
const available = computed(() => models.value.length > 0)

// Once the user types, the list narrows to matching names — a gateway list can be
// long, and the field stays free-text anyway.
const matches = computed(() => {
  if (!modelTouched.value) return models.value
  const q = trimmed.value.toLowerCase()
  if (!q) return models.value
  return models.value.filter((m) => m.toLowerCase().includes(q))
})

const pickerItems = computed(() =>
  matches.value.map((m) => ({
    label: m,
    checked: m === trimmed.value,
    // The env default is called out in the list so it can be found without
    // reading the Default line below.
    detail: m === defaultModel.value ? 'default' : undefined,
    onClick: () => {
      // A picked model is a deliberate choice, not a filter: clear the touched
      // flag so reopening the picker shows the whole list again.
      model.value = m
      modelTouched.value = false
    },
  })),
)

// llmDirty reports whether the model field differs from what is stored, counting
// an emptied field as a staged reset.
const llmDirty = computed(() => {
  if (trimmed.value === '') return overridden.value
  return trimmed.value !== savedModel.value
})

// The footer Save covers both sections, so it is enabled when either has
// something to save.
const canSave = computed(() => {
  if (saving.value || loading.value) return false
  return llmDirty.value || emailDirty.value
})

// reset is LOCAL: it stages "use the default model again" in the field, and
// nothing is written until the footer Save. It used to DELETE immediately, which
// made this one button save while every other control in the dialog waited — and
// a misplaced click silently changed the setting.
//
// Staging an EMPTY field (not the default model's name) is deliberate: the
// backend stores an empty override as "no override", so the account follows
// future changes to the env default. Writing today's default name as an override
// would shadow it forever.
function reset() {
  if (!canReset.value) return
  model.value = ''
}

// save stores both sections. It does NOT close on failure: a rejected AgentMail
// key must not also discard the model edit sitting unsaved in the field.
async function save() {
  if (!canSave.value) return
  saving.value = true
  error.value = ''
  try {
    if (llmDirty.value) {
      // '' is the staged reset, which the backend reads as "clear the override".
      apply(await settingsApi.updateLLM(trimmed.value))
    }
    // Always called, even when the email section is clean: it decides for itself
    // whether it has anything to write.
    const emailOk = (await emailSection.value?.save()) ?? true
    if (!emailOk) return
    toastSuccess('Settings saved')
    emit('update:open', false)
  } catch (e) {
    // Inline message stays for the form itself; the toast is what the user sees
    // if they have already scrolled away from the field.
    toastError(e, 'Failed to save settings')
    error.value = e instanceof Error ? e.message : 'Failed to save settings'
  } finally {
    saving.value = false
  }
}
</script>

<template>
  <Dialog
    :open="props.open"
    title="Settings"
    @update:open="emit('update:open', $event)"
  >
    <div class="flex flex-col gap-4" data-settings-dialog>
      <!-- The LLM section keeps its original hooks: the same markup and
           attributes, and its tests query data-llm-settings-dialog. -->
      <div class="flex flex-col gap-4" data-llm-settings-dialog>
        <div v-if="loading" class="text-sm text-muted-foreground">Loading settings…</div>
        <template v-else>
          <div class="flex flex-col gap-1.5">
            <div class="flex flex-col gap-1">
              <h3 class="text-sm font-medium">LLM model</h3>
              <p class="text-xs text-muted-foreground">
                Name of the LLM model to be called for summaries.
              </p>
            </div>
            <!-- Text field with the model list attached as a picker: the list is
                 advisory and may be absent, and any name can still be typed. -->
            <div class="flex">
              <Input
                id="llm-model"
                v-model="model"
                placeholder="Model name"
                autocomplete="off"
                spellcheck="false"
                class="rounded-r-none font-mono text-xs"
                aria-label="LLM model"
                @input="onModelInput"
                @keyup.enter="save"
              />
              <DropdownMenu v-if="available" :items="pickerItems" data-llm-model-menu>
                <template #trigger>
                  <button
                    type="button"
                    aria-label="Choose a model"
                    data-llm-model-picker
                    class="inline-flex h-9 shrink-0 items-center border border-l-0 border-border bg-background px-2 text-muted-foreground transition-colors hover:bg-accent hover:text-accent-foreground"
                  >
                    <ChevronDown class="size-4" />
                  </button>
                </template>
              </DropdownMenu>
              <Button
                variant="outline"
                size="icon"
                title="Use the default model again"
                :disabled="!canReset"
                data-llm-settings-reset
                class="ml-2"
                @click="reset"
              >
                <ResetIcon class="size-4" />
              </Button>
            </div>
            <p class="text-xs text-muted-foreground">
              Default:
              <span class="font-mono">{{ defaultModel || 'not configured' }}</span>
              <span v-if="overridden" data-llm-settings-override class="ml-1">(overridden for your account)</span>
            </p>
          </div>

          <p v-if="error" class="text-sm text-red-500" data-llm-settings-error>{{ error }}</p>

          <EmailSettingsSection
            ref="emailSection"
            :open="props.open"
            @dirty="emailDirty = $event"
            @submit="save"
          />
        </template>
      </div>

      <div class="flex justify-end gap-2 border-t border-border pt-4">
        <Button variant="ghost" :disabled="saving" data-settings-cancel @click="emit('update:open', false)">
          Cancel
        </Button>
        <Button type="button" :disabled="!canSave" data-settings-save @click="save">
          {{ saving ? 'Saving…' : 'Save' }}
        </Button>
      </div>
    </div>
  </Dialog>
</template>
