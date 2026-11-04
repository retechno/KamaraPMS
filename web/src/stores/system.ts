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
        apiStatus.value = failure(e)
        databaseStatus.value = 'unknown' // cannot tell while the API is unreachable
        lastError.value = asApiError(e)
        return
      }
      try {
        await api.GET('/readyz')
        databaseStatus.value = 'up'
      } catch (e) {
        databaseStatus.value = failure(e)
        lastError.value = asApiError(e)
      }
    } finally {
      checkedAt.value = new Date()
      checking.value = false
    }
  }

  return { apiStatus, databaseStatus, lastError, checkedAt, checking, check }
})

/** What a failed check says: down when nothing answered or the server said it is not well (5xx), unverified when it answered with something else (a 4xx: the route is not there, or not allowed). */
function failure(e: unknown): ComponentStatus {
  return e instanceof ApiError && e.status >= 400 && e.status < 500 ? 'unverified' : 'down'
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
