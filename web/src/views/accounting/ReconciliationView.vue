<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { Reconciliation } from '@/api/types'
import DataTable, { type Column } from '@/components/app/DataTable.vue'
import FormField from '@/components/app/FormField.vue'
import PageHeader from '@/components/app/PageHeader.vue'
import { Button } from '@/components/ui/button'
import { Card, CardContent } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { t } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import { money } from './reportApi'

const auth = useAuthStore()
const property = usePropertyStore()

const report = ref<Reconciliation | null>(null)
const error = ref<ApiError | null>(null)
const busy = ref(false)
const asOf = ref('')

type Control = Reconciliation['controls'][number]
const columns = computed<Column<Control>[]>(() => [
  { key: 'title', label: t('accounting.rcAccount') },
  { key: 'ledger', label: t('accounting.rcBooks'), align: 'right', format: 'money' as const },
  { key: 'source', label: t('accounting.rcFolios'), align: 'right', format: 'money' as const },
  { key: 'difference', label: t('accounting.rcDifference'), align: 'right', format: 'money' as const },
])

const pid = computed(() => property.currentId)
const can = (p: string) => auth.can(p, pid.value)

async function load(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null || !can('accounting.view')) return
  busy.value = true
  error.value = null
  try {
    const { data } = await api.GET('/api/v1/properties/{propertyId}/accounting/reconciliation', { params: { path: { propertyId }, query: { as_of: asOf.value || undefined } } })
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
  <PageHeader :title="t('accounting.rcTitle')" />
  <p v-if="error" class="alert" role="alert" data-testid="report-error">{{ error.message }} <code>{{ error.code }}</code></p>
  <p v-if="pid === null" class="muted">{{ t('setup.selectProperty') }}</p>
  <p v-else-if="!can('accounting.view')" class="muted" data-testid="no-access">{{ t('accounting.noAccessView', { what: t('accounting.whatReport'), permission: 'accounting.view' }) }}</p>
  <template v-else>
    <p class="mb-4 text-sm text-muted-foreground">{{ t('accounting.rcIntro') }}</p>
    <Card class="mb-4">
      <form class="flex items-end gap-3 p-4" novalidate @submit.prevent="load">
        <FormField :label="t('accounting.rcAsOf')"><template #default="{ id }"><Input :id="id" v-model="asOf" name="as_of" type="date" /></template></FormField>
        <Button type="submit" variant="outline" :disabled="busy" data-testid="apply">{{ t('accounting.rcCheck') }}</Button>
      </form>
    </Card>
    <Card v-if="report">
      <CardContent class="pt-4">
        <p v-if="report.reconciled" class="notice" data-testid="reconciled">{{ t('accounting.rcReconciled', { date: $date(report.as_of) }) }}</p>
        <p v-else class="alert" data-testid="not-reconciled">{{ t('accounting.rcNot', { date: $date(report.as_of) }) }}</p>
        <p v-if="report.pending_days" class="alert" data-testid="pending">{{ t('accounting.rcPending', { n: report.pending_days }) }}</p>
        <p v-if="report.includes_open_day" class="mb-3 text-sm text-muted-foreground" data-testid="open-day">{{ t('accounting.rcOpenDay') }}</p>
        <DataTable :columns="columns" :rows="report.controls" row-key="key" :row-test-id="(c) => `control-${c.key}`" :row-class="(c) => (Number(c.difference) !== 0 ? 'text-destructive' : undefined)" :caption="t('accounting.rcTitle')" data-testid="controls">
          <template #cell-title="{ row }">{{ row.title }}<br /><small class="text-muted-foreground">{{ row.account }} · {{ row.basis }}</small></template>
          <template #cell-difference="{ row }">{{ money(row.difference) }}</template>
        </DataTable>
        <p class="mb-0 mt-3 text-sm text-muted-foreground">{{ t('accounting.rcStart', { date: $date(report.start_date) }) }}</p>
      </CardContent>
    </Card>
  </template>
</template>
