<script setup lang="ts">
import { cn } from '../../lib/utils'

// modelValue is string|number because v-model.number on numeric inputs
// passes a number; the native <input> emits strings unless the .number
// modifier coerces. Accept both.
const model = defineModel<string | number>({ required: false })
</script>

<template>
  <input
    v-model="model"
    :class="cn(
      'flex h-9 w-full rounded-md border border-border bg-background px-3 py-1 text-sm shadow-sm transition-colors file:border-0 file:bg-transparent file:text-sm file:font-medium placeholder:text-muted-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring disabled:cursor-not-allowed disabled:opacity-50',
      $attrs.class as string,
    )"
  />
</template>
