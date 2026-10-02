<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { Approval, Bill, GlAccount, Supplier } from '@/api/types'
import ApprovalDialog from '@/components/ApprovalDialog.vue'
import DataTable, { type Column } from '@/components/app/DataTable.vue'
import EmptyState from '@/components/app/EmptyState.vue'
import FormField from '@/components/app/FormField.vue'
import PageHeader from '@/components/app/PageHeader.vue'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Combobox } from '@/components/ui/combobox'
import { NativeSelect } from '@/components/ui/native-select'
import { t } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import { newIdempotencyKey } from '@/utils/reservations'
import { listAccounts } from '@/views/accounting/accountApi'
import { fromMilli, toMilli } from '@/views/accounting/accountMeta'

const auth = useAuthStore()
const property = usePropertyStore()
const route = useRoute()

const bills = ref<Bill[]>([])
const suppliers = ref<Supplier[]>([])
const accounts = ref<GlAccount[]>([])
const opened = ref<Bill | null>(null)
const loaded = ref(false)
const error = ref<ApiError | null>(null)
const dialogError = ref<ApiError | null>(null)
const notice = ref('')
const busy = ref(false)
const filter = reactive({ supplier: Number(route.query.supplier) || 0, status: '', open_only: false, q: '', from: '', to: '' })
const creating = ref(false)
const form = reactive({ supplier_id: 0, invoice: '', bill_date: '', due_date: '', description: '', lines: [] as { account_id: number; description: string; amount: string }[] })
const voiding = ref<{ reason: string; asking: boolean } | null>(null)
let postKey = newIdempotencyKey()

const pid = computed(() => property.currentId)
const can = (p: string) => auth.can(p, pid.value)
const fieldError = (field: string) => error.value?.fieldMessage(field)
const chargeable = computed(() => accounts.value.filter((a) => a.is_postable && a.is_active && a.account_type !== 'REVENUE'))
const total = computed(() => {
  let sum = 0n
  let valid = true
  for (const l of form.lines) {
    if (l.amount.trim() === '') continue
    const n = toMilli(l.amount)
    if (n === null || n <= 0n) valid = false
    else sum += n
  }
  return { sum, valid }
})
const statusLabel = (s: string): string => t(`payables.st_${s}` as 'payables.st_PAID')
const statusVariant = (s: string) => ({ UNPAID: 'warning', PARTIAL: 'secondary', PAID: 'success', VOIDED: 'destructive' })[s] as 'warning' | 'secondary' | 'success' | 'destructive'
const columns = computed<Column<Bill>[]>(() => [
  { key: 'bill_date', label: t('payables.date'), format: 'date' as const },
  { key: 'bill_number', label: t('payables.bill') },
  { key: 'supplier', label: t('payables.supplier') },
  { key: 'supplier_invoice_number', label: t('payables.invoice') },
  { key: 'due_date', label: t('payables.due'), format: 'date' as const },
  { key: 'total', label: t('payables.total'), align: 'right', format: 'money' as const },
  { key: 'outstanding', label: t('payables.owed'), align: 'right', format: 'money' as const },
  { key: 'payment_status', label: t('payables.status') },
])

async function load(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null || !can('payables.view')) return
  try {
    const { data } = await api.GET('/api/v1/properties/{propertyId}/payables/bills', {
      params: {
        path: { propertyId },
        query: {
          supplier_id: filter.supplier || undefined, status: (filter.status || undefined) as 'POSTED' | undefined, open_only: filter.open_only || undefined,
          q: filter.q.trim() || undefined, from: filter.from || undefined, to: filter.to || undefined,
        },
      },
    })
    bills.value = data?.data ?? []
    if (!suppliers.value.length) {
      const res = await api.GET('/api/v1/properties/{propertyId}/payables/suppliers', { params: { path: { propertyId } } })
      suppliers.value = res.data?.data ?? []
    }
    if (can('accounting.view') && !accounts.value.length) accounts.value = await listAccounts(propertyId, { active: true })
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    loaded.value = true
  }
}

async function open(b: Bill): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null) return
  if (opened.value?.id === b.id) {
    opened.value = null
    return
  }
  try {
    const { data } = await api.GET('/api/v1/properties/{propertyId}/payables/bills/{id}', { params: { path: { propertyId, id: b.id } } })
    opened.value = data ?? null
    voiding.value = null
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  }
}

function startNew(): void {
  Object.assign(form, { supplier_id: filter.supplier || 0, invoice: '', bill_date: '', due_date: '', description: '' })
  form.lines = [{ account_id: 0, description: '', amount: '' }]
  applyDefaultAccount()
  error.value = null
  postKey = newIdempotencyKey()
  creating.value = true
}

