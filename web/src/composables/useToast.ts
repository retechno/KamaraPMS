import { readonly, ref } from 'vue'
import { ApiError } from '@/api/problem'

export type ToastKind = 'success' | 'error' | 'info'

export interface ToastItem {
  id: number
  kind: ToastKind
  message: string
}

/** How long a toast stays: a failure is kept longer so it can be read. A time of 0 keeps it until it is closed. */
const DEFAULT_MS: Record<ToastKind, number> = { success: 4000, info: 4000, error: 10000 }
/** The time a toast gets once the pointer leaves it (it is held while the pointer is on it, so it cannot go away under the reader). */
const AFTER_HOVER_MS = 4000

// One list for the whole application: any page or store can raise a toast, the host in the shell shows them.
const items = ref<ToastItem[]>([])
const timers = new Map<number, ReturnType<typeof setTimeout>>()
let nextId = 1

function stop(id: number): void {
  const timer = timers.get(id)
  if (timer !== undefined) clearTimeout(timer)
  timers.delete(id)
}

function dismiss(id: number): void {
  stop(id)
  items.value = items.value.filter((t) => t.id !== id)
}

function arm(id: number, ms: number): void {
  stop(id)
  if (ms > 0) timers.set(id, setTimeout(() => dismiss(id), ms))
}

function push(kind: ToastKind, message: string, ms?: number): number {
  // The same message that is on screen already is not stacked again (one failure raised twice, a button pressed twice): the one that is there stays, for another full time.
  const same = items.value.find((t) => t.kind === kind && t.message === message)
  const after = ms ?? DEFAULT_MS[kind]
  if (same) {
    arm(same.id, after)
    return same.id
  }
  const id = nextId++
  items.value = [...items.value, { id, kind, message }]
  arm(id, after)
  return id
}

function clear(): void {
  for (const id of [...timers.keys()]) stop(id)
  items.value = []
}

/** The text a person reads for a failure: the sentence of the server for the code (in the language of the page when it has one), and for a failure of the server itself the reference to give to support. */
export function errorText(e: ApiError): string {
  const text = e.message || e.code
  return e.status >= 500 && e.requestId ? `${text} (${e.requestId})` : text
}

/** Raise a toast: `toast.success('Saved')`. The shell's ToastHost shows them. */
export const toast = {
  success: (message: string, ms?: number) => push('success', message, ms),
  error: (message: string, ms?: number) => push('error', message, ms),
  info: (message: string, ms?: number) => push('info', message, ms),
  /**
   * A failure as a toast. An ApiError says what the server said (its message is safe to show: it is the sentence of the error, never a stack trace); anything else is a failure of the page and says that
   * something went wrong, without its details. Returns the ApiError, or null for anything else, so that a caller can keep it for the fields it names.
   */
  fromError(e: unknown, fallback = 'Something went wrong. Try again.'): ApiError | null {
    if (e instanceof ApiError) {
      push('error', errorText(e))
      return e
    }
    push('error', fallback)
    return null
  },
  /** Holds a toast while the pointer is on it, and gives it a short time once the pointer leaves. */
  hold: stop,
  release: (id: number) => {
    if (items.value.some((t) => t.id === id)) arm(id, AFTER_HOVER_MS)
  },
  dismiss,
  clear,
}

/** The toasts on screen (read only), for the host that shows them. */
export function useToasts() {
  return { items: readonly(items), dismiss }
}
