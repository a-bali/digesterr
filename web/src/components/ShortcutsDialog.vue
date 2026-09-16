<script setup lang="ts">
import Dialog from './ui/Dialog.vue'

const open = defineModel<boolean>('open', { default: false })

const groups: { title: string; bindings: [string, string][] }[] = [
  {
    title: 'Items',
    bindings: [
      ['j / n / →', 'Next item'],
      ['k / p / ←', 'Previous item'],
      ['Home / End', 'First / last item'],
      ['m or Space', 'Toggle read/unread'],
      ['s', 'Toggle saved'],
      ['v', 'Open article URL'],
    ],
  },
  {
    title: 'Feeds & content',
    bindings: [
      ['↑ / ↓', 'Previous / next feed'],
      ['PageUp / PageDown', 'Scroll content'],
      ['c', 'Scroll to comments'],
    ],
  },
  {
    title: 'General',
    bindings: [
      ['u', 'Toggle unread-only filter'],
      ['Shift+A', 'Mark feed as read'],
      ['x', 'Maximize content pane'],
      ['r', 'Refresh unread counts'],
      ['Shift+D', 'Toggle dark/light theme'],
      ['?', 'Show this help'],
    ],
  },
]
</script>

<template>
  <Dialog v-model:open="open" title="Keyboard shortcuts" content-class="max-w-md">
    <div class="space-y-4">
      <div v-for="group in groups" :key="group.title">
        <h3 class="text-xs font-semibold uppercase text-muted-foreground mb-1.5">{{ group.title }}</h3>
        <table class="w-full text-sm">
          <tbody>
            <tr v-for="[keys, action] in group.bindings" :key="keys">
              <td class="py-0.5 pr-4 whitespace-nowrap">
                <kbd
                  v-for="(k, i) in keys.split(' ')"
                  :key="i"
                  class="inline-block rounded border bg-muted px-1.5 py-0.5 font-mono text-xs"
                >{{ k }}</kbd>
              </td>
              <td class="py-0.5 text-muted-foreground">{{ action }}</td>
            </tr>
          </tbody>
        </table>
      </div>
    </div>
  </Dialog>
</template>
