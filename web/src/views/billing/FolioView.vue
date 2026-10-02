<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { api } from '@/api/client'
import { fetchAll } from '@/api/paging'
import { ApiError } from '@/api/problem'
import type { Approval, ChargeCode, Company, Folio, FolioItem, PaymentMethod } from '@/api/types'
import ApprovalDialog from '@/components/ApprovalDialog.vue'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import { documentPath, openPdf } from '@/utils/documents'
import { newIdempotencyKey } from '@/utils/reservations'

const props = defineProps<{ id: string }>()
const auth = useAuthStore()
const property = usePropertyStore()

const folio = ref<Folio | null>(null)
const codes = ref<ChargeCode[]>([])
const companies = ref<Company[]>([])
const companiesDenied = ref(false)
const error = ref<ApiError | null>(null)
const dialogError = ref<ApiError | null>(null)
const notice = ref('')
const busy = ref(false)
const open = ref<number | null>(null) // item whose components are shown

const METHODS: PaymentMethod[] = ['CASH', 'CARD', 'BANK_TRANSFER', 'OTHER']
const charge = reactive({ codeId: 0, quantity: '1', unitPrice: '', description: '' })
const payment = reactive({ amount: '', method: 'CASH' as PaymentMethod, reference: '' })
const adjust = reactive({ codeId: 0, amount: '', reason: '' })
const transfer = reactive({ companyId: 0, amount: '', reference: '' })
// A correction waiting for its reason and its approval.
type Pending =
  | { kind: 'adjust' }
  | { kind: 'reverse'; item: FolioItem }
  | { kind: 'void'; item: FolioItem }
  | { kind: 'refund'; item: FolioItem }
const pending = ref<Pending | null>(null)
const correction = reactive({ reason: '', amount: '', method: '', reference: '' })
const approving = ref(false)
// One key per attempt; it is kept while a request may have been lost and renewed once the server has answered.
const keys: Record<string, string> = {}
function keyFor(name: string): string {
  return (keys[name] ??= newIdempotencyKey())
}

const pid = computed(() => property.currentId)
const can = (p: string) => auth.can(p, pid.value)
const businessDate = computed(() => property.clock?.business_date ?? '')
const isOpen = computed(() => folio.value?.status === 'OPEN')
const chargeCodes = computed(() => codes.value.filter((c) => c.is_active && c.charge_type !== 'ROOM'))
const adjustCodes = computed(() => codes.value.filter((c) => c.is_active))
const fieldError = (field: string) => error.value?.fieldMessage(field)

const base = () => ({ path: { propertyId: pid.value as number, id: Number(props.id) } })

async function load(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null || !can('folio.read')) return
  error.value = null
  try {
    const { data } = await api.GET('/api/v1/properties/{propertyId}/folios/{id}', { params: base() })
    folio.value = data ?? null
    codes.value = await fetchAll((cursor) => api.GET('/api/v1/properties/{propertyId}/charge-codes', { params: { path: { propertyId }, query: { limit: 200, cursor } } }))
    charge.codeId ||= chargeCodes.value[0]?.id ?? 0
    adjust.codeId ||= adjustCodes.value[0]?.id ?? 0
    await loadCompanies(propertyId)
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  }
}

// The companies a folio can be transferred to. Picking one needs reservation.read or cityledger.read; a role that
// has only cityledger.transfer is told so instead of seeing an empty list.
async function loadCompanies(propertyId: number): Promise<void> {
  if (!can('cityledger.transfer') || folio.value?.status !== 'OPEN') return
  try {
    const all = await fetchAll((cursor) => api.GET('/api/v1/properties/{propertyId}/companies', { params: { path: { propertyId }, query: { limit: 200, cursor, active: true } } }))
    companies.value = all
    companiesDenied.value = false
    transfer.companyId ||= all[0]?.id ?? 0
  } catch (e) {
    companies.value = []
    companiesDenied.value = e instanceof ApiError && e.status === 403
  }
}

async function run(name: string, action: () => Promise<unknown>): Promise<boolean> {
  busy.value = true
  error.value = null
  notice.value = ''
  try {
    await action()
    delete keys[name]
    await load()
    return true
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
    if (e instanceof ApiError) delete keys[name]
    return false
  } finally {
    busy.value = false
  }
}

