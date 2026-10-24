<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { RouterLink } from 'vue-router'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { InventoryCostDayDoc, InventoryCostDayReport } from '@/api/types'
import ErrorNotice from '@/components/app/ErrorNotice.vue'
import { Button } from '@/components/ui/button'
import { t } from '@/i18n'
import { usePropertyStore } from '@/stores/property'
import { formatDate, formatMoney } from '@/utils/format'

/**
 * The documents and movements behind the costs of one business day (docs/architecture/35-inventory.md 22), by kind of cost, opened from the date in the daily cost.
 * Each line links to its document when the source has a page: a use or waste, a count, a POS bill, a supplier bill.
 */
const props = defineProps<{ date: string }>()
defineEmits<{ close: [] }>()
const property = usePropertyStore()
const data = ref<InventoryCostDayReport | null>(null)
const loaded = ref(false)
const error = ref<ApiError | null>(null)

async function load(): Promise<void> {
  const propertyId = property.currentId
  loaded.value = false
  error.value = null
  if (propertyId === null) return
  try {
    data.value = (await api.GET('/api/v1/properties/{propertyId}/inventory/reports/cost-day', { params: { path: { propertyId }, query: { date: props.date } } })).data ?? null
  } catch (e) {
    data.value = null
    error.value = e instanceof ApiError ? e : null
  } finally {
    loaded.value = true
  }
}
watch(() => [props.date, property.currentId], load, { immediate: true })

const kinds = computed(() => (data.value?.kinds ?? []).filter((k) => k.docs.length))
function link(d: InventoryCostDayDoc): string | null {
  switch (d.source_type) {
    case 'INV_ISSUE': return `/inventory/issues/${d.source_id}`
    case 'INV_COUNT': return `/inventory/counts/${d.source_id}`
    case 'INV_TRANSFER': return `/inventory/transfers/${d.source_id}`
    case 'POS_QUEUE': return d.link_id ? `/pos/bills/${d.link_id}` : null
    case 'INV_BILL': return '/payables/bills'
    default: return null
  }
}
</script>

<template>
  <section class="mb-4 rounded-xl border border-border bg-card p-3" :aria-label="t('inventory.reports.costDay.title', { date: formatDate(date) })" data-testid="cost-day">
    <div class="flex flex-wrap items-center justify-between gap-2">
      <h2 class="m-0 text-sm font-semibold">{{ t('inventory.reports.costDay.title', { date: formatDate(date) }) }}</h2>
      <Button type="button" variant="outline" size="sm" class="print:hidden" data-testid="cost-day-close" @click="$emit('close')">{{ t('inventory.reports.costDay.close') }}</Button>
    </div>
    <p class="m-0 mt-1 text-xs text-muted-foreground">{{ t('inventory.reports.costDay.intro') }}</p>
    <ErrorNotice v-if="error" :error="error" inline />
    <p v-if="loaded && !kinds.length && !error" class="m-0 mt-2 text-sm text-muted-foreground" data-testid="cost-day-empty">{{ t('inventory.reports.costDay.empty') }}</p>
    <div v-for="k in kinds" :key="k.cost_kind" class="mt-3" :data-testid="`cost-day-${k.cost_kind}`">
      <div class="flex items-center justify-between text-sm font-semibold"><span>{{ t(`inventory.reports.costDay.kind.${k.cost_kind}`) }}</span><span>{{ formatMoney(k.total) }}</span></div>
      <div class="overflow-x-auto">
        <table class="w-full min-w-[34rem] text-sm">
          <thead><tr class="text-left text-xs text-muted-foreground">
            <th class="py-1 pr-2 font-medium">{{ t('inventory.reports.costDay.document') }}</th><th class="py-1 pr-2 font-medium">{{ t('inventory.reports.filter.department') }}</th><th class="py-1 pr-2 text-right font-medium">{{ t('inventory.reports.costDay.lines') }}</th><th class="py-1 text-right font-medium">{{ t('inventory.reports.costDay.amount') }}</th>
          </tr></thead>
          <tbody>
            <tr v-for="(d, n) in k.docs" :key="n" class="border-t border-border">
              <td class="py-1.5 pr-2"><RouterLink v-if="link(d)" :to="link(d)!" class="underline">{{ d.number || d.source_type }}</RouterLink><span v-else>{{ d.number || t('inventory.reports.costDay.noLink') }}</span></td>
              <td class="py-1.5 pr-2">{{ d.outlet_name || d.department_name || t('inventory.reports.noDepartment') }}</td>
              <td class="py-1.5 pr-2 text-right">{{ d.lines }}</td><td class="py-1.5 text-right">{{ formatMoney(d.amount) }}</td>
            </tr>
          </tbody>
        </table>
      </div>
    </div>
  </section>
</template>
