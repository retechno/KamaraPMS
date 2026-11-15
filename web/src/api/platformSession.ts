import type { components } from './schema'

export type PlatformSession = components['schemas']['PlatformSession']

/**
 * The session of an operator of the platform console (docs/architecture/39-platform-console.md 11). It is separate from the hotel's (`session.ts`): its own access token, its
 * own refresh cookie (`pms_platform_refresh`, sent to /api/v1/platform/auth only) and its own lost-session handler. Nothing here reads or writes the hotel's token.
 *
 * The access token lives only in memory; JavaScript never sees the refresh cookie; concurrent 401s share one refresh.
 */
let accessToken: string | null = null
let inFlight: Promise<PlatformSession | null> | null = null
let sessionLostHandler: (() => void) | null = null

export function getPlatformToken(): string | null {
  return accessToken
}

export function setPlatformToken(token: string | null): void {
  accessToken = token
}

/** Called when the session cannot be renewed (to show the sign-in page). */
export function onPlatformSessionLost(handler: () => void): void {
  sessionLostHandler = handler
}

export function notifyPlatformSessionLost(): void {
  accessToken = null
  sessionLostHandler?.()
}

/** Exchanges the refresh cookie for a new access token. Null when signed out. */
export function refreshPlatformSession(): Promise<PlatformSession | null> {
  if (!inFlight) {
    inFlight = (async () => {
      try {
        const res = await fetch('/api/v1/platform/auth/refresh', { method: 'POST', credentials: 'same-origin' })
        if (!res.ok) {
          accessToken = null
          return null
        }
        const session = (await res.json()) as PlatformSession
        accessToken = session.access_token
        return session
      } catch {
        return null
      }
    })().finally(() => {
      inFlight = null
    })
  }
  return inFlight
}

// The routes that answer without a token: a 401 there is the answer, not an expired token.
const PUBLIC_ENDPOINTS = [
  '/api/v1/platform/auth/login',
  '/api/v1/platform/auth/totp',
  '/api/v1/platform/auth/enroll/start',
  '/api/v1/platform/auth/enroll/confirm',
  '/api/v1/platform/auth/refresh',
  '/api/v1/platform/auth/logout',
]

function withToken(request: Request): Request {
  if (!accessToken) return request
  const headers = new Headers(request.headers)
  headers.set('Authorization', `Bearer ${accessToken}`)
  return new Request(request, { headers })
}

/** fetch of the platform client: adds the access token and, on 401, renews the session once and retries. The public routes are never retried. */
export async function platformFetch(request: Request): Promise<Response> {
  const path = new URL(request.url, 'http://localhost').pathname
  if (PUBLIC_ENDPOINTS.includes(path)) return fetch(request)

  const retry = request.clone() // a body can only be read once
  const res = await fetch(withToken(request))
  if (res.status !== 401) return res

  const renewed = await refreshPlatformSession()
  if (!renewed) {
    notifyPlatformSessionLost()
    return res
  }
  return fetch(withToken(retry))
}
