<script setup lang="ts">
import { computed, type Component } from 'vue'
import DropdownMenu from './ui/DropdownMenu.vue'
import Button from './ui/Button.vue'
import { Filter, ArrowUpDown, Check, CircleOff } from 'lucide-vue-next'

export type FilterStatus = 'all' | 'unread' | 'read' | 'digested' | 'saved'
export type FilterSort = 'newest' | 'oldest' | 'highest' | 'lowest'

const props = defineProps<{
  statusFilter: FilterStatus
  sortValue: FilterSort
  minScore: number | null
  scoreSupported: boolean
  itemCount: number
  feedName?: string | null
  statusOptions: { value: FilterStatus; label: string }[]
}>()

const emit = defineEmits<{
  (e: 'update:statusFilter', v: FilterStatus): void
  (e: 'update:sortValue', v: FilterSort): void
  (e: 'update:minScore', v: number | null): void
}>()

const SORT_OPTIONS: { value: FilterSort; label: string }[] = [
  { value: 'newest', label: 'Newest' },
  { value: 'oldest', label: 'Oldest' },
  { value: 'highest', label: 'Highest score' },
  { value: 'lowest', label: 'Lowest score' },
]

const SORT_SHORT: Record<FilterSort, string> = {
  newest: 'Newest', oldest: 'Oldest', highest: 'Highest', lowest: 'Lowest',
}

type MenuItem = { label: string; icon?: Component; disabled?: boolean; separator?: boolean; onClick?: () => void }

const isFilterDefault = computed(() => {
  if (props.statusFilter !== 'all') return false
  if (props.scoreSupported && props.minScore != null && props.minScore > 0) return false
  return true
})

const isSortDefault = computed(() => props.sortValue === 'newest')

const filterLabel = computed(() => {
  const opt = props.statusOptions.find(o => o.value === props.statusFilter)
  let label = opt?.label ?? 'All'
  if (props.scoreSupported && props.minScore != null && props.minScore > 0) {
    label += ' \u2265' + props.minScore
  }
  return label
})

const sortLabel = computed(() => SORT_SHORT[props.sortValue])

const filterItems = computed<MenuItem[]>(() =>
  props.statusOptions.map(opt => ({
    label: opt.label,
    icon: props.statusFilter === opt.value ? Check : undefined,
    onClick: () => emit('update:statusFilter', opt.value),
  })),
)

const sortItems = computed<MenuItem[]>(() =>
  SORT_OPTIONS.map(opt => ({
    label: opt.label,
    disabled: !props.scoreSupported && (opt.value === 'highest' || opt.value === 'lowest'),
    icon: props.sortValue === opt.value ? Check : undefined,
    onClick: () => emit('update:sortValue', opt.value),
  })),
)

function clampScore(raw: string): number | null {
  const n = Number(raw.trim())
  return Number.isFinite(n) && n > 0 ? Math.floor(n) : null
}

function incrementScore() {
  const cur = props.minScore ?? 0
  emit('update:minScore', cur + 5)
}

function decrementScore() {
  const cur = props.minScore ?? 0
  const next = Math.max(0, cur - 5)
  emit('update:minScore', next > 0 ? next : null)
}

function resetScore() {
  emit('update:minScore', null)
}
</script>

<template>
  <div class="flex items-center gap-1 px-2.5 py-1.5 border-b">
    <!-- Filter button -->
    <DropdownMenu :items="filterItems">
      <template #trigger>
        <Button variant="ghost" size="sm" aria-label="Filter items">
          <Filter class="size-4" />
          <span class="text-xs hidden lg:inline">{{ filterLabel }}</span>
          <span v-if="!isFilterDefault" class="absolute top-1 right-1 size-1.5 rounded-full bg-current opacity-60 lg:hidden" />
        </Button>
      </template>
      <template #separator>
        <div class="px-2 py-1.5">
          <p class="mb-1 text-[10px] font-semibold uppercase tracking-wide text-muted-foreground">Minimum score</p>
          <div class="flex items-center gap-1" :class="{ 'opacity-35 pointer-events-none': !scoreSupported }">
            <button
              type="button"
              class="inline-flex items-center justify-center size-6 rounded border border-border bg-background text-xs font-semibold hover:bg-muted disabled:opacity-35 disabled:pointer-events-none"
              :disabled="!scoreSupported || (minScore ?? 0) <= 0"
              @click="decrementScore"
            >−</button>
            <input
              type="text"
              inputmode="numeric"
              class="h-6 w-10 rounded border border-border bg-background text-center text-xs font-medium [-moz-appearance:textfield] [&::-webkit-outer-spin-button]:appearance-none [&::-webkit-inner-spin-button]:appearance-none focus:outline-none focus:ring-2 focus:ring-ring"
              :value="minScore ?? 0"
              :disabled="!scoreSupported"
              aria-label="Minimum score"
              @change="($event: Event) => emit('update:minScore', clampScore(($event.target as HTMLInputElement).value))"
            />
            <button
              type="button"
              class="inline-flex items-center justify-center size-6 rounded border border-border bg-background text-xs font-semibold hover:bg-muted"
              :disabled="!scoreSupported"
              @click="incrementScore"
            >+</button>
            <button
              type="button"
              class="inline-flex items-center justify-center size-6 rounded border border-border bg-background hover:bg-muted disabled:opacity-35 disabled:pointer-events-none"
              :disabled="!scoreSupported || (minScore ?? 0) === 0"
              title="Reset to 0"
              @click="resetScore"
            >
              <CircleOff class="size-3" :class="(minScore ?? 0) === 0 ? 'text-muted-foreground' : ''" />
            </button>
          </div>
        </div>
      </template>
    </DropdownMenu>

    <!-- Feed name (mobile only) -->
    <span v-if="feedName" class="text-xs font-semibold truncate lg:hidden">{{ feedName }}</span>

    <!-- Item count -->
    <span class="text-[11px] text-muted-foreground whitespace-nowrap flex-1 text-center">
      {{ itemCount }} items
    </span>

    <!-- Sort button -->
    <DropdownMenu :items="sortItems">
      <template #trigger>
        <Button variant="ghost" size="sm" aria-label="Sort items">
          <ArrowUpDown class="size-4" />
          <span class="text-xs hidden lg:inline">{{ sortLabel }}</span>
          <span v-if="!isSortDefault" class="absolute top-1 right-1 size-1.5 rounded-full bg-current opacity-60 lg:hidden" />
        </Button>
      </template>
    </DropdownMenu>
  </div>
</template>
