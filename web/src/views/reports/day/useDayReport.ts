import { computed, ref, watch } from 'vue'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import { openPdf } from '@/utils/documents'
import { downloadCsv } from '@/views/accounting/reportApi'

/**
 * The reports of the day and of the night audit pack (docs/architecture/38-reports-night-audit-pack.md 7): one request for the JSON,
 * the same request with `format=csv`, and the PDF at `<slug>.pdf`. The permissions are all needed; the server checks them again.
 */
export function useDayReport<T>(slug: string, query: () => Record<string, string | undefined>, permissions: string[] = ['report.view']) {
  const auth = useAuthStore()
  const property = usePropertyStore()
  const pid = computed(() => property.currentId)
  const businessDate = computed(() => property.clock?.business_date ?? '')
  const allowed = computed(() => pid.value !== null && permissions.every((p) => auth.can(p, pid.value)))
  const data = ref<T | null>(null)
  const error = ref<ApiError | null>(null)
  const busy = ref(false)
  const loaded = ref(false)
  const denied = computed(() => pid.value !== null && !allowed.value)

  const clean = (): Record<string, string> => Object.fromEntries(Object.entries(query()).filter(([, v]) => v !== undefined && v !== '')) as Record<string, string>

  async function run(): Promise<void> {
    const propertyId = pid.value
    error.value = null
    if (propertyId === null || !allowed.value) {
      loaded.value = true
      return
    }
    busy.value = true
    try {
      // The report paths are fixed strings; the typed client needs the literal, so the call goes through one cast.
      const get = api.GET as unknown as (path: string, init: object) => Promise<{ data?: unknown }>
      const res = await get(`/api/v1/properties/{propertyId}/reports/${slug}`, { params: { path: { propertyId }, query: clean() } })
      data.value = typeof res.data === 'string' ? null : ((res.data as T | undefined) ?? null)
    } catch (e) {
      data.value = null
      error.value = e instanceof ApiError ? e : null
    } finally {
      busy.value = false
      loaded.value = true
    }
  }

  async function csv(stamp: string): Promise<void> {
    const propertyId = pid.value
    if (propertyId === null) return
    error.value = null
    try {
      await downloadCsv(`/api/v1/properties/{propertyId}/reports/${slug}`, { path: { propertyId }, query: clean() }, `${slug}-${stamp}.csv`)
    } catch (e) {
      error.value = e instanceof ApiError ? e : null
    }
  }

  async function pdf(): Promise<void> {
    const propertyId = pid.value
    if (propertyId === null) return
    error.value = null
    try {
      const q = new URLSearchParams(clean()).toString()
      await openPdf(`/api/v1/properties/${propertyId}/reports/${slug}.pdf${q ? `?${q}` : ''}`)
    } catch (e) {
      error.value = e instanceof ApiError ? e : null
    }
  }

  // another property is another report: nothing of the first one stays on the screen
  watch(pid, () => {
    data.value = null
    error.value = null
    loaded.value = false
  })

  return { pid, businessDate, allowed, denied, data, error, busy, loaded, run, csv, pdf }
}
