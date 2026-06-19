<script setup lang="ts">
import { ref, computed } from 'vue'
import { X } from 'lucide-vue-next'

const props = withDefaults(defineProps<{
  items: { label: string; value: number }[]
  modelValue: number[]
  placeholder?: string
  allowCreate?: boolean
}>(), { placeholder: 'Search…', allowCreate: false })

const emit = defineEmits<{
  (e: 'update:modelValue', v: number[]): void
  (e: 'create', name: string): void
}>()

const input = ref('')
const focused = ref(false)
const activeIdx = ref(-1)

const filtered = computed(() => {
  const q = input.value.toLowerCase().trim()
  if (!q) return props.items.filter(i => !props.modelValue.includes(i.value))
  return props.items.filter(i =>
    !props.modelValue.includes(i.value) &&
    i.label.toLowerCase().includes(q)
  )
})

function select(val: number) {
  emit('update:modelValue', [...props.modelValue, val])
  input.value = ''
  activeIdx.value = -1
}

function remove(val: number) {
  emit('update:modelValue', props.modelValue.filter(v => v !== val))
}

function onKeyDown(e: KeyboardEvent) {
  if (e.key === 'ArrowDown') {
    e.preventDefault()
    activeIdx.value = Math.min(activeIdx.value + 1, filtered.value.length - 1)
  } else if (e.key === 'ArrowUp') {
    e.preventDefault()
    activeIdx.value = Math.max(activeIdx.value - 1, 0)
  } else if (e.key === 'Enter') {
    e.preventDefault()
    if (activeIdx.value >= 0 && activeIdx.value < filtered.value.length) {
      select(filtered.value[activeIdx.value].value)
    } else if (props.allowCreate && input.value.trim()) {
      emit('create', input.value.trim())
      input.value = ''
    }
  } else if (e.key === 'Backspace' && !input.value && props.modelValue.length) {
    remove(props.modelValue[props.modelValue.length - 1])
  }
}

function onBlur() {
  setTimeout(() => { focused.value = false; activeIdx.value = -1 }, 150)
}
</script>

<template>
  <div class="relative" @click="focused = true">
    <div class="flex flex-wrap items-center gap-1 rounded-md border border-border bg-background px-2 py-1 min-h-9 text-sm cursor-text" :class="{ 'ring-2 ring-ring': focused }">
      <span v-for="val in modelValue" :key="val" class="inline-flex items-center gap-0.5 rounded bg-accent/15 text-accent px-1.5 py-0.5 text-xs">
        {{ items.find(i => i.value === val)?.label || val }}
        <button type="button" class="hover:text-accent-foreground" @click.stop="remove(val)"><X class="size-3" /></button>
      </span>
      <input
        :placeholder="modelValue.length ? '' : placeholder"
        v-model="input"
        @keydown="onKeyDown"
        @focus="focused = true"
        @blur="onBlur"
        class="flex-1 min-w-[60px] bg-transparent outline-none text-sm py-0.5"
      />
    </div>
    <ul v-if="focused && filtered.length" class="absolute left-0 right-0 top-full mt-1 z-50 max-h-40 overflow-y-auto rounded-md border border-border bg-background shadow-md text-sm">
      <li
        v-for="(item, i) in filtered" :key="item.value"
        class="px-3 py-1.5 cursor-pointer hover:bg-accent/10 transition-colors"
        :class="{ 'bg-accent/15': i === activeIdx }"
        @mousedown.prevent="select(item.value)"
      >{{ item.label }}</li>
    </ul>
  </div>
</template>
