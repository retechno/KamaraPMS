import createClient from 'openapi-fetch'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { watchSubscriptionAccess } from './client'
import { onSubscriptionAccess } from './subscriptionEvents'
import type { paths } from './schema'

describe('the access state reported by the client', () => {
  const stops: Array<() => void> = []
  const send = vi.fn()
  const client = createClient<paths>({ baseUrl: 'http://localhost', fetch: (r: Request) => send(r) })
  client.use(watchSubscriptionAccess)
  afterEach(() => {
    stops.splice(0).forEach((s) => s())
    send.mockReset()
  })

  function listen() {
    const heard = vi.fn()
    stops.push(onSubscriptionAccess(heard))
    return heard
  }
  const json = (status: number, body: unknown, access?: string) =>
    new Response(JSON.stringify(body), { status, headers: { 'content-type': 'application/json', ...(access ? { 'X-Subscription-Access': access } : {}) } })

  it('passes on the header of every response', async () => {
    const heard = listen()
    send.mockResolvedValue(json(200, {}, 'GRACE'))
    await client.GET('/api/v1/subscription')
    expect(heard).toHaveBeenCalledWith('GRACE', false)
  })

  it('says null when the response has no header', async () => {
    const heard = listen()
    send.mockResolvedValue(json(200, {}))
    await client.GET('/api/v1/subscription')
    expect(heard).toHaveBeenCalledWith(null, false)
  })

  it('knows a write refused because the subscription is read only, and the body stays readable', async () => {
    const heard = listen()
    send.mockResolvedValue(json(403, { code: 'SUBSCRIPTION_READ_ONLY' }, 'READ_ONLY'))
    const { error } = await client.GET('/api/v1/subscription')
    expect(heard).toHaveBeenCalledWith('READ_ONLY', true)
    expect(error).toMatchObject({ code: 'SUBSCRIPTION_READ_ONLY' })
  })

  it('does not take another refusal for it, nor a body that is not JSON', async () => {
    const heard = listen()
    send.mockResolvedValueOnce(json(403, { code: 'PERMISSION_DENIED' }, 'FULL'))
    await client.GET('/api/v1/subscription')
    expect(heard).toHaveBeenLastCalledWith('FULL', false)
    send.mockResolvedValueOnce(new Response('<html>', { status: 403, headers: { 'X-Subscription-Access': 'FULL' } }))
    await client.GET('/api/v1/subscription')
    expect(heard).toHaveBeenLastCalledWith('FULL', false)
  })
})
