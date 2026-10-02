import { readonly, ref } from 'vue'

export type ToastKind = 'success' | 'error' | 'info'

export interface ToastItem {
  id: number
  kind: ToastKind
  message: string
}

/** How long a toast stays: a failure is kept longer so it can be read. A time of 0 keeps it until it is closed. */
const DEFAULT_MS: Record<ToastKind, number> = { success: 4000, info: 4000, error: 8000 }

// One list for the whole application: any page or store can raise a toast, the host in the shell shows them.
const items = ref<ToastItem[]>([])
const timers = new Map<number, ReturnType<typeof setTimeout>>()
let nextId = 1

function dismiss(id: number): void {
  const timer = timers.get(id)
  if (timer !== undefined) clearTimeout(timer)
  timers.delete(id)
  items.value = items.value.filter((t) => t.id !== id)
}

function push(kind: ToastKind, message: string, ms?: number): number {
  const id = nextId++
  items.value = [...items.value, { id, kind, message }]
  const after = ms ?? DEFAULT_MS[kind]
  if (after > 0) timers.set(id, setTimeout(() => dismiss(id), after))
  return id
}

function clear(): void {
  for (const id of [...timers.keys()]) dismiss(id)
  items.value = []
}

/** Raise a toast: `toast.success('Saved')`. The shell's ToastHost shows them. */
export const toast = {
  success: (message: string, ms?: number) => push('success', message, ms),
  error: (message: string, ms?: number) => push('error', message, ms),
  info: (message: string, ms?: number) => push('info', message, ms),
  dismiss,
  clear,
}

/** The toasts on screen (read only), for the host that shows them. */
export function useToasts() {
  return { items: readonly(items), dismiss }
}
