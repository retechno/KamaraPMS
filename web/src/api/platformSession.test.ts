import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { getPlatformToken, notifyPlatformSessionLost, onPlatformSessionLost, platformFetch, refreshPlatformSession, setPlatformToken } from './platformSession'
import { getAccessToken, setAccessToken } from './session'

function json(status: number, body: unknown): Response {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })
}

describe('platformFetch', () => {
  let calls: { url: string; auth: string | null }[]
  let responder: (url: string, auth: string | null) => Response

  beforeEach(() => {
    calls = []
    setPlatformToken(null)
    setAccessToken(null)
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: Request | string) => {
        const req = typeof input === 'string' ? new Request('http://localhost' + input, { method: 'POST' }) : input // the refresh is called with a path
        const auth = req.headers.get('Authorization')
        const url = new URL(req.url).pathname
        calls.push({ url, auth })
        return responder(url, auth)
      }),
    )
  })
  afterEach(() => vi.unstubAllGlobals())

  it('sends the operator token and never the hotel token', async () => {
    setPlatformToken('op-1')
    setAccessToken('hotel-1')
    responder = () => json(200, {})
    await platformFetch(new Request('http://localhost/api/v1/platform/tenants'))
    expect(calls[0]).toEqual({ url: '/api/v1/platform/tenants', auth: 'Bearer op-1' })
  })

  it('renews the session once on 401 through the platform refresh, and retries with the new token', async () => {
    setPlatformToken('expired')
    setAccessToken('hotel-1')
    responder = (url, auth) => {
      if (url === '/api/v1/platform/auth/refresh') return json(200, { access_token: 'fresh' })
      return auth === 'Bearer fresh' ? json(200, { ok: true }) : json(401, { code: 'PLATFORM_TOKEN_INVALID' })
    }
    const res = await platformFetch(new Request('http://localhost/api/v1/platform/plans', { method: 'POST', body: '{"a":1}' }))
    expect(res.status).toBe(200)
    expect(calls.map((c) => c.url)).toEqual(['/api/v1/platform/plans', '/api/v1/platform/auth/refresh', '/api/v1/platform/plans'])
    expect(getPlatformToken()).toBe('fresh')
    expect(getAccessToken()).toBe('hotel-1') // the hotel's session is untouched
  })

  it('shares one refresh between concurrent 401s', async () => {
    setPlatformToken('expired')
    responder = (url, auth) => {
      if (url === '/api/v1/platform/auth/refresh') return json(200, { access_token: 'fresh' })
      return auth === 'Bearer fresh' ? json(200, {}) : json(401, {})
    }
    await Promise.all(['a', 'b', 'c'].map((p) => platformFetch(new Request(`http://localhost/api/v1/platform/${p}`))))
    expect(calls.filter((c) => c.url === '/api/v1/platform/auth/refresh')).toHaveLength(1)
  })

  it('reports a lost session when the renewal fails, and clears the token', async () => {
    setPlatformToken('expired')
    const lost = vi.fn()
    onPlatformSessionLost(lost)
    responder = () => json(401, { code: 'PLATFORM_TOKEN_INVALID' })
    const res = await platformFetch(new Request('http://localhost/api/v1/platform/tenants'))
    expect(res.status).toBe(401)
    expect(lost).toHaveBeenCalledOnce()
    expect(getPlatformToken()).toBeNull()
  })

  it('never retries the public routes', async () => {
    responder = () => json(401, { code: 'INVALID_CREDENTIALS' })
    await platformFetch(new Request('http://localhost/api/v1/platform/auth/login', { method: 'POST', body: '{}' }))
    await platformFetch(new Request('http://localhost/api/v1/platform/auth/totp', { method: 'POST', body: '{}' }))
    expect(calls).toHaveLength(2)
  })

  it('refreshPlatformSession is null when signed out, and notifies nobody', async () => {
    const lost = vi.fn()
    onPlatformSessionLost(lost)
    responder = () => json(401, { code: 'REFRESH_TOKEN_MISSING' })
    expect(await refreshPlatformSession()).toBeNull()
    expect(lost).not.toHaveBeenCalled()
    notifyPlatformSessionLost()
    expect(lost).toHaveBeenCalledOnce()
  })
})
