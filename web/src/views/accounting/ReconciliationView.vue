<script setup lang="ts">
import ErrorNotice from '@/components/app/ErrorNotice.vue'
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
import { t, te } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import { addDays } from '@/utils/dates'
import { money } from './reportApi'

const auth = useAuthStore()
const property = usePropertyStore()

const report = ref<Reconciliation | null>(null)
const error = ref<ApiError | null>(null)
const busy = ref(false)
const asOf = ref('')

type Control = Reconciliation['controls'][number]
const columns = computed<Column<Control>[]>(() => [
  { key: 'title', label: t('accounting.rcAccount'), card: 'primary' },
  { key: 'ledger', label: t('accounting.rcBooks'), align: 'right', format: 'money' as const },
  { key: 'source', label: t('accounting.rcFolios'), align: 'right', format: 'money' as const },
  { key: 'difference', label: t('accounting.rcDifference'), align: 'right', format: 'money' as const },
])

/** The name and the explanation of a row are in the language of the page, by the code of the row; the English text of the server is only the fallback for a code this page does not know. */
const titleOf = (c: Control): string => (te(`accounting.rcControls.${c.key}.title`) ? t(`accounting.rcControls.${c.key}.title`) : c.title)
const basisOf = (c: Control): string => (te(`accounting.rcControls.${c.key}.basis`) ? t(`accounting.rcControls.${c.key}.basis`) : c.basis)

/** Only the open business day is behind: the books are not wrong, today is not in them yet. Shown as a warning, not as "does not agree". */
const openDayOnly = computed(() => !!report.value && !report.value.reconciled && report.value.includes_open_day && report.value.pending_days === 0)
const rowClass = (c: Control): string | undefined => (Number(c.difference) === 0 ? undefined : openDayOnly.value ? 'text-warning-text' : 'text-destructive')

const pid = computed(() => property.currentId)
const can = (p: string) => auth.can(p, pid.value)
const openDay = computed(() => property.clock?.business_date ?? '')
/** The last business day that is closed is the day before the open one: that is where the books and the folios must agree. */
const lastClosed = computed(() => (openDay.value ? addDays(openDay.value, -1) : ''))
const pick = (date: string): void => {
  asOf.value = date
  void load()
}

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

// The page opens on the last closed day; the open day is one button away.
watch(() => [pid.value, lastClosed.value], () => {
  report.value = null
  asOf.value = lastClosed.value
  void load()
}, { immediate: true })
</script>

<template>
  <PageHeader :title="t('accounting.rcTitle')" />
  <ErrorNotice v-if="error" :error="error" inline data-testid="report-error" />
  <p v-if="pid === null" class="muted">{{ t('setup.selectProperty') }}</p>
  <p v-else-if="!can('accounting.view')" class="muted" data-testid="no-access">{{ t('accounting.noAccessView', { what: t('accounting.whatReport'), permission: 'accounting.view' }) }}</p>
  <template v-else>
    <p class="mb-4 text-sm text-muted-foreground">{{ t('accounting.rcIntro') }}</p>
    <Card class="mb-4">
      <form class="flex flex-wrap items-end gap-3 p-4" novalidate @submit.prevent="load">
        <FormField :label="t('accounting.rcAsOf')"><template #default="{ id }"><Input :id="id" v-model="asOf" name="as_of" type="date" /></template></FormField>
        <Button type="submit" variant="outline" :disabled="busy" data-testid="apply">{{ t('accounting.rcCheck') }}</Button>
        <Button v-if="lastClosed" type="button" variant="ghost" :disabled="busy" data-testid="as-of-closed" @click="pick(lastClosed)">{{ t('accounting.rcLastClosed') }}</Button>
        <Button v-if="openDay" type="button" variant="ghost" :disabled="busy" data-testid="as-of-open" @click="pick(openDay)">{{ t('accounting.rcToday') }}</Button>
      </form>
    </Card>
    <Card v-if="report">
      <CardContent class="pt-4">
        <p v-if="report.reconciled" class="notice" data-testid="reconciled">{{ t('accounting.rcReconciled', { date: $date(report.as_of) }) }}</p>
        <p v-else-if="openDayOnly" class="alert warning" data-testid="open-day-warning">{{ t('accounting.rcOpenDayWarning') }}</p>
        <p v-else class="alert" data-testid="not-reconciled">{{ t('accounting.rcNot', { date: $date(report.as_of) }) }}</p>
        <p v-if="report.pending_days" class="alert" data-testid="pending">{{ t('accounting.rcPending', { n: report.pending_days }) }}</p>
        <p v-if="report.includes_open_day" class="mb-3 text-sm text-muted-foreground" data-testid="open-day">{{ t('accounting.rcOpenDay') }}</p>
        <DataTable :columns="columns" :rows="report.controls" row-key="key" cards :row-test-id="(c) => `control-${c.key}`" :row-class="rowClass" :caption="t('accounting.rcTitle')" data-testid="controls">
          <template #cell-title="{ row }">{{ titleOf(row) }}<br /><small class="text-muted-foreground">{{ row.account }} · {{ basisOf(row) }}</small></template>
          <template #cell-difference="{ row }">{{ money(row.difference) }}</template>
        </DataTable>
        <p class="mb-0 mt-3 text-sm text-muted-foreground">{{ t('accounting.rcStart', { date: $date(report.start_date) }) }}</p>
      </CardContent>
    </Card>
  </template>
</template>
