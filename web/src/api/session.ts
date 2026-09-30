import type { components } from './schema'

export type Session = components['schemas']['Session']

/**
 * Session plumbing shared by the API client and the auth store.
 *
 * - The access token lives only in memory (never localStorage): an XSS bug
 *   cannot read a long-lived credential.
 * - The refresh token is an httpOnly cookie the browser sends to /api/v1/auth
 *   only; JavaScript never sees it.
 * - Concurrent 401s share a single refresh request (single flight).
 */
let accessToken: string | null = null
let inFlight: Promise<Session | null> | null = null
let sessionLostHandler: (() => void) | null = null

export function getAccessToken(): string | null {
  return accessToken
}

export function setAccessToken(token: string | null): void {
  accessToken = token
}

/** Called when the session cannot be renewed (e.g. to show the login page). */
export function onSessionLost(handler: () => void): void {
  sessionLostHandler = handler
}

export function notifySessionLost(): void {
  accessToken = null
  sessionLostHandler?.()
}

/** Exchanges the refresh cookie for a new access token. Returns null if signed out. */
export function refreshSession(): Promise<Session | null> {
  if (!inFlight) {
    inFlight = (async () => {
      try {
        const res = await fetch('/api/v1/auth/refresh', { method: 'POST', credentials: 'same-origin' })
        if (!res.ok) {
          accessToken = null
          return null
        }
        const session = (await res.json()) as Session
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

const AUTH_ENDPOINTS = ['/api/v1/auth/login', '/api/v1/auth/refresh', '/api/v1/auth/logout']

function withToken(request: Request): Request {
  if (!accessToken) return request
  const headers = new Headers(request.headers)
  headers.set('Authorization', `Bearer ${accessToken}`)
  return new Request(request, { headers })
}

/**
 * fetch used by the API client: adds the access token and, on 401, renews the
 * session once and retries. Sign-in endpoints are never retried.
 */
export async function authFetch(request: Request): Promise<Response> {
  const path = new URL(request.url, 'http://localhost').pathname
  if (AUTH_ENDPOINTS.includes(path)) return fetch(request)

  const retry = request.clone() // a body can only be read once
  const res = await fetch(withToken(request))
  if (res.status !== 401) return res

  const renewed = await refreshSession()
  if (!renewed) {
    notifySessionLost()
    return res
  }
  return fetch(withToken(retry))
}
