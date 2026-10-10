<script setup lang="ts">
import ErrorNotice from '@/components/app/ErrorNotice.vue'
import { computed, reactive, ref, watch } from 'vue'
import { RouterLink, useRoute } from 'vue-router'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { BankAccount, BankStatement } from '@/api/types'
import { confirm } from '@/composables/useConfirm'
import DataTable, { type Column } from '@/components/app/DataTable.vue'
import EmptyState from '@/components/app/EmptyState.vue'
import FormField from '@/components/app/FormField.vue'
import PageHeader from '@/components/app/PageHeader.vue'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { NativeSelect } from '@/components/ui/native-select'
import { t } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'

const auth = useAuthStore()
const property = usePropertyStore()
const route = useRoute()

const banks = ref<BankAccount[]>([])
const statements = ref<BankStatement[]>([])
const loaded = ref(false)
const error = ref<ApiError | null>(null)
const notice = ref('')
const busy = ref(false)
const bank = ref(Number(route.query.bank) || 0)
const importing = ref(false)
const form = reactive({ period_from: '', period_to: '', opening_balance: '', closing_balance: '', note: '', csv: '', currency: '' })

const columns = computed<Column<BankStatement>[]>(() => [
  { key: 'bank_name', label: t('bankStatements.bank') },
  { key: 'period', label: t('bankStatements.period') },
  { key: 'opening_balance', label: t('bankStatements.opening'), align: 'right', format: 'money' as const },
  { key: 'closing_balance', label: t('bankStatements.closing'), align: 'right', format: 'money' as const },
  { key: 'matched', label: t('bankStatements.matched') },
  { key: 'status', label: t('setup.status') },
  { key: 'actions', label: '', align: 'right' },
])

const pid = computed(() => property.currentId)
const can = (p: string) => auth.can(p, pid.value)
const fieldError = (field: string) => error.value?.fieldMessage(field)
const rowErrors = computed(() => (error.value?.fieldErrors ?? []).filter((f) => f.field.startsWith('rows[')))

async function load(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null || !can('bank.view')) return
  try {
    if (!banks.value.length) {
      const res = await api.GET('/api/v1/properties/{propertyId}/bank/accounts', { params: { path: { propertyId } } })
      banks.value = res.data?.data ?? []
      if (!bank.value && banks.value.length === 1) bank.value = banks.value[0]?.id ?? 0
    }
    const { data } = await api.GET('/api/v1/properties/{propertyId}/bank/statements', { params: { path: { propertyId }, query: { bank_account_id: bank.value || undefined } } })
    statements.value = data?.data ?? []
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    loaded.value = true
  }
}

function startImport(): void {
  Object.assign(form, { period_from: '', period_to: '', opening_balance: '', closing_balance: '', note: '', csv: '', currency: '' })
  // the next statement starts the day after the latest one and opens with its closing balance
  const last = statements.value.filter((s) => s.bank_account_id === bank.value).sort((a, b) => a.period_to.localeCompare(b.period_to)).at(-1)
  if (last) {
    const next = new Date(`${last.period_to}T00:00:00Z`)
    next.setUTCDate(next.getUTCDate() + 1)
    form.period_from = next.toISOString().slice(0, 10)
    form.opening_balance = last.closing_balance
  }
  error.value = null
  importing.value = true
}

async function readFile(ev: Event): Promise<void> {
  const file = (ev.target as HTMLInputElement).files?.[0]
  if (file) form.csv = await file.text()
}

async function runImport(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null) return
  busy.value = true
  error.value = null
  notice.value = ''
  try {
    const { data } = await api.POST('/api/v1/properties/{propertyId}/bank/statements', {
      params: { path: { propertyId } },
      body: {
        bank_account_id: bank.value, period_from: form.period_from, period_to: form.period_to, opening_balance: form.opening_balance.trim(), closing_balance: form.closing_balance.trim(),
        note: form.note || undefined, csv: form.csv, currency: form.currency.trim() || undefined,
      },
    })
    importing.value = false
    notice.value = t('bankStatements.imported', { n: data?.lines.length ?? 0 })
    await load()
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    busy.value = false
  }
}

async function remove(s: BankStatement): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null || !(await confirm({ title: t('bankStatements.deleteTitle', { from: s.period_from, to: s.period_to }), description: t('bankStatements.deleteHint'), destructive: true }))) return
  busy.value = true
  error.value = null
  try {
    await api.DELETE('/api/v1/properties/{propertyId}/bank/statements/{id}', { params: { path: { propertyId, id: s.id } } })
    notice.value = t('bankStatements.deleted')
    await load()
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    busy.value = false
  }
}

watch(() => pid.value, () => {
  banks.value = []
  statements.value = []
  loaded.value = false
  void load()
}, { immediate: true })
</script>

