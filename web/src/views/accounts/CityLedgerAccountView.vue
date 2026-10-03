<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { Approval, CityLedgerAccount, CityLedgerAdjustment, CityLedgerAging, CityLedgerCandidate, CityLedgerInvoice, CityLedgerReceipt, CityLedgerReminder, CityLedgerStatement, GlAccount } from '@/api/types'
import ApprovalDialog from '@/components/ApprovalDialog.vue'
import DataTable, { type Column } from '@/components/app/DataTable.vue'
import FormField from '@/components/app/FormField.vue'
import PageHeader from '@/components/app/PageHeader.vue'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Combobox } from '@/components/ui/combobox'
import { Input } from '@/components/ui/input'
import { NativeSelect } from '@/components/ui/native-select'
import { t } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import { documentPath, openPdf } from '@/utils/documents'
import { newIdempotencyKey } from '@/utils/reservations'
import { listAccounts } from '@/views/accounting/accountApi'
import { fromMilli, toMilli } from '@/views/accounting/accountMeta'

const props = defineProps<{ id: string }>()
const auth = useAuthStore()
const property = usePropertyStore()

const account = ref<CityLedgerAccount | null>(null)
const aging = ref<CityLedgerAging | null>(null)
const statement = ref<CityLedgerStatement | null>(null)
const receipts = ref<CityLedgerReceipt[]>([])
const candidates = ref<CityLedgerCandidate[]>([])
const invoices = ref<CityLedgerInvoice[]>([])
const adjustments = ref<CityLedgerAdjustment[]>([])
const reminders = ref<CityLedgerReminder[]>([])
const chart = ref<GlAccount[]>([])
const taxes = ref<{ id: number; code: string; name: string; rate: string; is_active: boolean }[]>([])
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
const voiding = ref<{ kind: 'receipt' | 'invoice' | 'adjustment'; id: number; label: string; reason: string; asking: boolean } | null>(null)
// One key per attempt: kept while a request may have been lost, renewed once the server has answered.
let receiptKey = newIdempotencyKey()
let invoiceKey = newIdempotencyKey()
let payKey = newIdempotencyKey()
let adjustKey = newIdempotencyKey()
/** A credit note or a write-off being made: against an invoice or a transfer, with the reason and then the approval. */
interface AdjustForm {
  mode: 'credit' | 'writeoff'
  invoice: CityLedgerInvoice | null
  transfer: CityLedgerCandidate | null
  lines: { description: string; account_id: number; net_amount: string; tax_id: number }[]
  amount: string
  account_id: number
  reason: string
  asking: boolean
}
const adjusting = ref<AdjustForm | null>(null)
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
    const [a, g, s, r, c, v, j, m] = await Promise.all([
      api.GET('/api/v1/properties/{propertyId}/city-ledger/accounts/{id}', { params: base() }),
      api.GET('/api/v1/properties/{propertyId}/city-ledger/accounts/{id}/aging', { params: base() }),
      api.GET('/api/v1/properties/{propertyId}/city-ledger/accounts/{id}/statement', { params: { ...base(), query } }),
      api.GET('/api/v1/properties/{propertyId}/city-ledger/accounts/{id}/receipts', { params: base() }),
      api.GET('/api/v1/properties/{propertyId}/city-ledger/accounts/{id}/invoice-candidates', { params: base() }),
      api.GET('/api/v1/properties/{propertyId}/city-ledger/accounts/{id}/invoices', { params: base() }),
      api.GET('/api/v1/properties/{propertyId}/city-ledger/accounts/{id}/adjustments', { params: base() }),
      api.GET('/api/v1/properties/{propertyId}/city-ledger/accounts/{id}/reminders', { params: base() }),
    ])
    account.value = a.data ?? null
    aging.value = g.data ?? null
    statement.value = s.data ?? null
    receipts.value = r.data?.data ?? []
    candidates.value = c.data?.data ?? []
    invoices.value = v.data?.data ?? []
    adjustments.value = j.data?.data ?? []
    reminders.value = m.data?.data ?? []
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
    else if (v.kind === 'adjustment') await api.POST('/api/v1/properties/{propertyId}/city-ledger/adjustments/{id}/void', { params, body })
    else await api.POST('/api/v1/properties/{propertyId}/city-ledger/invoices/{id}/void', { params, body })
    voiding.value = null
    notice.value = v.kind === 'receipt' ? t('clAccount.receiptVoided') : v.kind === 'adjustment' ? t('clAccount.adjustmentVoided') : t('clAccount.invoiceVoided')
    await load()
  } catch (e) {
    dialogError.value = e instanceof ApiError ? e : null
  } finally {
    busy.value = false
  }
}

