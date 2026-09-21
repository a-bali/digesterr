<script setup lang="ts">
import { RouterLink } from 'vue-router'
import Button from '../components/ui/Button.vue'
</script>

<template>
  <div class="flex min-h-screen flex-col items-center justify-center gap-4 bg-muted/50 px-6 text-center">
    <p class="text-6xl font-semibold text-muted-foreground">404</p>
    <p class="max-w-sm text-sm text-muted-foreground">
      This page does not exist. Use the link below to get back to your feeds.
    </p>
    <RouterLink to="/">
      <Button>Back to feeds</Button>
    </RouterLink>
  </div>
</template>
