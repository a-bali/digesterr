<script setup lang="ts">
import { ref, watch, onMounted, onBeforeUnmount } from 'vue'
import { enhanceRedditVideos } from '../lib/redditVideo'

const props = defineProps<{ html: string }>()
const root = ref<HTMLElement>()
let cleanup = () => {}
function attach() {
  if (!root.value) return
  cleanup = enhanceRedditVideos(root.value)
  root.value.querySelectorAll('a:not([target])').forEach(link => {
    link.setAttribute('target', '_blank')
    link.setAttribute('rel', 'noopener noreferrer')
  })
}
// Stop old media before Vue replaces v-html nodes; attach only after replacement.
watch(() => props.html, () => cleanup(), { flush: 'pre' })
watch(() => props.html, attach, { flush: 'post' })
onMounted(attach)
onBeforeUnmount(() => cleanup())
</script>

<template>
  <div ref="root" v-html="html" />
</template>
