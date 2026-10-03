<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { Approval, CityLedgerAccount, CityLedgerAging, CityLedgerCandidate, CityLedgerInvoice, CityLedgerReceipt, CityLedgerStatement } from '@/api/types'
import ApprovalDialog from '@/components/ApprovalDialog.vue'
import DataTable, { type Column } from '@/components/app/DataTable.vue'
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
import { documentPath, openPdf } from '@/utils/documents'
import { newIdempotencyKey } from '@/utils/reservations'

const props = defineProps<{ id: string }>()
const auth = useAuthStore()
const property = usePropertyStore()

const account = ref<CityLedgerAccount | null>(null)
const aging = ref<CityLedgerAging | null>(null)
const statement = ref<CityLedgerStatement | null>(null)
const receipts = ref<CityLedgerReceipt[]>([])
const candidates = ref<CityLedgerCandidate[]>([])
const invoices = ref<CityLedgerInvoice[]>([])
const picked = ref<number[]>([])
const invoiceNotes = ref('')
const error = ref<ApiError | null>(null)
const dialogError = ref<ApiError | null>(null)
const notice = ref('')
const busy = ref(false)
const period = reactive({ from: '', to: '' })
const receipt = reactive({ amount: '', method: 'BANK_TRANSFER' as CityLedgerReceipt['payment_method'], reference: '', remarks: '' })
const METHODS: CityLedgerReceipt['payment_method'][] = ['BANK_TRANSFER', 'CASH', 'CARD', 'OTHER']
// A void waiting for its reason and its approval: a receipt or an invoice.
const voiding = ref<{ kind: 'receipt' | 'invoice'; id: number; label: string; reason: string; asking: boolean } | null>(null)
// One key per attempt: kept while a request may have been lost, renewed once the server has answered.
let receiptKey = newIdempotencyKey()
let invoiceKey = newIdempotencyKey()
let payKey = newIdempotencyKey()
// Paying an invoice: a receipt that is allocated to it.
const paying = ref<{ invoice: CityLedgerInvoice; amount: string; method: CityLedgerReceipt['payment_method']; reference: string } | null>(null)

const pid = computed(() => property.currentId)
const can = (p: string) => auth.can(p, pid.value)
const businessDate = computed(() => property.clock?.business_date ?? '')
const fieldError = (field: string) => error.value?.fieldMessage(field)
const base = () => ({ path: { propertyId: pid.value as number, id: Number(props.id) } })
const receiptOf = (number: string) => receipts.value.find((r) => r.receipt_number === number)
const voidable = (number: string) => {
  const r = receiptOf(number)
  return !!r && r.status === 'POSTED' && r.business_date === businessDate.value && can('cityledger.receive')
}

async function load(): Promise<void> {
  if (pid.value === null || !can('cityledger.read')) return
  error.value = null
  try {
    const query = { from: period.from || undefined, to: period.to || undefined }
    const [a, g, s, r, c, v] = await Promise.all([
      api.GET('/api/v1/properties/{propertyId}/city-ledger/accounts/{id}', { params: base() }),
      api.GET('/api/v1/properties/{propertyId}/city-ledger/accounts/{id}/aging', { params: base() }),
      api.GET('/api/v1/properties/{propertyId}/city-ledger/accounts/{id}/statement', { params: { ...base(), query } }),
      api.GET('/api/v1/properties/{propertyId}/city-ledger/accounts/{id}/receipts', { params: base() }),
      api.GET('/api/v1/properties/{propertyId}/city-ledger/accounts/{id}/invoice-candidates', { params: base() }),
      api.GET('/api/v1/properties/{propertyId}/city-ledger/accounts/{id}/invoices', { params: base() }),
    ])
    account.value = a.data ?? null
    aging.value = g.data ?? null
    statement.value = s.data ?? null
    receipts.value = r.data?.data ?? []
    candidates.value = c.data?.data ?? []
    invoices.value = v.data?.data ?? []
    picked.value = picked.value.filter((id) => candidates.value.some((x) => x.payment_id === id && x.invoiceable))
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  }
}

