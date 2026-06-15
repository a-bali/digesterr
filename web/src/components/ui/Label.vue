<script setup lang="ts">
import { Label as RekaLabel } from 'reka-ui'
import { cn } from '../../lib/utils'
</script>

<template>
  <RekaLabel
    :class="cn('text-sm font-medium leading-none peer-disabled:cursor-not-allowed peer-disabled:opacity-70', $attrs.class as string)"
    v-bind="$attrs"
  >
    <slot />
  </RekaLable>
</template>
