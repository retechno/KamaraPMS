<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { PayablesAging } from '@/api/types'
import DataTable, { type Column } from '@/components/app/DataTable.vue'
import EmptyState from '@/components/app/EmptyState.vue'
import FormField from '@/components/app/FormField.vue'
import PageHeader from '@/components/app/PageHeader.vue'
import { Button } from '@/components/ui/button'
import { Card, CardContent } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { t } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import { money } from '@/views/accounting/reportApi'

const auth = useAuthStore()
const property = usePropertyStore()

const report = ref<PayablesAging | null>(null)
const error = ref<ApiError | null>(null)
const busy = ref(false)
const asOf = ref('')
const expanded = ref<number | null>(null)

const pid = computed(() => property.currentId)
const can = (p: string) => auth.can(p, pid.value)
const BUCKETS = ['CURRENT', 'DAYS_1_30', 'DAYS_31_60', 'DAYS_61_90', 'DAYS_OVER_90'] as const
const bucketLabel = (k: (typeof BUCKETS)[number]): string => t(`payables.b_${k}` as 'payables.b_CURRENT')
type SupplierRow = PayablesAging['suppliers'][number]
const columns = computed<Column<SupplierRow>[]>(() => [
  { key: 'supplier', label: t('payables.supplier') },
  ...BUCKETS.map((k) => ({ key: k, label: bucketLabel(k), align: 'right' as const })),
  { key: 'total', label: t('payables.total'), align: 'right' as const },
])

async function load(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null || !can('payables.view')) return
  busy.value = true
  error.value = null
  try {
    const { data } = await api.GET('/api/v1/properties/{propertyId}/payables/aging', { params: { path: { propertyId }, query: { as_of: asOf.value || undefined } } })
    report.value = data ?? null
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    busy.value = false
  }
}

watch(() => pid.value, () => {
  report.value = null
  void load()
}, { immediate: true })
</script>

<template>
  <PageHeader :title="t('payables.aTitle')" />
  <p v-if="error" class="alert" role="alert" data-testid="aging-error">{{ error.message }} <code>{{ error.code }}</code></p>
  <p v-if="pid === null" class="muted">{{ t('setup.selectProperty') }}</p>
  <p v-else-if="!can('payables.view')" class="muted" data-testid="no-access">{{ t('payables.aNoAccess', { permission: 'payables.view' }) }}</p>
  <template v-else>
    <Card class="mb-4">
      <form class="flex items-end gap-3 p-4" novalidate @submit.prevent="load">
        <FormField :label="t('payables.asOf')"><template #default="{ id }"><Input :id="id" v-model="asOf" name="as_of" type="date" /></template></FormField>
        <Button type="submit" variant="outline" :disabled="busy" data-testid="apply">{{ t('payables.show') }}</Button>
      </form>
    </Card>
    <Card v-if="report">
      <CardContent class="pt-4">
        <p class="mb-3 mt-0 text-sm text-muted-foreground" data-testid="range">{{ t('payables.asOfNote', { date: report.as_of }) }}</p>
        <EmptyState v-if="!report.suppliers.length" :title="t('payables.aEmpty')" data-testid="empty" />
        <DataTable
          v-else
          :columns="columns"
          :rows="report.suppliers"
          row-key="supplier_id"
          clickable
          :row-test-id="(s) => `supplier-${s.supplier_code}`"
          :is-expanded="(s) => expanded === s.supplier_id"
          :detail-test-id="(s) => `bills-${s.supplier_code}`"
          :caption="t('payables.aTitle')"
          data-testid="aging"
          @row-click="(s) => (expanded = expanded === s.supplier_id ? null : s.supplier_id)"
        >
          <template #cell-supplier="{ row }">{{ row.supplier_code }} · {{ row.supplier_name }}</template>
          <template v-for="k in BUCKETS" :key="k" #[`cell-${k}`]="{ row }">{{ money(row.buckets[k]) }}</template>
          <template #cell-total="{ row }"><b>{{ row.total }}</b></template>
          <template #detail="{ row }">
            <table class="w-full border-collapse text-sm">
              <thead><tr class="border-b border-border text-left text-xs text-muted-foreground"><th class="py-1 pr-3 font-medium">{{ t('payables.bill') }}</th><th class="px-3 font-medium">{{ t('payables.invoice') }}</th><th class="px-3 font-medium">{{ t('payables.billDateCol') }}</th><th class="px-3 font-medium">{{ t('payables.due') }}</th><th class="px-3 text-right font-medium">{{ t('payables.daysLate') }}</th><th class="pl-3 text-right font-medium">{{ t('payables.owed') }}</th></tr></thead>
              <tbody>
                <tr v-for="bill in row.bills" :key="bill.bill_id" class="border-b border-border">
                  <td class="py-1 pr-3">{{ bill.bill_number }}</td><td class="px-3">{{ bill.supplier_invoice_number }}</td><td class="px-3">{{ bill.bill_date }}</td><td class="px-3">{{ bill.due_date }}</td>
                  <td class="px-3 text-right tabular-nums">{{ bill.days_overdue || '' }}</td><td class="pl-3 text-right tabular-nums">{{ bill.outstanding }}</td>
                </tr>
              </tbody>
            </table>
          </template>
          <template #footer>
            <div class="mt-2 grid grid-cols-[1fr_repeat(6,minmax(5rem,auto))] gap-x-3 border-t border-border px-3 pt-3 text-sm font-semibold" data-testid="totals">
              <span>{{ t('payables.total') }}</span>
              <span v-for="k in BUCKETS" :key="k" class="text-right tabular-nums">{{ money(report.buckets[k]) }}</span>
              <span class="text-right tabular-nums">{{ report.total }}</span>
            </div>
          </template>
        </DataTable>
      </CardContent>
    </Card>
  </template>
</template>
