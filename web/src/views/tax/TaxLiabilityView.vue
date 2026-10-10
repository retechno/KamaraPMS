<script setup lang="ts">
import ErrorNotice from '@/components/app/ErrorNotice.vue'
import { computed, ref, watch } from 'vue'
import { RouterLink } from 'vue-router'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { TaxFilingLiability } from '@/api/types'
import DataTable, { type Column } from '@/components/app/DataTable.vue'
import EmptyState from '@/components/app/EmptyState.vue'
import FormField from '@/components/app/FormField.vue'
import PageHeader from '@/components/app/PageHeader.vue'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { t } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import { money } from '@/views/accounting/reportApi'

const auth = useAuthStore()
const property = usePropertyStore()

const report = ref<TaxFilingLiability | null>(null)
const error = ref<ApiError | null>(null)
const busy = ref(false)
const asOf = ref('')

type TaxRow = TaxFilingLiability['taxes'][number]
type AccountRow = TaxFilingLiability['accounts'][number]
const columns = computed<Column<TaxRow>[]>(() => [
  { key: 'tax', label: t('taxLiability.tax') },
  { key: 'collected', label: t('taxLiability.collected'), align: 'right', format: 'money' as const },
  { key: 'filed', label: t('taxLiability.onReturns'), align: 'right', format: 'money' as const },
  { key: 'unfiled', label: t('taxLiability.unfiled'), align: 'right', format: 'money' as const },
  { key: 'credit', label: t('taxLiability.credit'), align: 'right' },
  { key: 'paid', label: t('taxLiability.paid'), align: 'right', format: 'money' as const },
  { key: 'owed', label: t('taxLiability.owed'), align: 'right', format: 'money' as const },
  { key: 'overdue', label: t('taxLiability.overdue') },
])
const accountColumns = computed<Column<AccountRow>[]>(() => [
  { key: 'account_code', label: t('taxLiability.payableAccount') },
  { key: 'books', label: t('taxLiability.books'), align: 'right', format: 'money' as const },
  { key: 'owed', label: t('taxLiability.taxesSay'), align: 'right', format: 'money' as const },
  { key: 'difference', label: t('taxLiability.difference'), align: 'right', format: 'money' as const },
])

const pid = computed(() => property.currentId)
const can = (p: string) => auth.can(p, pid.value)

async function load(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null || !can('tax.view')) return
  busy.value = true
  error.value = null
  try {
    const { data } = await api.GET('/api/v1/properties/{propertyId}/tax/liability', { params: { path: { propertyId }, query: { as_of: asOf.value || undefined } } })
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
  <PageHeader :title="t('taxLiability.title')" />
  <ErrorNotice v-if="error" :error="error" inline data-testid="liability-error" />
  <p v-if="pid === null" class="muted">{{ t('setup.selectProperty') }}</p>
  <p v-else-if="!can('tax.view')" class="muted" data-testid="no-access">{{ t('taxLiability.noAccess', { permission: 'tax.view' }) }}</p>
  <template v-else>
    <Card class="mb-4">
      <form class="flex items-end gap-3 p-4" novalidate @submit.prevent="load">
        <FormField :label="t('taxLiability.asOf')"><template #default="{ id }"><Input :id="id" v-model="asOf" name="as_of" type="date" /></template></FormField>
        <Button type="submit" variant="outline" :disabled="busy" data-testid="apply">{{ t('taxLiability.show') }}</Button>
      </form>
    </Card>
    <template v-if="report">
      <EmptyState v-if="!report.taxes.length" :title="t('taxLiability.empty')" data-testid="empty" />
      <template v-else>
        <Card class="mb-4">
          <CardContent class="pt-4">
            <p class="mb-3 mt-0 text-sm text-muted-foreground" data-testid="owed-total">{{ t('taxLiability.owedTotal', { date: $date(report.as_of) }) }} <b>{{ $money(report.owed) }}</b></p>
            <DataTable :columns="columns" :rows="report.taxes" row-key="tax_id" :row-test-id="(x) => `tax-${x.tax_code}`" :caption="t('taxLiability.title')" data-testid="taxes">
              <template #cell-tax="{ row }">
                <RouterLink :to="{ path: '/tax/returns', query: { tax: String(row.tax_id) } }" class="text-primary hover:underline">{{ row.tax_code }}</RouterLink>
                <small class="text-muted-foreground"> · {{ row.authority }} · {{ t('taxLiability.account', { code: row.account_code }) }}</small>
              </template>
              <template #cell-unfiled="{ row }">{{ money(row.unfiled) }}</template>
              <template #cell-credit="{ row }">{{ Number(row.credit_available) ? $money(row.credit_available) : '' }}</template>
              <template #cell-owed="{ row }"><b>{{ $money(row.owed) }}</b></template>
              <template #cell-overdue="{ row }">
                <span v-if="row.overdue_unfiled_months" class="mr-2 text-destructive" data-testid="overdue-unfiled">{{ t('taxLiability.monthsNotFiled', { n: row.overdue_unfiled_months }) }}</span>
                <span v-if="Number(row.overdue_unpaid)" class="text-destructive" data-testid="overdue-unpaid">{{ t('taxLiability.unpaid', { amount: $money(row.overdue_unpaid) }) }}</span>
              </template>
            </DataTable>
          </CardContent>
        </Card>
        <Card>
          <CardHeader><CardTitle>{{ t('taxLiability.againstBooks') }}</CardTitle></CardHeader>
          <CardContent>
            <DataTable :columns="accountColumns" :rows="report.accounts" row-key="account_code" :row-test-id="(a) => `account-${a.account_code}`" :caption="t('taxLiability.againstBooks')" data-testid="accounts">
              <template #cell-difference="{ row }"><span :class="Number(row.difference) !== 0 ? 'text-destructive' : undefined">{{ money(row.difference) }}</span></template>
            </DataTable>
            <p class="mb-0 mt-3 text-sm text-muted-foreground">{{ t('taxLiability.differenceHint') }}</p>
          </CardContent>
        </Card>
      </template>
    </template>
  </template>
</template>
