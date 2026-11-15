import createClient, { type Middleware } from 'openapi-fetch'
import { platformFetch } from './platformSession'
import { toApiError } from './problem'
import type { paths } from './schema'

/**
 * Typed client of the platform console (`/api/v1/platform/`). It has its own fetch (the operator's token, never the hotel's) and its own middleware: the hotel's client
 * announces writes to the night audit chip and reads the subscription header, and none of that belongs to the console. Every non-2xx answer is thrown as an ApiError.
 */
export const throwOnPlatformError: Middleware = {
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

export const platformApi = createClient<paths>({ baseUrl: '', fetch: platformFetch })
// openapi-fetch runs `onResponse` from the last middleware to the first: a middleware added later sees the answer before the thrower does, so any
// future observer of the answers must be registered AFTER this one.
platformApi.use(throwOnPlatformError)