async function receive(): Promise<void> {
  busy.value = true
  error.value = null
  notice.value = ''
  try {
    await api.POST('/api/v1/properties/{propertyId}/city-ledger/accounts/{id}/receipts', {
      params: { ...base(), header: { 'Idempotency-Key': receiptKey } },
      body: { amount: receipt.amount, payment_method: receipt.method, reference_number: receipt.reference || undefined, remarks: receipt.remarks || undefined },
    })
    receiptKey = newIdempotencyKey()
    receipt.amount = ''
    receipt.reference = ''
    receipt.remarks = ''
    notice.value = t('clAccount.receiptRecorded')
    await load()
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
    if (e instanceof ApiError) receiptKey = newIdempotencyKey() // the server answered: the next submit is a new attempt
  } finally {
    busy.value = false
  }
}

function startVoid(number: string): void {
  const r = receiptOf(number)
  if (!r) return
  voiding.value = { kind: 'receipt', id: r.id, label: t('clAccount.labelReceipt', { number: r.receipt_number }), reason: '', asking: false }
  dialogError.value = null
  error.value = null
}

async function approveVoid(approval: Approval): Promise<void> {
  const v = voiding.value
  if (!v || pid.value === null) return
  busy.value = true
  dialogError.value = null
  try {
    const params = { path: { propertyId: pid.value, id: v.id } }
    const body = { reason: v.reason, approval }
    if (v.kind === 'receipt') await api.POST('/api/v1/properties/{propertyId}/city-ledger/receipts/{id}/void', { params, body })
    else await api.POST('/api/v1/properties/{propertyId}/city-ledger/invoices/{id}/void', { params, body })
    voiding.value = null
    notice.value = v.kind === 'receipt' ? t('clAccount.receiptVoided') : t('clAccount.invoiceVoided')
    await load()
  } catch (e) {
    dialogError.value = e instanceof ApiError ? e : null
  } finally {
    busy.value = false
  }
}

const statusText = (s: CityLedgerInvoice['payment_status']): string =>
  ({ UNPAID: t('clAccount.unpaid'), PARTIAL: t('clAccount.partial'), PAID: t('clAccount.paidStatus'), VOID: t('clAccount.voided') })[s]

const candidateColumns = computed<Column<CityLedgerCandidate>[]>(() => [
  { key: 'select', label: '' },
  { key: 'checked_out_at', label: t('clAccount.checkOut'), format: 'datetime' as const },
  { key: 'guest_name', label: t('clAccount.guest') },
  { key: 'room_numbers', label: t('clAccount.room') },
  { key: 'folio_number', label: t('clAccount.folio') },
  { key: 'reference_number', label: t('clAccount.reference') },
  { key: 'amount', label: t('clAccount.amount'), align: 'right', format: 'money' as const },
])
const invoiceColumns = computed<Column<CityLedgerInvoice>[]>(() => [
  { key: 'invoice_number', label: t('clAccount.number') },
  { key: 'invoice_date', label: t('clAccount.date'), format: 'date' as const },
  { key: 'due_date', label: t('clAccount.due'), format: 'date' as const },
  { key: 'total', label: t('clAccount.total'), align: 'right', format: 'money' as const },
  { key: 'paid', label: t('clAccount.paid'), align: 'right', format: 'money' as const },
  { key: 'outstanding', label: t('clAccount.outstanding'), align: 'right', format: 'money' as const },
  { key: 'payment_status', label: t('setup.status') },
  { key: 'actions', label: '', align: 'right' },
])

const invoiceable = computed(() => candidates.value.filter((c) => c.invoiceable))
const waiting = computed(() => candidates.value.filter((c) => !c.invoiceable))
const pickedTotal = computed(() => candidates.value.filter((c) => picked.value.includes(c.payment_id)).reduce((sum, c) => sum + Number(c.amount), 0))

function toggleAll(on: boolean): void {
  picked.value = on ? invoiceable.value.map((c) => c.payment_id) : []
}

async function createInvoice(): Promise<void> {
  busy.value = true
  error.value = null
  notice.value = ''
  try {
    const { data } = await api.POST('/api/v1/properties/{propertyId}/city-ledger/accounts/{id}/invoices', {
      params: { ...base(), header: { 'Idempotency-Key': invoiceKey } },
      body: { payment_ids: picked.value, notes: invoiceNotes.value.trim() || undefined },
    })
    invoiceKey = newIdempotencyKey()
    picked.value = []
    invoiceNotes.value = ''
    notice.value = t('clAccount.invoiceIssued', { number: data?.invoice_number ?? '' })
    await load()
  } catch (e) {
    const failure = e instanceof ApiError ? e : null
    if (failure) invoiceKey = newIdempotencyKey() // the server answered: the next submit is a new attempt
    await load() // another user may have invoiced or voided in the meantime
    error.value = failure
  } finally {
    busy.value = false
  }
}

