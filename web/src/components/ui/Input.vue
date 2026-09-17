<script setup lang="ts">
import { cn } from '../../lib/utils'

// modelValue is string|number because v-model.number on numeric inputs
// passes a number; the native <input> emits strings unless the .number
// modifier coerces. Accept both.
// A focused field must not be painted over by a neighbour attached to its right
// edge (the model picker beside the LLM field, a split button's chevron). The
// neighbour comes later in the DOM, and with both static the neighbour wins the
// paint order and hides the ring's right band. The ring is a box-shadow drawn
// OUTSIDE the border box, so it cannot be rescued by an inset ring; raising the
// focused field instead keeps the ring visible on all four sides. `relative` is
// what makes `z-10` apply at all — z-index is ignored on a static box.
const model = defineModel<string | number>({ required: false })
</script>

<template>
  <input
    v-model="model"
    :class="cn(
      'flex h-9 w-full rounded-md border border-border bg-background px-3 py-1 text-sm shadow-sm transition-colors file:border-0 file:bg-transparent file:text-sm file:font-medium placeholder:text-muted-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:relative focus-visible:z-10 disabled:cursor-not-allowed disabled:opacity-50',
      $attrs.class as string,
    )"
  />
</template>
