import { afterEach, describe, expect, it, vi } from 'vitest'
import { api } from './client'
import { ApiError } from './problem'
import { onSubscriptionAccess } from './subscriptionEvents'

describe('the exported client and the access state', () => {
  const stops: Array<() => void> = []
  afterEach(() => {
    stops.splice(0).forEach((s) => s())
    vi.unstubAllGlobals()
  })

  function serve(status: number, body: unknown, access: string) {
    vi.stubGlobal('fetch', vi.fn(async () => new Response(JSON.stringify(body), { status, headers: { 'content-type': 'application/json', 'X-Subscription-Access': access } })))
  }

  it('hears a refusal because the subscription is read only, and the call still rejects with an ApiError', async () => {
    const heard = vi.fn()
    stops.push(onSubscriptionAccess(heard))
    serve(403, { type: 't', title: 'T', status: 403, code: 'SUBSCRIPTION_READ_ONLY', detail: 'read only', request_id: 'r' }, 'READ_ONLY')
    const call = api.POST('/api/v1/subscription' as never, { body: {}, baseUrl: 'http://localhost' } as never)
    await expect(call).rejects.toBeInstanceOf(ApiError)
    expect(heard).toHaveBeenCalledWith('READ_ONLY', true)
  })

  it('hears the header of an error response that is not that refusal', async () => {
    const heard = vi.fn()
    stops.push(onSubscriptionAccess(heard))
    serve(500, { type: 't', title: 'T', status: 500, code: 'INTERNAL', detail: 'boom', request_id: 'r' }, 'GRACE')
    await expect(api.GET('/api/v1/subscription', { baseUrl: 'http://localhost' })).rejects.toBeInstanceOf(ApiError)
    expect(heard).toHaveBeenCalledWith('GRACE', false)
  })
})
