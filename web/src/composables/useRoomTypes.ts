import { ref, watch } from 'vue'
import { api } from '@/api/client'
import { fetchAll } from '@/api/paging'
import type { RoomType } from '@/api/types'
import { usePropertyStore } from '@/stores/property'

/** The active room types of the current property, for a filter. A person who may not read them simply gets no choices: the filter hides itself. */
export function useRoomTypes() {
  const property = usePropertyStore()
  const types = ref<RoomType[]>([])

  async function load(): Promise<void> {
    const propertyId = property.currentId
    types.value = []
    if (propertyId === null) return
    try {
      types.value = (await fetchAll((cursor) => api.GET('/api/v1/properties/{propertyId}/room-types', { params: { path: { propertyId }, query: { limit: 200, cursor } } }))).filter((x) => x.is_active)
    } catch {
      types.value = [] // the filter is optional
    }
  }

  watch(() => property.currentId, () => void load(), { immediate: true })
  return { types }
}
