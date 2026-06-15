<script setup lang="ts">
import {
  TooltipArrow,
  TooltipContent,
  TooltipPortal,
  TooltipProvider,
  TooltipRoot,
  TooltipTrigger,
} from 'reka-ui'
import { cn } from '../../lib/utils'
</script>

<template>
  <TooltipProvider>
    <TooltipRoot>
      <TooltipTrigger as-child>
        <slot name="trigger" />
      </TooltipTrigger>
      <TooltipPortal>
        <TooltipContent
          side="top"
          :class="cn(
            'z-50 overflow-hidden rounded-md border bg-background px-3 py-1.5 text-xs shadow-md animate-in fade-in-0 zoom-in-95',
            $attrs.class as string,
          )"
        >
          <slot />
          <TooltipArrow class="fill-border" />
        </TooltipContent>
      </TooltipPortal>
    </TooltipRoot>
  </TooltipProvider>
</template>
