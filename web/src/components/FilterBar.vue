<script setup lang="ts">
import { ref, onMounted, onUnmounted } from 'vue'

const props = defineProps<{
  options: { value: string; label: string }[]
  modelValue: string
}>()

const emit = defineEmits<{
  (e: 'update:modelValue', value: string): void
}>()

const container = ref<HTMLElement | null>(null)
const useDropdown = ref(false)

let observer: ResizeObserver | null = null

onMounted(() => {
  if (!container.value) return
  observer = new ResizeObserver((entries) => {
    const width = entries[0]?.contentRect.width ?? 0
    useDropdown.value = width < props.options.length * 55
  })
  observer.observe(container.value)
})

onUnmounted(() => {
  observer?.disconnect()
})

function onSelectChange(e: Event) {
  emit('update:modelValue', (e.target as HTMLSelectElement).value)
}
</script>

<template>
  <div ref="container">
    <div v-if="!useDropdown" class="flex items-center gap-1 w-full">
      <button
        v-for="opt in options" :key="opt.value"
        class="flex-1 px-2 py-1 text-xs rounded-md transition-colors text-center"
        :class="modelValue === opt.value ? 'bg-accent text-accent-foreground' : 'hover:bg-muted text-muted-foreground'"
        @click="emit('update:modelValue', opt.value)"
      >{{ opt.label }}</button>
    </div>
    <select
      v-else
      :value="modelValue"
      @change="onSelectChange"
      class="text-xs bg-transparent border-0 outline-none w-full"
    >
      <option v-for="opt in options" :key="opt.value" :value="opt.value">{{ opt.label }}</option>
    </select>
  </div>
</template>
