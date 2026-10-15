import { computed, ref, watch } from 'vue'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { PosOutlet } from '@/api/types'
import { usePropertyStore } from '@/stores/property'

const STORAGE_KEY = 'kamarapms.posOutlet'

/**
 * The outlets the person works at, and the one chosen (remembered per browser). The cashier of a hotel with one outlet never sees a choice.
 */
export function usePosOutlet() {
  const property = usePropertyStore()
  const outlets = ref<PosOutlet[]>([])
  const outletId = ref<number | null>(null)
  const error = ref<ApiError | null>(null)
  const loaded = ref(false)

  function remembered(): number | null {
    try {
      const v = Number(localStorage.getItem(`${STORAGE_KEY}.${property.currentId}`))
      return Number.isInteger(v) && v > 0 ? v : null
    } catch {
      return null
    }
  }

  async function load(): Promise<void> {
    const propertyId = property.currentId
    if (propertyId === null) return
    try {
      const { data } = await api.GET('/api/v1/properties/{propertyId}/pos/outlets', { params: { path: { propertyId }, query: { active: true } } })
      outlets.value = data?.data ?? []
      error.value = null
      const want = remembered()
      outletId.value = outlets.value.find((o) => o.id === want)?.id ?? outlets.value[0]?.id ?? null
    } catch (e) {
      error.value = e instanceof ApiError ? e : null
    } finally {
      loaded.value = true
    }
  }

  watch(outletId, (id) => {
    if (id === null) return
    try {
      localStorage.setItem(`${STORAGE_KEY}.${property.currentId}`, String(id))
    } catch {
      // private window: the choice is not remembered
    }
  })
  watch(() => property.currentId, () => void load(), { immediate: true })

  const outlet = computed(() => outlets.value.find((o) => o.id === outletId.value) ?? null)
  return { outlets, outletId, outlet, error, loaded, load }
}
