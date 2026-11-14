import { defineStore } from 'pinia'
import { computed, ref } from 'vue'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import { onSubscriptionAccess } from '@/api/subscriptionEvents'
import type { SubscriptionSummary } from '@/api/types'

/**
 * The plan of the tenant (docs/architecture/26-subscriptions.md): which optional modules it has, and (docs/architecture/39-platform-console.md §10) where its paid period stands. The menu leaves
 * out the pages of a module that is not in the plan, and a page of such a module sends a person home. Until it is read nothing is hidden (the server decides in every case: a route of a
 * module that is off answers 403 MODULE_NOT_IN_PLAN). The banner and the Subscription page read the access state, the days and the dates from here, never from the browser's clock.
 */
export const useSubscriptionStore = defineStore('subscription', () => {
  const summary = ref<SubscriptionSummary | null>(null)
  const modules = ref<string[] | null>(null)
  const error = ref<ApiError | null>(null)
  let pending: Promise<void> | null = null

  async function load(force = false): Promise<void> {
    if (!force && modules.value !== null) return
    if (pending) return pending
    pending = (async () => {
      try {
        const { data } = await api.GET('/api/v1/subscription')
        summary.value = data ?? null
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
    summary.value = null
    modules.value = null
    error.value = null
  }

  let timer: ReturnType<typeof setTimeout> | undefined

  /**
   * Reloads the summary when a response says the access state is not the one that is held (debounced: a page that fires ten calls reloads once), or when a write was refused as read only.
   * Returns the function that stops listening; the shell listens while it is on screen. Nothing happens before the first load: that one is already on its way.
   */
  function watchAccess(): () => void {
    const off = onSubscriptionAccess((access, readOnlyRefused) => {
      if (summary.value === null) return
      if (!readOnlyRefused && (access === null || access === summary.value.access)) return
      clearTimeout(timer)
      timer = setTimeout(() => void load(true), 1000)
    })
    return () => {
      off()
      clearTimeout(timer)
    }
  }

  return {
    summary,
    watchAccess,
    modules,
    error,
    load,
    has,
    reset,
    loaded: computed(() => modules.value !== null),
    access: computed(() => summary.value?.access ?? null),
    daysLeft: computed(() => summary.value?.days_left ?? null),
    paidThrough: computed(() => summary.value?.paid_through ?? null),
    graceEndsOn: computed(() => summary.value?.grace_ends_on ?? null),
    isTrialEnding: computed(() => summary.value?.is_trial_ending ?? false),
    billingContact: computed(() => summary.value?.billing_contact ?? null),
  }
})
