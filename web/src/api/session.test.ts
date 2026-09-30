import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { authFetch, getAccessToken, onSessionLost, refreshSession, setAccessToken } from './session'

function json(status: number, body: unknown): Response {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })
}

describe('authFetch', () => {
  let calls: { url: string; auth: string | null }[]
  let responder: (url: string, auth: string | null) => Response

  beforeEach(() => {
    calls = []
    setAccessToken(null)
    vi.stubGlobal('fetch', vi.fn(async (input: Request | string) => {
      const req = typeof input === 'string' ? new Request('http://localhost' + input, { method: 'POST' }) : input
      const auth = req.headers.get('Authorization')
      const url = new URL(req.url).pathname
      calls.push({ url, auth })
      return responder(url, auth)
    }))
  })
  afterEach(() => vi.unstubAllGlobals())

  it('sends the access token', async () => {
    setAccessToken('tok-1')
    responder = () => json(200, {})
    await authFetch(new Request('http://localhost/api/v1/auth/me'))
    expect(calls[0]).toEqual({ url: '/api/v1/auth/me', auth: 'Bearer tok-1' })
  })

  it('renews the session once on 401 and retries with the new token', async () => {
    setAccessToken('expired')
    responder = (url, auth) => {
      if (url === '/api/v1/auth/refresh') return json(200, { access_token: 'fresh' })
      return auth === 'Bearer fresh' ? json(200, { ok: true }) : json(401, { code: 'TOKEN_EXPIRED' })
    }
    const res = await authFetch(new Request('http://localhost/api/v1/properties', { method: 'POST', body: '{"a":1}' }))
    expect(res.status).toBe(200)
    expect(calls.map((c) => c.url)).toEqual(['/api/v1/properties', '/api/v1/auth/refresh', '/api/v1/properties'])
    expect(getAccessToken()).toBe('fresh')
  })

  it('shares one refresh between concurrent 401s', async () => {
    setAccessToken('expired')
    responder = (url, auth) => {
      if (url === '/api/v1/auth/refresh') return json(200, { access_token: 'fresh' })
      return auth === 'Bearer fresh' ? json(200, {}) : json(401, {})
    }
    await Promise.all([
      authFetch(new Request('http://localhost/api/v1/a')),
      authFetch(new Request('http://localhost/api/v1/b')),
      authFetch(new Request('http://localhost/api/v1/c')),
    ])
    expect(calls.filter((c) => c.url === '/api/v1/auth/refresh')).toHaveLength(1)
  })

  it('reports a lost session when renewal fails', async () => {
    setAccessToken('expired')
    const lost = vi.fn()
    onSessionLost(lost)
    responder = () => json(401, { code: 'SESSION_REVOKED' })
    const res = await authFetch(new Request('http://localhost/api/v1/properties'))
    expect(res.status).toBe(401)
    expect(lost).toHaveBeenCalledOnce()
    expect(getAccessToken()).toBeNull()
  })

  it('never retries sign-in endpoints', async () => {
    responder = () => json(401, { code: 'INVALID_CREDENTIALS' })
    await authFetch(new Request('http://localhost/api/v1/auth/login', { method: 'POST', body: '{}' }))
    expect(calls).toHaveLength(1)
  })

  it('refreshSession returns null when signed out', async () => {
    responder = () => json(401, { code: 'REFRESH_TOKEN_MISSING' })
    expect(await refreshSession()).toBeNull()
  })
})