function startPay(inv: CityLedgerInvoice): void {
  paying.value = { invoice: inv, amount: inv.outstanding, method: 'BANK_TRANSFER', reference: '' }
  payKey = newIdempotencyKey()
  error.value = null
}

async function payInvoice(): Promise<void> {
  const v = paying.value
  if (!v) return
  busy.value = true
  error.value = null
  notice.value = ''
  try {
    await api.POST('/api/v1/properties/{propertyId}/city-ledger/accounts/{id}/receipts', {
      params: { ...base(), header: { 'Idempotency-Key': payKey } },
      body: {
        amount: v.amount, payment_method: v.method, reference_number: v.reference || undefined,
        allocations: [{ invoice_id: v.invoice.id, amount: v.amount }],
      },
    })
    notice.value = t('clAccount.paymentRecorded', { number: v.invoice.invoice_number })
    paying.value = null
    await load()
  } catch (e) {
    const failure = e instanceof ApiError ? e : null
    if (failure) payKey = newIdempotencyKey() // the server answered: the next submit is a new attempt
    await load() // another user may have paid or voided in the meantime
    error.value = failure
  } finally {
    busy.value = false
  }
}

function startVoidInvoice(inv: CityLedgerInvoice): void {
  voiding.value = { kind: 'invoice', id: inv.id, label: t('clAccount.labelInvoice', { number: inv.invoice_number }), reason: '', asking: false }
  dialogError.value = null
  error.value = null
}

async function printInvoice(inv: CityLedgerInvoice): Promise<void> {
  if (pid.value === null) return
  error.value = null
  try {
    await openPdf(documentPath.companyInvoice(pid.value, inv.id))
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  }
}

async function printStatement(): Promise<void> {
  if (pid.value === null) return
  error.value = null
  try {
    await openPdf(documentPath.companyStatement(pid.value, Number(props.id), period.from || undefined, period.to || undefined))
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  }
}

watch(() => [pid.value, props.id], () => void load(), { immediate: true })
</script>

