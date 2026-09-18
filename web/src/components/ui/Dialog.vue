<script setup lang="ts">
import {
  DialogClose,
  DialogContent,
  DialogDescription,
  DialogOverlay,
  DialogPortal,
  DialogRoot,
  DialogTitle,
  DialogTrigger,
} from 'reka-ui'
import { cn } from '../../lib/utils'
import { X } from 'lucide-vue-next'
import Button from '../../components/ui/Button.vue'

const props = withDefaults(defineProps<{
  open?: boolean
  title?: string
  description?: string
  contentClass?: string
}>(), {
  contentClass: '',
})

const emit = defineEmits<{
  (e: 'update:open', v: boolean): void
}>()
</script>

<template>
  <DialogRoot :open="open" @update:open="emit('update:open', $event)">
    <DialogTrigger as-child>
      <slot name="trigger" />
    </DialogTrigger>
    <DialogPortal>
      <DialogOverlay
        class="fixed inset-0 z-50 bg-black/50 data-[state=open]:animate-in data-[state=closed]:animate-out data-[state=closed]:fade-out-0 data-[state=open]:fade-in-0"
      />
      <DialogContent
        :class="cn(
          'fixed left-1/2 top-1/2 z-50 w-full max-w-lg -translate-x-1/2 -translate-y-1/2 rounded-lg border bg-background shadow-lg animate-in fade-in-0 zoom-in-95 data-[state=closed]:animate-out data-[state=closed]:fade-out-0 data-[state=closed]:zoom-out-95 flex flex-col max-h-[90vh] p-0',
          props.contentClass,
        )"
      >
        <div class="flex items-center justify-between shrink-0 px-6 pt-6 pb-2">
          <DialogTitle v-if="props.title" class="text-lg font-semibold">{{ props.title }}</DialogTitle>
          <DialogClose as-child>
            <Button variant="ghost" size="icon">
              <X class="size-4" />
            </Button>
          </DialogClose>
        </div>
        <DialogDescription v-if="props.description" class="shrink-0 px-6 pb-4 text-sm text-muted-foreground">
          {{ props.description }}
        </DialogDescription>
        <!-- `pt-0.5 -mt-0.5`: a scroll container clips at its padding box, so a
             control sitting flush against the top of this body loses the top band
             of its focus ring (the ring is a 2px box-shadow spread drawn OUTSIDE
             the border box). Measured in the "Mark all as read?" dialog: with a
             focused button the ring drew 2px on the left, right and bottom edges
             and NOTHING on top — the nearest ring pixel above the button did not
             exist, while y=518/519 held the bottom band. The zero-sum padding
             and negative margin move the scrollport's edge up by exactly the
             2px the ring needs while leaving every child pixel where it was
             (the button's rect was byte-identical, y=482 before and after), so
             nothing else in any of the 9 dialogs shifts. `overflow-clip-margin`
             does NOT fix this — it applies to `overflow: clip`, not to a scroll
             container, and was measured to change nothing. -->
        <div class="flex-1 min-h-0 overflow-y-auto pt-0.5 -mt-0.5 px-6 pb-6">
          <slot />
        </div>
      </DialogContent>
    </DialogPortal>
  </DialogRoot>
</template>
