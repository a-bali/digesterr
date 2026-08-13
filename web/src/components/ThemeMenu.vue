<script setup lang="ts">
import { ref, onMounted, onUnmounted } from 'vue'
import { useThemeStore, accents } from '../stores/theme'
import Button from './ui/Button.vue'
import { Sun, Moon } from 'lucide-vue-next'

const theme = useThemeStore()
const open = ref(false)
const root = ref<HTMLElement | null>(null)

function onClickOutside(e: MouseEvent) {
  if (root.value && !root.value.contains(e.target as Node)) open.value = false
}

onMounted(() => document.addEventListener('click', onClickOutside))
onUnmounted(() => document.removeEventListener('click', onClickOutside))
</script>

<template>
  <div ref="root" class="relative">
    <Button variant="ghost" size="icon" @click="open = !open">
      <Sun v-if="theme.mode !== 'dark'" class="size-4" />
      <Moon v-else class="size-4" />
    </Button>
    <div v-if="open" class="absolute right-0 top-full mt-1 z-50 min-w-40 rounded-md border bg-background p-2 shadow-md">
      <div class="flex items-center gap-2 px-1 py-1">
        <span class="text-xs text-muted-foreground">Mode</span>
        <div class="flex rounded-md border border-border overflow-hidden text-xs">
          <button class="px-2 py-1 transition-colors" :class="theme.mode === 'light' ? 'bg-accent text-accent-foreground' : 'hover:bg-muted'" @click="theme.setMode('light'); open = false"><Sun class="size-3 inline" /> Light</button>
          <button class="px-2 py-1 border-x border-border transition-colors" :class="theme.mode === 'dark' ? 'bg-accent text-accent-foreground' : 'hover:bg-muted'" @click="theme.setMode('dark'); open = false"><Moon class="size-3 inline" /> Dark</button>
          <button class="px-2 py-1 transition-colors" :class="theme.mode === 'system' ? 'bg-accent text-accent-foreground' : 'hover:bg-muted'" @click="theme.setMode('system'); open = false">Auto</button>
        </div>
      </div>
      <div class="flex items-center gap-2 px-1 py-1">
        <span class="text-xs text-muted-foreground">Accent</span>
        <div class="flex gap-1.5">
          <button v-for="a in ['blue','green','purple','orange','rose','teal','amber']" :key="a"
            class="size-5 rounded-full border transition-all"
            :class="theme.accent === a ? 'border-foreground scale-110' : 'border-transparent'"
            :style="{ backgroundColor: `hsl(${accents[a][0]})` }"
            @click="theme.setAccent(a); open = false" />
        </div>
      </div>
    </div>
  </div>
</template>
