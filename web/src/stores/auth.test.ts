import { describe, it, expect, beforeEach, vi } from 'vitest'
import { setActivePinia, createPinia } from 'pinia'
import { useAuthStore } from './auth'
import { router } from '../router'

vi.mock('../services/api', () => ({
  authApi: {
    signup: vi.fn(),
    signin: vi.fn(),
    signout: vi.fn(),
    changePassword: vi.fn(),
  },
  setAuthToken: vi.fn(),
  getAuthToken: vi.fn(() => null),
  setOnUnauthorized: vi.fn(),
  ApiError: class extends Error { constructor(public status: number, body: any) { super(body?.error) } },
}))

vi.mock('../router', () => ({
  router: { push: vi.fn() },
}))

beforeEach(() => {
  setActivePinia(createPinia())
})

describe('auth store', () => {
  it('initializes with no user', () => {
    const store = useAuthStore()
    expect(store.user).toBeNull()
    expect(store.token).toBeNull()
  })

  it('signin sets user and token', async () => {
    const { authApi } = await import('../services/api')
    const mockSignin = authApi.signin as ReturnType<typeof vi.fn>
    mockSignin.mockResolvedValue({
      user: { id: 1, email: 'test@example.com', created_at: '', updated_at: '' },
      access_token: 'jwt-token',
    })

    const store = useAuthStore()
    await store.signin('test@example.com', 'password123')
    expect(store.user?.email).toBe('test@example.com')
    expect(store.token).toBe('jwt-token')
  })

  it('signup creates account and sets token', async () => {
    const { authApi } = await import('../services/api')
    const mockSignup = authApi.signup as ReturnType<typeof vi.fn>
    mockSignup.mockResolvedValue({
      user: { id: 2, email: 'new@example.com', created_at: '', updated_at: '' },
      access_token: 'new-jwt',
    })

    const store = useAuthStore()
    await store.signup('new@example.com', 'password123')
    expect(store.user?.email).toBe('new@example.com')
    expect(store.token).toBe('new-jwt')
  })

  it('signout clears user and token', async () => {
    const { authApi } = await import('../services/api')
    ;(authApi.signout as ReturnType<typeof vi.fn>).mockResolvedValue(undefined)

    const store = useAuthStore()
    store.token = 'some-token'
    store.user = { id: 1, email: 'a@b.com', created_at: '', updated_at: '' }

    await store.signout()
    expect(store.user).toBeNull()
    expect(store.token).toBeNull()
    expect(router.push).toHaveBeenCalledWith('/login')
  })
})
