/**
 * What the server says of the subscription on a response, told to whoever listens, without the client knowing who: the subscription store listens so that the banner follows the paid period
 * (a plan renewed, a grace that ran out) without waiting for a reload. (The client cannot import a store: the store imports the client.)
 */
type Listener = (access: string | null, readOnlyRefused: boolean) => void

const listeners = new Set<Listener>()

/** Listens to the access state of the responses; returns the function that stops listening. */
export function onSubscriptionAccess(fn: Listener): () => void {
  listeners.add(fn)
  return () => listeners.delete(fn)
}

/** `access` is the `X-Subscription-Access` header (null when the response has none); `readOnlyRefused` is a 403 SUBSCRIPTION_READ_ONLY. */
export function emitSubscriptionAccess(access: string | null, readOnlyRefused: boolean): void {
  for (const fn of [...listeners]) fn(access, readOnlyRefused)
}