/** The supplier's usual expense account is put on the first line while it has none. */
function applyDefaultAccount(): void {
  const s = suppliers.value.find((x) => x.id === form.supplier_id)
  const first = form.lines[0]
  if (s?.default_account_id && first && !first.account_id) first.account_id = s.default_account_id
}

async function post(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null) return
  busy.value = true
  error.value = null
  notice.value = ''
  try {
    const { data } = await api.POST('/api/v1/properties/{propertyId}/payables/bills', {
      params: { path: { propertyId }, header: { 'Idempotency-Key': postKey } },
      body: {
        supplier_id: form.supplier_id, supplier_invoice_number: form.invoice, bill_date: form.bill_date, due_date: form.due_date || null, description: form.description || undefined,
        lines: form.lines.map((l) => ({ account_id: l.account_id, description: l.description || undefined, amount: l.amount.trim() })),
      },
    })
    postKey = newIdempotencyKey()
    creating.value = false
    notice.value = t('payables.billEntered', { number: data?.bill_number ?? '' })
    await load()
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    busy.value = false
  }
}

async function voidBill(approval: Approval): Promise<void> {
  const propertyId = pid.value
  const b = opened.value
  if (propertyId === null || b === null || voiding.value === null) return
  busy.value = true
  dialogError.value = null
  try {
    await api.POST('/api/v1/properties/{propertyId}/payables/bills/{id}/void', { params: { path: { propertyId, id: b.id } }, body: { reason: voiding.value.reason.trim(), approval } })
    notice.value = t('payables.billVoided', { number: b.bill_number })
    voiding.value = null
    opened.value = null
    await load()
  } catch (e) {
    dialogError.value = e instanceof ApiError ? e : null
  } finally {
    busy.value = false
  }
}

watch(() => pid.value, () => {
  bills.value = []
  suppliers.value = []
  accounts.value = []
  opened.value = null
  loaded.value = false
  void load()
}, { immediate: true })
</script>

