import { describe, it, expect, beforeEach, vi } from 'vitest'
import { setAuthToken, getAuthToken, setOnUnauthorized, api, ApiError } from './api'

// Track token in a variable so our mocks can interact with it
let currentToken: string | null = null

beforeEach(() => {
  ;(globalThis as any).localStorage?.clear()
  currentToken = null
  setAuthToken(null)
  setOnUnauthorized(() => {})
})

describe('setAuthToken / getAuthToken', () => {
  it('stores token in memory only, never localStorage', () => {
    setAuthToken('test-token')
    expect(getAuthToken()).toBe('test-token')
    expect((globalThis as any).localStorage?.getItem('digesterr-token') ?? null).toBeNull()
  })

  it('clears the in-memory token', () => {
    setAuthToken('test-token')
    setAuthToken(null)
    expect(getAuthToken()).toBeNull()
  })

  it('removes tokens persisted by older versions', () => {
    const ls = (globalThis as any).localStorage as Storage | undefined
    if (!ls) {
      // No Storage in this runner — setAuthToken must still keep the token.
      setAuthToken('fresh-token')
      expect(getAuthToken()).toBe('fresh-token')
      return
    }
    ls.setItem('digesterr-token', 'stale-token')
    setAuthToken('fresh-token')
    expect(ls.getItem('digesterr-token')).toBeNull()
    expect(getAuthToken()).toBe('fresh-token')
  })
})

describe('api', () => {
  it('sends GET request and returns JSON', async () => {
    let calledUrl = ''
    vi.spyOn(globalThis, 'fetch').mockImplementation(async (url) => {
      calledUrl = url as string
      return { ok: true, status: 200, text: () => Promise.resolve(JSON.stringify({ data: 'ok' })) } as any
    })

    const result = await api('/test')
    expect(result).toEqual({ data: 'ok' })
    expect(calledUrl).toBe('/api/test')
  })

  it('includes Authorization header when token is set', async () => {
    setAuthToken('my-jwt')
    let capturedHeaders: any
    vi.spyOn(globalThis, 'fetch').mockImplementation(async (url, init) => {
      capturedHeaders = init?.headers
      return { ok: true, status: 200, text: () => Promise.resolve('{}') } as any
    })

    await api('/test')
    expect(capturedHeaders['Authorization']).toBe('Bearer my-jwt')
  })

  it('throws ApiError on non-OK response', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue({
      ok: false, status: 404,
      text: () => Promise.resolve(JSON.stringify({ error: 'not found' })),
    } as any)

    await expect(api('/missing')).rejects.toThrow(ApiError)
    await expect(api('/missing')).rejects.toThrow('not found')
  })

  it('triggers onUnauthorized on 401', async () => {
    let called = false
    setOnUnauthorized(() => { called = true })

    vi.spyOn(globalThis, 'fetch').mockResolvedValue({
      ok: false, status: 401,
      text: () => Promise.resolve(JSON.stringify({ error: 'unauthorized' })),
    } as any)

    await expect(api('/protected')).rejects.toThrow()
    expect(called).toBe(true)
  })

  it('clears token on 401', async () => {
    setAuthToken('old-token')
    vi.spyOn(globalThis, 'fetch').mockResolvedValue({
      ok: false, status: 401,
      text: () => Promise.resolve(JSON.stringify({ error: 'unauthorized' })),
    } as any)

    await expect(api('/protected')).rejects.toThrow()
    expect(getAuthToken()).toBeNull()
  })

  // A 401 from signin carries the reason the credential was rejected. The
  // generic "unauthorized" string replaced it, so the login form showed a
  // message that told the user nothing about what to fix.
  it('surfaces the server 401 message instead of a generic one', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue({
      ok: false, status: 401,
      text: () => Promise.resolve(JSON.stringify({ error: 'invalid email or password' })),
    } as any)

    await expect(api('/auth/signin', { method: 'POST' })).rejects.toThrow('invalid email or password')
  })

  it('converts a non-JSON error body (proxy HTML) into a readable ApiError message', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue({
      ok: false, status: 502,
      text: () => Promise.resolve('<html><body>502 Bad Gateway</body></html>'),
    } as any)

    const err: any = await api('/test').catch((e) => e)
    expect(err).toBeInstanceOf(ApiError)
    expect(err.status).toBe(502)
    expect(err.message).toContain('502 Bad Gateway')
    // Long non-JSON bodies are truncated rather than dumped whole.
    const longHtml = `<html>${'x'.repeat(1000)}</html>`
    vi.spyOn(globalThis, 'fetch').mockResolvedValue({
      ok: false, status: 500,
      text: () => Promise.resolve(longHtml),
    } as any)
    const err2: any = await api('/test').catch((e) => e)
    expect(err2.message.length).toBeLessThanOrEqual(200)
  })

  it('falls back to statusText for an empty error body', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue({
      ok: false, status: 504, statusText: 'Gateway Timeout',
      text: () => Promise.resolve(''),
    } as any)

    await expect(api('/test')).rejects.toThrow('Gateway Timeout')
  })

  it('returns null for 204 responses', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue({
      ok: true, status: 204,
      text: () => Promise.resolve(''),
    } as any)

    const result = await api('/test')
    expect(result).toBeNull()
  })

  it('throws ApiError when a success response is not JSON', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue({
      ok: true, status: 200,
      text: () => Promise.resolve('<html>oops</html>'),
    } as any)

    await expect(api('/test')).rejects.toThrow(ApiError)
  })
})
