<script setup lang="ts">
import ErrorNotice from '@/components/app/ErrorNotice.vue'
import { computed, reactive, ref, watch } from 'vue'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { Approval, OpenBill, Supplier, SupplierPayment } from '@/api/types'
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
import { vAutofocus } from '@/directives/autofocus'
import { t } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import { newIdempotencyKey } from '@/utils/reservations'
import { fromMilli, toMilli } from '@/views/accounting/accountMeta'

const auth = useAuthStore()
const property = usePropertyStore()

const payments = ref<SupplierPayment[]>([])
const suppliers = ref<Supplier[]>([])
const openBills = ref<OpenBill[]>([])
const opened = ref<SupplierPayment | null>(null)
const loaded = ref(false)
const error = ref<ApiError | null>(null)
const dialogError = ref<ApiError | null>(null)
const notice = ref('')
const busy = ref(false)
const filter = reactive({ supplier: 0, status: '' })
const creating = ref(false)
const form = reactive({ supplier_id: 0, payment_date: '', method: 'BANK_TRANSFER', reference: '', remarks: '' })
const amounts = reactive<Record<number, string>>({})
const voiding = ref<{ reason: string; asking: boolean } | null>(null)
let postKey = newIdempotencyKey()

const pid = computed(() => property.currentId)
const can = (p: string) => auth.can(p, pid.value)
const fieldError = (field: string) => error.value?.fieldMessage(field)
const METHODS = ['CASH', 'BANK_TRANSFER', 'OTHER'] as const
const methodLabel = (m: string): string => t(`payables.m_${m}` as 'payables.m_CASH')
const columns = computed<Column<SupplierPayment>[]>(() => [
  { key: 'payment_date', label: t('payables.date'), format: 'date' as const },
  { key: 'payment_number', label: t('payables.payment') },
  { key: 'supplier', label: t('payables.supplier') },
  { key: 'method', label: t('payables.method') },
  { key: 'amount', label: t('payables.amount'), align: 'right', format: 'money' as const },
  { key: 'status', label: t('payables.status') },
])
const openColumns = computed<Column<OpenBill>[]>(() => [
  { key: 'bill_number', label: t('payables.bill') },
  { key: 'supplier_invoice_number', label: t('payables.invoice') },
  { key: 'due_date', label: t('payables.due'), format: 'date' as const },
  { key: 'outstanding', label: t('payables.owed'), align: 'right', format: 'money' as const },
  { key: 'pay', label: t('payables.pay'), align: 'right' },
])

const picked = computed(() => {
  let sum = 0n
  let valid = true
  let count = 0
  for (const b of openBills.value) {
    const v = (amounts[b.bill_id] ?? '').trim()
    if (v === '') continue
    const n = toMilli(v)
    const owed = toMilli(b.outstanding)
    if (n === null || n <= 0n || (owed !== null && n > owed)) valid = false
    else {
      sum += n
      count++
    }
  }
  return { sum, valid, count }
})

async function load(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null || !can('payables.view')) return
  try {
    const { data } = await api.GET('/api/v1/properties/{propertyId}/payables/payments', {
      params: { path: { propertyId }, query: { supplier_id: filter.supplier || undefined, status: (filter.status || undefined) as 'POSTED' | undefined } },
    })
    payments.value = data?.data ?? []
    if (!suppliers.value.length) {
      const res = await api.GET('/api/v1/properties/{propertyId}/payables/suppliers', { params: { path: { propertyId } } })
      suppliers.value = res.data?.data ?? []
    }
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    loaded.value = true
  }
}

async function open(p: SupplierPayment): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null) return
  if (opened.value?.id === p.id) {
    opened.value = null
    return
  }
  try {
    const { data } = await api.GET('/api/v1/properties/{propertyId}/payables/payments/{id}', { params: { path: { propertyId, id: p.id } } })
    opened.value = data ?? null
    voiding.value = null
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  }
}

function startNew(): void {
  Object.assign(form, { supplier_id: filter.supplier || 0, payment_date: '', method: 'BANK_TRANSFER', reference: '', remarks: '' })
  error.value = null
  postKey = newIdempotencyKey()
  creating.value = true
  void loadOpen()
}

async function loadOpen(): Promise<void> {
  const propertyId = pid.value
  openBills.value = []
  for (const k of Object.keys(amounts)) delete amounts[Number(k)]
  if (propertyId === null || !form.supplier_id) return
  try {
    const { data } = await api.GET('/api/v1/properties/{propertyId}/payables/suppliers/{id}/open-bills', { params: { path: { propertyId, id: form.supplier_id } } })
    openBills.value = data?.data ?? []
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  }
}

/** Settles every open bill in full, oldest due date first. */
function payAll(): void {
  for (const b of openBills.value) amounts[b.bill_id] = b.outstanding
}

