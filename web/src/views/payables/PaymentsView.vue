<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { Approval, OpenBill, Supplier, SupplierPayment } from '@/api/types'
import ApprovalDialog from '@/components/ApprovalDialog.vue'
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
const METHODS = [{ value: 'CASH', label: 'Cash' }, { value: 'BANK_TRANSFER', label: 'Bank transfer' }, { value: 'OTHER', label: 'Other' }]

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
    notice.value = `Payment ${data?.payment_number ?? ''} made.`
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
    notice.value = `${p.payment_number} voided.`
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
  <div class="page-head">
    <h1 class="page-title">Supplier payments</h1>
    <button v-if="can('payables.post') && !creating" type="button" class="btn-primary" data-testid="new-payment" @click="startNew">Pay a supplier</button>
  </div>
  <p v-if="error" class="alert" role="alert" data-testid="payment-error">
    {{ error.message }} <code>{{ error.code }}</code>
    <template v-for="(f, i) in (error.fieldErrors ?? []).slice(0, 6)" :key="i"><br /><span class="muted">{{ f.field }}: {{ f.message }}</span></template>
  </p>
  <p v-if="notice" class="notice" role="status" data-testid="notice">{{ notice }}</p>
  <p v-if="pid === null" class="muted">Select a property first.</p>
  <p v-else-if="!can('payables.view')" class="muted" data-testid="no-access">Your role at this property cannot see supplier payments: the <code>payables.view</code> permission is needed.</p>
  <template v-else>
    <form v-if="creating" class="card" novalidate data-testid="payment-form" @submit.prevent="post">
      <h2>Pay a supplier</h2>
      <div class="form-grid">
        <label class="field">
          <span>Supplier</span>
          <select v-model.number="form.supplier_id" name="supplier_id" :aria-invalid="!!fieldError('supplier_id')" @change="loadOpen">
            <option :value="0">Choose a supplier</option>
            <option v-for="s in suppliers" :key="s.id" :value="s.id">{{ s.code }} · {{ s.name }} (owed {{ s.outstanding }})</option>
          </select>
        </label>
        <label class="field">
          <span>Payment date</span>
          <input v-model="form.payment_date" name="payment_date" type="date" :aria-invalid="!!fieldError('payment_date')" />
          <small v-if="fieldError('payment_date')" class="error-text">{{ fieldError('payment_date') }}</small>
        </label>
        <label class="field">
          <span>Paid from</span>
          <select v-model="form.method" name="method">
            <option v-for="m in METHODS" :key="m.value" :value="m.value">{{ m.label }}</option>
          </select>
        </label>
        <label class="field"><span>Reference</span><input v-model="form.reference" name="reference" maxlength="100" /></label>
        <label class="field wide"><span>Remarks</span><input v-model="form.remarks" name="remarks" maxlength="500" /></label>
      </div>
      <p v-if="form.supplier_id && !openBills.length" class="muted" data-testid="nothing-owed">Nothing is owed to this supplier.</p>
      <table v-if="openBills.length" class="list" data-testid="open-bills">
        <thead><tr><th>Bill</th><th>Invoice</th><th>Due</th><th class="num">Owed</th><th class="num">Pay</th></tr></thead>
        <tbody>
          <tr v-for="(b, i) in openBills" :key="b.bill_id" :data-testid="`open-${b.bill_number}`">
            <td>{{ b.bill_number }}</td>
            <td>{{ b.supplier_invoice_number }}</td>
            <td>{{ b.due_date }}</td>
            <td class="num">{{ b.outstanding }}</td>
            <td class="num">
              <input v-model="amounts[b.bill_id]" :name="`pay_${b.bill_number}`" inputmode="decimal" :aria-invalid="!!fieldError(`allocations[${i}].amount`)" />
              <small v-if="fieldError(`allocations[${i}].amount`)" class="error-text">{{ fieldError(`allocations[${i}].amount`) }}</small>
            </td>
          </tr>
        </tbody>
        <tfoot>
          <tr>
            <td colspan="3"><button type="button" data-testid="pay-all" @click="payAll">Pay everything owed</button></td>
            <td><b>Payment</b></td>
            <td class="num" data-testid="payment-total"><b>{{ fromMilli(picked.sum) }}</b></td>
          </tr>
        </tfoot>
      </table>
      <div class="form-actions">
        <button type="button" @click="creating = false">Cancel</button>
        <button type="submit" class="btn-primary" :disabled="busy || !picked.valid || picked.count === 0 || !form.payment_date" data-testid="payment-post">Make the payment</button>
      </div>
    </form>

    <section class="card">
      <form class="filters" novalidate @submit.prevent="load">
        <label class="field">
          <span>Supplier</span>
          <select v-model.number="filter.supplier" name="supplier">
            <option :value="0">All</option>
            <option v-for="s in suppliers" :key="s.id" :value="s.id">{{ s.code }} · {{ s.name }}</option>
          </select>
        </label>
        <label class="field">
          <span>Status</span>
          <select v-model="filter.status" name="status">
            <option value="">All</option>
            <option value="POSTED">Made</option>
            <option value="VOIDED">Voided</option>
          </select>
        </label>
        <button type="submit" data-testid="apply">Apply</button>
      </form>
      <p v-if="loaded && !payments.length" class="muted" data-testid="empty">No payments match.</p>
      <table v-else class="list" data-testid="payments">
        <thead><tr><th>Date</th><th>Payment</th><th>Supplier</th><th>Method</th><th class="num">Amount</th><th>Status</th></tr></thead>
        <tbody>
          <template v-for="p in payments" :key="p.id">
            <tr class="clickable" :class="{ selected: opened?.id === p.id, voided: p.status === 'VOIDED' }" :data-testid="`payment-${p.payment_number}`" @click="open(p)">
              <td>{{ p.payment_date }}</td>
              <td>{{ p.payment_number }}</td>
              <td>{{ p.supplier_code }} · {{ p.supplier_name }}</td>
              <td>{{ METHODS.find((m) => m.value === p.payment_method)?.label }}<small v-if="p.reference_number" class="muted"> · {{ p.reference_number }}</small></td>
              <td class="num">{{ p.amount }}</td>
              <td>{{ p.status === 'VOIDED' ? 'Voided' : 'Made' }}</td>
            </tr>
            <tr v-if="opened?.id === p.id" class="detail" data-testid="payment-detail">
              <td colspan="6">
                <table class="list inner">
                  <thead><tr><th>Bill</th><th>Invoice</th><th class="num">Settled</th></tr></thead>
                  <tbody>
                    <tr v-for="a in opened.allocations" :key="a.bill_id"><td>{{ a.bill_number }}</td><td>{{ a.supplier_invoice_number }}</td><td class="num">{{ a.amount }}</td></tr>
                  </tbody>
                </table>
                <p class="muted">Journal {{ opened.journal_number }}<template v-if="opened.void_reason"> · voided: {{ opened.void_reason }}</template><template v-if="opened.remarks"> · {{ opened.remarks }}</template></p>
                <div v-if="can('payables.post') && opened.status === 'POSTED'" class="reverse">
                  <button v-if="!voiding" type="button" data-testid="void" @click="voiding = { reason: '', asking: false }">Void…</button>
                  <form v-else novalidate @submit.prevent="voiding.asking = true">
                    <label class="field"><span>Reason</span><input v-model="voiding.reason" name="reason" maxlength="500" /></label>
                    <button type="button" @click="voiding = null">Cancel</button>
                    <button type="submit" class="btn-primary" :disabled="!voiding.reason.trim()" data-testid="void-ask">Void with approval</button>
                  </form>
                </div>
              </td>
            </tr>
          </template>
        </tbody>
      </table>
    </section>
  </template>
  <ApprovalDialog v-if="voiding?.asking" title="Approve void" :busy="busy" :error="dialogError" @approve="voidPayment" @cancel="voiding = null; dialogError = null" />
</template>

<style scoped>
.wide {
  grid-column: 1 / -1;
}
.num {
  text-align: right;
  white-space: nowrap;
}
.clickable {
  cursor: pointer;
}
.selected td {
  background: var(--accent-soft);
}
.voided td {
  color: var(--muted, #6b7280);
  text-decoration: line-through;
}
.reverse {
  margin-top: 8px;
}
</style>
