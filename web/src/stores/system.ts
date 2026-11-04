import { defineStore } from 'pinia'
import { ref } from 'vue'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'

/** `unverified`: the answer was not the one of a health check (a proxy that does not pass the route on, a login that is asked): that does not say the part is down. */
export type ComponentStatus = 'unknown' | 'up' | 'down' | 'unverified'

/** Live health of the backend, shown on the system page of the administrator. */
export const useSystemStore = defineStore('system', () => {
  const apiStatus = ref<ComponentStatus>('unknown')
  const databaseStatus = ref<ComponentStatus>('unknown')
  const lastError = ref<ApiError | null>(null)
  const checkedAt = ref<Date | null>(null)
  const checking = ref(false)

  async function check(): Promise<void> {
    checking.value = true
    lastError.value = null
    try {
      try {
        await api.GET('/healthz')
        apiStatus.value = 'up'
      } catch (e) {
        apiStatus.value = await failure(e)
        databaseStatus.value = 'unknown' // cannot tell while the API is unreachable
        lastError.value = asApiError(e)
        return
      }
      try {
        await api.GET('/readyz')
        databaseStatus.value = 'up'
      } catch (e) {
        databaseStatus.value = await failure(e, true)
        lastError.value = asApiError(e)
      }
    } finally {
      checkedAt.value = new Date()
      checking.value = false
    }
  }

  return { apiStatus, databaseStatus, lastError, checkedAt, checking, check }
})

/**
 * What a failed check says: unverified when the server answered with something else than a health check (a 4xx: the route is not there, or not allowed); down when nothing answered or it said it is not well (5xx).
 * A proxy often passes only the routes of the application, so before calling a part down the check asks a route that is certainly the application's (`/auth/me`): any answer of it, even a 401, means the server is there and only the health route did not get through.
 * `/readyz` answering 503 is the database itself saying so: that stays down.
 */
async function failure(e: unknown, readiness = false): Promise<ComponentStatus> {
  if (e instanceof ApiError && e.status >= 400 && e.status < 500) return 'unverified'
  if (readiness && e instanceof ApiError && e.status === 503) return 'down'
  return (await applicationAnswers()) ? 'unverified' : 'down'
}

async function applicationAnswers(): Promise<boolean> {
  try {
    await api.GET('/api/v1/auth/me')
    return true
  } catch (e) {
    return e instanceof ApiError && e.status < 500
  }
}

function asApiError(e: unknown): ApiError {
  if (e instanceof ApiError) return e
  // fetch rejects with a TypeError when the server is unreachable.
  return new ApiError({
    type: 'about:blank',
    title: 'Network error',
    status: 0,
    code: 'NETWORK_ERROR',
    detail: 'The server could not be reached.',
  })
}
