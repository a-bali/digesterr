<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import Button from '../components/ui/Button.vue'
import Input from '../components/ui/Input.vue'
import { useAuthStore } from '../stores/auth'
import { authApi } from '../services/api'

const store = useAuthStore()
const router = useRouter()
const route = useRoute()
const isSignup = ref(false)
const email = ref('')
const password = ref('')
const error = ref('')

// OIDC is optional and server-configured. `providerName` stays empty unless the
// server reports an enabled provider, which is also what controls whether the
// button is rendered at all — an instance without OIDC must look unchanged.
const providerName = ref('')
const oidcEnabled = ref(false)

// The callback redirects here with a stable code, never with provider text:
// reflecting provider-supplied strings into the page would turn an error path
// into an injection point, and the raw code is meaningless to a user anyway.
const OIDC_ERRORS: Record<string, string> = {
  oidc_denied: 'Sign-in was cancelled at your provider.',
  oidc_state_invalid:
    'That sign-in link is no longer valid — it may have expired or already been used. Please try again.',
  oidc_invalid_token: 'Your provider’s response could not be verified. Please try again.',
  oidc_exchange_failed: 'Sign-in could not be completed. Please try again.',
  oidc_email_unverified:
    'Your provider has not verified that email address, so it cannot be used to sign in.',
  oidc_email_missing: 'Your provider did not share an email address, which is required here.',
  oidc_email_ambiguous:
    'That email matches more than one local account, so it cannot be linked automatically. Ask the operator to resolve it.',
  oidc_error: 'Sign-in failed. Please try again.',
}

function messageForCode(code: string): string {
  return OIDC_ERRORS[code] ?? 'Sign-in failed. Please try again.'
}

onMounted(async () => {
  const code = typeof route.query.error === 'string' ? route.query.error : ''
  if (code) error.value = messageForCode(code)

  try {
    const status = await authApi.oidcStatus()
    oidcEnabled.value = !!status?.enabled
    providerName.value = status?.provider_name || ''
  } catch {
    // No provider, or the feature is off: the routes do not exist in that case,
    // so a failure here is the expected answer and not something to report.
    oidcEnabled.value = false
  }
})

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
  } finally {
    // The credential must not linger in JS memory/DOM after the attempt —
    // clear it on both success and failure.
    password.value = ''
  }
}
</script>

<template>
  <div class="flex min-h-screen items-center justify-center bg-muted/50 overflow-x-hidden">
    <div class="w-full max-w-sm rounded-lg border bg-background p-6 shadow-sm">
      <h1 class="text-xl font-semibold mb-2">Digesterr</h1>
      <p class="text-sm text-muted-foreground mb-6">
        {{ isSignup ? 'Create an account' : 'Sign in to your account' }}
      </p>
      <div v-if="oidcEnabled" class="mb-6 space-y-3">
        <!-- A plain link to the server's start route: the browser leaves this
             page entirely. The SPA must not build the provider URL itself —
             state, nonce and PKCE are minted server-side, and no token, code or
             state belongs in a URL this application renders. -->
        <a
          data-test="oidc-signin"
          href="/api/auth/oidc/start"
          class="flex w-full items-center justify-center rounded-md border px-3 py-2 text-sm font-medium hover:bg-muted"
        >
          Continue with {{ providerName || 'single sign-on' }}
        </a>
        <div class="flex items-center gap-3 text-xs uppercase text-muted-foreground">
          <span class="h-px flex-1 bg-border" />
          or
          <span class="h-px flex-1 bg-border" />
        </div>
      </div>
      <form @submit.prevent="submit" class="space-y-4">
        <div class="space-y-2">
          <label class="text-sm font-medium" for="email">Email</label>
          <Input id="email" v-model="email" type="email" placeholder="you@example.com" required />
        </div>
        <div class="space-y-2">
          <label class="text-sm font-medium" for="password">Password</label>
          <Input id="password" v-model="password" type="password" placeholder="••••••••" required />
        </div>
        <p v-if="error" data-test="login-error" class="text-sm text-destructive">{{ error }}</p>
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
