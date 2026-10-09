import { ref, watch } from 'vue'
import { api } from '@/api/client'
import { fetchAll } from '@/api/paging'
import { usePropertyStore } from '@/stores/property'

export interface Choice {
  id: number
  code: string
  name: string
}

/**
 * The rate plans and the companies of the current property, for the choices of a filter or a form. They are optional: a person who may not read one of them gets no choices and the filter
 * hides itself, which is told apart from an empty list by `failed`.
 */
export function useReservationLookups() {
  const property = usePropertyStore()
  const ratePlans = ref<Choice[]>([])
  const companies = ref<Choice[]>([])
  const failed = ref(false)

  async function load(): Promise<void> {
    const propertyId = property.currentId
    ratePlans.value = []
    companies.value = []
    failed.value = false
    if (propertyId === null) return
    const [plans, firms] = await Promise.allSettled([
      fetchAll((cursor) => api.GET('/api/v1/properties/{propertyId}/rate-plans', { params: { path: { propertyId }, query: { limit: 200, cursor, active: true } } })),
      fetchAll((cursor) => api.GET('/api/v1/properties/{propertyId}/companies', { params: { path: { propertyId }, query: { limit: 200, cursor, active: true } } })),
    ])
    if (plans.status === 'fulfilled') ratePlans.value = plans.value.map((p) => ({ id: p.id, code: p.code, name: p.name }))
    if (firms.status === 'fulfilled') companies.value = firms.value.map((c) => ({ id: c.id, code: c.code, name: c.name }))
    failed.value = plans.status === 'rejected' || firms.status === 'rejected'
  }

  watch(() => property.currentId, () => void load(), { immediate: true })
  return { ratePlans, companies, failed }
}
