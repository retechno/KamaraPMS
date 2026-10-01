<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { Approval, CityLedgerAccount, CityLedgerAging, CityLedgerReceipt, CityLedgerStatement } from '@/api/types'
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
const error = ref<ApiError | null>(null)
const dialogError = ref<ApiError | null>(null)
const notice = ref('')
const busy = ref(false)
const period = reactive({ from: '', to: '' })
const receipt = reactive({ amount: '', method: 'BANK_TRANSFER' as CityLedgerReceipt['payment_method'], reference: '', remarks: '' })
const METHODS: CityLedgerReceipt['payment_method'][] = ['BANK_TRANSFER', 'CASH', 'CARD', 'OTHER']
const voiding = ref<{ receipt: CityLedgerReceipt; reason: string; asking: boolean } | null>(null)
// One key per attempt: kept while a request may have been lost, renewed once the server has answered.
let receiptKey = newIdempotencyKey()

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
    const [a, g, s, r] = await Promise.all([
      api.GET('/api/v1/properties/{propertyId}/city-ledger/accounts/{id}', { params: base() }),
      api.GET('/api/v1/properties/{propertyId}/city-ledger/accounts/{id}/aging', { params: base() }),
      api.GET('/api/v1/properties/{propertyId}/city-ledger/accounts/{id}/statement', { params: { ...base(), query } }),
      api.GET('/api/v1/properties/{propertyId}/city-ledger/accounts/{id}/receipts', { params: base() }),
    ])
    account.value = a.data ?? null
    aging.value = g.data ?? null
    statement.value = s.data ?? null
    receipts.value = r.data?.data ?? []
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
  voiding.value = { receipt: r, reason: '', asking: false }
  dialogError.value = null
  error.value = null
}

async function approveVoid(approval: Approval): Promise<void> {
  const v = voiding.value
  if (!v || pid.value === null) return
  busy.value = true
  dialogError.value = null
  try {
    await api.POST('/api/v1/properties/{propertyId}/city-ledger/receipts/{id}/void', {
      params: { path: { propertyId: pid.value, id: v.receipt.id } },
      body: { reason: v.reason, approval },
    })
    voiding.value = null
    notice.value = 'Receipt voided.'
    await load()
  } catch (e) {
    dialogError.value = e instanceof ApiError ? e : null
  } finally {
    busy.value = false
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
    <h2>Void receipt {{ voiding.receipt.receipt_number }}</h2>
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
.total td {
  font-weight: 600;
}
</style>