const postCharge = () => run('charge', async () => {
  await api.POST('/api/v1/properties/{propertyId}/folios/{id}/charges', {
    params: { ...base(), header: { 'Idempotency-Key': keyFor('charge') } },
    body: { charge_code_id: charge.codeId, quantity: charge.quantity, unit_price: charge.unitPrice || undefined, description: charge.description || undefined },
  })
  charge.unitPrice = ''
  charge.description = ''
  notice.value = 'Charge posted.'
})

const postPayment = () => run('payment', async () => {
  await api.POST('/api/v1/properties/{propertyId}/folios/{id}/payments', {
    params: { ...base(), header: { 'Idempotency-Key': keyFor('payment') } },
    body: { amount: payment.amount, payment_method: payment.method, reference_number: payment.reference || undefined },
  })
  payment.amount = ''
  payment.reference = ''
  notice.value = 'Payment posted.'
})

const postTransfer = () => run('transfer', async () => {
  await api.POST('/api/v1/properties/{propertyId}/folios/{id}/city-ledger-transfers', {
    params: { ...base(), header: { 'Idempotency-Key': keyFor('transfer') } },
    body: { company_id: transfer.companyId, amount: transfer.amount, reference_number: transfer.reference || undefined },
  })
  transfer.amount = ''
  transfer.reference = ''
  notice.value = 'Transferred to the company\'s city ledger account.'
})

const closeFolio = () => run('close', async () => {
  await api.POST('/api/v1/properties/{propertyId}/folios/{id}/close', { params: base(), body: { version: folio.value?.version ?? 0 } })
  notice.value = 'Folio closed.'
})

function startAdjust(): void {
  correction.reason = adjust.reason
  pending.value = { kind: 'adjust' }
  dialogError.value = null
  approving.value = true
}

function startCorrection(kind: 'reverse' | 'void' | 'refund', item: FolioItem): void {
  pending.value = { kind, item }
  correction.reason = ''
  correction.amount = kind === 'refund' ? item.credit : ''
  correction.method = ''
  correction.reference = ''
  dialogError.value = null
  approving.value = false
  error.value = null
}

function cancelCorrection(): void {
  pending.value = null
  approving.value = false
}

async function approve(approval: Approval): Promise<void> {
  const p = pending.value
  if (!p) return
  busy.value = true
  dialogError.value = null
  try {
    const propertyId = pid.value as number
    if (p.kind === 'adjust') {
      await api.POST('/api/v1/properties/{propertyId}/folios/{id}/adjustments', {
        params: { ...base(), header: { 'Idempotency-Key': keyFor('adjust') } },
        body: { charge_code_id: adjust.codeId, amount: adjust.amount, reason: adjust.reason, approval },
      })
      adjust.amount = ''
      adjust.reason = ''
      notice.value = 'Adjustment posted.'
    } else if (p.kind === 'reverse') {
      await api.POST('/api/v1/properties/{propertyId}/folio-items/{id}/reverse', { params: { path: { propertyId, id: p.item.id } }, body: { reason: correction.reason, approval } })
      notice.value = 'Item reversed.'
    } else if (p.kind === 'void') {
      await api.POST('/api/v1/properties/{propertyId}/payments/{id}/void', { params: { path: { propertyId, id: p.item.payment_id as number } }, body: { reason: correction.reason, approval } })
      notice.value = 'Payment voided.'
    } else {
      await api.POST('/api/v1/properties/{propertyId}/payments/{id}/refunds', {
        params: { path: { propertyId, id: p.item.payment_id as number }, header: { 'Idempotency-Key': keyFor(`refund-${p.item.id}`) } },
        body: {
          amount: correction.amount,
          reason: correction.reason,
          approval,
          ...(correction.method ? { payment_method: correction.method as PaymentMethod } : {}),
          ...(correction.reference.trim() ? { reference_number: correction.reference.trim() } : {}),
        },
      })
      delete keys[`refund-${p.item.id}`]
      notice.value = 'Refund posted.'
    }
    delete keys.adjust
    pending.value = null
    approving.value = false
    await load()
  } catch (e) {
    // The dialog stays open with the reason, so a mistyped password can be retried.
    dialogError.value = e instanceof ApiError ? e : null
    if (e instanceof ApiError) delete keys.adjust
  } finally {
    busy.value = false
  }
}

