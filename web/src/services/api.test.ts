import { describe, it, expect, beforeEach, vi } from 'vitest'
import { setAuthToken, getAuthToken, setOnUnauthorized, api, ApiError } from './api'

// Track token in a variable so our mocks can interact with it
let currentToken: string | null = null

beforeEach(() => {
  localStorage.clear()
  currentToken = null
  setAuthToken(null)
  setOnUnauthorized(() => {})
})

describe('setAuthToken / getAuthToken', () => {
  it('stores token in memory and localStorage', () => {
    setAuthToken('test-token')
    expect(getAuthToken()).toBe('test-token')
    expect(localStorage.getItem('digesterr-token')).toBe('test-token')
  })

  it('removes token from localStorage', () => {
    setAuthToken('test-token')
    setAuthToken(null)
    expect(getAuthToken()).toBeNull()
    expect(localStorage.getItem('digesterr-token')).toBeNull()
  })

  it('falls back to localStorage on page reload', () => {
    localStorage.setItem('digesterr-token', 'saved-token')
    expect(getAuthToken()).toBe('saved-token')
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
      json: () => Promise.resolve({ error: 'unauthorized' }),
    } as any)

    await expect(api('/protected')).rejects.toThrow()
    expect(called).toBe(true)
  })

  it('clears token on 401', async () => {
    setAuthToken('old-token')
    vi.spyOn(globalThis, 'fetch').mockResolvedValue({
      ok: false, status: 401,
      json: () => Promise.resolve({ error: 'unauthorized' }),
    } as any)

    await expect(api('/protected')).rejects.toThrow()
    expect(getAuthToken()).toBeNull()
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
