<script setup lang="ts">
import { ref } from 'vue'
import { useRouter } from 'vue-router'
import Button from '../components/ui/Button'
import Input from '../components/ui/Input'
import { useAuthStore } from '../stores/auth'

const store = useAuthStore()
const router = useRouter()
const isSignup = ref(false)
const email = ref('')
const password = ref('')
const error = ref('')

async function submit() {
  error.value = ''
  try {
    if (isSignup.value) {
      await store.signup(email.value, password.value)
    } else {
      await store.signin(email.value, password.value)
    }
    router.push('/')
  } catch (e: any) {
    error.value = e.message || 'An error occurred'
  }
}
</script>

<template>
  <div class="flex min-h-screen items-center justify-center bg-muted/50">
    <div class="w-full max-w-sm rounded-lg border bg-background p-6 shadow-sm">
      <h1 class="text-xl font-semibold mb-2">Digesterr</h1>
      <p class="text-sm text-muted-foreground mb-6">
        {{ isSignup ? 'Create an account' : 'Sign in to your account' }}
      </p>
      <form @submit.prevent="submit" class="space-y-4">
        <div class="space-y-2">
          <label class="text-sm font-medium" for="email">Email</label>
          <Input id="email" v-model="email" type="email" placeholder="you@example.com" required />
        </div>
        <div class="space-y-2">
          <label class="text-sm font-medium" for="password">Password</label>
          <Input id="password" v-model="password" type="password" placeholder="••••••••" required />
        </div>
        <p v-if="error" class="text-sm text-red-600">{{ error }}</p>
        <Button type="submit" class="w-full" :disabled="store.loading">
          {{ isSignup ? 'Sign Up' : 'Sign In' }}
        </Button>
        <p class="text-center text-sm text-muted-foreground">
          {{ isSignup ? 'Already have an account?' : "Don't have an account?" }}
          <button type="button" class="text-accent hover:underline" @click="isSignup = !isSignup">
            {{ isSignup ? 'Sign In' : 'Sign Up' }}
          </button>
        </p>
      </form>
    </div>
  </div>
</template>
