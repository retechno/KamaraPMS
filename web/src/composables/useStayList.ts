import { computed, ref, watch } from 'vue'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { InHouseRow } from '@/api/types'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'

export type StayQuery = { departure_date?: string; departure_until?: string; room_type_id?: number; q?: string }

/**
 * The paged list of the in-house read model that the In-house and Departures tabs share: the filters go to the server (a page is never filtered on the client), a change of filter or of
 * property starts again, "load more" follows the cursor, and a reload after an edit fetches again as many rows as are shown so the page and the cursor stay.
 */
/** `query` answers null while the list cannot be asked yet (the business date is not known): nothing is loaded then. */
export function useStayList(query: () => StayQuery | null, onLoaded?: (rows: InHouseRow[], more: boolean) => void) {
  const auth = useAuthStore()
  const property = usePropertyStore()
  const rows = ref<InHouseRow[]>([])
  const nextCursor = ref<string | undefined>()
  const error = ref<ApiError | null>(null)
  const loading = ref(false)
  const loaded = ref(false)
  const canRead = computed(() => auth.can('reservation.read', property.currentId))

  const clean = (): StayQuery => Object.fromEntries(Object.entries(query() ?? {}).filter(([, v]) => v !== undefined && v !== '')) as StayQuery

  async function fetchPage(cursor: string | undefined): Promise<{ data: InHouseRow[]; next?: string } | null> {
    const propertyId = property.currentId
    if (propertyId === null) return null
    const { data } = await api.GET('/api/v1/properties/{propertyId}/stays/in-house', { params: { path: { propertyId }, query: { limit: 50, cursor, ...clean() } } })
    return { data: data?.data ?? [], next: data?.next_cursor }
  }

  async function load(more = false): Promise<void> {
    if (property.currentId === null || !canRead.value || query() === null) return
    loading.value = true
    error.value = null
    try {
      const page = await fetchPage(more ? nextCursor.value : undefined)
      if (!page) return
      rows.value = more ? [...rows.value, ...page.data] : page.data
      nextCursor.value = page.next
      loaded.value = true
      onLoaded?.(rows.value, !!page.next)
    } catch (e) {
      error.value = e instanceof ApiError ? e : new ApiError({ type: 'about:blank', title: 'Error', status: 0, code: 'NETWORK_ERROR', detail: String(e) })
    } finally {
      loading.value = false
    }
  }

  /** Fetch again as many rows as are shown (after an edit that changes rates or balances), so the filters, the sorting and the page stay. */
  async function reloadLoaded(): Promise<void> {
    const wanted = rows.value.length
    try {
      let all: InHouseRow[] = []
      let cursor: string | undefined
      do {
        const page = await fetchPage(cursor)
        if (!page) return
        all = [...all, ...page.data]
        cursor = page.next
        nextCursor.value = cursor
      } while (cursor && all.length < wanted)
      rows.value = all
      onLoaded?.(rows.value, !!cursor)
    } catch (e) {
      error.value = e instanceof ApiError ? e : null
    }
  }

  function renameGuest(guestId: number, name: string): void {
    rows.value = rows.value.map((r) => (r.guest.id === guestId ? { ...r, guest: { ...r.guest, name } } : r))
  }

  function restart(): void {
    rows.value = []
    nextCursor.value = undefined
    loaded.value = false
    void load()
  }

  watch(() => [property.currentId, JSON.stringify(query())], restart, { immediate: true })

  return { rows, nextCursor, error, loading, loaded, canRead, load, reloadLoaded, renameGuest, restart }
}
