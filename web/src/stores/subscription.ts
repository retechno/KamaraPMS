import { defineStore } from 'pinia'
import { computed, ref } from 'vue'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'

/**
 * The plan of the tenant (docs/architecture/26-subscriptions.md): which optional modules it has. The menu leaves out the pages of a module that is not in the plan, and a page of such a module
 * sends a person home. Until it is read nothing is hidden (the server decides in every case: a route of a module that is off answers 403 MODULE_NOT_IN_PLAN).
 */
export const useSubscriptionStore = defineStore('subscription', () => {
  const modules = ref<string[] | null>(null)
  const error = ref<ApiError | null>(null)
  let pending: Promise<void> | null = null

  async function load(force = false): Promise<void> {
    if (!force && modules.value !== null) return
    if (pending) return pending
    pending = (async () => {
      try {
        const { data } = await api.GET('/api/v1/subscription')
        modules.value = Array.isArray(data?.modules) ? data.modules.map(String) : null
        error.value = null
      } catch (e) {
        error.value = e instanceof ApiError ? e : null
      } finally {
        pending = null
      }
    })()
    return pending
  }

  /** Whether the plan has the module; true until the plan was read. CORE is always there. */
  const has = (module: string): boolean => module === 'CORE' || modules.value === null || modules.value.includes(module)

  function reset(): void {
    modules.value = null
    error.value = null
  }

  return { modules, error, load, has, reset, loaded: computed(() => modules.value !== null) }
})
