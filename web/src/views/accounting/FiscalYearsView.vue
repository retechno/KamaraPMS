<script setup lang="ts">
import ErrorNotice from '@/components/app/ErrorNotice.vue'
import { computed, ref, watch } from 'vue'
import { RouterLink } from 'vue-router'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { Approval, FiscalYear } from '@/api/types'
import { confirm } from '@/composables/useConfirm'
import ApprovalDialog from '@/components/ApprovalDialog.vue'
import DataTable, { type Column } from '@/components/app/DataTable.vue'
import EmptyState from '@/components/app/EmptyState.vue'
import FormField from '@/components/app/FormField.vue'
import PageHeader from '@/components/app/PageHeader.vue'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { t } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'

const auth = useAuthStore()
const property = usePropertyStore()

const years = ref<FiscalYear[]>([])
const loaded = ref(false)
const error = ref<ApiError | null>(null)
const dialogError = ref<ApiError | null>(null)
const notice = ref('')
const busy = ref(false)
const reopening = ref<{ start: string; label: string; reason: string; asking: boolean } | null>(null)

const pid = computed(() => property.currentId)
const can = (p: string) => auth.can(p, pid.value)
const intro = computed(() => t('accounting.fyIntro', { link: '\u0000' }).split('\u0000'))
const columns = computed<Column<FiscalYear>[]>(() => [
  { key: 'label', label: t('accounting.fyYear') },
  { key: 'range', label: t('accounting.fyFromTo') },
  { key: 'months', label: t('accounting.fyMonthsClosed') },
  { key: 'net_income', label: t('accounting.fyResult'), align: 'right', format: 'money' as const },
  { key: 'status', label: t('accounting.status') },
  { key: 'actions', label: '', align: 'right' },
])
const path = { close: '/api/v1/properties/{propertyId}/accounting/fiscal-years/{start}/close', reopen: '/api/v1/properties/{propertyId}/accounting/fiscal-years/{start}/reopen' } as const

async function load(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null || !can('accounting.view')) return
  try {
    const { data } = await api.GET('/api/v1/properties/{propertyId}/accounting/fiscal-years', { params: { path: { propertyId } } })
    years.value = data?.data ?? []
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    loaded.value = true
  }
}

async function close(y: FiscalYear): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null) return
  const question = {
    title: t('accounting.fyCloseTitle', { label: y.label }),
    description: t('accounting.fyCloseHint', { amount: y.net_income }),
    destructive: true,
  }
  if (!(await confirm(question))) return
  busy.value = true
  error.value = null
  notice.value = ''
  try {
    await api.POST(path.close, { params: { path: { propertyId, start: y.year_start } } })
    notice.value = t('accounting.fyIsClosed', { label: y.label })
    await load()
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    busy.value = false
  }
}

async function reopen(approval: Approval): Promise<void> {
  const propertyId = pid.value
  const r = reopening.value
  if (propertyId === null || r === null) return
  busy.value = true
  dialogError.value = null
  try {
    await api.POST(path.reopen, { params: { path: { propertyId, start: r.start } }, body: { reason: r.reason.trim(), approval } })
    notice.value = t('accounting.fyIsOpen', { label: r.label })
    reopening.value = null
    await load()
  } catch (e) {
    dialogError.value = e instanceof ApiError ? e : null
  } finally {
    busy.value = false
  }
}

watch(() => pid.value, () => {
  years.value = []
  loaded.value = false
  void load()
}, { immediate: true })
</script>

<template>
  <PageHeader :title="t('accounting.fyTitle')" />
  <ErrorNotice v-if="error" :error="error" inline data-testid="year-error" />
  <p v-if="notice" class="notice" role="status" data-testid="notice">{{ notice }}</p>
  <p v-if="pid === null" class="muted">{{ t('setup.selectProperty') }}</p>
  <p v-else-if="!can('accounting.view')" class="muted" data-testid="no-access">{{ t('accounting.noAccessView', { what: t('accounting.whatYears'), permission: 'accounting.view' }) }}</p>
  <template v-else>
    <p class="mb-4 text-sm text-muted-foreground">
      {{ intro[0] }}<RouterLink to="/accounting/periods" class="text-primary hover:underline">{{ t('accounting.fyPeriods') }}</RouterLink>{{ intro[1] }}
    </p>
    <Card v-if="reopening && !reopening.asking" class="mb-4">
      <form novalidate data-testid="reopen-form" @submit.prevent="reopening.asking = true">
        <CardHeader>
          <CardTitle>{{ t('accounting.reopenTitle', { label: reopening.label }) }}</CardTitle>
          <p class="m-0 text-sm text-muted-foreground">{{ t('accounting.fyReopenHint') }}</p>
        </CardHeader>
        <CardContent>
          <FormField class="max-w-md" :label="t('accounting.reason')">
            <template #default="{ id }"><Input :id="id" v-model="reopening.reason" name="reason" maxlength="500" /></template>
          </FormField>
          <div class="mt-4 flex justify-end gap-2">
            <Button type="button" variant="outline" @click="reopening = null">{{ t('common.cancel') }}</Button>
            <Button type="submit" :disabled="!reopening.reason.trim()" data-testid="reopen-ask">{{ t('accounting.continue') }}</Button>
          </div>
        </CardContent>
      </form>
    </Card>
    <Card>
      <EmptyState v-if="loaded && !years.length" :title="t('accounting.fyEmpty')" data-testid="empty" />
      <DataTable v-else :columns="columns" :rows="years" row-key="year_start" :row-test-id="(y) => `year-${y.label}`" :caption="t('accounting.fyTitle')" data-testid="years">
        <template #cell-label="{ row }"><b>{{ row.label }}</b></template>
        <template #cell-range="{ row }">{{ $date(row.year_start) }} – {{ $date(row.year_end) }}</template>
        <template #cell-months="{ row }">{{ t('accounting.fyOf', { n: row.closed_months, total: row.months }) }}</template>
        <template #cell-status="{ row }">
          <Badge :variant="row.status === 'CLOSED' ? 'outline' : 'success'">{{ row.status === 'CLOSED' ? t('accounting.closed') : t('accounting.open') }}</Badge>
          <small v-if="row.closing_journal_number" class="text-muted-foreground"> · {{ t('accounting.fyJournal', { number: row.closing_journal_number }) }}</small>
          <small v-if="row.reopen_reason && row.status === 'OPEN'" class="text-muted-foreground"> · {{ t('accounting.fyReopened', { reason: row.reopen_reason }) }}</small>
        </template>
        <template #cell-actions="{ row }">
          <div class="flex justify-end gap-2">
            <Button v-if="can('accounting.close') && row.closable" type="button" size="sm" :disabled="busy" :data-testid="`close-${row.label}`" @click="close(row)">{{ t('accounting.fyCloseYear') }}</Button>
            <Button v-if="can('accounting.close') && row.reopenable" type="button" variant="outline" size="sm" :data-testid="`reopen-${row.label}`" @click="reopening = { start: row.year_start, label: row.label, reason: '', asking: false }">{{ t('accounting.reopen') }}</Button>
          </div>
        </template>
      </DataTable>
    </Card>
  </template>
  <ApprovalDialog v-if="reopening?.asking" :title="t('accounting.approveReopen')" :busy="busy" :error="dialogError" @approve="reopen" @cancel="reopening = null; dialogError = null" />
</template>
