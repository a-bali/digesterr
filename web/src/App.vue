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
  <!-- `expand` is what makes a burst of toasts readable. Sonner's default is a
       collapsed stack: only the newest toast is drawn to full height and the rest
       sit behind it as slivers, so firing several in a row (refresh several feeds,
       mark read in a few scopes) hides all but one until you hover. With `expand`
       every toast keeps its own height and they stack, so all stay visible.
       `visibleToasts` caps the stack: past that the oldest is dropped, which
       bounds how much of the pane a flood can cover. -->
  <Toaster
    position="bottom-right"
    :theme="themeStore.isDark ? 'dark' : 'light'"
    :expand="true"
    :visible-toasts="5"
    :gap="8"
  />
</template>
