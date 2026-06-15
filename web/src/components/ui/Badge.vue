<script setup lang="ts">
import { cva, type VariantProps } from 'class-variance-authority'

const badgeVariants = cva(
  'inline-flex items-center rounded-full border px-2.5 py-0.5 text-xs font-semibold transition-colors',
  {
    variants: {
      variant: {
        default: 'border-transparent bg-accent text-accent-foreground shadow',
        unread: 'border-transparent bg-blue-100 text-blue-800 dark:bg-blue-900 dark:text-blue-100',
        read: 'border-transparent bg-muted text-muted-foreground',
        digested: 'border-transparent bg-purple-100 text-purple-800 dark:bg-purple-900 dark:text-purple-100',
        saved: 'border-transparent bg-amber-100 text-amber-800 dark:bg-amber-900 dark:text-amber-100',
        outline: 'text-foreground',
      },
    },
    defaultVariants: { variant: 'default' },
  },
)

type BadgeVariants = VariantProps<typeof badgeVariants>

const props = withDefaults(defineProps<{
  variant?: BadgeVariants['variant']
}>(), { variant: 'default' })
</script>

<template>
  <span :class="badgeVariants({ variant: props.variant })">
    <slot />
  </span>
</template>
