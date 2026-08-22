<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { systemApi, type VersionInfo } from '../services/api'
import Tooltip from './ui/Tooltip.vue'

const version = ref('')
const info = ref<VersionInfo | null>(null)

onMounted(async () => {
  try {
    info.value = await systemApi.version()
    version.value = info.value.version
  } catch { /* silent — badge simply stays empty */ }
})

function uptime(): string {
  if (!info.value) return ''
  const s = info.value.uptime_seconds
  const d = Math.floor(s / 86400)
  const h = Math.floor((s % 86400) / 3600)
  const m = Math.floor((s % 3600) / 60)
  if (d > 0) return `up ${d}d ${h}h ${m}m`
  if (h > 0) return `up ${h}h ${m}m`
  return `up ${m}m ${s % 60}s`
}
</script>

<template>
  <Tooltip v-if="version">
    <template #trigger>
      <span class="cursor-default select-none text-[10px] text-muted-foreground/60" data-version-badge>{{ version }}</span>
    </template>
    {{ uptime() }}
  </Tooltip>
</template>
