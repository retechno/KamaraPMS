import { onScopeDispose, ref, watch, type Ref } from 'vue'
import { authFetch } from '@/api/session'

/**
 * The realtime of the POS screens (docs/architecture/34-pos.md 10.1): a Server-Sent Events stream read with `fetch`, because the browser's EventSource
 * cannot send the Authorization header. An event says *what changed* (ids only); the screen reads the data again with its own permission.
 *
 * The stream resumes by id: the client keeps the id of the last event and sends it as `Last-Event-ID`, so a dropped connection loses nothing. Reconnects wait
 * 1, 2, 4 ... 15 seconds. The server ends a stream after ten minutes (the token is short-lived); that is only a reconnect. A resume point the server no longer
 * holds (410) means the screen must read everything again: `onReload`.
 */
export interface PosEvent {
  id: number
  channel: string
  type: string
  entity_id: number
  at: string
}

export type PosEventsState = 'connecting' | 'live' | 'offline'

export interface PosEventsOptions {
  /** The property of the stream; null keeps it closed. */
  propertyId: () => number | null
  /** The channels: outlet:{id}, station:{id}, frontdesk, printers. An empty list keeps the stream closed. */
  channels: () => string[]
  onEvent: (e: PosEvent) => void
  /** The screen has to read everything again: after a 410, and when the tab comes back after a long time away. */
  onReload?: () => void
}

/** Cuts a received text into complete frames; the rest is kept for the next chunk. Exported for the tests. */
export function parseFrames(buffer: string): { events: { id?: number; type: string; data: string }[]; rest: string } {
  const events: { id?: number; type: string; data: string }[] = []
  let rest = buffer.replace(/\r\n/g, '\n')
  for (;;) {
    const end = rest.indexOf('\n\n')
    if (end < 0) break
    const frame = rest.slice(0, end)
    rest = rest.slice(end + 2)
    let id: number | undefined
    let type = 'message'
    const data: string[] = []
    for (const line of frame.split('\n')) {
      if (line.startsWith(':')) continue // a comment: the heartbeat
      const i = line.indexOf(':')
      const field = i < 0 ? line : line.slice(0, i)
      const value = i < 0 ? '' : line.slice(i + 1).replace(/^ /, '')
      if (field === 'id') id = Number(value)
      else if (field === 'event') type = value
      else if (field === 'data') data.push(value)
    }
    if (data.length > 0 || type !== 'message') events.push({ id, type, data: data.join('\n') })
  }
  return { events, rest }
}

const MAX_BACKOFF_MS = 15_000
const AWAY_RELOAD_MS = 30_000

export function usePosEvents(opts: PosEventsOptions): { state: Ref<PosEventsState>; stop: () => void } {
  const state = ref<PosEventsState>('connecting')
  let abort: AbortController | null = null
  let lastId: number | null = null
  let backoff = 1000
  let timer: ReturnType<typeof setTimeout> | null = null
  let stopped = false
  let generation = 0
  let hiddenAt: number | null = null

  function clearTimer(): void {
    if (timer !== null) clearTimeout(timer)
    timer = null
  }

  function close(): void {
    clearTimer()
    abort?.abort()
    abort = null
  }

  async function run(gen: number): Promise<void> {
    const propertyId = opts.propertyId()
    const channels = opts.channels()
    if (stopped || propertyId === null || channels.length === 0) {
      state.value = 'offline'
      return
    }
    abort = new AbortController()
    const signal = abort.signal
    state.value = 'connecting'
    let gone = false
    try {
      const headers: Record<string, string> = { Accept: 'text/event-stream' }
      if (lastId !== null) headers['Last-Event-ID'] = String(lastId)
      const url = `/api/v1/properties/${propertyId}/pos/events?channels=${encodeURIComponent(channels.join(','))}`
      const res = await authFetch(new Request(new URL(url, globalThis.location?.origin ?? 'http://localhost'), { headers, signal }))
      if (res.status === 410) {
        gone = true
        lastId = null
      } else if (!res.ok || !res.body) {
        throw new Error(`stream ${res.status}`)
      } else {
        state.value = 'live'
        backoff = 1000
        const reader = res.body.getReader()
        const decoder = new TextDecoder()
        let buffer = ''
        for (;;) {
          const { done, value } = await reader.read()
          if (done) break
          buffer += decoder.decode(value, { stream: true })
          const parsed = parseFrames(buffer)
          buffer = parsed.rest
          for (const f of parsed.events) {
            if (f.type === 'reconnect') continue
            if (f.id !== undefined && Number.isFinite(f.id)) lastId = f.id
            if (!f.data) continue
            try {
              opts.onEvent(JSON.parse(f.data) as PosEvent)
            } catch {
              // a frame that is not ours is skipped
            }
          }
        }
      }
    } catch {
      if (signal.aborted) return
    }
    if (stopped || gen !== generation || signal.aborted) return
    state.value = 'offline'
    if (gone) {
      opts.onReload?.()
      backoff = 1000
    }
    timer = setTimeout(() => void run(gen), gone ? 0 : backoff)
    if (!gone) backoff = Math.min(backoff * 2, MAX_BACKOFF_MS)
  }

  function restart(): void {
    close()
    generation++
    lastId = null
    backoff = 1000
    void run(generation)
  }

  function onVisibility(): void {
    if (typeof document === 'undefined') return
    if (document.hidden) {
      hiddenAt = Date.now()
      return
    }
    if (hiddenAt !== null && Date.now() - hiddenAt > AWAY_RELOAD_MS) opts.onReload?.()
    hiddenAt = null
  }
  if (typeof document !== 'undefined') document.addEventListener('visibilitychange', onVisibility)

  watch(() => [opts.propertyId(), opts.channels().join(',')], restart, { immediate: true })

  function stop(): void {
    stopped = true
    close()
    state.value = 'offline'
    if (typeof document !== 'undefined') document.removeEventListener('visibilitychange', onVisibility)
  }
  onScopeDispose(stop)
  return { state, stop }
}

/** Calls fn once after a burst of calls: a send makes many events and the screen reads once. */
export function debounce(fn: () => void, ms: number): () => void {
  let timer: ReturnType<typeof setTimeout> | null = null
  return () => {
    if (timer !== null) clearTimeout(timer)
    timer = setTimeout(() => {
      timer = null
      fn()
    }, ms)
  }
}
