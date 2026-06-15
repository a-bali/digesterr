<script setup lang="ts">
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

const props = withDefaults(defineProps<{
  items: { label: string; icon?: string; disabled?: boolean; onClick?: () => void }[]
}>(), {})
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
        <DropdownMenuItem
          v-for="(item, i) in items"
          :key="i"
          :disabled="item.disabled"
          class="flex cursor-pointer items-center gap-2 rounded-sm px-2 py-1.5 text-sm outline-none hover:bg-muted focus:bg-muted data-[disabled]:pointer-events-none data-[disabled]:opacity-50"
          @click="item.onClick"
        >
          {{ item.label }}
        </DropdownMenuItem>
        <DropdownMenuSeparator v-if="$slots.separator" class="my-1 h-px bg-border" />
        <slot name="separator" />
        <DropdownMenuArrow class="fill-border" />
      </DropdownMenuContent>
    </DropdownMenuPortal>
  </DropdownMenuRoot>
</template>
