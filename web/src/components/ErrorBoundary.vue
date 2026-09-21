<script setup lang="ts">
import { onErrorCaptured, ref } from 'vue'
import Button from './ui/Button.vue'

const failed = ref<Error | null>(null)

onErrorCaptured((err) => {
  failed.value = err instanceof Error ? err : new Error(String(err))
  // Stop the error here: without a boundary a render throw whitescreens the
  // whole app. The pane offers a reload instead.
  return false
})

function reload() {
  failed.value = null
  window.location.reload()
}
</script>

<template>
  <div v-if="failed" class="flex h-full min-h-64 flex-col items-center justify-center gap-3 p-6 text-center">
    <p class="text-sm font-medium">Something went wrong rendering this view.</p>
    <p class="max-w-md text-xs text-muted-foreground">{{ failed.message }}</p>
    <Button size="sm" variant="outline" @click="reload">Reload</Button>
  </div>
  <slot v-else />
</template>
