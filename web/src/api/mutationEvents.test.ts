import createClient from 'openapi-fetch'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { announceWrites } from './client'
import { onApiMutation } from './mutationEvents'
import type { paths } from './schema'

describe('the writes announced by the client', () => {
  const stops: Array<() => void> = []
  const send = vi.fn()
  // the same middleware as the app client, on a client that can build a request in node (the app one has a relative base)
  const client = createClient<paths>({ baseUrl: 'http://localhost', fetch: (r: Request) => send(r) })
  client.use(announceWrites)
  afterEach(() => {
    stops.splice(0).forEach((s) => s())
    send.mockReset()
  })

  function listen() {
    const heard = vi.fn()
    stops.push(onApiMutation(heard))
    return heard
  }
  const reply = (status: number) => new Response('{}', { status, headers: { 'content-type': 'application/json' } })
  const checkOut = () => client.POST('/api/v1/properties/{propertyId}/stays/{id}/check-out', { params: { path: { propertyId: 7, id: 2 }, header: { 'Idempotency-Key': 'k' } }, body: {} as never })

  it('tells the method and the path of a write that went through', async () => {
    const heard = listen()
    send.mockResolvedValue(reply(200))
    await checkOut()
    expect(heard).toHaveBeenCalledWith('POST', '/api/v1/properties/7/stays/2/check-out')
  })

  it('says nothing for a read, or for a write that failed', async () => {
    const heard = listen()
    send.mockResolvedValue(reply(200))
    await client.GET('/api/v1/properties/{propertyId}/night-audit/preview', { params: { path: { propertyId: 7 } } })
    send.mockResolvedValue(reply(409))
    await checkOut()
    expect(heard).not.toHaveBeenCalled()
  })

  it('stops telling once the listener is removed', async () => {
    const heard = vi.fn()
    onApiMutation(heard)()
    send.mockResolvedValue(reply(200))
    await checkOut()
    expect(heard).not.toHaveBeenCalled()
  })
})
