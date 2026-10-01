<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { Approval, CityLedgerAccount, CityLedgerAging, CityLedgerCandidate, CityLedgerInvoice, CityLedgerReceipt, CityLedgerStatement } from '@/api/types'
import ApprovalDialog from '@/components/ApprovalDialog.vue'
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
    notice.value = 'Receipt recorded.'
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
  voiding.value = { kind: 'receipt', id: r.id, label: `receipt ${r.receipt_number}`, reason: '', asking: false }
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
    notice.value = v.kind === 'receipt' ? 'Receipt voided.' : 'Invoice voided.'
    await load()
  } catch (e) {
    dialogError.value = e instanceof ApiError ? e : null
  } finally {
    busy.value = false
  }
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
    notice.value = `Invoice ${data?.invoice_number ?? ''} issued.`
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
    notice.value = `Payment recorded for invoice ${v.invoice.invoice_number}.`
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
  voiding.value = { kind: 'invoice', id: inv.id, label: `invoice ${inv.invoice_number}`, reason: '', asking: false }
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
  <div class="page-head">
    <h1 class="page-title">{{ account ? `${account.code} · ${account.name}` : 'Company account' }}</h1>
    <RouterLink to="/city-ledger">City ledger</RouterLink>
  </div>

  <p v-if="error" class="alert" role="alert" data-testid="form-error">
    {{ error.message }} <code>{{ error.code }}</code>
    <span v-if="error.code === 'RECEIPT_EXCEEDS_BALANCE'"> A receipt cannot be more than the company owes.</span>
  </p>
  <p v-if="notice" class="notice" role="status" data-testid="notice">{{ notice }}</p>
  <p v-if="pid === null" class="muted">Select a property first.</p>
  <p v-else-if="!can('cityledger.read')" class="muted" data-testid="no-access">Your role at this property cannot see the city ledger.</p>

  <template v-else-if="account">
    <section class="card" data-testid="account-summary">
      <dl class="facts">
        <div><dt>Balance owed</dt><dd><b data-testid="balance">{{ account.balance }}</b></dd></div>
        <div><dt>Transferred</dt><dd>{{ account.transferred }}</dd></div>
        <div><dt>Received</dt><dd>{{ account.received }}</dd></div>
        <div><dt>Credit limit</dt><dd>{{ account.credit_limit ?? 'No limit' }}</dd></div>
        <div><dt>Available</dt><dd>{{ account.available ?? '-' }}</dd></div>
        <div><dt>Terms</dt><dd>{{ account.payment_terms_days }} days</dd></div>
      </dl>
      <table v-if="aging" class="list" data-testid="aging">
        <caption>Aging as of {{ aging.as_of }} (days since the folio was transferred)</caption>
        <thead><tr><th v-for="b in aging.buckets" :key="b.label" class="num">{{ b.label }}</th></tr></thead>
        <tbody><tr><td v-for="b in aging.buckets" :key="b.label" class="num">{{ b.amount }}</td></tr></tbody>
      </table>
    </section>

    <form v-if="can('cityledger.receive')" class="card" novalidate data-testid="receipt-form" @submit.prevent="receive">
      <h2>Record a payment from the company</h2>
      <div class="form-grid">
        <label class="field">
          <span>Amount</span>
          <input v-model="receipt.amount" name="amount" inputmode="decimal" :aria-invalid="!!fieldError('amount')" />
          <small v-if="fieldError('amount')" class="error-text">{{ fieldError('amount') }}</small>
        </label>
        <label class="field">
          <span>Method</span>
          <select v-model="receipt.method" name="payment_method">
            <option v-for="m in METHODS" :key="m" :value="m">{{ m }}</option>
          </select>
        </label>
        <label class="field">
          <span>Reference</span>
          <input v-model="receipt.reference" name="reference" />
        </label>
        <label class="field">
          <span>Remarks</span>
          <input v-model="receipt.remarks" name="remarks" />
        </label>
      </div>
      <div class="form-actions"><button type="submit" class="btn-primary" :disabled="busy">Record receipt</button></div>
    </form>

    <section v-if="can('cityledger.invoice') && (invoiceable.length || waiting.length)" class="card" data-testid="invoice-builder">
      <h2>New invoice</h2>
      <p class="muted">Pick the transfers to bill together. Only guests who have checked out can be invoiced.</p>
      <table class="list">
        <thead>
          <tr>
            <th><input type="checkbox" aria-label="Select all checked-out transfers" data-testid="pick-all" :checked="invoiceable.length > 0 && picked.length === invoiceable.length" :disabled="!invoiceable.length" @change="toggleAll(($event.target as HTMLInputElement).checked)" /></th>
            <th>Check-out</th><th>Guest</th><th>Room</th><th>Folio</th><th>Reference</th><th class="num">Amount</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="c in candidates" :key="c.payment_id" :class="{ waiting: !c.invoiceable }" :data-testid="`candidate-${c.payment_number}`">
            <td><input v-model="picked" type="checkbox" :value="c.payment_id" :disabled="!c.invoiceable" :aria-label="`Invoice ${c.payment_number}`" /></td>
            <td>{{ c.checked_out_at ? c.checked_out_at.slice(0, 10) : '' }}<small v-if="!c.invoiceable" class="muted">in house</small></td>
            <td>{{ c.guest_name }}</td>
            <td>{{ c.room_numbers }}</td>
            <td>{{ c.folio_number }}</td>
            <td>{{ c.reference_number }}</td>
            <td class="num">{{ c.amount }}</td>
          </tr>
        </tbody>
      </table>
      <form class="filters" novalidate @submit.prevent="createInvoice">
        <label class="field">
          <span>Note on the invoice</span>
          <input v-model="invoiceNotes" name="invoice_notes" maxlength="500" />
        </label>
        <button type="submit" class="btn-primary" :disabled="busy || !picked.length" data-testid="create-invoice">
          Create invoice ({{ picked.length }} selected, {{ pickedTotal }})
        </button>
      </form>
    </section>

    <form v-if="paying" class="card" novalidate data-testid="pay-form" @submit.prevent="payInvoice">
      <h2>Payment for invoice {{ paying.invoice.invoice_number }}</h2>
      <p class="muted">Outstanding {{ paying.invoice.outstanding }}. A smaller amount pays the invoice in part.</p>
      <div class="form-grid">
        <label class="field">
          <span>Amount</span>
          <input v-model="paying.amount" name="pay_amount" inputmode="decimal" :aria-invalid="!!fieldError('amount') || !!fieldError('allocations')" />
          <small v-if="fieldError('amount')" class="error-text">{{ fieldError('amount') }}</small>
          <small v-if="fieldError('allocations')" class="error-text">{{ fieldError('allocations') }}</small>
        </label>
        <label class="field">
          <span>Method</span>
          <select v-model="paying.method" name="pay_method">
            <option v-for="m in METHODS" :key="m" :value="m">{{ m }}</option>
          </select>
        </label>
        <label class="field">
          <span>Reference</span>
          <input v-model="paying.reference" name="pay_reference" />
        </label>
      </div>
      <div class="form-actions">
        <button type="button" @click="paying = null">Cancel</button>
        <button type="submit" class="btn-primary" :disabled="busy || !paying.amount" data-testid="pay-submit">Record payment</button>
      </div>
    </form>

    <section class="card" data-testid="invoices">
      <h2>Invoices</h2>
      <p v-if="!invoices.length" class="muted" data-testid="no-invoices">No invoice has been issued to this company.</p>
      <table v-else class="list">
        <thead><tr><th>Number</th><th>Date</th><th>Due</th><th class="num">Total</th><th class="num">Paid</th><th class="num">Outstanding</th><th>Status</th><th /></tr></thead>
        <tbody>
          <tr v-for="i in invoices" :key="i.id" :class="{ voided: i.status === 'VOIDED' }" :data-testid="`invoice-${i.invoice_number}`">
            <td>{{ i.invoice_number }}</td>
            <td>{{ i.invoice_date }}</td>
            <td>{{ i.due_date }}</td>
            <td class="num">{{ i.total }}</td>
            <td class="num">{{ i.paid }}</td>
            <td class="num">{{ i.outstanding }}</td>
            <td>{{ { UNPAID: 'Unpaid', PARTIAL: 'Part paid', PAID: 'Paid', VOID: 'Voided' }[i.payment_status] }}</td>
            <td class="row-actions">
              <button v-if="i.status === 'ISSUED' && Number(i.outstanding) > 0 && can('cityledger.receive')" type="button" :data-testid="`pay-${i.invoice_number}`" @click="startPay(i)">Pay</button>
              <button type="button" :data-testid="`print-${i.invoice_number}`" @click="printInvoice(i)">Print</button>
              <button v-if="i.status === 'ISSUED' && can('cityledger.invoice')" type="button" :data-testid="`void-${i.invoice_number}`" @click="startVoidInvoice(i)">Void</button>
            </td>
          </tr>
        </tbody>
      </table>
    </section>

    <section class="card" data-testid="statement">
      <h2>Statement</h2>
      <form class="filters" novalidate @submit.prevent="load()">
        <label class="field">
          <span>From</span>
          <input v-model="period.from" name="from" type="date" :aria-invalid="!!fieldError('from')" />
        </label>
        <label class="field">
          <span>To</span>
          <input v-model="period.to" name="to" type="date" :aria-invalid="!!fieldError('to')" />
          <small v-if="fieldError('to')" class="error-text">{{ fieldError('to') }}</small>
        </label>
        <button type="submit">Show</button>
        <button type="button" data-testid="print-statement" @click="printStatement">Print statement</button>
      </form>
      <table v-if="statement" class="list">
        <thead>
          <tr><th>Date</th><th>Number</th><th>Description</th><th>Reference</th><th class="num">Debit</th><th class="num">Credit</th><th class="num">Balance</th><th /></tr>
        </thead>
        <tbody>
          <tr><td colspan="6">Opening balance</td><td class="num">{{ statement.opening_balance }}</td><td /></tr>
          <tr v-for="l in statement.lines" :key="`${l.kind}-${l.number}`" :class="{ voided: l.status === 'VOIDED' }" :data-testid="`line-${l.number}`">
            <td>{{ l.date }}</td>
            <td>{{ l.number }}</td>
            <td>{{ l.description }}<template v-if="l.guest_name"> · {{ l.guest_name }}</template><small v-if="l.status === 'VOIDED'"> (voided)</small></td>
            <td>{{ l.reference }}</td>
            <td class="num">{{ l.debit === '0' ? '' : l.debit }}</td>
            <td class="num">{{ l.credit === '0' ? '' : l.credit }}</td>
            <td class="num">{{ l.balance }}</td>
            <td><button v-if="l.kind === 'RECEIPT' && voidable(l.number)" type="button" :data-testid="`void-${l.number}`" @click="startVoid(l.number)">Void</button></td>
          </tr>
          <tr class="total"><td colspan="4">Total</td><td class="num">{{ statement.total_debit }}</td><td class="num">{{ statement.total_credit }}</td><td class="num"><b>{{ statement.closing_balance }}</b></td><td /></tr>
        </tbody>
      </table>
    </section>
  </template>

  <section v-if="voiding && !voiding.asking" class="card" data-testid="void-form">
    <h2>Void {{ voiding.label }}</h2>
    <label class="field">
      <span>Reason</span>
      <input v-model="voiding.reason" name="void_reason" />
    </label>
    <div class="form-actions">
      <button type="button" @click="voiding = null">Cancel</button>
      <button type="button" class="btn-primary" :disabled="!voiding.reason.trim()" data-testid="void-continue" @click="voiding.asking = true">Continue</button>
    </div>
  </section>
  <ApprovalDialog v-if="voiding?.asking" title="Approve void" :busy="busy" :error="dialogError" @approve="approveVoid" @cancel="voiding = null" />
</template>

<style scoped>
.voided td {
  color: var(--muted, #6b7280);
  text-decoration: line-through;
}
.waiting td {
  color: var(--muted, #6b7280);
}
.total td {
  font-weight: 600;
}
</style>
