import createClient, { type Middleware } from 'openapi-fetch'
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

export const api = createClient<paths>({ baseUrl: '', fetch: authFetch })
api.use(throwOnError)
