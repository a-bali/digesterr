<script setup lang="ts">
import { onMounted } from 'vue'
import { useDigestStore } from '../stores/digest'
import { useRouter } from 'vue-router'
import Button from './ui/Button'
import { Plus, MessageSquare, Calendar, Sparkles, Settings } from 'lucide-vue-next'

const store = useDigestStore()
const router = useRouter()

onMounted(() => {
  store.fetchConfigs()
})

function selectConfig(id: number) {
  store.setActiveConfig(id)
  router.push(`/digests/${id}`)
}

function createDigest() {
  router.push('/settings')
}

function generateNow(e: Event, cfgId: number) {
  e.stopPropagation()
  store.generateNow(cfgId)
}
</script>

<template>
  <div class="flex flex-col h-full">
    <div class="flex items-center justify-between mb-2">
      <span class="text-xs font-semibold text-muted-foreground uppercase">Digest Configs</span>
      <Button variant="ghost" size="icon" title="Create digest" @click="createDigest">
        <Plus class="size-3.5" />
      </Button>
    </div>
    <div class="space-y-1 flex-1 overflow-y-auto">
      <div
        v-for="cfg in store.configs"
        :key="cfg.id"
        class="group flex flex-col rounded-md px-2 py-1.5 cursor-pointer transition-colors"
        :class="store.activeConfigId === cfg.id ? 'bg-accent/10 font-medium text-accent' : 'hover:bg-muted'"
        @click="selectConfig(cfg.id)"
      >
        <div class="flex items-center justify-between">
          <div class="flex items-center gap-1.5 text-sm min-w-0">
            <MessageSquare class="size-3 shrink-0" />
            <span class="truncate font-medium">{{ cfg.schedule }}</span>
          </div>
          <div class="flex items-center gap-0.5 shrink-0 opacity-0 group-hover:opacity-100 transition-opacity">
            <Button variant="ghost" size="icon" class="size-6" title="Generate now" :disabled="store.generating" @click="generateNow($event, cfg.id)">
              <Sparkles class="size-3" />
            </Button>
            <Button variant="ghost" size="icon" class="size-6" title="Edit" @click.stop="router.push('/settings')">
              <Settings class="size-3" />
            </Button>
          </div>
        </div>
        <div class="flex items-center gap-1 text-xs text-muted-foreground mt-0.5">
          <Calendar class="size-2.5" />
          <span>{{ new Date(cfg.created_at).toLocaleDateString() }}</span>
        </div>
      </div>
      <div v-if="store.configs.length === 0" class="text-xs text-muted-foreground p-2">
        No digest configs yet. Go to Settings to create one.
      </div>
    </div>
  </div>
</template>
