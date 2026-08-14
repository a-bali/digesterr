<script setup lang="ts">
import { reactive, ref, computed, watch } from 'vue'
import { toast } from 'vue-sonner'
import Dialog from './ui/Dialog.vue'
import Input from './ui/Input.vue'
import Button from './ui/Button.vue'
import { authApi } from '../services/api'

const props = withDefaults(defineProps<{ open?: boolean }>(), { open: false })

const emit = defineEmits<{
  (e: 'update:open', v: boolean): void
}>()

const form = reactive({ current: '', next: '', confirm: '' })
const submitting = ref(false)
const error = ref('')

// Fresh form on every open (the dialog can stay mounted between opens).
watch(
  () => props.open,
  (open) => {
    if (open) {
      form.current = ''
      form.next = ''
      form.confirm = ''
      error.value = ''
      submitting.value = false
    }
  },
  { immediate: true },
)

const allFilled = computed(() => form.current !== '' && form.next !== '' && form.confirm !== '')
const passwordsMatch = computed(() => form.next === form.confirm)
const formValid = computed(() => allFilled.value && passwordsMatch.value)

async function submit() {
  if (!formValid.value || submitting.value) return
  submitting.value = true
  error.value = ''
  try {
    await authApi.changePassword(form.current, form.next)
    toast.success('Password changed')
    emit('update:open', false)
  } catch (e) {
    error.value = e instanceof Error ? e.message : 'Failed to change password'
  } finally {
    submitting.value = false
  }
}
</script>

<template>
  <Dialog
    :open="props.open"
    title="Change password"
    description="Enter your current password and choose a new one."
    @update:open="emit('update:open', $event)"
  >
    <form @submit.prevent="submit" class="space-y-4" data-change-password-dialog>
      <div class="space-y-1">
        <label for="current-password" class="text-sm font-medium">Current password</label>
        <Input
          id="current-password"
          v-model="form.current"
          type="password"
          autocomplete="current-password"
          placeholder="Current password"
          required
        />
      </div>
      <div class="space-y-1">
        <label for="new-password" class="text-sm font-medium">New password</label>
        <Input
          id="new-password"
          v-model="form.next"
          type="password"
          autocomplete="new-password"
          placeholder="New password"
          required
        />
      </div>
      <div class="space-y-1">
        <label for="confirm-password" class="text-sm font-medium">Confirm new password</label>
        <Input
          id="confirm-password"
          v-model="form.confirm"
          type="password"
          autocomplete="new-password"
          placeholder="Confirm new password"
          required
        />
        <p v-if="form.confirm !== '' && !passwordsMatch" class="text-xs text-red-500">Passwords do not match.</p>
      </div>

      <p v-if="error" class="text-sm text-red-500" data-change-password-error>{{ error }}</p>

      <div class="flex justify-end gap-2">
        <Button type="submit" :disabled="submitting || !formValid">
          {{ submitting ? 'Changing…' : 'Change password' }}
        </Button>
      </div>
    </form>
  </Dialog>
</template>