const reversible = (i: FolioItem) => isOpen.value && can('folio.reverse') && ['CHARGE', 'ADJUSTMENT'].includes(i.transaction_type) && i.business_date === businessDate.value && !i.reversed_by_item_id
const voidable = (i: FolioItem) => isOpen.value && can('payment.void') && i.transaction_type === 'PAYMENT' && i.business_date === businessDate.value && !i.reversed_by_item_id
// A transfer to a company is settled by the company's receipt, never refunded (its description names the method).
const isTransfer = (i: FolioItem) => i.description.includes('(CITY_LEDGER)')
const refundable = (i: FolioItem) => isOpen.value && can('payment.refund') && i.transaction_type === 'PAYMENT' && !i.reversed_by_item_id && !isTransfer(i)
const dialogTitle = computed(() => {
  switch (pending.value?.kind) {
    case 'adjust': return 'Approve adjustment'
    case 'reverse': return 'Approve reversal'
    case 'void': return 'Approve void'
    case 'refund': return 'Approve refund'
    default: return ''
  }
})

const canPrint = computed(() => can('reservation.read')) // the document names the reservation and the guest

async function print(path: string): Promise<void> {
  error.value = null
  try {
    await openPdf(path)
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  }
}

watch(() => [pid.value, props.id], () => void load(), { immediate: true })
</script>

