import createClient, { type Middleware } from 'openapi-fetch'
import { emitApiMutation } from './mutationEvents'
import { toApiError } from './problem'
import type { paths } from './schema'
import { authFetch } from './session'
import { emitSubscriptionAccess } from './subscriptionEvents'

/**
 * Typed API client generated from api/openapi.yaml (run `npm run gen:api`).
 * Requests carry the in-memory access token (renewed transparently on 401), and
 * every non-2xx response is thrown as an ApiError, so callers only handle success.
 */
const throwOnError: Middleware = {
  async onResponse({ response }) {
    if (response.ok) return undefined
    let body: unknown
    try {
      body = await response.clone().json()
    } catch {
      body = undefined
    }
    throw toApiError(body, response.status)
  },
}

/** Tells the listeners (the night audit chip) that a write went through; a read and a failure say nothing. */
export const announceWrites: Middleware = {
  onResponse({ request, response }) {
    if (!response.ok || request.method === 'GET' || request.method === 'HEAD') return undefined
    try {
      emitApiMutation(request.method, new URL(request.url, 'http://localhost').pathname)
    } catch {
      // a listener must never break the call it listens to
    }
    return undefined
  },
}

/**
 * Tells the listeners (the subscription store) what the server says of the access state: every authenticated response carries `X-Subscription-Access`, and a write that was refused because
 * the subscription is read only says so in its body. It reads a copy of the body and only for a 403, and it never throws. It runs before `throwOnError`, which throws on a refusal.
 */
export const watchSubscriptionAccess: Middleware = {
  async onResponse({ response }) {
    try {
      const refused = response.status === 403 && ((await response.clone().json()) as { code?: unknown } | null)?.code === 'SUBSCRIPTION_READ_ONLY'
      emitSubscriptionAccess(response.headers.get('X-Subscription-Access'), refused)
    } catch {
      emitSubscriptionAccess(response.headers.get('X-Subscription-Access'), false) // a body that is not JSON is not a refusal of ours
    }
    return undefined
  },
}

export const api = createClient<paths>({ baseUrl: '', fetch: authFetch })
api.use(watchSubscriptionAccess)
api.use(throwOnError)
api.use(announceWrites)
