import { defineStore } from 'pinia'
import { ref } from 'vue'
import { toast } from 'vue-sonner'
import { authApi, getAuthToken, setAuthToken, setOnUnauthorized } from '../services/api'
import type { User } from '../types/api'
import { router } from '../router'

export const useAuthStore = defineStore('auth', () => {
  const user = ref<User | null>(null)
  const token = ref<string | null>(getAuthToken())
  const loading = ref(false)

  setOnUnauthorized(() => {
    user.value = null
    token.value = null
    setAuthToken(null)
    router.push('/login')
  })

  async function signup(email: string, password: string) {
    loading.value = true
    try {
      const res = await authApi.signup(email, password)
      user.value = res.user
      token.value = res.access_token
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
      setAuthToken(null)
      router.push('/login')
    }
  }

  return { user, token, loading, signup, signin, signout }
})