const statusText = (s: CityLedgerInvoice['payment_status']): string =>
  ({ UNPAID: t('clAccount.unpaid'), PARTIAL: t('clAccount.partial'), PAID: t('clAccount.paidStatus'), CREDITED: t('clAccount.creditedStatus'), WRITTEN_OFF: t('clAccount.writtenOffStatus'), VOID: t('clAccount.voided') })[s]

const candidateColumns = computed<Column<CityLedgerCandidate>[]>(() => [
  { key: 'select', label: '' },
  { key: 'checked_out_at', label: t('clAccount.checkOut'), format: 'datetime' as const },
  { key: 'guest_name', label: t('clAccount.guest') },
  { key: 'room_numbers', label: t('clAccount.room') },
  { key: 'folio_number', label: t('clAccount.folio') },
  { key: 'reference_number', label: t('clAccount.reference') },
  { key: 'amount', label: t('clAccount.amount'), align: 'right', format: 'money' as const },
  { key: 'actions', label: '', align: 'right' },
])
const invoiceColumns = computed<Column<CityLedgerInvoice>[]>(() => [
  { key: 'invoice_number', label: t('clAccount.number') },
  { key: 'invoice_date', label: t('clAccount.date'), format: 'date' as const },
  { key: 'due_date', label: t('clAccount.due'), format: 'date' as const },
  { key: 'total', label: t('clAccount.total'), align: 'right', format: 'money' as const },
  { key: 'paid', label: t('clAccount.paid'), align: 'right', format: 'money' as const },
  { key: 'taken', label: t('clAccount.takenOff'), align: 'right' },
  { key: 'outstanding', label: t('clAccount.outstanding'), align: 'right', format: 'money' as const },
  { key: 'payment_status', label: t('setup.status') },
  { key: 'actions', label: '', align: 'right' },
])

const reminderColumns = computed<Column<CityLedgerReminder>[]>(() => [
  { key: 'reminder_date', label: t('clReminders.date'), format: 'date' as const },
  { key: 'number', label: t('clReminders.number') },
  { key: 'level', label: t('clReminders.level') },
  { key: 'items', label: t('clReminders.invoices') },
  { key: 'total_outstanding', label: t('clReminders.outstanding'), align: 'right', format: 'money' as const },
  { key: 'total_interest', label: t('clReminders.interest'), align: 'right', format: 'money' as const },
  { key: 'actions', label: '', align: 'right' },
])
async function printReminder(rem: CityLedgerReminder): Promise<void> {
  if (pid.value === null) return
  error.value = null
  try {
    await openPdf(documentPath.reminder(pid.value, rem.id))
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  }
}
const adjustmentColumns = computed<Column<CityLedgerAdjustment>[]>(() => [
  { key: 'business_date', label: t('clAccount.date'), format: 'date' as const },
  { key: 'number', label: t('clAccount.number') },
  { key: 'kind', label: t('clAccount.kind') },
  { key: 'target', label: t('clAccount.target') },
  { key: 'amount', label: t('clAccount.amount'), align: 'right', format: 'money' as const },
  { key: 'status', label: t('setup.status') },
  { key: 'actions', label: '', align: 'right' },
])
const revenue = computed(() => chart.value.filter((a) => a.account_type === 'REVENUE' && a.is_postable && a.is_active))
const bearers = computed(() => chart.value.filter((a) => a.is_postable && a.is_active && (a.account_type === 'EXPENSE' || a.code === '1240')))
const adjustTarget = computed(() => (adjusting.value?.invoice ? adjusting.value.invoice.invoice_number : adjusting.value?.transfer?.payment_number ?? ''))
const adjustLimit = computed(() => {
  const a = adjusting.value
  if (!a) return '0'
  if (a.invoice) return a.invoice.outstanding
  return a.transfer?.net ?? '0'
})
/** The tax of a line as the server will take it: the rate of the tax on the net amount (shown as a hint; the server decides). */
function lineTotal(l: { net_amount: string; tax_id: number }): bigint {
  const net = toMilli(l.net_amount)
  if (net === null || net <= 0n) return 0n
  const tax = taxes.value.find((x) => x.id === l.tax_id)
  return tax ? net + (net * BigInt(Math.round(Number(tax.rate) * 1000))) / 100000n : net
}
const adjustTotal = computed(() => {
  const a = adjusting.value
  if (!a) return 0n
  if (a.mode === 'writeoff') return toMilli(a.amount) ?? 0n
  return a.lines.reduce((sum, l) => sum + lineTotal(l), 0n)
})

