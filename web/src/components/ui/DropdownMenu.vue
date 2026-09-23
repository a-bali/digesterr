<script setup lang="ts">
import { computed, type Component } from 'vue'
import {
  DropdownMenuArrow,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuPortal,
  DropdownMenuRoot,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from 'reka-ui'
import { Check } from 'lucide-vue-next'
import { cn } from '../../lib/utils'

type MenuItem = {
  label: string
  icon?: Component
  // A TEXT GLYPH in the icon slot, for menus that mirror a text mark elsewhere in
  // the app (the item header's status mark is `◉ ◐ ◯ ★`). Preferred over `icon`
  // for those: a Lucide icon renders at its own size/weight and reads as a
  // different family than the mark it is meant to echo, while a glyph inherits
  // the same font metrics and so matches it exactly.
  glyph?: string
  disabled?: boolean
  separator?: boolean
  onClick?: () => void
  // Marks the current choice in a single-select menu. Declaring `checked` on any
  // item reserves the indicator column for all of them, so labels stay aligned.
  checked?: boolean
  // Secondary text shown to the right of the label (e.g. "2-3 sentences").
  detail?: string
}

const props = defineProps<{
  items: MenuItem[]
}>()

const hasChecks = computed(() => props.items.some(i => i.checked !== undefined))

// Disabled items must be inert even if a click somehow reaches them (CSS
// pointer-events does not block synthetic events).
function onItemClick(item: MenuItem) {
  if (!item.disabled) item.onClick?.()
}
</script>

<template>
  <DropdownMenuRoot>
    <DropdownMenuTrigger as-child>
      <slot name="trigger" />
    </DropdownMenuTrigger>
    <DropdownMenuPortal>
      <DropdownMenuContent
        :class="cn(
          'z-50 min-w-40 overflow-hidden rounded-md border bg-background p-1 shadow-md animate-in fade-in-0 zoom-in-95',
        )"
        side="bottom"
        align="end"
      >
        <template v-for="(item, i) in items" :key="i">
          <DropdownMenuSeparator v-if="item.separator" class="my-1 h-px bg-border" />
          <DropdownMenuItem
            :disabled="item.disabled"
            :data-checked="item.checked || undefined"
            class="flex cursor-pointer items-center gap-2 rounded-sm px-2 py-1.5 text-sm outline-none hover:bg-muted focus:bg-muted data-[disabled]:pointer-events-none data-[disabled]:opacity-50"
            @click="onItemClick(item)"
          >
            <span v-if="hasChecks" class="flex size-4 shrink-0 items-center justify-center">
              <Check v-if="item.checked" class="size-4" />
            </span>
            <component :is="item.icon" v-if="item.icon" class="size-4 shrink-0" />
            <!-- Same 16px slot as an icon, so a glyph and an icon in the same menu
                 keep the labels aligned. `text-[15px] leading-none` matches the
                 header status mark's glyph size exactly. -->
            <span v-else-if="item.glyph" class="flex size-4 shrink-0 items-center justify-center text-[15px] leading-none">{{ item.glyph }}</span>
            <span>{{ item.label }}</span>
            <span v-if="item.detail" class="ml-auto pl-3 text-xs text-muted-foreground">{{ item.detail }}</span>
          </DropdownMenuItem>
        </template>
        <DropdownMenuSeparator v-if="$slots.separator" class="my-1 h-px bg-border" />
        <slot name="separator" />
        <DropdownMenuArrow class="fill-border" />
      </DropdownMenuContent>
    </DropdownMenuPortal>
  </DropdownMenuRoot>
</template>
