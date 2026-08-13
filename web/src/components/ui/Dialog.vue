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
          'fixed left-1/2 top-1/2 z-50 w-full max-w-lg -translate-x-1/2 -translate-y-1/2 rounded-lg border bg-background p-6 shadow-lg animate-in fade-in-0 zoom-in-95 data-[state=closed]:animate-out data-[state=closed]:fade-out-0 data-[state=closed]:zoom-out-95',
          props.contentClass,
        )"
      >
        <div class="flex items-center justify-between mb-4">
          <DialogTitle v-if="props.title" class="text-lg font-semibold">{{ props.title }}</DialogTitle>
          <DialogClose as-child>
            <Button variant="ghost" size="icon" class="absolute right-4 top-4">
              <X class="size-4" />
            </Button>
          </DialogClose>
        </div>
        <DialogDescription v-if="props.description" class="text-sm text-muted-foreground mb-4">
          {{ props.description }}
        </DialogDescription>
        <slot />
      </DialogContent>
    </DialogPortal>
  </DialogRoot>
</template>
