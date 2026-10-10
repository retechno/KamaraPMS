<script setup lang="ts">
import { useReportPeriod } from '@/composables/useReportPeriod'
import PeriodPicks from '@/components/app/PeriodPicks.vue'
import ErrorNotice from '@/components/app/ErrorNotice.vue'
import { computed, reactive, ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { GeneralLedger, GlAccount } from '@/api/types'
import DataTable, { type Column } from '@/components/app/DataTable.vue'
import FormField from '@/components/app/FormField.vue'
import PageHeader from '@/components/app/PageHeader.vue'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Combobox } from '@/components/ui/combobox'
import { t } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import { listAccounts } from './accountApi'
import { documentPath, openPdf } from '@/utils/documents'
import { downloadCsv, money } from './reportApi'

const auth = useAuthStore()
const property = usePropertyStore()
const route = useRoute()

const accounts = ref<GlAccount[]>([])
const report = ref<GeneralLedger | null>(null)
const error = ref<ApiError | null>(null)
const busy = ref(false)
const queryDate = (v: unknown): string => (typeof v === 'string' && /^\d{4}-\d{2}-\d{2}$/.test(v) ? v : '')
const form = reactive({ account: Number(route.query.account) || 0, from: queryDate(route.query.from), to: queryDate(route.query.to) })

type Line = GeneralLedger['lines'][number] & { idx: number }
const entries = computed<Line[]>(() => (report.value?.lines ?? []).map((l, i) => ({ ...l, idx: i })))
const columns = computed<Column<Line>[]>(() => [
  { key: 'journal_date', label: t('accountingBooks.date'), format: 'date' as const },
  { key: 'journal_number', label: t('accountingBooks.journal') },
  { key: 'description', label: t('accountingBooks.detail') },
  { key: 'debit', label: t('accountingBooks.debit'), align: 'right', format: 'money' as const },
  { key: 'credit', label: t('accountingBooks.creditCol'), align: 'right', format: 'money' as const },
  { key: 'balance', label: t('accountingBooks.balance'), align: 'right', format: 'money' as const },
])

const pid = computed(() => property.currentId)
const can = (p: string) => auth.can(p, pid.value)
const postable = computed(() => accounts.value.filter((a) => a.is_postable))
const query = () => ({ from: form.from || undefined, to: form.to || undefined })
const period = useReportPeriod(form, () => load())

async function loadAccounts(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null || !can('accounting.view')) return
  try {
    accounts.value = await listAccounts(propertyId)
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  }
}

async function load(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null || !form.account) return
  busy.value = true
  error.value = null
  try {
    const { data } = await api.GET('/api/v1/properties/{propertyId}/accounting/accounts/{id}/ledger', { params: { path: { propertyId, id: form.account }, query: query() } })
    report.value = data ?? null
    period.adopt(data)
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    busy.value = false
  }
}

async function exportCsv(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null || !report.value) return
  try {
    await downloadCsv('/api/v1/properties/{propertyId}/accounting/accounts/{id}/ledger', { path: { propertyId, id: form.account }, query: query() }, `ledger-${report.value.account.code}.csv`)
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  }
}

async function showPdf(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null || !form.account) return
  try {
    await openPdf(documentPath.ledger(propertyId, form.account, query()))
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  }
}

watch(() => pid.value, () => {
  accounts.value = []
  report.value = null
  void loadAccounts().then(load)
}, { immediate: true })
</script>

<template>
  <PageHeader :title="t('accountingBooks.glTitle')">
    <template #actions>
      <template v-if="report">
        <Button type="button" variant="outline" data-testid="pdf" @click="showPdf">{{ t('accountingBooks.pdf') }}</Button>
        <Button type="button" variant="outline" data-testid="export" @click="exportCsv">{{ t('accountingBooks.coaExport') }}</Button>
      </template>
    </template>
  </PageHeader>
  <ErrorNotice v-if="error" :error="error" inline data-testid="report-error" />
  <p v-if="pid === null" class="muted">{{ t('setup.selectProperty') }}</p>
  <p v-else-if="!can('accounting.view')" class="muted" data-testid="no-access">{{ t('accountingBooks.glNoAccess', { permission: 'accounting.view' }) }}</p>
  <template v-else>
    <Card class="mb-4">
      <form class="flex flex-wrap items-end gap-x-4 gap-y-6 px-4 pb-8 pt-4" novalidate @submit.prevent="load">
        <FormField class="w-80" :label="t('accountingBooks.account')">
          <template #default="{ id }">
            <Combobox :id="id" v-model="form.account" name="account" :options="[{ value: 0, label: `${t('accountingBooks.chooseAccount')}` }, ...postable.map((a) => ({ value: a.id, label: `${a.code} · ${a.name}` }))]" />
          </template>
        </FormField>
        <FormField float-hint :hint="$weekday(form.from)" :label="t('accountingBooks.from')"><template #default="{ id }"><Input :id="id" v-model="form.from" name="from" type="date" /></template></FormField>
        <FormField float-hint :hint="$weekday(form.to)" :label="t('accountingBooks.to')"><template #default="{ id }"><Input :id="id" v-model="form.to" name="to" type="date" /></template></FormField>
        <PeriodPicks :business-date="period.businessDate.value" :years="period.years.value" @pick="period.pick" />
        <Button type="submit" variant="outline" :disabled="busy || !form.account" data-testid="apply">{{ t('accountingBooks.show') }}</Button>
      </form>
    </Card>
    <Card v-if="report">
      <CardHeader><CardTitle>{{ report.account.code }} · {{ report.account.name }}</CardTitle></CardHeader>
      <CardContent>
        <p class="mb-3 mt-0 text-sm text-muted-foreground" data-testid="range">{{ t('accountingBooks.glRange', { from: $date(report.from), to: $date(report.to), side: t(`accountingBooks.side_${report.account.normal_side}` as 'accountingBooks.side_DEBIT') }) }}</p>
        <p v-if="report.truncated" class="alert" data-testid="truncated">{{ t('accountingBooks.glTruncated') }}</p>
        <p class="mb-2 mt-0 text-sm" data-testid="opening"><span class="text-muted-foreground">{{ t('accountingBooks.opening') }}:</span> <b class="tabular-nums">{{ $money(report.opening_balance) }}</b></p>
        <DataTable :columns="columns" :rows="entries" row-key="idx" :row-test-id="(l) => `entry-${l.idx}`" :caption="t('accountingBooks.glTitle')" data-testid="ledger">
          <template #cell-debit="{ row }">{{ money(row.debit) }}</template>
          <template #cell-credit="{ row }">{{ money(row.credit) }}</template>
          <template #footer>
            <div class="mt-2 grid grid-cols-[1fr_repeat(3,minmax(6rem,auto))] gap-x-3 border-t border-border px-3 pt-3 text-sm font-semibold" data-testid="closing">
              <span>{{ t('accountingBooks.closingBalance') }}</span>
              <span class="text-right tabular-nums">{{ money(report.total_debit) }}</span>
              <span class="text-right tabular-nums">{{ money(report.total_credit) }}</span>
              <span class="text-right tabular-nums">{{ $money(report.closing_balance) }}</span>
            </div>
          </template>
        </DataTable>
      </CardContent>
    </Card>
  </template>
</template>