async function loadChart(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null || chart.value.length) return
  try {
    if (auth.can('accounting.view', propertyId)) chart.value = await listAccounts(propertyId, { active: true })
    const res = await api.GET('/api/v1/properties/{propertyId}/taxes', { params: { path: { propertyId } } })
    taxes.value = ((res.data as { data?: typeof taxes.value } | undefined)?.data ?? []).filter((x) => x.is_active)
  } catch {
    // the roles that make credit notes may not read the chart or the taxes: the form then takes the ids
  }
}

function startAdjust(mode: 'credit' | 'writeoff', invoice: CityLedgerInvoice | null, transfer: CityLedgerCandidate | null): void {
  adjusting.value = {
    mode, invoice, transfer, lines: [{ description: '', account_id: 0, net_amount: '', tax_id: 0 }], amount: invoice?.outstanding ?? '', account_id: 0, reason: '', asking: false,
  }
  adjustKey = newIdempotencyKey()
  error.value = null
  notice.value = ''
  dialogError.value = null
  void loadChart()
}

async function submitAdjust(approval: Approval): Promise<void> {
  const a = adjusting.value
  const propertyId = pid.value
  if (!a || propertyId === null) return
  busy.value = true
  dialogError.value = null
  try {
    if (a.mode === 'credit') {
      await api.POST('/api/v1/properties/{propertyId}/city-ledger/credit-notes', {
        params: { path: { propertyId }, header: { 'Idempotency-Key': adjustKey } },
        body: {
          invoice_id: a.invoice?.id, payment_id: a.transfer?.payment_id, reason: a.reason.trim(), approval,
          lines: a.lines.map((l) => ({ description: l.description.trim(), account_id: l.account_id, net_amount: l.net_amount.trim(), tax_id: l.tax_id || undefined })),
        },
      })
    } else {
      await api.POST('/api/v1/properties/{propertyId}/city-ledger/write-offs', {
        params: { path: { propertyId }, header: { 'Idempotency-Key': adjustKey } },
        body: { invoice_id: a.invoice?.id ?? 0, amount: a.amount.trim(), account_id: a.account_id, reason: a.reason.trim(), approval },
      })
    }
    adjustKey = newIdempotencyKey()
    notice.value = a.mode === 'credit' ? t('clAccount.creditNoteMade') : t('clAccount.writeOffMade')
    adjusting.value = null
    await load()
  } catch (e) {
    const failure = e instanceof ApiError ? e : null
    if (failure && failure.code !== 'APPROVAL_INVALID_CREDENTIALS') {
      // a failure that is not the approval: back to the form with the message
      error.value = failure
      a.asking = false
      adjustKey = newIdempotencyKey()
    } else {
      dialogError.value = failure
    }
  } finally {
    busy.value = false
  }
}

function startVoidAdjustment(adj: CityLedgerAdjustment): void {
  voiding.value = { kind: 'adjustment', id: adj.id, label: t('clAccount.labelAdjustment', { number: adj.number }), reason: '', asking: false }
  dialogError.value = null
  error.value = null
}

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

