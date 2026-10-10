import { defineStore } from 'pinia'
import { computed, ref } from 'vue'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { DayClock, Property, PropertyWithDay } from '@/api/types'
import { setDisplayTimeZone } from '@/utils/format'

const STORAGE_KEY = 'kamarapms.currentPropertyId'

function readStoredId(): number | null {
  try {
    const v = localStorage.getItem(STORAGE_KEY)
    return v ? Number(v) : null
  } catch {
    return null
  }
}

function storeId(id: number | null): void {
  try {
    if (id === null) localStorage.removeItem(STORAGE_KEY)
    else localStorage.setItem(STORAGE_KEY, String(id))
  } catch {
    // storage unavailable (private mode): selection just isn't remembered
  }
}

/** The properties the user can access, the selected one, and its business-date clock. */
export const usePropertyStore = defineStore('property', () => {
  const properties = ref<Property[]>([])
  const loaded = ref(false)
  const currentId = ref<number | null>(readStoredId())
  const current = ref<PropertyWithDay | null>(null)
  const clock = ref<DayClock | null>(null)
  const error = ref<ApiError | null>(null)

  const hasProperties = computed(() => properties.value.length > 0)

  async function loadProperties(): Promise<void> {
    error.value = null
    try {
      const { data } = await api.GET('/api/v1/properties', { params: { query: { limit: 200 } } })
      properties.value = data?.data ?? []
    } catch (e) {
      error.value = e instanceof ApiError ? e : null
      properties.value = []
    } finally {
      loaded.value = true
    }
    const stillVisible = properties.value.some((p) => p.id === currentId.value)
    const next = stillVisible ? currentId.value : (properties.value[0]?.id ?? null)
    if (next === null) {
      clear()
    } else {
      await select(next)
    }
  }

  async function select(id: number): Promise<void> {
    currentId.value = id
    storeId(id)
    const { data } = await api.GET('/api/v1/properties/{propertyId}', { params: { path: { propertyId: id } } })
    current.value = data ?? null
    await refreshClock()
  }

  async function refreshClock(): Promise<void> {
    if (currentId.value === null) return
    try {
      const { data } = await api.GET('/api/v1/properties/{propertyId}/business-date', {
        params: { path: { propertyId: currentId.value } },
      })
      clock.value = data ?? null
      setDisplayTimeZone(clock.value?.timezone)
    } catch (e) {
      error.value = e instanceof ApiError ? e : null
    }
  }

  /** Records a created/updated property in the list (and as current if selected). */
  function upsert(p: PropertyWithDay): void {
    const i = properties.value.findIndex((x) => x.id === p.id)
    if (i >= 0) properties.value[i] = p
    else properties.value.push(p)
    if (currentId.value === p.id) current.value = p
  }

  function clear(): void {
    currentId.value = null
    current.value = null
    clock.value = null
    setDisplayTimeZone(null)
    storeId(null)
  }

  /** Forgets everything (sign-out). The remembered selection stays for the next sign-in. */
  function reset(): void {
    properties.value = []
    loaded.value = false
    current.value = null
    clock.value = null
    setDisplayTimeZone(null)
    error.value = null
  }

  return { properties, loaded, currentId, current, clock, error, hasProperties, loadProperties, select, refreshClock, upsert, reset }
})
