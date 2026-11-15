import createClient from 'openapi-fetch'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { onApiMutation } from './mutationEvents'
import { platformApi, throwOnPlatformError } from './platformClient'
import { setPlatformToken } from './platformSession'
import { ApiError } from './problem'
import { onSubscriptionAccess } from './subscriptionEvents'

describe('the platform client', () => {
  const stops: Array<() => void> = []
  afterEach(() => {
    stops.splice(0).forEach((s) => s())
    setPlatformToken(null)
    vi.unstubAllGlobals()
  })

  function serve(status: number, body: unknown, headers: Record<string, string> = {}) {
    const f = vi.fn(async (_: Request) => new Response(JSON.stringify(body), { status, headers: { 'content-type': 'application/json', ...headers } }))
    vi.stubGlobal('fetch', f)
    return f
  }

  it('throws an ApiError with the stable code of a failed answer', async () => {
    serve(422, { type: 't', title: 'T', status: 422, code: 'TOTP_INVALID', detail: 'no', request_id: 'r', context: { attempts_left: 3 } })
    const err = await platformApi.POST('/api/v1/platform/auth/totp', { body: { challenge: 'c', code: '1' }, baseUrl: 'http://localhost' }).catch((e: unknown) => e)
    expect(err).toBeInstanceOf(ApiError)
    expect((err as ApiError).code).toBe('TOTP_INVALID')
    expect((err as ApiError).context).toEqual({ attempts_left: 3 })
  })

  it('has the middleware of the console only: no night audit announcement and no subscription state of the hotel', async () => {
    const mutated = vi.fn()
    const access = vi.fn()
    stops.push(onApiMutation(mutated), onSubscriptionAccess(access))
    serve(200, { ok: true }, { 'X-Subscription-Access': 'READ_ONLY' })
    setPlatformToken('op')
    await platformApi.POST('/api/v1/platform/plans', { body: {} as never, baseUrl: 'http://localhost' })
    expect(mutated).not.toHaveBeenCalled()
    expect(access).not.toHaveBeenCalled()
  })

  it('carries the operator token', async () => {
    const f = serve(200, { items: [] })
    setPlatformToken('op-token')
    await platformApi.GET('/api/v1/platform/plans', { baseUrl: 'http://localhost' })
    expect(f.mock.calls[0]![0].headers.get('Authorization')).toBe('Bearer op-token')
  })

  it('throws last: an observer registered after the thrower sees an error answer before it throws', async () => {
    // openapi-fetch runs onResponse from the last middleware to the first.
    const seen: string[] = []
    const c = createClient<Record<string, never>>({ baseUrl: 'http://localhost', fetch: async () => new Response('{}', { status: 500 }) })
    c.use({
      async onResponse(o) {
        seen.push('thrower')
        return throwOnPlatformError.onResponse?.(o)
      },
    })
    c.use({
      onResponse() {
        seen.push('observer')
        return undefined
      },
    })
    await expect(c.GET('/x' as never, {} as never)).rejects.toBeInstanceOf(ApiError)
    expect(seen).toEqual(['observer', 'thrower'])
  })
})
