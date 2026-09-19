import { defineStore } from 'pinia'
import { ref } from 'vue'
import { authApi, getAuthToken, setAuthToken, setOnUnauthorized } from '../services/api'
import type { User } from '../types/api'
import { router } from '../router'

export const useAuthStore = defineStore('auth', () => {
  const user = ref<User | null>(null)
  const token = ref<string | null>(getAuthToken())
  const loading = ref(false)
  // Rehydration runs once per page load: the router guard has already done the
  // silent refresh, so this only fetches the profile the in-memory token can
  // no longer carry across a reload.
  let loaded = false

  setOnUnauthorized(() => {
    user.value = null
    token.value = null
    loaded = false
    setAuthToken(null)
    router.push('/login')
  })

  // loadUser rehydrates the profile for an existing session. Safe to call from
  // every view mount: it is a no-op once loaded, and a 401 is handled by the
  // api layer's onUnauthorized path.
  async function loadUser() {
    if (loaded || !getAuthToken()) return
    loaded = true
    try {
      user.value = await authApi.me()
    } catch {
      // Token invalid/expired despite the guard: let onUnauthorized redirect.
    }
  }

  async function signup(email: string, password: string) {
    loading.value = true
    try {
      const res = await authApi.signup(email, password)
      user.value = res.user
      token.value = res.access_token
      loaded = true
      setAuthToken(res.access_token)
    } finally {
      loading.value = false
    }
  }

  async function signin(email: string, password: string) {
    loading.value = true
    try {
      const res = await authApi.signin(email, password)
      user.value = res.user
      token.value = res.access_token
      loaded = true
      setAuthToken(res.access_token)
    } finally {
      loading.value = false
    }
  }

  async function signout() {
    try {
      await authApi.signout()
    } finally {
      user.value = null
      token.value = null
      loaded = false
      setAuthToken(null)
      router.push('/login')
    }
  }

  return { user, token, loading, loadUser, signup, signin, signout }
})