<template>
  <div class="page-head">
    <h1 class="page-title">Folio <span v-if="folio">{{ folio.folio_number }}</span></h1>
    <div class="links">
      <RouterLink v-if="folio" :to="`/reservations/${folio.reservation_id}`">Reservation</RouterLink>
      <RouterLink to="/folios">Folios</RouterLink>
    </div>
  </div>

  <p v-if="error" class="alert" role="alert" data-testid="form-error">{{ error.message }} <code>{{ error.code }}</code></p>
  <p v-if="notice" class="alert warning" role="status" data-testid="notice">{{ notice }}</p>
  <p v-if="pid === null" class="muted">Select a property first.</p>
  <p v-else-if="!can('folio.read')" class="muted" data-testid="no-access">Your role at this property does not allow viewing folios.</p>

  <template v-else-if="folio">
    <section class="card summary" data-testid="summary">
      <span class="badge" :class="folio.status.toLowerCase()" data-testid="folio-status">{{ folio.status }}</span>
      <span>Debit <strong data-testid="debit">{{ folio.totals.debit }}</strong></span>
      <span>Credit <strong data-testid="credit">{{ folio.totals.credit }}</strong></span>
      <span>Balance <strong data-testid="balance" class="balance">{{ folio.balance }}</strong></span>
      <button v-if="isOpen && can('folio.post_charge') && !folio.stay_id" type="button" :disabled="busy" data-testid="close" @click="closeFolio">Close folio</button>
      <button v-if="canPrint && pid !== null" type="button" data-testid="print-invoice" @click="print(documentPath.invoice(pid, folio.id))">{{ isOpen ? 'Print bill' : 'Print invoice' }}</button>
    </section>

    <section class="card">
      <table class="list" data-testid="items">
        <thead><tr><th>Date</th><th>Description</th><th>Code</th><th class="num">Debit</th><th class="num">Credit</th><th /></tr></thead>
        <tbody>
          <template v-for="i in folio.items" :key="i.id">
            <tr :data-testid="`item-${i.id}`" :class="{ struck: i.reversed_by_item_id }">
              <td>{{ i.business_date }}</td>
              <td>
                <button type="button" class="link" :data-testid="`toggle-${i.id}`" :aria-expanded="open === i.id" @click="open = open === i.id ? null : i.id">{{ i.description }}</button>
                <small class="muted"> {{ i.transaction_type }}</small>
              </td>
              <td>{{ i.charge_code }}</td>
              <td class="num">{{ i.debit }}</td>
              <td class="num">{{ i.credit }}</td>
              <td class="row-actions">
                <button v-if="reversible(i)" type="button" :data-testid="`reverse-${i.id}`" @click="startCorrection('reverse', i)">Reverse</button>
                <button v-if="voidable(i)" type="button" :data-testid="`void-${i.id}`" @click="startCorrection('void', i)">Void</button>
                <button v-if="refundable(i)" type="button" :data-testid="`refund-${i.id}`" @click="startCorrection('refund', i)">Refund</button>
                <button v-if="i.payment_id && canPrint && pid !== null" type="button" :data-testid="`receipt-${i.id}`" @click="print(documentPath.receipt(pid, i.payment_id))">Receipt</button>
              </td>
            </tr>
            <tr v-if="open === i.id" class="detail" :data-testid="`detail-${i.id}`">
              <td colspan="6">
                <span class="muted">{{ i.quantity }} x {{ i.unit_price }} ({{ i.price_mode.toLowerCase() }}) · net {{ i.net_amount }}<template v-if="i.reason"> · {{ i.reason }}</template></span>
                <ul v-if="i.components.length" class="comps">
                  <li v-for="c in i.components" :key="`${c.component_type}-${c.sequence}`">{{ c.name }} {{ c.rate }}% on {{ c.base_amount }} = {{ c.amount }}</li>
                </ul>
              </td>
            </tr>
          </template>
        </tbody>
      </table>
      <p v-if="!folio.items.length" class="muted" data-testid="empty">Nothing is posted to this folio yet.</p>
    </section>

    <form v-if="pending && pending.kind !== 'adjust' && !approving" class="card" novalidate data-testid="correction-form" @submit.prevent="approving = true">
      <h2>{{ pending.kind === 'reverse' ? 'Reverse' : pending.kind === 'void' ? 'Void' : 'Refund' }}: {{ pending.item.description }}</h2>
      <div class="form-grid">
        <label v-if="pending.kind === 'refund'" class="field">
          <span>Amount</span>
          <input v-model="correction.amount" name="refund_amount" inputmode="decimal" />
        </label>
        <label v-if="pending.kind === 'refund'" class="field">
          <span>Refund by</span>
          <select v-model="correction.method" name="refund_method" data-testid="refund-method">
            <option value="">Same method as the payment</option>
            <option v-for="m in METHODS" :key="m" :value="m">{{ m }}</option>
          </select>
          <small class="muted">The money can leave by another method than it came in, e.g. cash for a bank transfer. The day close books it to the account of the method chosen.</small>
        </label>
        <label v-if="pending.kind === 'refund'" class="field">
          <span>Reference (optional)</span>
          <input v-model="correction.reference" name="refund_reference" maxlength="100" />
        </label>
        <label class="field">
          <span>Reason</span>
          <input v-model="correction.reason" name="reason" maxlength="500" />
        </label>
      </div>
      <div class="form-actions">
        <button type="button" @click="cancelCorrection">Cancel</button>
        <button type="submit" class="btn-primary" :disabled="!correction.reason.trim()">Continue to approval</button>
      </div>
    </form>

    <ApprovalDialog v-if="approving && pending" :title="dialogTitle" :busy="busy" :error="dialogError" @approve="approve" @cancel="cancelCorrection" />

    <template v-if="isOpen">
      <form v-if="can('folio.post_charge')" class="card" novalidate data-testid="charge-form" @submit.prevent="postCharge">
        <h2>Post a charge</h2>
        <div class="form-grid">
          <label class="field">
            <span>Charge code</span>
            <select v-model.number="charge.codeId" name="charge_code">
              <option v-for="c in chargeCodes" :key="c.id" :value="c.id">{{ c.code }} · {{ c.name }}</option>
            </select>
            <small v-if="fieldError('charge_code_id')" class="error-text">{{ fieldError('charge_code_id') }}</small>
          </label>
          <label class="field">
            <span>Quantity</span>
            <input v-model="charge.quantity" name="quantity" inputmode="decimal" :aria-invalid="!!fieldError('quantity')" />
            <small v-if="fieldError('quantity')" class="error-text">{{ fieldError('quantity') }}</small>
          </label>
          <label class="field">
            <span>Unit price (default when empty)</span>
            <input v-model="charge.unitPrice" name="unit_price" inputmode="decimal" :aria-invalid="!!fieldError('unit_price')" />
            <small v-if="fieldError('unit_price')" class="error-text">{{ fieldError('unit_price') }}</small>
          </label>
          <label class="field">
            <span>Description</span>
            <input v-model="charge.description" name="description" />
          </label>
        </div>
        <div class="form-actions"><button type="submit" class="btn-primary" :disabled="busy">Post charge</button></div>
      </form>

      <form v-if="can('payment.post')" class="card" novalidate data-testid="payment-form" @submit.prevent="postPayment">
        <h2>Take a payment</h2>
        <div class="form-grid">
          <label class="field">
            <span>Amount</span>
            <input v-model="payment.amount" name="amount" inputmode="decimal" :aria-invalid="!!fieldError('amount')" />
            <small v-if="fieldError('amount')" class="error-text">{{ fieldError('amount') }}</small>
          </label>
          <label class="field">
            <span>Method</span>
            <select v-model="payment.method" name="payment_method">
              <option v-for="m in METHODS" :key="m" :value="m">{{ m }}</option>
            </select>
          </label>
          <label class="field">
            <span>Reference</span>
            <input v-model="payment.reference" name="reference" />
          </label>
        </div>
        <div class="form-actions"><button type="submit" class="btn-primary" :disabled="busy">Post payment</button></div>
      </form>

      <form v-if="can('cityledger.transfer') && isOpen" class="card" novalidate data-testid="transfer-form" @submit.prevent="postTransfer">
        <h2>Transfer to a company (city ledger)</h2>
        <p v-if="companiesDenied" class="muted" data-testid="transfer-denied">Choosing a company needs the <code>cityledger.read</code> or <code>reservation.read</code> permission too.</p>
        <p v-else-if="!companies.length" class="muted" data-testid="transfer-none">There is no active company: add one under Setup → Companies.</p>
        <div v-else class="form-grid">
          <label class="field">
            <span>Company</span>
            <select v-model.number="transfer.companyId" name="transfer_company">
              <option v-for="c in companies" :key="c.id" :value="c.id">{{ c.code }} · {{ c.name }}</option>
            </select>
            <small v-if="fieldError('company_id')" class="error-text">{{ fieldError('company_id') }}</small>
          </label>
          <label class="field">
            <span>Amount (folio balance {{ folio?.balance }})</span>
            <input v-model="transfer.amount" name="transfer_amount" inputmode="decimal" :aria-invalid="!!fieldError('amount')" />
            <small v-if="fieldError('amount')" class="error-text">{{ fieldError('amount') }}</small>
          </label>
          <label class="field">
            <span>Reference (PO, voucher)</span>
            <input v-model="transfer.reference" name="transfer_reference" />
          </label>
        </div>
        <div v-if="companies.length" class="form-actions"><button type="submit" class="btn-primary" :disabled="busy">Transfer</button></div>
      </form>

      <form v-if="can('folio.adjust')" class="card" novalidate data-testid="adjust-form" @submit.prevent="startAdjust">
        <h2>Adjustment (needs approval)</h2>
        <div class="form-grid">
          <label class="field">
            <span>Charge code</span>
            <select v-model.number="adjust.codeId" name="adjust_code">
              <option v-for="c in adjustCodes" :key="c.id" :value="c.id">{{ c.code }} · {{ c.name }}</option>
            </select>
          </label>
          <label class="field">
            <span>Amount (negative to credit)</span>
            <input v-model="adjust.amount" name="adjust_amount" inputmode="decimal" :aria-invalid="!!fieldError('amount')" />
            <small v-if="fieldError('amount')" class="error-text">{{ fieldError('amount') }}</small>
          </label>
          <label class="field">
            <span>Reason</span>
            <input v-model="adjust.reason" name="adjust_reason" maxlength="500" />
          </label>
        </div>
        <div class="form-actions"><button type="submit" :disabled="busy || !adjust.amount || !adjust.reason.trim()">Continue to approval</button></div>
      </form>
    </template>
  </template>
</template>

<style scoped>
.links {
  display: flex;
  gap: 16px;
}
.summary {
  display: flex;
  flex-wrap: wrap;
  gap: 24px;
  align-items: center;
}
.balance {
  font-size: 18px;
}
.list {
  width: 100%;
  border-collapse: collapse;
  font-size: 14px;
}
.list th,
.list td {
  text-align: left;
  padding: 6px 8px;
  border-bottom: 1px solid var(--border);
}
.num {
  text-align: right !important;
  font-variant-numeric: tabular-nums;
}
.struck td {
  color: var(--text-muted);
  text-decoration: line-through;
}
.struck .row-actions {
  text-decoration: none;
}
.row-actions {
  white-space: nowrap;
  display: flex;
  gap: 6px;
}
.detail td {
  background: var(--accent-soft);
}
.comps {
  margin: 4px 0 0;
  padding-left: 18px;
}
.link {
  border: 0;
  background: none;
  padding: 0;
  color: var(--accent);
  text-decoration: underline;
  text-align: left;
}
.badge {
  border-radius: 999px;
  padding: 2px 10px;
  font-size: 12px;
  background: var(--accent-soft);
}
.badge.closed {
  background: #eef0f3;
}
</style>
