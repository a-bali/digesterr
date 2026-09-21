<script setup lang="ts">
import { RouterView } from 'vue-router'
import { Toaster } from 'vue-sonner'
import { useThemeStore } from './stores/theme'
// vue-sonner ships its layout CSS in a separate stylesheet that is NOT bundled
// with the component: without it [data-sonner-toaster] has no `position: fixed`
// and no z-index, so the toaster falls back to `static` and renders as a
// full-width block in normal document flow — visible as an empty strip under the
// left pane, with toasts painted below the 3-pane layout instead of over it.
// The component still sets --offset-*/--width inline, so this import is the only
// thing that turns those coordinates into a fixed overlay.
import 'vue-sonner/style.css'

// The Toaster paints its own palette and does NOT inherit the `.dark` class from
// <html>, so it needs the resolved theme explicitly — otherwise toasts stay a
// white card on the app's dark background.
const themeStore = useThemeStore()
</script>

<template>
  <RouterView />
  <Toaster position="bottom-right" :theme="themeStore.isDark ? 'dark' : 'light'" />
</template>