<template>
  <PageHeader :title="account ? `${account.code} · ${account.name}` : t('clAccount.fallback')">
    <template #actions><RouterLink to="/city-ledger" class="text-sm text-primary hover:underline">{{ t('clAccount.back') }}</RouterLink></template>
  </PageHeader>

  <p v-if="error" class="alert" role="alert" data-testid="form-error">
    {{ error.message }} <code>{{ error.code }}</code>
    <span v-if="error.code === 'RECEIPT_EXCEEDS_BALANCE'"> {{ t('clAccount.exceeds') }}</span>
  </p>
  <p v-if="notice" class="notice" role="status" data-testid="notice">{{ notice }}</p>
  <p v-if="pid === null" class="muted">{{ t('setup.selectProperty') }}</p>
  <p v-else-if="!can('cityledger.read')" class="muted" data-testid="no-access">{{ t('clAccount.noAccess') }}</p>

  <template v-else-if="account">
    <Card class="mb-4" data-testid="account-summary">
      <CardContent class="pt-4">
        <dl class="m-0 grid gap-x-6 gap-y-3 sm:grid-cols-3 lg:grid-cols-6">
          <div><dt class="text-xs text-muted-foreground">{{ t('clAccount.balance') }}</dt><dd class="m-0 text-sm"><b data-testid="balance">{{ $money(account.balance) }}</b></dd></div>
          <div><dt class="text-xs text-muted-foreground">{{ t('clAccount.transferred') }}</dt><dd class="m-0 text-sm">{{ $money(account.transferred) }}</dd></div>
          <div><dt class="text-xs text-muted-foreground">{{ t('clAccount.received') }}</dt><dd class="m-0 text-sm">{{ $money(account.received) }}</dd></div>
          <div><dt class="text-xs text-muted-foreground">{{ t('clAccount.creditLimit') }}</dt><dd class="m-0 text-sm">{{ account.credit_limit ? $money(account.credit_limit) : t('clAccount.noLimit') }}</dd></div>
          <div><dt class="text-xs text-muted-foreground">{{ t('clAccount.available') }}</dt><dd class="m-0 text-sm">{{ account.available ? $money(account.available) : '-' }}</dd></div>
          <div><dt class="text-xs text-muted-foreground">{{ t('clAccount.terms') }}</dt><dd class="m-0 text-sm">{{ t('clAccount.days', { n: account.payment_terms_days }) }}</dd></div>
        </dl>
        <table v-if="aging" class="mt-4 w-full border-collapse text-sm" data-testid="aging">
          <caption class="pb-1.5 text-left text-xs text-muted-foreground">{{ t('clAccount.aging', { date: $date(aging.as_of) }) }}</caption>
          <thead><tr class="border-b border-border"><th v-for="b in aging.buckets" :key="b.label" class="py-1 text-right text-xs font-medium text-muted-foreground">{{ b.label }}</th></tr></thead>
          <tbody><tr><td v-for="b in aging.buckets" :key="b.label" class="py-1.5 text-right tabular-nums">{{ $money(b.amount) }}</td></tr></tbody>
        </table>
      </CardContent>
    </Card>

    <Card v-if="can('cityledger.receive')" class="mb-4">
      <form novalidate data-testid="receipt-form" @submit.prevent="receive">
        <CardHeader><CardTitle>{{ t('clAccount.recordTitle') }}</CardTitle></CardHeader>
        <CardContent>
          <div class="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
            <FormField :label="t('clAccount.amount')" :error="fieldError('amount')">
              <template #default="{ id, invalid }"><Input :id="id" v-model="receipt.amount" name="amount" inputmode="decimal" :aria-invalid="invalid" /></template>
            </FormField>
            <FormField :label="t('clAccount.method')">
              <template #default="{ id }">
                <NativeSelect :id="id" v-model="receipt.method" name="payment_method">
                  <option v-for="m in METHODS" :key="m" :value="m">{{ t(`clAccount.method_${m}`) }}</option>
                </NativeSelect>
              </template>
            </FormField>
            <FormField :label="t('clAccount.reference')">
              <template #default="{ id }"><Input :id="id" v-model="receipt.reference" name="reference" /></template>
            </FormField>
            <FormField :label="t('clAccount.remarks')">
              <template #default="{ id }"><Input :id="id" v-model="receipt.remarks" name="remarks" /></template>
            </FormField>
          </div>
          <div class="mt-4 flex justify-end"><Button type="submit" :disabled="busy">{{ t('clAccount.recordReceipt') }}</Button></div>
        </CardContent>
      </form>
    </Card>

    <Card v-if="can('cityledger.invoice') && (invoiceable.length || waiting.length)" class="mb-4" data-testid="invoice-builder">
      <CardHeader>
        <CardTitle>{{ t('clAccount.newInvoice') }}</CardTitle>
        <p class="m-0 text-sm text-muted-foreground">{{ t('clAccount.newInvoiceHint') }}</p>
      </CardHeader>
      <CardContent>
        <DataTable :columns="candidateColumns" :rows="candidates" row-key="payment_id" :row-test-id="(c) => `candidate-${c.payment_number}`" :row-class="(c) => (c.invoiceable ? undefined : 'text-muted-foreground')" :caption="t('clAccount.newInvoice')">
          <template #header-select>
            <input
              type="checkbox"
              class="size-4 accent-primary"
              :aria-label="t('clAccount.selectAll')"
              data-testid="pick-all"
              :checked="invoiceable.length > 0 && picked.length === invoiceable.length"
              :disabled="!invoiceable.length"
              @change="toggleAll(($event.target as HTMLInputElement).checked)"
            />
          </template>
          <template #cell-select="{ row }">
            <input v-model="picked" type="checkbox" class="size-4 accent-primary" :value="row.payment_id" :disabled="!row.invoiceable" :aria-label="t('clAccount.invoiceRow', { number: row.payment_number })" />
          </template>
          <template #cell-checked_out_at="{ row }">{{ row.checked_out_at ? row.checked_out_at.slice(0, 10) : '' }}<small v-if="!row.invoiceable" class="ml-1 text-muted-foreground">{{ t('clAccount.inHouse') }}</small></template>
        </DataTable>
        <form class="mt-4 flex flex-wrap items-end gap-3" novalidate @submit.prevent="createInvoice">
          <FormField class="w-80" :label="t('clAccount.invoiceNote')">
            <template #default="{ id }"><Input :id="id" v-model="invoiceNotes" name="invoice_notes" maxlength="500" /></template>
          </FormField>
          <Button type="submit" :disabled="busy || !picked.length" data-testid="create-invoice">{{ t('clAccount.createInvoice', { n: picked.length, total: $money(pickedTotal) }) }}</Button>
        </form>
      </CardContent>
    </Card>

    <Card v-if="paying" class="mb-4">
      <form novalidate data-testid="pay-form" @submit.prevent="payInvoice">
        <CardHeader>
          <CardTitle>{{ t('clAccount.payTitle', { number: paying.invoice.invoice_number }) }}</CardTitle>
          <p class="m-0 text-sm text-muted-foreground">{{ t('clAccount.payHint', { amount: $money(paying.invoice.outstanding) }) }}</p>
        </CardHeader>
        <CardContent>
          <div class="grid gap-4 sm:grid-cols-3">
            <FormField :label="t('clAccount.amount')" :error="fieldError('amount') || fieldError('allocations')">
              <template #default="{ id, invalid }"><Input :id="id" v-model="paying.amount" name="pay_amount" inputmode="decimal" :aria-invalid="invalid" /></template>
            </FormField>
            <FormField :label="t('clAccount.method')">
              <template #default="{ id }">
                <NativeSelect :id="id" v-model="paying.method" name="pay_method">
                  <option v-for="m in METHODS" :key="m" :value="m">{{ t(`clAccount.method_${m}`) }}</option>
                </NativeSelect>
              </template>
            </FormField>
            <FormField :label="t('clAccount.reference')">
              <template #default="{ id }"><Input :id="id" v-model="paying.reference" name="pay_reference" /></template>
            </FormField>
          </div>
          <div class="mt-4 flex justify-end gap-2">
            <Button type="button" variant="outline" @click="paying = null">{{ t('clAccount.keep') }}</Button>
            <Button type="submit" :disabled="busy || !paying.amount" data-testid="pay-submit">{{ t('clAccount.recordPayment') }}</Button>
          </div>
        </CardContent>
      </form>
    </Card>

    <Card class="mb-4" data-testid="invoices">
      <CardHeader><CardTitle>{{ t('clAccount.invoices') }}</CardTitle></CardHeader>
      <CardContent>
        <p v-if="!invoices.length" class="m-0 text-sm text-muted-foreground" data-testid="no-invoices">{{ t('clAccount.noInvoices') }}</p>
        <DataTable v-else :columns="invoiceColumns" :rows="invoices" row-key="id" :row-test-id="(i) => `invoice-${i.invoice_number}`" :row-class="(i) => (i.status === 'VOIDED' ? 'text-muted-foreground line-through' : undefined)" :caption="t('clAccount.invoices')">
          <template #cell-payment_status="{ row }"><Badge variant="outline">{{ statusText(row.payment_status) }}</Badge></template>
          <template #cell-actions="{ row }">
            <div class="flex justify-end gap-1.5">
              <Button v-if="row.status === 'ISSUED' && Number(row.outstanding) > 0 && can('cityledger.receive')" type="button" variant="outline" size="sm" :data-testid="`pay-${row.invoice_number}`" @click="startPay(row)">{{ t('clAccount.pay') }}</Button>
              <Button type="button" variant="outline" size="sm" :data-testid="`print-${row.invoice_number}`" @click="printInvoice(row)">{{ t('clAccount.print') }}</Button>
              <RouterLink v-if="row.status === 'ISSUED' && can('tax.invoice')" :to="{ path: '/tax/invoices', query: { source_type: 'CITY_LEDGER_INVOICE', id: String(row.id) } }" class="inline-flex h-8 items-center rounded-md border border-border px-3 text-sm hover:bg-accent" :data-testid="`tax-invoice-${row.invoice_number}`">{{ t('clAccount.taxInvoice') }}</RouterLink>
              <Button v-if="row.status === 'ISSUED' && can('cityledger.invoice')" type="button" variant="outline" size="sm" :data-testid="`void-${row.invoice_number}`" @click="startVoidInvoice(row)">{{ t('clAccount.void') }}</Button>
            </div>
          </template>
        </DataTable>
      </CardContent>
    </Card>

    <Card data-testid="statement">
      <CardHeader><CardTitle>{{ t('clAccount.statement') }}</CardTitle></CardHeader>
      <CardContent>
        <form class="mb-4 flex flex-wrap items-end gap-3" novalidate @submit.prevent="load()">
          <FormField :label="t('clAccount.from')">
            <template #default="{ id, invalid }"><Input :id="id" v-model="period.from" name="from" type="date" :aria-invalid="invalid || !!fieldError('from')" /></template>
          </FormField>
          <FormField :label="t('clAccount.to')" :error="fieldError('to')">
            <template #default="{ id, invalid }"><Input :id="id" v-model="period.to" name="to" type="date" :aria-invalid="invalid" /></template>
          </FormField>
          <Button type="submit" variant="outline">{{ t('clAccount.show') }}</Button>
          <Button type="button" variant="outline" data-testid="print-statement" @click="printStatement">{{ t('clAccount.printStatement') }}</Button>
        </form>
        <div v-if="statement" class="overflow-x-auto">
          <table class="w-full border-collapse text-sm">
            <thead>
              <tr class="border-b border-border text-left text-xs text-muted-foreground">
                <th class="py-1.5 pr-3 font-medium">{{ t('clAccount.date') }}</th>
                <th class="px-3 font-medium">{{ t('clAccount.number') }}</th>
                <th class="px-3 font-medium">{{ t('clAccount.description') }}</th>
                <th class="px-3 font-medium">{{ t('clAccount.reference') }}</th>
                <th class="px-3 text-right font-medium">{{ t('clAccount.debit') }}</th>
                <th class="px-3 text-right font-medium">{{ t('clAccount.credit') }}</th>
                <th class="px-3 text-right font-medium">{{ t('clAccount.balanceCol') }}</th>
                <th />
              </tr>
            </thead>
            <tbody class="[&_td]:py-1.5 [&_td]:pr-3 [&_tr]:border-b [&_tr]:border-border">
              <tr><td colspan="6">{{ t('clAccount.opening') }}</td><td class="text-right tabular-nums">{{ $money(statement.opening_balance) }}</td><td /></tr>
              <tr v-for="l in statement.lines" :key="`${l.kind}-${l.number}`" :class="{ voided: l.status === 'VOIDED', 'text-muted-foreground line-through': l.status === 'VOIDED' }" :data-testid="`line-${l.number}`">
                <td>{{ $date(l.date) }}</td>
                <td>{{ l.number }}</td>
                <td>{{ l.description }}<template v-if="l.guest_name"> · {{ l.guest_name }}</template><small v-if="l.status === 'VOIDED'"> {{ t('clAccount.voidedNote') }}</small></td>
                <td>{{ l.reference }}</td>
                <td class="text-right tabular-nums">{{ l.debit === '0' ? '' : l.debit }}</td>
                <td class="text-right tabular-nums">{{ l.credit === '0' ? '' : l.credit }}</td>
                <td class="text-right tabular-nums">{{ $money(l.balance) }}</td>
                <td><Button v-if="l.kind === 'RECEIPT' && voidable(l.number)" type="button" variant="outline" size="sm" :data-testid="`void-${l.number}`" @click="startVoid(l.number)">{{ t('clAccount.void') }}</Button></td>
              </tr>
              <tr class="font-semibold"><td colspan="4">{{ t('clAccount.total') }}</td><td class="text-right tabular-nums">{{ $money(statement.total_debit) }}</td><td class="text-right tabular-nums">{{ $money(statement.total_credit) }}</td><td class="text-right tabular-nums"><b>{{ $money(statement.closing_balance) }}</b></td><td /></tr>
            </tbody>
          </table>
        </div>
      </CardContent>
    </Card>
  </template>

  <Card v-if="voiding && !voiding.asking" class="mt-4" data-testid="void-form">
    <CardHeader><CardTitle>{{ t('clAccount.voidTitle', { label: voiding.label }) }}</CardTitle></CardHeader>
    <CardContent>
      <FormField class="max-w-md" :label="t('clAccount.reason')">
        <template #default="{ id }"><Input :id="id" v-model="voiding.reason" name="void_reason" /></template>
      </FormField>
      <div class="mt-4 flex justify-end gap-2">
        <Button type="button" variant="outline" @click="voiding = null">{{ t('clAccount.keep') }}</Button>
        <Button type="button" :disabled="!voiding.reason.trim()" data-testid="void-continue" @click="voiding.asking = true">{{ t('clAccount.continue') }}</Button>
      </div>
    </CardContent>
  </Card>
  <ApprovalDialog v-if="voiding?.asking" :title="t('clAccount.approveVoid')" :busy="busy" :error="dialogError" @approve="approveVoid" @cancel="voiding = null" />
</template>
