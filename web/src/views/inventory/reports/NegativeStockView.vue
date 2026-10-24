<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { InventoryNegativeStock } from '@/api/types'
import ErrorNotice from '@/components/app/ErrorNotice.vue'
import PageHeader from '@/components/app/PageHeader.vue'
import ReportPrintHeader from '@/components/inventory/ReportPrintHeader.vue'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { t } from '@/i18n'
import { usePropertyStore } from '@/stores/property'
import { downloadCsv } from '@/views/accounting/reportApi'
import { formatDate, formatMoney } from '@/utils/format'
import { formatQty } from '@/utils/inventory'

/** Stock sold before the goods came in (docs/architecture/35-inventory.md 4.4, 12.5): the shortfalls that are still open, at the estimated cost, with the age and where the sale came from. */
const property = usePropertyStore()
const pid = computed(() => property.currentId)
const data = ref<InventoryNegativeStock | null>(null)
const loaded = ref(false)
const error = ref<ApiError | null>(null)

async function load(): Promise<void> {
  const propertyId = pid.value
  loaded.value = false
  error.value = null
  if (propertyId === null) return
  try {
    data.value = (await api.GET('/api/v1/properties/{propertyId}/inventory/reports/negative-stock', { params: { path: { propertyId } } })).data ?? null
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    loaded.value = true
  }
}
const print = (): void => window.print()
async function csv(): Promise<void> {
  if (pid.value !== null) await downloadCsv('/api/v1/properties/{propertyId}/inventory/reports/negative-stock', { path: { propertyId: pid.value } }, 'negative-stock.csv')
}
watch(pid, load, { immediate: true })
const source = (s: string): string => (s === 'POS_SALE' || s === 'POS_QUEUE' ? t('inventory.reports.neg.sourcePos') : s.startsWith('INV_ISSUE') ? t('inventory.reports.neg.sourceIssue') : s)
</script>

<template>
  <PageHeader :title="t('inventory.reports.neg.title')" :description="t('inventory.reports.neg.intro')" />
  <ReportPrintHeader />
  <ErrorNotice v-if="error" :error="error" inline />
  <p v-if="pid === null" class="muted">{{ t('setup.selectProperty') }}</p>
  <template v-else>
    <div class="mb-4 flex items-center justify-between gap-2">
      <p v-if="data && data.rows.length" class="m-0 text-sm" data-testid="neg-total">{{ t('inventory.reports.neg.total', { n: data.rows.length, amount: formatMoney(data.total) }) }}</p>
      <span v-else />
      <span class="flex gap-2 print:hidden"><Button type="button" variant="outline" size="sm" data-testid="report-print" @click="print">{{ t('inventory.reports.print') }}</Button><Button type="button" variant="outline" size="sm" data-testid="report-csv" @click="csv">{{ t('inventory.reports.csv') }}</Button></span>
    </div>
    <p v-if="loaded && !data?.rows.length" class="m-0 text-sm text-muted-foreground" data-testid="neg-empty">{{ t('inventory.reports.neg.empty') }}</p>
    <div v-else-if="data" class="overflow-x-auto">
      <table class="w-full min-w-[48rem] text-sm" data-testid="neg-table">
        <thead>
          <tr class="text-left text-xs text-muted-foreground">
            <th class="py-1 pr-2 font-medium">{{ t('inventory.reports.value.warehouse') }}</th><th class="py-1 pr-2 font-medium">{{ t('inventory.reports.item') }}</th>
            <th class="py-1 pr-2 text-right font-medium">{{ t('inventory.reports.neg.open') }}</th><th class="py-1 pr-2 text-right font-medium">{{ t('inventory.reports.neg.estCost') }}</th>
            <th class="py-1 pr-2 text-right font-medium">{{ t('inventory.reports.neg.estValue') }}</th><th class="py-1 pr-2 font-medium">{{ t('inventory.reports.neg.since') }}</th><th class="py-1 font-medium">{{ t('inventory.reports.neg.source') }}</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="x in data.rows" :key="x.shortfall_id" class="border-t border-border" :data-testid="`neg-${x.shortfall_id}`">
            <td class="py-2 pr-2">{{ x.warehouse_code }}</td><td class="py-2 pr-2">{{ x.item_name }}<span class="block text-xs text-muted-foreground">{{ x.item_code }}</span></td>
            <td class="py-2 pr-2 text-right font-semibold text-destructive">▼ {{ formatQty(x.qty_open) }} {{ x.unit_code }}</td>
            <td class="py-2 pr-2 text-right">{{ formatMoney(x.est_unit_cost) }}</td><td class="py-2 pr-2 text-right">{{ formatMoney(x.est_value) }}</td>
            <td class="py-2 pr-2">{{ formatDate(x.since) }}<span class="block text-xs text-muted-foreground">{{ t('inventory.reports.days', { n: x.age_days }) }}</span></td>
            <td class="py-2"><Badge variant="outline">{{ source(x.source) }}</Badge></td>
          </tr>
        </tbody>
      </table>
      <p class="mt-3 text-xs text-muted-foreground">{{ t('inventory.reports.neg.note') }}</p>
    </div>
  </template>
</template>
