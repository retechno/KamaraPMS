import { computed, ref, watch } from 'vue'
import { api } from '@/api/client'
import type { FiscalYear } from '@/api/types'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import type { Period } from '@/utils/periods'

/**
 * What a report's filter needs besides its two dates: the quick periods (this month, last month, the fiscal year to date) and the habit of showing the period that is on
 * the screen. A report asked with no dates shows the period the server chose; `adopt` puts that period in the filter, so the filter says what the report shows.
 * The fiscal years are read once per property; when they cannot be read there are none and the "fiscal year" choice is not offered.
 */
export function useReportPeriod(range: Period, load: () => Promise<void>) {
  const property = usePropertyStore()
  const auth = useAuthStore()
  const years = ref<FiscalYear[]>([])
  const businessDate = computed(() => property.clock?.business_date ?? '')

  watch(
    () => property.currentId,
    async (propertyId) => {
      years.value = []
      if (propertyId === null || !auth.can('accounting.view', propertyId)) return
      try {
        const { data } = await api.GET('/api/v1/properties/{propertyId}/accounting/fiscal-years', { params: { path: { propertyId } } })
        const list = (data as { data?: unknown } | undefined)?.data
        years.value = Array.isArray(list) ? (list as FiscalYear[]).filter((y) => typeof y?.year_start === 'string') : []
      } catch {
        years.value = []
      }
    },
    { immediate: true },
  )

  function pick(period: Period): void {
    range.from = period.from
    range.to = period.to
    void load()
  }

  /** An empty end of the filter takes the end of the period that was shown; what the person typed is left alone. */
  function adopt(shown: { from?: string; to?: string } | null | undefined): void {
    if (!shown) return
    range.from ||= shown.from ?? ''
    range.to ||= shown.to ?? ''
  }

  return { years, businessDate, pick, adopt }
}
