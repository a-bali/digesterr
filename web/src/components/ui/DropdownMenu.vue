<script setup lang="ts">
import type { Component } from 'vue'
import {
  DropdownMenuArrow,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuPortal,
  DropdownMenuRoot,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from 'reka-ui'
import { cn } from '../../lib/utils'

type MenuItem = { label: string; icon?: Component; disabled?: boolean; separator?: boolean; onClick?: () => void }

defineProps<{
  items: MenuItem[]
}>()

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
            class="flex cursor-pointer items-center gap-2 rounded-sm px-2 py-1.5 text-sm outline-none hover:bg-muted focus:bg-muted data-[disabled]:pointer-events-none data-[disabled]:opacity-50"
            @click="onItemClick(item)"
          >
            <component :is="item.icon" v-if="item.icon" class="size-4 shrink-0" />
            {{ item.label }}
          </DropdownMenuItem>
        </template>
        <DropdownMenuSeparator v-if="$slots.separator" class="my-1 h-px bg-border" />
        <slot name="separator" />
        <DropdownMenuArrow class="fill-border" />
      </DropdownMenuContent>
    </DropdownMenuPortal>
  </DropdownMenuRoot>
</template>
