<script setup lang="ts">
import { ref, onMounted, onUnmounted, type Component } from 'vue'
import { ChevronDown } from 'lucide-vue-next'

defineProps<{
  options: { value: string; label: string; icon?: Component }[]
  modelValue: string
}>()

const emit = defineEmits<{
  (e: 'update:modelValue', value: string): void
}>()

const container = ref<HTMLElement | null>(null)
const row = ref<HTMLElement | null>(null)
const useDropdown = ref(false)

let observer: ResizeObserver | null = null

// Collapse to a dropdown when the segmented buttons would overflow the
// container. Measured from actual content (scrollWidth) rather than a
// per-option width guess, so a 3-option and a 5-option filter collapse at
// the same effective density.
function measure() {
  if (!container.value || !row.value) return
  useDropdown.value = row.value.scrollWidth > container.value.clientWidth + 1
}

onMounted(() => {
  if (!container.value) return
  observer = new ResizeObserver(measure)
  observer.observe(container.value)
  measure()
})

onUnmounted(() => observer?.disconnect())

function onSelectChange(e: Event) {
  emit('update:modelValue', (e.target as HTMLSelectElement).value)
}
</script>

<template>
  <div ref="container" class="relative w-full">
    <!-- Segmented buttons: kept in the DOM (visibility-toggled) so measure()
         can still read scrollWidth and re-expand when the pane widens. -->
    <div
      ref="row"
      class="flex items-center gap-1 w-full"
      :class="useDropdown ? 'invisible absolute inset-0' : ''"
      :aria-hidden="useDropdown || undefined"
    >
      <button
        v-for="opt in options" :key="opt.value"
        class="flex flex-1 items-center justify-center gap-1 px-2 py-1 text-xs rounded-md whitespace-nowrap transition-colors"
        :class="modelValue === opt.value ? 'bg-accent text-accent-foreground' : 'hover:bg-muted text-muted-foreground'"
        @click="emit('update:modelValue', opt.value)"
      >
        <component :is="opt.icon" v-if="opt.icon" class="size-3.5 shrink-0" />
        <span>{{ opt.label }}</span>
      </button>
    </div>

    <div v-if="useDropdown" class="relative">
      <select
        :value="modelValue"
        @change="onSelectChange"
        class="h-8 w-full appearance-none rounded-md border border-border bg-background px-2 pr-7 text-xs font-medium shadow-sm transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
      >
        <option v-for="opt in options" :key="opt.value" :value="opt.value">{{ opt.label }}</option>
      </select>
      <ChevronDown class="pointer-events-none absolute right-2 top-1/2 -translate-y-1/2 size-3.5 text-muted-foreground" />
    </div>
  </div>
</template>
