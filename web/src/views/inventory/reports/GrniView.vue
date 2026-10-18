<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { RouterLink } from 'vue-router'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { InventoryGrni } from '@/api/types'
import ErrorNotice from '@/components/app/ErrorNotice.vue'
import KpiCard from '@/components/app/KpiCard.vue'
import PageHeader from '@/components/app/PageHeader.vue'
import { Button } from '@/components/ui/button'
import { t } from '@/i18n'
import { usePropertyStore } from '@/stores/property'
import { downloadCsv } from '@/views/accounting/reportApi'
import { formatDate, formatMoney } from '@/utils/format'

/** Goods received and not invoiced (docs/architecture/35-inventory.md 6.3, 12.5): the receipts nobody has billed yet by age, with the balance the books hold for them on the account. */
const property = usePropertyStore()
const pid = computed(() => property.currentId)
const data = ref<InventoryGrni | null>(null)
const loaded = ref(false)
const error = ref<ApiError | null>(null)

async function load(): Promise<void> {
  const propertyId = pid.value
  loaded.value = false
  error.value = null
  if (propertyId === null) return
  try {
    data.value = (await api.GET('/api/v1/properties/{propertyId}/inventory/reports/received-not-invoiced', { params: { path: { propertyId } } })).data ?? null
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    loaded.value = true
  }
}
async function csv(): Promise<void> {
  if (pid.value !== null) await downloadCsv('/api/v1/properties/{propertyId}/inventory/reports/received-not-invoiced', { path: { propertyId: pid.value } }, 'received-not-invoiced.csv')
}
watch(pid, load, { immediate: true })
const old = (bucket: string): boolean => bucket === '61-90' || bucket === '90+'
</script>

<template>
  <PageHeader :title="t('inventory.reports.grni.title')" :description="t('inventory.reports.grni.intro')" />
  <ErrorNotice v-if="error" :error="error" inline />
  <p v-if="pid === null" class="muted">{{ t('setup.selectProperty') }}</p>
  <template v-else-if="data">
    <div class="mb-4 flex items-start justify-between gap-3">
      <div class="grid flex-1 gap-3 sm:grid-cols-2 xl:grid-cols-5" data-testid="grni-buckets">
        <KpiCard v-for="b in data.buckets" :key="b.bucket" :label="t('inventory.reports.grni.age', { bucket: b.bucket })" :value="formatMoney(b.value)" :tone="old(b.bucket) && Number(b.value) > 0 ? 'warning' : 'default'" :hint="t('inventory.reports.grni.receipts', { n: b.count })" :data-testid="`bucket-${b.bucket}`" />
        <KpiCard :label="t('inventory.reports.grni.ledger')" :value="formatMoney(data.ledger_balance)" :hint="t('inventory.reports.grni.ledgerHint')" data-testid="grni-ledger" />
      </div>
      <Button type="button" variant="outline" size="sm" data-testid="report-csv" @click="csv">{{ t('inventory.reports.csv') }}</Button>
    </div>
    <p v-if="loaded && data.rows.length === 0" class="m-0 text-sm text-muted-foreground" data-testid="grni-empty">{{ t('inventory.reports.grni.empty') }}</p>
    <div v-else class="overflow-x-auto">
      <table class="w-full min-w-[40rem] text-sm" data-testid="grni-table">
        <thead>
          <tr class="text-left text-xs text-muted-foreground">
            <th class="py-1 pr-2 font-medium">{{ t('inventory.reports.grni.receipt') }}</th><th class="py-1 pr-2 font-medium">{{ t('inventory.reports.grni.supplier') }}</th>
            <th class="py-1 pr-2 font-medium">{{ t('inventory.reports.grni.received') }}</th><th class="py-1 pr-2 text-right font-medium">{{ t('inventory.reports.grni.ageDays') }}</th>
            <th class="py-1 text-right font-medium">{{ t('inventory.reports.grni.value') }}</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="x in data.rows" :key="x.draft_id" class="border-t border-border" :data-testid="`grni-${x.receipt_number}`">
            <td class="py-2 pr-2"><RouterLink :to="`/inventory/receipts/${x.receipt_id}`" class="underline">{{ x.receipt_number }}</RouterLink><span class="block text-xs text-muted-foreground">{{ x.warehouse_name }}</span></td>
            <td class="py-2 pr-2">{{ x.supplier_name }}</td><td class="py-2 pr-2">{{ formatDate(x.received_on) }}</td>
            <td class="py-2 pr-2 text-right" :class="old(x.bucket) ? 'font-semibold text-destructive' : ''"><span v-if="old(x.bucket)" aria-hidden="true">▲ </span>{{ x.age_days }}<span class="ml-1 text-xs text-muted-foreground">({{ x.bucket }})</span></td>
            <td class="py-2 text-right">{{ formatMoney(x.value) }}</td>
          </tr>
          <tr class="border-t-2 border-border font-semibold"><td class="py-2 pr-2" colspan="4">{{ t('inventory.reports.total') }}</td><td class="py-2 text-right" data-testid="grni-total">{{ formatMoney(data.total) }}</td></tr>
        </tbody>
      </table>
    </div>
  </template>
</template>
