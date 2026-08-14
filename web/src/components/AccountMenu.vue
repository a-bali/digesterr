<script setup lang="ts">
import { CircleUserRound, KeyRound, KeySquare, LogOut } from 'lucide-vue-next'
import DropdownMenu from './ui/DropdownMenu.vue'
import Button from './ui/Button.vue'
import { useAuthStore } from '../stores/auth'

const emit = defineEmits<{
  'change-password': []
  'google-reader-api': []
}>()

const auth = useAuthStore()

// 'Change password' and 'Google Reader API' are wired up by the dialogs that
// listen to the corresponding events; logout is handled here, exactly as the
// old logout button did. reka-ui closes the menu on outside click, Escape,
// and after selecting an item.
</script>

<template>
  <DropdownMenu
    :items="[
      { label: 'Change password', icon: KeyRound, onClick: () => emit('change-password') },
      { label: 'Google Reader API', icon: KeySquare, onClick: () => emit('google-reader-api') },
      { label: 'Logout', icon: LogOut, onClick: () => auth.signout() },
    ]"
  >
    <template #trigger>
      <Button variant="ghost" size="sm" class="flex items-center gap-2" aria-label="Account menu">
        <CircleUserRound class="size-4" />
        <span class="hidden sm:inline">{{ auth.user?.email }}</span>
      </Button>
    </template>
  </DropdownMenu>
</template>
