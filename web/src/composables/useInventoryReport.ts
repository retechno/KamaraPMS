import { computed, reactive, ref, type Ref } from 'vue'
import { ApiError } from '@/api/problem'
import { usePropertyStore } from '@/stores/property'
import { downloadCsv } from '@/views/accounting/reportApi'

/**
 * What every report of the inventory shares: the property, the period (the month of the business date up to that date until the person changes it), the loading state, the error and the CSV file.
 * `run` is the request of the report (it gets the property id and the period); `path` is the same route for the file.
 */
export function useInventoryReport<T>(path: string, file: string, run: (propertyId: number, period: { from: string; to: string }) => Promise<T | null>, opts: { single?: boolean } = {}) {
  const property = usePropertyStore()
  const pid = computed(() => property.currentId)
  const period = reactive({ from: '', to: '' })
  const data: Ref<T | null> = ref(null)
  const loaded = ref(false)
  const busy = ref(false)
  const error = ref<ApiError | null>(null)

  function defaults(): void {
    const bd = property.clock?.business_date ?? new Date().toISOString().slice(0, 10)
    if (!period.to) period.to = bd
    if (!period.from && !opts.single) period.from = `${period.to.slice(0, 8)}01`
  }

  async function load(): Promise<void> {
    const propertyId = pid.value
    error.value = null
    if (propertyId === null) return
    defaults()
    busy.value = true
    try {
      data.value = await run(propertyId, { from: period.from, to: period.to })
    } catch (e) {
      data.value = null
      error.value = e instanceof ApiError ? e : null
    } finally {
      busy.value = false
      loaded.value = true
    }
  }

  async function csv(extra: Record<string, unknown> = {}): Promise<void> {
    const propertyId = pid.value
    if (propertyId === null) return
    defaults()
    try {
      const query: Record<string, unknown> = opts.single ? { as_of: period.to || undefined } : { from: period.from, to: period.to }
      await downloadCsv(path, { path: { propertyId }, query: { ...query, ...extra } }, file)
    } catch (e) {
      error.value = e instanceof ApiError ? e : null
    }
  }

  return { pid, period, data, loaded, busy, error, load, csv }
}
