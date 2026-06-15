<script setup lang="ts">
import { Separator as RekaSeparator } from 'reka-ui'
import { cn } from '../../lib/utils'
</script>

<template>
  <RekaSeparator
    :class="cn('shrink-0 bg-border', $attrs.orientation === 'vertical' ? 'w-px h-full' : 'h-px w-full', $attrs.class as string)"
    v-bind="$attrs"
  />
</template>