async function post(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null) return
  busy.value = true
  error.value = null
  notice.value = ''
  try {
    const { data } = await api.POST('/api/v1/properties/{propertyId}/payables/payments', {
      params: { path: { propertyId }, header: { 'Idempotency-Key': postKey } },
      body: {
        supplier_id: form.supplier_id, payment_date: form.payment_date, payment_method: form.method as 'CASH', reference_number: form.reference || undefined, remarks: form.remarks || undefined,
        allocations: openBills.value.filter((b) => (amounts[b.bill_id] ?? '').trim() !== '').map((b) => ({ bill_id: b.bill_id, amount: (amounts[b.bill_id] ?? '').trim() })),
      },
    })
    postKey = newIdempotencyKey()
    creating.value = false
    notice.value = t('payables.paymentMade', { number: data?.payment_number ?? '' })
    await load()
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    busy.value = false
  }
}

async function voidPayment(approval: Approval): Promise<void> {
  const propertyId = pid.value
  const p = opened.value
  if (propertyId === null || p === null || voiding.value === null) return
  busy.value = true
  dialogError.value = null
  try {
    await api.POST('/api/v1/properties/{propertyId}/payables/payments/{id}/void', { params: { path: { propertyId, id: p.id } }, body: { reason: voiding.value.reason.trim(), approval } })
    notice.value = t('payables.billVoided', { number: p.payment_number })
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
  payments.value = []
  suppliers.value = []
  opened.value = null
  loaded.value = false
  void load()
}, { immediate: true })
</script>

<template>
  <PageHeader :title="t('payables.pTitle')">
    <template #actions>
      <Button v-if="can('payables.post') && !creating" type="button" data-testid="new-payment" @click="startNew">{{ t('payables.pPay') }}</Button>
    </template>
  </PageHeader>
  <ErrorNotice v-if="error" :error="error" inline data-testid="payment-error">
