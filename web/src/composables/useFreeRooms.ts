import { computed, onBeforeUnmount, ref, watch, type Ref } from 'vue'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { FreeRoom } from '@/api/types'
import { usePropertyStore } from '@/stores/property'
import { sortFreeRooms, type RoomOrder } from '@/utils/freeRooms'

/** What the rooms are asked for: the type, and the nights. */
export interface FreeRoomsQuery {
  typeId: number
  arrival: string
  departure: string
}

/**
 * The free rooms of a type for a period (GET availability/rooms). A room that is held by this very line is not "free" (the line holds it), so it is not in the answer.
 * The period never starts before the business date: a line that has been arriving for some days is asked for from today.
 */
export async function fetchFreeRooms(propertyId: number, q: FreeRoomsQuery, businessDate = ''): Promise<FreeRoom[]> {
  const arrival = businessDate && q.arrival < businessDate ? businessDate : q.arrival
  const { data } = await api.GET('/api/v1/properties/{propertyId}/availability/rooms', { params: { path: { propertyId }, query: { room_type_id: q.typeId, arrival, departure: q.departure } } })
  return [...(data?.data ?? [])]
}

/**
 * The free rooms of a line, in the one order every place uses (see `sortFreeRooms`). `query` returns null while there is nothing to ask (a closed picker): then nothing is read.
 * `reload()` reads again (after a conflict: a room was taken meanwhile). Only the latest read counts.
 */
export function useFreeRooms(query: () => FreeRoomsQuery | null, order: () => Omit<RoomOrder, 'requiresInspection'>) {
  const property = usePropertyStore()
  const raw = ref<FreeRoom[]>([]) as Ref<FreeRoom[]>
  const loading = ref(false)
  const loaded = ref(false)
  const error = ref<ApiError | null>(null)
  const requiresInspection = computed(() => property.current?.require_room_inspection_for_checkin ?? false)
  const rooms = computed(() => sortFreeRooms(raw.value, { ...order(), requiresInspection: requiresInspection.value }))
  let latest = 0
  let alive = true
  onBeforeUnmount(() => {
    alive = false
  })

  async function reload(): Promise<void> {
    const q = query()
    const propertyId = property.currentId
    if (!q || propertyId === null) return
    const mine = ++latest
    loading.value = true
    error.value = null
    try {
      const free = await fetchFreeRooms(propertyId, q, property.clock?.business_date ?? '')
      if (mine !== latest || !alive) return
      raw.value = free
      loaded.value = true
    } catch (e) {
      if (mine !== latest || !alive) return
      error.value = e instanceof ApiError ? e : new ApiError({ type: 'about:blank', title: 'Error', status: 0, code: 'NETWORK_ERROR', detail: String(e) })
    } finally {
      if (mine === latest) loading.value = false
    }
  }

  // The rooms are read when the question changes (another type, other nights), and not before there is one.
  watch(() => JSON.stringify(query()), () => {
    raw.value = []
    loaded.value = false
    void reload()
  }, { immediate: true })

  return { rooms, loading, loaded, error, reload, requiresInspection }
}