<template>
  <PageHeader :title="t('payables.bTitle')">
    <template #actions>
      <Button v-if="can('payables.post') && !creating" type="button" data-testid="new-bill" @click="startNew">{{ t('payables.bEnter') }}</Button>
    </template>
  </PageHeader>
  <p v-if="error" class="alert" role="alert" data-testid="bill-error">
    {{ error.message }} <code>{{ error.code }}</code>
    <template v-for="(f, i) in (error.fieldErrors ?? []).slice(0, 6)" :key="i"><br /><span class="muted">{{ f.field }}: {{ f.message }}</span></template>
  </p>
  <p v-if="notice" class="notice" role="status" data-testid="notice">{{ notice }}</p>
  <p v-if="pid === null" class="muted">{{ t('setup.selectProperty') }}</p>
  <p v-else-if="!can('payables.view')" class="muted" data-testid="no-access">{{ t('payables.bNoAccess', { permission: 'payables.view' }) }}</p>
  <template v-else>
    <Card v-if="creating" class="mb-4">
      <form novalidate data-testid="bill-form" @submit.prevent="post">
        <CardHeader><CardTitle>{{ t('payables.bFormTitle') }}</CardTitle></CardHeader>
        <CardContent>
          <div class="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
            <FormField :label="t('payables.supplier')" :error="fieldError('supplier_id')">
              <template #default="{ id, invalid }">
                <Combobox :id="id" v-model="form.supplier_id" name="supplier_id" :aria-invalid="invalid" @update:model-value="applyDefaultAccount" :options="[{ value: 0, label: `${t('payables.chooseSupplier')}` }, ...suppliers.filter((x) => x.is_active).map((s) => ({ value: s.id, label: `${s.code} · ${s.name}` }))]" />
              </template>
            </FormField>
            <FormField :label="t('payables.supplierInvoiceNumber')" :error="fieldError('supplier_invoice_number')">
              <template #default="{ id, invalid }"><Input :id="id" v-model="form.invoice" name="invoice" maxlength="60" :aria-invalid="invalid" /></template>
            </FormField>
            <FormField :label="t('payables.billDate')" :error="fieldError('bill_date')">
              <template #default="{ id, invalid }"><Input :id="id" v-model="form.bill_date" name="bill_date" type="date" :aria-invalid="invalid" /></template>
            </FormField>
            <FormField :label="t('payables.dueDate')" :hint="t('payables.dueHint')" :error="fieldError('due_date')">
              <template #default="{ id, invalid }"><Input :id="id" v-model="form.due_date" name="due_date" type="date" :aria-invalid="invalid" /></template>
            </FormField>
            <FormField class="sm:col-span-2 lg:col-span-4" :label="t('payables.description')">
              <template #default="{ id }"><Input :id="id" v-model="form.description" name="description" maxlength="300" /></template>
            </FormField>
          </div>
          <table class="mt-4 w-full border-collapse text-sm">
            <thead>
              <tr class="border-b border-border text-left text-xs text-muted-foreground">
                <th class="py-1.5 pr-3 font-medium">{{ t('payables.chargedTo') }}</th>
                <th class="px-3 font-medium">{{ t('payables.detail') }}</th>
                <th class="px-3 text-right font-medium">{{ t('payables.amount') }}</th>
                <th />
              </tr>
            </thead>
            <tbody class="[&_td]:py-1.5 [&_td]:pr-3 [&_td]:align-top">
              <tr v-for="(l, i) in form.lines" :key="i" :data-testid="`line-${i}`">
                <td>
                  <Combobox v-model="l.account_id" :name="`account_${i}`" :aria-invalid="!!fieldError(`lines[${i}].account_id`)" :options="[{ value: 0, label: `${t('payables.chooseAccount')}` }, ...chargeable.map((a) => ({ value: a.id, label: `${a.code} · ${a.name}` }))]" />
                  <small v-if="fieldError(`lines[${i}].account_id`)" role="alert" class="text-xs text-destructive">{{ fieldError(`lines[${i}].account_id`) }}</small>
                </td>
                <td><Input v-model="l.description" :name="`detail_${i}`" maxlength="300" /></td>
                <td>
                  <Input v-model="l.amount" class="text-right" :name="`amount_${i}`" inputmode="decimal" :aria-invalid="!!fieldError(`lines[${i}].amount`)" />
                  <small v-if="fieldError(`lines[${i}].amount`)" role="alert" class="text-xs text-destructive">{{ fieldError(`lines[${i}].amount`) }}</small>
                </td>
                <td><Button v-if="form.lines.length > 1" type="button" variant="outline" size="sm" :data-testid="`remove-line-${i}`" @click="form.lines.splice(i, 1)">{{ t('payables.remove') }}</Button></td>
              </tr>
            </tbody>
            <tfoot>
              <tr class="border-t border-border">
                <td class="pt-2">
                  <Button type="button" variant="outline" size="sm" data-testid="add-line" @click="form.lines.push({ account_id: 0, description: '', amount: '' })">{{ t('payables.addLine') }}</Button>
                  <small class="ml-2 text-xs text-muted-foreground">{{ t('payables.taxLineHint') }}</small>
                </td>
                <td class="pt-2 text-right"><b>{{ t('payables.total') }}</b></td>
                <td class="pt-2 pr-3 text-right tabular-nums" data-testid="bill-total"><b>{{ $money(fromMilli(total.sum)) }}</b></td>
                <td />
              </tr>
            </tfoot>
          </table>
          <small v-if="fieldError('lines')" role="alert" class="text-xs text-destructive">{{ fieldError('lines') }}</small>
          <div class="mt-4 flex justify-end gap-2">
            <Button type="button" variant="outline" @click="creating = false">{{ t('common.cancel') }}</Button>
            <Button type="submit" :disabled="busy || !total.valid || total.sum <= 0n || !form.supplier_id || !form.invoice.trim() || !form.bill_date || form.lines.some((l) => !l.account_id)" data-testid="bill-post">{{ t('payables.enterTheBill') }}</Button>
          </div>
        </CardContent>
      </form>
    </Card>

    <Card>
      <CardContent class="pt-4">
        <form class="mb-4 grid gap-4 sm:grid-cols-2 lg:grid-cols-6" novalidate @submit.prevent="load">
          <FormField :label="t('payables.supplier')">
            <template #default="{ id }">
              <Combobox :id="id" v-model="filter.supplier" name="supplier" :options="[{ value: 0, label: `${t('payables.all')}` }, ...suppliers.map((s) => ({ value: s.id, label: `${s.code} · ${s.name}` }))]" />
            </template>
          </FormField>
          <FormField :label="t('payables.status')">
            <template #default="{ id }">
              <NativeSelect :id="id" v-model="filter.status" name="status">
                <option value="">{{ t('payables.all') }}</option>
                <option value="POSTED">{{ t('payables.statusEntered') }}</option>
                <option value="VOIDED">{{ t('payables.st_VOIDED') }}</option>
              </NativeSelect>
            </template>
          </FormField>
          <FormField :label="t('payables.from')"><template #default="{ id }"><Input :id="id" v-model="filter.from" name="from" type="date" /></template></FormField>
          <FormField :label="t('payables.to')"><template #default="{ id }"><Input :id="id" v-model="filter.to" name="to" type="date" /></template></FormField>
          <FormField :label="t('payables.search')"><template #default="{ id }"><Input :id="id" v-model="filter.q" name="q" type="search" :placeholder="t('payables.billSearch')" /></template></FormField>
          <div class="flex items-end gap-3">
            <label class="flex items-center gap-2 pb-2 text-sm">
              <input v-model="filter.open_only" name="open_only" type="checkbox" class="size-4 accent-primary" /><span>{{ t('payables.openOnly') }}</span>
            </label>
            <Button type="submit" variant="outline" data-testid="apply">{{ t('payables.apply') }}</Button>
          </div>
        </form>
        <EmptyState v-if="loaded && !bills.length" :title="t('payables.billsEmpty')" data-testid="empty" />
        <DataTable
          v-else
          :columns="columns"
          :rows="bills"
          row-key="id"
          clickable
          :row-test-id="(b) => `bill-${b.bill_number}`"
          :row-class="(b) => (b.status === 'VOIDED' ? 'voided text-muted-foreground line-through' : opened?.id === b.id ? 'bg-accent' : undefined)"
          :is-expanded="(b) => opened?.id === b.id"
          :detail-test-id="() => 'bill-detail'"
          :caption="t('payables.bTitle')"
          data-testid="bills"
          @row-click="open"
        >
          <template #cell-supplier="{ row }">{{ row.supplier_code }} · {{ row.supplier_name }}</template>
          <template #cell-payment_status="{ row }"><Badge :variant="statusVariant(row.payment_status)">{{ statusLabel(row.payment_status) }}</Badge></template>
          <template #detail="{ row }">
            <template v-if="opened">
              <p v-if="opened.description" class="mb-2 mt-0 text-sm text-muted-foreground">{{ opened.description }}</p>
              <table class="w-full border-collapse text-sm">
                <thead><tr class="border-b border-border text-left text-xs text-muted-foreground"><th class="py-1 pr-3 font-medium">#</th><th class="px-3 font-medium">{{ t('payables.account') }}</th><th class="px-3 font-medium">{{ t('payables.detail') }}</th><th class="pl-3 text-right font-medium">{{ t('payables.amount') }}</th></tr></thead>
                <tbody>
                  <tr v-for="l in opened.lines" :key="l.line_no" class="border-b border-border">
                    <td class="py-1 pr-3">{{ l.line_no }}</td>
                    <td class="px-3">{{ l.account_code }} · {{ l.account_name }}</td>
                    <td class="px-3"><small class="text-muted-foreground">{{ l.description }}</small></td>
                    <td class="pl-3 text-right tabular-nums">{{ $money(l.amount) }}</td>
                  </tr>
                </tbody>
              </table>
              <p class="mb-0 mt-2 text-sm text-muted-foreground" @click.stop>
                {{ t('payables.journal', { number: opened.journal_number }) }} · {{ t('payables.paidInfo', { amount: $money(opened.paid) }) }}<template v-if="opened.void_reason"> · {{ t('payables.voidedReason', { reason: opened.void_reason }) }}</template>
              </p>
              <div v-if="can('payables.post') && opened.status === 'POSTED' && row.id === opened.id" class="mt-2" @click.stop>
                <Button v-if="!voiding" type="button" variant="outline" size="sm" data-testid="void" @click="voiding = { reason: '', asking: false }">{{ t('payables.voidEllipsis') }}</Button>
                <form v-else class="flex flex-wrap items-end gap-3" novalidate @submit.prevent="voiding.asking = true">
                  <FormField class="w-80" :label="t('payables.reason')">
                    <template #default="{ id }"><Input :id="id" v-model="voiding.reason" name="reason" maxlength="500" /></template>
                  </FormField>
                  <Button type="button" variant="outline" @click="voiding = null">{{ t('common.cancel') }}</Button>
                  <Button type="submit" :disabled="!voiding.reason.trim()" data-testid="void-ask">{{ t('payables.voidWithApproval') }}</Button>
                </form>
              </div>
            </template>
          </template>
        </DataTable>
      </CardContent>
    </Card>
  </template>
  <ApprovalDialog v-if="voiding?.asking" :title="t('payables.approveVoid')" :busy="busy" :error="dialogError" @approve="voidBill" @cancel="voiding = null; dialogError = null" />
</template>
