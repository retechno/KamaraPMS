import createClient, { type Middleware } from 'openapi-fetch'
import { emitApiMutation } from './mutationEvents'
import { toApiError } from './problem'
import type { paths } from './schema'
import { authFetch } from './session'

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

export const api = createClient<paths>({ baseUrl: '', fetch: authFetch })
api.use(throwOnError)
api.use(announceWrites)