<template v-for="(f, i) in (error.fieldErrors ?? []).slice(0, 6)" :key="i"><br /><span class="muted">{{ f.field }}: {{ f.message }}</span></template>
</ErrorNotice>
  <p v-if="notice" class="notice" role="status" data-testid="notice">{{ notice }}</p>
  <p v-if="pid === null" class="muted">{{ t('setup.selectProperty') }}</p>
  <p v-else-if="!can('payables.view')" class="muted" data-testid="no-access">{{ t('payables.pNoAccess', { permission: 'payables.view' }) }}</p>
  <template v-else>
    <Card v-if="creating" class="mb-4">
      <form v-autofocus novalidate data-testid="payment-form" @submit.prevent="post">
        <CardHeader><CardTitle>{{ t('payables.pPay') }}</CardTitle></CardHeader>
        <CardContent>
          <div class="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
            <FormField :label="t('payables.supplier')" :error="fieldError('supplier_id')">
              <template #default="{ id, invalid }">
                <Combobox :id="id" v-model="form.supplier_id" name="supplier_id" :aria-invalid="invalid" @update:model-value="loadOpen" :options="[{ value: 0, label: `${t('payables.chooseSupplier')}` }, ...suppliers.map((s) => ({ value: s.id, label: `${s.code} · ${s.name} (${t('payables.owedSuffix', { amount: $money(s.outstanding) })})` }))]" />
              </template>
            </FormField>
            <FormField :label="t('payables.paymentDate')" :error="fieldError('payment_date')">
              <template #default="{ id, invalid }"><Input :id="id" v-model="form.payment_date" name="payment_date" type="date" :aria-invalid="invalid" /></template>
            </FormField>
            <FormField :label="t('payables.paidFrom')">
              <template #default="{ id }">
                <NativeSelect :id="id" v-model="form.method" name="method">
                  <option v-for="m in METHODS" :key="m" :value="m">{{ methodLabel(m) }}</option>
                </NativeSelect>
              </template>
            </FormField>
            <FormField :label="t('payables.reference')">
              <template #default="{ id }"><Input :id="id" v-model="form.reference" name="reference" maxlength="100" /></template>
            </FormField>
            <FormField class="sm:col-span-2 lg:col-span-4" :label="t('payables.remarks')">
              <template #default="{ id }"><Input :id="id" v-model="form.remarks" name="remarks" maxlength="500" /></template>
            </FormField>
          </div>
          <p v-if="form.supplier_id && !openBills.length" class="mb-0 mt-3 text-sm text-muted-foreground" data-testid="nothing-owed">{{ t('payables.nothingOwed') }}</p>
          <template v-if="openBills.length">
            <DataTable class="mt-4" :columns="openColumns" :rows="openBills" row-key="bill_id" :row-test-id="(b) => `open-${b.bill_number}`" :caption="t('payables.pPay')" data-testid="open-bills">
              <template #cell-pay="{ row }">
                <Input v-model="amounts[row.bill_id]" class="ml-auto w-36 text-right" :name="`pay_${row.bill_number}`" inputmode="decimal" :aria-invalid="!!fieldError(`allocations[${openBills.indexOf(row)}].amount`)" />
                <small v-if="fieldError(`allocations[${openBills.indexOf(row)}].amount`)" role="alert" class="text-xs text-destructive">{{ fieldError(`allocations[${openBills.indexOf(row)}].amount`) }}</small>
              </template>
              <template #footer>
                <div class="mt-2 flex items-center justify-between border-t border-border pt-3">
                  <Button type="button" variant="outline" size="sm" data-testid="pay-all" @click="payAll">{{ t('payables.payEverything') }}</Button>
                  <span class="text-sm"><b>{{ t('payables.payment') }}</b> <b class="ml-3 tabular-nums" data-testid="payment-total">{{ $money(fromMilli(picked.sum)) }}</b></span>
                </div>
              </template>
            </DataTable>
          </template>
          <div class="mt-4 flex justify-end gap-2">
            <Button type="button" variant="outline" @click="creating = false">{{ t('common.cancel') }}</Button>
            <Button type="submit" :disabled="busy || !picked.valid || picked.count === 0 || !form.payment_date" data-testid="payment-post">{{ t('payables.makePayment') }}</Button>
          </div>
        </CardContent>
      </form>
    </Card>

    <Card>
      <CardContent class="pt-4">
        <form class="mb-4 flex flex-wrap items-end gap-4" novalidate @submit.prevent="load">
          <FormField class="w-64" :label="t('payables.supplier')">
            <template #default="{ id }">
              <Combobox :id="id" v-model="filter.supplier" name="supplier" :options="[{ value: 0, label: `${t('payables.all')}` }, ...suppliers.map((s) => ({ value: s.id, label: `${s.code} · ${s.name}` }))]" />
            </template>
          </FormField>
          <FormField class="w-44" :label="t('payables.status')">
            <template #default="{ id }">
              <NativeSelect :id="id" v-model="filter.status" name="status">
                <option value="">{{ t('payables.all') }}</option>
                <option value="POSTED">{{ t('payables.pStatusMade') }}</option>
                <option value="VOIDED">{{ t('payables.st_VOIDED') }}</option>
              </NativeSelect>
            </template>
          </FormField>
          <Button type="submit" variant="outline" data-testid="apply">{{ t('payables.apply') }}</Button>
        </form>
        <EmptyState v-if="loaded && !payments.length" :title="t('payables.pEmpty')" data-testid="empty" />
        <DataTable
          v-else
          :columns="columns"
          :rows="payments"
          row-key="id"
          clickable
          :row-test-id="(p) => `payment-${p.payment_number}`"
          :row-class="(p) => (p.status === 'VOIDED' ? 'voided text-muted-foreground line-through' : opened?.id === p.id ? 'bg-accent' : undefined)"
          :is-expanded="(p) => opened?.id === p.id"
          :detail-test-id="() => 'payment-detail'"
          :caption="t('payables.pTitle')"
          data-testid="payments"
          @row-click="open"
        >
          <template #cell-supplier="{ row }">{{ row.supplier_code }} · {{ row.supplier_name }}</template>
          <template #cell-method="{ row }">{{ methodLabel(row.payment_method) }}<small v-if="row.reference_number" class="text-muted-foreground"> · {{ row.reference_number }}</small></template>
          <template #cell-status="{ row }"><Badge :variant="row.status === 'VOIDED' ? 'destructive' : 'success'">{{ row.status === 'VOIDED' ? t('payables.st_VOIDED') : t('payables.pStatusMade') }}</Badge></template>
          <template #detail="{ row }">
            <template v-if="opened && opened.id === row.id">
              <table class="w-full border-collapse text-sm">
                <thead><tr class="border-b border-border text-left text-xs text-muted-foreground"><th class="py-1 pr-3 font-medium">{{ t('payables.bill') }}</th><th class="px-3 font-medium">{{ t('payables.invoice') }}</th><th class="pl-3 text-right font-medium">{{ t('payables.settled') }}</th></tr></thead>
                <tbody>
                  <tr v-for="a in opened.allocations" :key="a.bill_id" class="border-b border-border"><td class="py-1 pr-3">{{ a.bill_number }}</td><td class="px-3">{{ a.supplier_invoice_number }}</td><td class="pl-3 text-right tabular-nums">{{ $money(a.amount) }}</td></tr>
                </tbody>
              </table>
              <p class="mb-0 mt-2 text-sm text-muted-foreground">
                {{ t('payables.journal', { number: opened.journal_number }) }}<template v-if="opened.void_reason"> · {{ t('payables.voidedReason', { reason: opened.void_reason }) }}</template><template v-if="opened.remarks"> · {{ opened.remarks }}</template>
              </p>
              <div v-if="can('payables.post') && opened.status === 'POSTED'" class="mt-2">
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
  <ApprovalDialog v-if="voiding?.asking" :title="t('payables.approveVoid')" :busy="busy" :error="dialogError" @approve="voidPayment" @cancel="voiding = null; dialogError = null" />
</template>
