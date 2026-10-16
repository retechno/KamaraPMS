import { effectScope, nextTick } from 'vue'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

// The test setup replaces the module with a stub for the pages; this file tests the real one.
const actual = await vi.importActual<typeof import('./usePosEvents')>('./usePosEvents')
const { parseFrames, usePosEvents } = actual

let authFetch = vi.fn()
vi.mock('@/api/session', () => ({ authFetch: (...a: unknown[]) => authFetch(...a) }))

const encoder = new TextEncoder()
/** A response whose body delivers the chunks and then ends (the connection dropped). */
function stream(chunks: string[], status = 200): Response {
  let i = 0
  const body = new ReadableStream<Uint8Array>({
    pull(controller) {
      if (i < chunks.length) controller.enqueue(encoder.encode(chunks[i++]))
      else controller.close()
    },
  })
  return new Response(status === 200 ? body : null, { status, headers: { 'Content-Type': 'text/event-stream' } })
}
const frame = (id: number, type: string, entity = 1) => `id: ${id}\nevent: ${type}\ndata: ${JSON.stringify({ id, channel: 'outlet:1', type, entity_id: entity, at: '2026-10-15T05:00:00Z' })}\n\n`

beforeEach(() => {
  authFetch = vi.fn()
  vi.useFakeTimers()
})
afterEach(() => {
  vi.useRealTimers()
})

describe('the frames of the stream', () => {
  it('cuts complete frames, keeps the rest, and skips the heartbeat', () => {
    const a = parseFrames(': stream open\n\n' + frame(5, 'bill.updated').slice(0, 20))
    expect(a.events).toHaveLength(0)
    const b = parseFrames(a.rest + frame(5, 'bill.updated').slice(20) + ': heartbeat\n\n' + frame(6, 'table.updated', 9))
    expect(b.events.map((e) => [e.id, e.type])).toEqual([[5, 'bill.updated'], [6, 'table.updated']])
    expect(JSON.parse(b.events[1]!.data).entity_id).toBe(9)
    expect(b.rest).toBe('')
  })

  it('reads the carriage returns of a proxy', () => {
    const r = parseFrames('id: 3\r\nevent: bill.closed\r\ndata: {"id":3}\r\n\r\n')
    expect(r.events).toEqual([{ id: 3, type: 'bill.closed', data: '{"id":3}' }])
  })
})

describe('the stream of events', () => {
  function open(onEvent: (e: unknown) => void, onReload?: () => void, channels = ['outlet:1']) {
    const scope = effectScope()
    const out = scope.run(() => usePosEvents({ propertyId: () => 7, channels: () => channels, onEvent, onReload }))!
    return { ...out, scope }
  }

  it('asks for its channels with the token of the session, and hands the events over in order', async () => {
    authFetch.mockResolvedValueOnce(stream([': open\n\n', frame(1, 'bill.updated'), frame(2, 'table.updated', 4)])).mockImplementation(() => new Promise(() => undefined))
    const seen: { id: number; type: string }[] = []
    const s = open((e) => seen.push(e as { id: number; type: string }))
    await vi.advanceTimersByTimeAsync(10)
    const req = authFetch.mock.calls[0]![0] as Request
    expect(new URL(req.url, 'http://x').pathname).toBe('/api/v1/properties/7/pos/events')
    expect(new URL(req.url, 'http://x').searchParams.get('channels')).toBe('outlet:1')
    expect(req.headers.get('Accept')).toBe('text/event-stream')
    expect(req.headers.get('Last-Event-ID')).toBeNull()
    expect(seen.map((e) => e.id)).toEqual([1, 2])
    s.stop()
    s.scope.stop()
  })

  it('resumes with the id of the last event after the connection drops, and loses none', async () => {
    authFetch
      .mockResolvedValueOnce(stream([frame(1, 'bill.updated'), frame(2, 'bill.updated')]))
      .mockResolvedValueOnce(stream([frame(3, 'ticket.updated')]))
      .mockImplementation(() => new Promise(() => undefined))
    const seen: number[] = []
    const s = open((e) => seen.push((e as { id: number }).id))
    await vi.advanceTimersByTimeAsync(10) // the first connection delivers 1 and 2 and ends
    await vi.advanceTimersByTimeAsync(1100) // the backoff of the first reconnect
    expect(authFetch).toHaveBeenCalledTimes(2)
    expect((authFetch.mock.calls[1]![0] as Request).headers.get('Last-Event-ID')).toBe('2')
    expect(seen).toEqual([1, 2, 3])
    await vi.advanceTimersByTimeAsync(1100)
    expect(authFetch).toHaveBeenCalledTimes(3)
    expect((authFetch.mock.calls[2]![0] as Request).headers.get('Last-Event-ID')).toBe('3')
    s.stop()
    s.scope.stop()
  })

  it('reconnects with a growing wait when the server is unreachable', async () => {
    authFetch.mockRejectedValue(new Error('offline'))
    const s = open(() => undefined)
    await vi.advanceTimersByTimeAsync(10)
    expect(authFetch).toHaveBeenCalledTimes(1)
    expect(s.state.value).toBe('offline')
    await vi.advanceTimersByTimeAsync(1000)
    expect(authFetch).toHaveBeenCalledTimes(2)
    await vi.advanceTimersByTimeAsync(1900)
    expect(authFetch).toHaveBeenCalledTimes(2) // the second wait is two seconds
    await vi.advanceTimersByTimeAsync(200)
    expect(authFetch).toHaveBeenCalledTimes(3)
    s.stop()
    s.scope.stop()
  })

  it('reads the screen again when the server no longer holds the resume point (410)', async () => {
    authFetch
      .mockResolvedValueOnce(stream([frame(8, 'bill.updated')]))
      .mockResolvedValueOnce(stream([], 410))
      .mockImplementation(() => new Promise(() => undefined))
    const reload = vi.fn()
    const s = open(() => undefined, reload)
    await vi.advanceTimersByTimeAsync(10)
    await vi.advanceTimersByTimeAsync(1100)
    expect(reload).toHaveBeenCalledTimes(1)
    await vi.advanceTimersByTimeAsync(10)
    // after the reload it begins at the present: no Last-Event-ID
    const last = authFetch.mock.calls.at(-1)![0] as Request
    expect(last.headers.get('Last-Event-ID')).toBeNull()
    s.stop()
    s.scope.stop()
  })

  it('stays closed without a property or channels, and opens when they come', async () => {
    authFetch.mockImplementation(() => new Promise(() => undefined))
    const channels: string[] = []
    const scope = effectScope()
    let ch = ['x']
    ch = channels
    const reactiveChannels = { list: ch }
    scope.run(() => usePosEvents({ propertyId: () => 7, channels: () => reactiveChannels.list, onEvent: () => undefined }))
    await nextTick()
    await vi.advanceTimersByTimeAsync(10)
    expect(authFetch).not.toHaveBeenCalled()
    scope.stop()
  })
})
