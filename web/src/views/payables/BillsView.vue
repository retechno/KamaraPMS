<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { Approval, Bill, GlAccount, Supplier } from '@/api/types'
import ApprovalDialog from '@/components/ApprovalDialog.vue'
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
const STATUS_LABEL: Record<string, string> = { UNPAID: 'Unpaid', PARTIAL: 'Part paid', PAID: 'Paid', VOIDED: 'Voided' }

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
    notice.value = `Bill ${data?.bill_number ?? ''} entered.`
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
    notice.value = `${b.bill_number} voided.`
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
  <div class="page-head">
    <h1 class="page-title">Supplier bills</h1>
    <button v-if="can('payables.post') && !creating" type="button" class="btn-primary" data-testid="new-bill" @click="startNew">Enter a bill</button>
  </div>
  <p v-if="error" class="alert" role="alert" data-testid="bill-error">
    {{ error.message }} <code>{{ error.code }}</code>
    <template v-for="(f, i) in (error.fieldErrors ?? []).slice(0, 6)" :key="i"><br /><span class="muted">{{ f.field }}: {{ f.message }}</span></template>
  </p>
  <p v-if="notice" class="notice" role="status" data-testid="notice">{{ notice }}</p>
  <p v-if="pid === null" class="muted">Select a property first.</p>
  <p v-else-if="!can('payables.view')" class="muted" data-testid="no-access">Your role at this property cannot see supplier bills: the <code>payables.view</code> permission is needed.</p>
  <template v-else>
    <form v-if="creating" class="card" novalidate data-testid="bill-form" @submit.prevent="post">
      <h2>Enter a supplier bill</h2>
      <div class="form-grid">
        <label class="field">
          <span>Supplier</span>
          <select v-model.number="form.supplier_id" name="supplier_id" :aria-invalid="!!fieldError('supplier_id')" @change="applyDefaultAccount">
            <option :value="0">Choose a supplier</option>
            <option v-for="s in suppliers.filter((x) => x.is_active)" :key="s.id" :value="s.id">{{ s.code }} · {{ s.name }}</option>
          </select>
          <small v-if="fieldError('supplier_id')" class="error-text">{{ fieldError('supplier_id') }}</small>
        </label>
        <label class="field">
          <span>Supplier's invoice number</span>
          <input v-model="form.invoice" name="invoice" maxlength="60" :aria-invalid="!!fieldError('supplier_invoice_number')" />
          <small v-if="fieldError('supplier_invoice_number')" class="error-text">{{ fieldError('supplier_invoice_number') }}</small>
        </label>
        <label class="field">
          <span>Bill date</span>
          <input v-model="form.bill_date" name="bill_date" type="date" :aria-invalid="!!fieldError('bill_date')" />
          <small v-if="fieldError('bill_date')" class="error-text">{{ fieldError('bill_date') }}</small>
        </label>
        <label class="field">
          <span>Due date</span>
          <input v-model="form.due_date" name="due_date" type="date" :aria-invalid="!!fieldError('due_date')" />
          <small class="hint">Empty: the bill date plus the supplier's payment terms.</small>
          <small v-if="fieldError('due_date')" class="error-text">{{ fieldError('due_date') }}</small>
        </label>
        <label class="field wide"><span>Description</span><input v-model="form.description" name="description" maxlength="300" /></label>
      </div>
      <table class="list lines">
        <thead><tr><th>Charged to</th><th>Detail</th><th class="num">Amount</th><th /></tr></thead>
        <tbody>
          <tr v-for="(l, i) in form.lines" :key="i" :data-testid="`line-${i}`">
            <td>
              <select v-model.number="l.account_id" :name="`account_${i}`" :aria-invalid="!!fieldError(`lines[${i}].account_id`)">
                <option :value="0">Choose an account</option>
                <option v-for="a in chargeable" :key="a.id" :value="a.id">{{ a.code }} · {{ a.name }}</option>
              </select>
              <small v-if="fieldError(`lines[${i}].account_id`)" class="error-text">{{ fieldError(`lines[${i}].account_id`) }}</small>
            </td>
            <td><input v-model="l.description" :name="`detail_${i}`" maxlength="300" /></td>
            <td class="num">
              <input v-model="l.amount" :name="`amount_${i}`" inputmode="decimal" :aria-invalid="!!fieldError(`lines[${i}].amount`)" />
              <small v-if="fieldError(`lines[${i}].amount`)" class="error-text">{{ fieldError(`lines[${i}].amount`) }}</small>
            </td>
            <td><button v-if="form.lines.length > 1" type="button" :data-testid="`remove-line-${i}`" @click="form.lines.splice(i, 1)">Remove</button></td>
          </tr>
        </tbody>
        <tfoot>
          <tr>
            <td><button type="button" data-testid="add-line" @click="form.lines.push({ account_id: 0, description: '', amount: '' })">Add a line</button> <small class="hint">A tax on the bill goes on a line of its own, charged to the tax account.</small></td>
            <td><b>Total</b></td>
            <td class="num" data-testid="bill-total"><b>{{ fromMilli(total.sum) }}</b></td>
            <td />
          </tr>
        </tfoot>
      </table>
      <small v-if="fieldError('lines')" class="error-text">{{ fieldError('lines') }}</small>
      <div class="form-actions">
        <button type="button" @click="creating = false">Cancel</button>
        <button type="submit" class="btn-primary" :disabled="busy || !total.valid || total.sum <= 0n || !form.supplier_id || !form.invoice.trim() || !form.bill_date || form.lines.some((l) => !l.account_id)" data-testid="bill-post">Enter the bill</button>
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
            <option value="POSTED">Entered</option>
            <option value="VOIDED">Voided</option>
          </select>
        </label>
        <label class="field"><span>From</span><input v-model="filter.from" name="from" type="date" /></label>
        <label class="field"><span>To</span><input v-model="filter.to" name="to" type="date" /></label>
        <label class="field"><span>Search</span><input v-model="filter.q" name="q" type="search" placeholder="Number, invoice or supplier" /></label>
        <label class="check"><input v-model="filter.open_only" name="open_only" type="checkbox" /><span>Only what is still owed</span></label>
        <button type="submit" data-testid="apply">Apply</button>
      </form>
      <p v-if="loaded && !bills.length" class="muted" data-testid="empty">No bills match.</p>
      <table v-else class="list" data-testid="bills">
        <thead><tr><th>Date</th><th>Bill</th><th>Supplier</th><th>Invoice</th><th>Due</th><th class="num">Total</th><th class="num">Owed</th><th>Status</th></tr></thead>
        <tbody>
          <template v-for="b in bills" :key="b.id">
            <tr class="clickable" :class="{ selected: opened?.id === b.id, voided: b.status === 'VOIDED' }" :data-testid="`bill-${b.bill_number}`" @click="open(b)">
              <td>{{ b.bill_date }}</td>
              <td>{{ b.bill_number }}</td>
              <td>{{ b.supplier_code }} · {{ b.supplier_name }}</td>
              <td>{{ b.supplier_invoice_number }}</td>
              <td>{{ b.due_date }}</td>
              <td class="num">{{ b.total }}</td>
              <td class="num">{{ b.outstanding }}</td>
              <td>{{ STATUS_LABEL[b.payment_status] }}</td>
            </tr>
            <tr v-if="opened?.id === b.id" class="detail" data-testid="bill-detail">
              <td colspan="8">
                <p v-if="opened.description" class="muted">{{ opened.description }}</p>
                <table class="list inner">
                  <thead><tr><th>#</th><th>Account</th><th>Detail</th><th class="num">Amount</th></tr></thead>
                  <tbody>
                    <tr v-for="l in opened.lines" :key="l.line_no">
                      <td>{{ l.line_no }}</td>
                      <td>{{ l.account_code }} · {{ l.account_name }}</td>
                      <td><small class="muted">{{ l.description }}</small></td>
                      <td class="num">{{ l.amount }}</td>
                    </tr>
                  </tbody>
                </table>
                <p class="muted">Journal {{ opened.journal_number }} · paid {{ opened.paid }}<template v-if="opened.void_reason"> · voided: {{ opened.void_reason }}</template></p>
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
  <ApprovalDialog v-if="voiding?.asking" title="Approve void" :busy="busy" :error="dialogError" @approve="voidBill" @cancel="voiding = null; dialogError = null" />
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
.lines input,
.lines select {
  width: 100%;
}
.reverse {
  margin-top: 8px;
}
</style>
