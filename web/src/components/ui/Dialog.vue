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
        <div class="flex-1 min-h-0 overflow-y-auto px-6 pb-6">
          <slot />
        </div>
      </DialogContent>
    </DialogPortal>
  </DialogRoot>
</template>
