import { defineStore } from 'pinia'
import { ref } from 'vue'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'

export type ComponentStatus = 'unknown' | 'up' | 'down'

/** Live health of the backend, shown on the dashboard. */
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
        apiStatus.value = 'down'
        databaseStatus.value = 'unknown' // cannot tell while the API is unreachable
        lastError.value = asApiError(e)
        return
      }
      try {
        await api.GET('/readyz')
        databaseStatus.value = 'up'
      } catch (e) {
        databaseStatus.value = 'down'
        lastError.value = asApiError(e)
      }
    } finally {
      checkedAt.value = new Date()
      checking.value = false
    }
  }

  return { apiStatus, databaseStatus, lastError, checkedAt, checking, check }
})

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