<template>
  <PageHeader :title="t('bankStatements.title')">
    <template #actions>
      <Button v-if="can('bank.reconcile') && bank && !importing" type="button" data-testid="new-statement" @click="startImport">{{ t('bankStatements.import') }}</Button>
    </template>
  </PageHeader>
  <ErrorNotice v-if="error && !importing" :error="error" inline data-testid="statement-error" />
  <p v-if="notice" class="notice" role="status" data-testid="notice">{{ notice }}</p>
  <p v-if="pid === null" class="muted">{{ t('setup.selectProperty') }}</p>
  <p v-else-if="!can('bank.view')" class="muted" data-testid="no-access">{{ t('bankStatements.noAccess', { permission: 'bank.view' }) }}</p>
  <template v-else>
    <Card class="mb-4">
      <form class="flex items-end gap-3 p-4" novalidate @submit.prevent="load">
        <FormField class="w-72" :label="t('bankStatements.bankAccount')">
          <template #default="{ id }">
            <NativeSelect :id="id" v-model.number="bank" name="bank" @change="load">
              <option :value="0">{{ t('bankStatements.all') }}</option>
              <option v-for="b in banks" :key="b.id" :value="b.id">{{ b.name }} ({{ b.account_code }})</option>
            </NativeSelect>
          </template>
        </FormField>
      </form>
    </Card>

    <Card v-if="importing" class="mb-4">
      <form novalidate data-testid="import-form" @submit.prevent="runImport">
        <CardHeader>
          <CardTitle>{{ t('bankStatements.importTitle') }}</CardTitle>
          <p class="m-0 text-sm text-muted-foreground">{{ t('bankStatements.importHint', { date: 'date', description: 'description', reference: 'reference', amount: 'amount', credit: 'credit', debit: 'debit' }) }}</p>
        </CardHeader>
        <CardContent>
          <div class="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
            <FormField :label="t('bankStatements.from')" :error="fieldError('period_from')">
              <template #default="{ id, invalid }"><Input :id="id" v-model="form.period_from" name="period_from" type="date" :aria-invalid="invalid" /></template>
            </FormField>
            <FormField :label="t('bankStatements.to')" :error="fieldError('period_to')">
              <template #default="{ id, invalid }"><Input :id="id" v-model="form.period_to" name="period_to" type="date" :aria-invalid="invalid" /></template>
            </FormField>
            <FormField :label="t('bankStatements.opening')" :error="fieldError('opening_balance')">
              <template #default="{ id, invalid }"><Input :id="id" v-model="form.opening_balance" name="opening_balance" inputmode="decimal" :aria-invalid="invalid" /></template>
            </FormField>
            <FormField :label="t('bankStatements.closing')" :error="fieldError('closing_balance')">
              <template #default="{ id, invalid }"><Input :id="id" v-model="form.closing_balance" name="closing_balance" inputmode="decimal" :aria-invalid="invalid" /></template>
            </FormField>
            <FormField :label="t('bankStatements.currency')" :hint="t('bankStatements.currencyHint', { currency: property.current?.currency_code ?? '' })" :error="fieldError('currency')">
              <template #default="{ id, invalid }"><Input :id="id" v-model="form.currency" name="currency" maxlength="3" :aria-invalid="invalid" /></template>
            </FormField>
            <FormField class="sm:col-span-2 lg:col-span-3" :label="t('bankStatements.note')">
              <template #default="{ id }"><Input :id="id" v-model="form.note" name="note" maxlength="300" /></template>
            </FormField>
          </div>
          <input type="file" accept=".csv,text/csv" class="mt-4 block text-sm" data-testid="import-file" @change="readFile" />
          <textarea
            v-model="form.csv"
            name="csv"
            rows="8"
            class="mt-2 w-full rounded-md border border-border bg-card p-3 font-mono text-sm outline-none focus-visible:ring-2 focus-visible:ring-ring"
            :placeholder="t('bankStatements.csvPlaceholder')"
          />
          <small v-if="fieldError('csv')" role="alert" class="text-xs text-destructive" data-testid="csv-error">{{ fieldError('csv') }}</small>
          <ErrorNotice v-if="error" :error="error" inline data-testid="import-error">
<template v-for="(f, i) in rowErrors.slice(0, 8)" :key="i"><br /><span class="muted">{{ f.field }}: {{ f.message }}</span></template>
</ErrorNotice>
          <div class="mt-4 flex justify-end gap-2">
            <Button type="button" variant="outline" @click="importing = false">{{ t('common.cancel') }}</Button>
            <Button type="submit" :disabled="busy || !form.csv.trim() || !form.period_from || !form.period_to || !form.closing_balance.trim()" data-testid="import-run">{{ t('bankStatements.run') }}</Button>
          </div>
        </CardContent>
      </form>
    </Card>

    <Card>
      <EmptyState v-if="loaded && !statements.length" :description="t('emptyState.bankStatements')" :action-label="can('bank.reconcile') && bank && !importing ? t('bankStatements.import') : ''" @action="startImport" :title="t('bankStatements.empty')" data-testid="empty" />
      <DataTable v-else :columns="columns" :rows="statements" row-key="id" :row-test-id="(s) => `statement-${s.id}`" :caption="t('bankStatements.title')" data-testid="statements">
        <template #cell-period="{ row }">{{ row.period_from }} – {{ row.period_to }}</template>
        <template #cell-matched="{ row }">{{ t('bankStatements.matchedOf', { n: row.matched_count, total: row.line_count }) }}</template>
        <template #cell-status="{ row }"><Badge :variant="row.status === 'RECONCILED' ? 'success' : 'warning'">{{ row.status === 'RECONCILED' ? t('bankStatements.reconciled') : t('bankStatements.open') }}</Badge></template>
        <template #cell-actions="{ row }">
          <div class="flex items-center justify-end gap-2">
            <RouterLink :to="`/bank/statements/${row.id}`" class="text-sm text-primary hover:underline" :data-testid="`open-${row.id}`">{{ row.status === 'OPEN' && can('bank.reconcile') ? t('bankStatements.reconcile') : t('bankStatements.view') }}</RouterLink>
            <Button v-if="row.status === 'OPEN' && can('bank.reconcile')" type="button" variant="outline" size="sm" :data-testid="`delete-${row.id}`" @click="remove(row)">{{ t('common.delete') }}</Button>
          </div>
        </template>
      </DataTable>
    </Card>
  </template>
</template>
