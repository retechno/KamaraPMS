/**
 * The writes that succeeded, told to whoever listens, without the client knowing who: the night audit store listens so that the chip of the top bar does not fall behind a check-in or a
 * check-out done on any screen. (The client cannot import a store: the store imports the client.)
 */
type Listener = (method: string, path: string) => void

const listeners = new Set<Listener>()

/** Listens to the successful writes; returns the function that stops listening. */
export function onApiMutation(fn: Listener): () => void {
  listeners.add(fn)
  return () => listeners.delete(fn)
}

export function emitApiMutation(method: string, path: string): void {
  for (const fn of [...listeners]) fn(method, path)
}