async function printCreditNote(adj: CityLedgerAdjustment): Promise<void> {
  if (pid.value === null) return
  error.value = null
  try {
    await openPdf(documentPath.creditNote(pid.value, adj.id))
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
        <dl class="m-0 grid gap-x-6 gap-y-3 sm:grid-cols-3 lg:grid-cols-7">
          <div><dt class="text-xs text-muted-foreground">{{ t('clAccount.balance') }}</dt><dd class="m-0 text-sm"><b data-testid="balance">{{ $money(account.balance) }}</b></dd></div>
          <div><dt class="text-xs text-muted-foreground">{{ t('clAccount.transferred') }}</dt><dd class="m-0 text-sm">{{ $money(account.transferred) }}</dd></div>
          <div><dt class="text-xs text-muted-foreground">{{ t('clAccount.received') }}</dt><dd class="m-0 text-sm">{{ $money(account.received) }}</dd></div>
          <div><dt class="text-xs text-muted-foreground">{{ t('clAccount.adjusted') }}</dt><dd class="m-0 text-sm" data-testid="adjusted">{{ $money(account.adjusted) }}</dd></div>
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

    <Card v-if="(can('cityledger.invoice') || can('cityledger.credit_note')) && (invoiceable.length || waiting.length)" class="mb-4" data-testid="invoice-builder">
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
          <template #cell-checked_out_at="{ row }">{{ row.checked_out_at ? row.checked_out_at.slice(0, 10) : '' }}<small v-if="!row.invoiceable && row.stay_status !== 'CHECKED_OUT'" class="ml-1 text-muted-foreground">{{ t('clAccount.inHouse') }}</small></template>
          <template #cell-amount="{ row }">{{ $money(row.amount) }}<small v-if="Number(row.credited)" class="block text-muted-foreground" :data-testid="`credited-${row.payment_number}`">{{ t('clAccount.creditedOf', { amount: $money(row.credited), net: $money(row.net) }) }}</small></template>
          <template #cell-actions="{ row }"><Button v-if="can('cityledger.credit_note') && Number(row.net) > 0" type="button" variant="outline" size="sm" :data-testid="`credit-transfer-${row.payment_number}`" @click="startAdjust('credit', null, row)">{{ t('clAccount.creditNoteEllipsis') }}</Button></template>
        </DataTable>
        <form class="mt-4 flex flex-wrap items-end gap-3" novalidate @submit.prevent="createInvoice">
          <FormField class="w-80" :label="t('clAccount.invoiceNote')">
            <template #default="{ id }"><Input :id="id" v-model="invoiceNotes" name="invoice_notes" maxlength="500" /></template>
          </FormField>
          <Button type="submit" :disabled="busy || !picked.length" data-testid="create-invoice">{{ t('clAccount.createInvoice', { n: picked.length, total: $money(pickedTotal) }) }}</Button>
        </form>
      </CardContent>
    </Card>

    <Card v-if="adjusting && !adjusting.asking" class="mb-4" data-testid="adjust-form">
      <form novalidate @submit.prevent="adjusting.asking = true">
        <CardHeader>
          <CardTitle>{{ adjusting.mode === 'credit' ? t('clAccount.creditNoteTitle', { target: adjustTarget }) : t('clAccount.writeOffTitle', { target: adjustTarget }) }}</CardTitle>
          <p class="m-0 text-sm text-muted-foreground">{{ adjusting.mode === 'credit' ? t(adjusting.invoice ? 'clAccount.creditNoteHintInvoice' : 'clAccount.creditNoteHintTransfer', { amount: $money(adjustLimit) }) : t('clAccount.writeOffHint', { amount: $money(adjustLimit) }) }}</p>
        </CardHeader>
        <CardContent>
          <template v-if="adjusting.mode === 'credit'">
            <table class="w-full border-collapse text-sm">
              <thead><tr class="border-b border-border text-left text-xs text-muted-foreground"><th class="py-1 pr-3 font-medium">{{ t('clAccount.lineDescription') }}</th><th class="px-3 font-medium">{{ t('clAccount.lineAccount') }}</th><th class="px-3 text-right font-medium">{{ t('clAccount.lineNet') }}</th><th class="px-3 font-medium">{{ t('clAccount.lineTax') }}</th><th /></tr></thead>
              <tbody class="[&_td]:py-1.5 [&_td]:pr-3 [&_td]:align-top">
                <tr v-for="(l, i) in adjusting.lines" :key="i" :data-testid="`adjust-line-${i}`">
                  <td><Input v-model="l.description" :name="`line_description_${i}`" maxlength="200" /></td>
                  <td>
                    <Combobox v-if="revenue.length" v-model="l.account_id" :name="`line_account_${i}`" :options="[{ value: 0, label: t('clAccount.chooseAccount') }, ...revenue.map((a) => ({ value: a.id, label: `${a.code} · ${a.name}` }))]" />
                    <Input v-else :model-value="l.account_id || ''" :name="`line_account_${i}`" inputmode="numeric" @update:model-value="(v) => (l.account_id = Number(v) || 0)" />
                  </td>
                  <td><Input v-model="l.net_amount" class="text-right" :name="`line_net_${i}`" inputmode="decimal" :aria-invalid="!!fieldError(`lines[${i}].net_amount`)" /></td>
                  <td>
                    <Combobox v-model="l.tax_id" :name="`line_tax_${i}`" :options="[{ value: 0, label: t('clAccount.noTax') }, ...taxes.map((x) => ({ value: x.id, label: `${x.code} · ${Number(x.rate)}%` }))]" />
                  </td>
                  <td><Button v-if="adjusting.lines.length > 1" type="button" variant="outline" size="sm" @click="adjusting.lines.splice(i, 1)">{{ t('clAccount.removeLine') }}</Button></td>
                </tr>
              </tbody>
              <tfoot><tr class="border-t border-border"><td class="pt-2"><Button type="button" variant="outline" size="sm" data-testid="add-line" @click="adjusting.lines.push({ description: '', account_id: 0, net_amount: '', tax_id: 0 })">{{ t('clAccount.addLine') }}</Button></td><td class="pt-2 text-right" colspan="2"><b>{{ t('clAccount.creditTotal') }}</b></td><td class="pt-2 text-right tabular-nums" data-testid="adjust-total"><b>{{ $money(fromMilli(adjustTotal)) }}</b></td><td /></tr></tfoot>
            </table>
            <small v-if="fieldError('lines')" role="alert" class="text-xs text-destructive">{{ fieldError('lines') }}</small>
          </template>
          <div v-else class="grid gap-4 sm:grid-cols-3">
            <FormField :label="t('clAccount.amount')" :error="fieldError('amount')">
              <template #default="{ id, invalid }"><Input :id="id" v-model="adjusting.amount" name="writeoff_amount" inputmode="decimal" :aria-invalid="invalid" /></template>
            </FormField>
            <FormField :label="t('clAccount.bearingAccount')" :hint="t('clAccount.bearingHint')" :error="fieldError('account_id')">
              <template #default="{ id, invalid }">
                <Combobox v-if="bearers.length" :id="id" v-model="adjusting.account_id" name="writeoff_account" :aria-invalid="invalid" :options="[{ value: 0, label: t('clAccount.chooseAccount') }, ...bearers.map((a) => ({ value: a.id, label: `${a.code} · ${a.name}` }))]" />
                <Input v-else :id="id" :model-value="adjusting.account_id || ''" name="writeoff_account" inputmode="numeric" @update:model-value="(v) => (adjusting && (adjusting.account_id = Number(v) || 0))" />
              </template>
            </FormField>
          </div>
          <FormField class="mt-4 max-w-xl" :label="t('clAccount.reason')" :error="fieldError('reason')">
            <template #default="{ id, invalid }"><Input :id="id" v-model="adjusting.reason" name="adjust_reason" maxlength="500" :aria-invalid="invalid" /></template>
          </FormField>
          <div class="mt-4 flex justify-end gap-2">
            <Button type="button" variant="outline" @click="adjusting = null">{{ t('clAccount.keep') }}</Button>
            <Button type="submit" :disabled="busy || !adjusting.reason.trim() || (adjusting.mode === 'credit' ? adjustTotal <= 0n || adjusting.lines.some((l) => !l.account_id || !l.description.trim()) : !adjusting.account_id || !adjusting.amount.trim())" data-testid="adjust-continue">{{ t('clAccount.continue') }}</Button>
          </div>
        </CardContent>
      </form>
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
          <template #cell-taken="{ row }">{{ Number(row.credited) + Number(row.written_off) ? $money(String(Number(row.credited) + Number(row.written_off))) : '' }}<small v-if="row.attached_credits?.length" class="block text-muted-foreground">{{ t('clAccount.attached', { n: row.attached_credits.length }) }}</small></template>
          <template #cell-actions="{ row }">
            <div class="flex justify-end gap-1.5">
              <Button v-if="row.status === 'ISSUED' && Number(row.outstanding) > 0 && can('cityledger.receive')" type="button" variant="outline" size="sm" :data-testid="`pay-${row.invoice_number}`" @click="startPay(row)">{{ t('clAccount.pay') }}</Button>
              <Button v-if="row.status === 'ISSUED' && Number(row.outstanding) > 0 && can('cityledger.credit_note')" type="button" variant="outline" size="sm" :data-testid="`credit-${row.invoice_number}`" @click="startAdjust('credit', row, null)">{{ t('clAccount.creditNoteEllipsis') }}</Button>
              <Button v-if="row.status === 'ISSUED' && Number(row.outstanding) > 0 && can('cityledger.write_off')" type="button" variant="outline" size="sm" :data-testid="`writeoff-${row.invoice_number}`" @click="startAdjust('writeoff', row, null)">{{ t('clAccount.writeOffEllipsis') }}</Button>
              <Button type="button" variant="outline" size="sm" :data-testid="`print-${row.invoice_number}`" @click="printInvoice(row)">{{ t('clAccount.print') }}</Button>
              <RouterLink v-if="row.status === 'ISSUED' && can('tax.invoice')" :to="{ path: '/tax/invoices', query: { source_type: 'CITY_LEDGER_INVOICE', id: String(row.id) } }" class="inline-flex h-8 items-center rounded-md border border-border px-3 text-sm hover:bg-accent" :data-testid="`tax-invoice-${row.invoice_number}`">{{ t('clAccount.taxInvoice') }}</RouterLink>
              <Button v-if="row.status === 'ISSUED' && can('cityledger.invoice')" type="button" variant="outline" size="sm" :data-testid="`void-${row.invoice_number}`" @click="startVoidInvoice(row)">{{ t('clAccount.void') }}</Button>
            </div>
          </template>
        </DataTable>
      </CardContent>
    </Card>

    <Card v-if="adjustments.length" class="mb-4" data-testid="adjustments">
      <CardHeader><CardTitle>{{ t('clAccount.adjustments') }}</CardTitle></CardHeader>
      <CardContent>
        <DataTable :columns="adjustmentColumns" :rows="adjustments" row-key="id" :row-test-id="(x) => `adjustment-${x.number}`" :row-class="(x) => (x.status === 'VOIDED' ? 'text-muted-foreground line-through' : undefined)" :caption="t('clAccount.adjustments')">
          <template #cell-kind="{ row }">{{ row.kind === 'CREDIT_NOTE' ? t('clAccount.creditNote') : t('clAccount.writeOff') }}</template>
          <template #cell-target="{ row }">{{ row.invoice_number ?? row.payment_number }}<small v-if="row.attached_invoice_id" class="block text-muted-foreground">{{ t('clAccount.onInvoice') }}</small></template>
          <template #cell-status="{ row }"><Badge :variant="row.status === 'POSTED' ? 'success' : 'outline'">{{ row.status === 'POSTED' ? t('clAccount.posted') : t('clAccount.voided') }}</Badge></template>
          <template #cell-actions="{ row }">
            <Button v-if="row.kind === 'CREDIT_NOTE'" type="button" variant="outline" size="sm" :data-testid="`print-${row.number}`" @click="printCreditNote(row)">{{ t('clAccount.print') }}</Button>
            <Button v-if="row.status === 'POSTED' && can(row.kind === 'WRITE_OFF' ? 'cityledger.write_off' : 'cityledger.credit_note')" type="button" variant="outline" size="sm" :data-testid="`void-${row.number}`" @click="startVoidAdjustment(row)">{{ t('clAccount.void') }}</Button>
          </template>
        </DataTable>
      </CardContent>
    </Card>

    <Card v-if="reminders.length" class="mb-4" data-testid="reminders">
      <CardHeader><CardTitle>{{ t('clReminders.title') }}</CardTitle></CardHeader>
      <CardContent>
        <DataTable :columns="reminderColumns" :rows="reminders" row-key="id" :row-test-id="(x) => `reminder-${x.number}`" :caption="t('clReminders.title')">
          <template #cell-items="{ row }">{{ row.items.map((i) => i.invoice_number).join(', ') }}</template>
          <template #cell-actions="{ row }">
            <Button type="button" variant="outline" size="sm" :data-testid="`print-${row.number}`" @click="printReminder(row)">{{ t('clReminders.print') }}</Button>
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
  <ApprovalDialog v-if="adjusting?.asking" :title="t('clAccount.approveAdjust')" :busy="busy" :error="dialogError" @approve="submitAdjust" @cancel="adjusting = null; dialogError = null" />
  <ApprovalDialog v-if="voiding?.asking" :title="t('clAccount.approveVoid')" :busy="busy" :error="dialogError" @approve="approveVoid" @cancel="voiding = null" />
</template>
