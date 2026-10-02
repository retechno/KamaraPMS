<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { Approval, GlAccount, TaxFilingPeriod, TaxFilingProfile, TaxFilingReturn, TaxFilingWorksheet } from '@/api/types'
import ApprovalDialog from '@/components/ApprovalDialog.vue'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import { documentPath, openPdf } from '@/utils/documents'
import { newIdempotencyKey } from '@/utils/reservations'
import { listAccounts } from '@/views/accounting/accountApi'
import { money } from '@/views/accounting/reportApi'

const auth = useAuthStore()
const property = usePropertyStore()
const route = useRoute()

const profiles = ref<TaxFilingProfile[]>([])
const accounts = ref<GlAccount[]>([])
const periods = ref<TaxFilingPeriod[]>([])
const worksheet = ref<TaxFilingWorksheet | null>(null)
const filed = ref<TaxFilingReturn | null>(null)
const taxId = ref(Number(route.query.tax) || 0)
const openMonth = ref('')
const loaded = ref(false)
const error = ref<ApiError | null>(null)
const dialogError = ref<ApiError | null>(null)
const notice = ref('')
const busy = ref(false)
const fileForm = reactive({ filed_on: '', reference: '', notes: '' })
const payForm = reactive({ open: false, payment_date: '', amount: '', penalty: '', penalty_account_id: 0, method: 'BANK_TRANSFER', reference: '', remarks: '' })
const voiding = ref<{ kind: 'return' | 'payment'; id: number; reason: string; asking: boolean } | null>(null)
let fileKey = newIdempotencyKey()
let payKey = newIdempotencyKey()

const pid = computed(() => property.currentId)
const can = (p: string) => auth.can(p, pid.value)
const fieldError = (field: string) => error.value?.fieldMessage(field)
const expenses = computed(() => accounts.value.filter((a) => a.is_postable && a.is_active && a.account_type === 'EXPENSE'))
const STATUS: Record<string, string> = { OPEN: 'Not over', READY: 'Ready to file', FILED: 'Filed' }
const PAY: Record<string, string> = { UNPAID: 'Unpaid', PARTIAL: 'Part paid', PAID: 'Paid', VOIDED: 'Voided' }
const base = () => ({ path: { propertyId: pid.value as number } })
const monthLabel = (start: string): string => new Date(`${start}T00:00:00Z`).toLocaleDateString('en-GB', { month: 'long', year: 'numeric', timeZone: 'UTC' })

async function load(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null || !can('tax.view')) return
  try {
    if (!profiles.value.length) {
      const { data } = await api.GET('/api/v1/properties/{propertyId}/tax/profiles', { params: base() })
      profiles.value = (data?.data ?? []).filter((p) => p.is_active)
      if (!taxId.value && profiles.value.length === 1) taxId.value = profiles.value[0]?.tax_id ?? 0
    }
    if (taxId.value) {
      const { data } = await api.GET('/api/v1/properties/{propertyId}/tax/periods', { params: { ...base(), query: { tax_id: taxId.value } } })
      periods.value = data?.data ?? []
    }
    if (can('accounting.view') && !accounts.value.length) accounts.value = await listAccounts(propertyId, { active: true })
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    loaded.value = true
  }
}

async function open(per: TaxFilingPeriod): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null) return
  if (openMonth.value === per.period_start) {
    openMonth.value = ''
    worksheet.value = null
    filed.value = null
    return
  }
  try {
    const { data } = await api.GET('/api/v1/properties/{propertyId}/tax/worksheet', { params: { ...base(), query: { tax_id: taxId.value, period: per.period_start } } })
    worksheet.value = data ?? null
    openMonth.value = per.period_start
    fileForm.filed_on = ''
    fileForm.reference = ''
    fileForm.notes = ''
    payForm.open = false
    voiding.value = null
    fileKey = newIdempotencyKey()
    filed.value = null
    if (data?.return) {
      const res = await api.GET('/api/v1/properties/{propertyId}/tax/returns/{id}', { params: { path: { propertyId, id: data.return.id } } })
      filed.value = res.data ?? null
    }
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  }
}

async function reload(): Promise<void> {
  const month = openMonth.value
  openMonth.value = ''
  await load()
  const per = periods.value.find((p) => p.period_start === month)
  if (per) await open(per)
}

async function run<T>(fn: () => Promise<T>, done: string): Promise<boolean> {
  busy.value = true
  error.value = null
  notice.value = ''
  try {
    await fn()
    notice.value = done
    await reload()
    return true
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
    return false
  } finally {
    busy.value = false
  }
}

async function file(): Promise<void> {
  const ok = await run(
    () => api.POST('/api/v1/properties/{propertyId}/tax/returns', {
      params: { ...base(), header: { 'Idempotency-Key': fileKey } },
      body: { tax_id: taxId.value, period_start: openMonth.value, filed_on: fileForm.filed_on || null, filing_reference: fileForm.reference || undefined, notes: fileForm.notes || undefined },
    }),
    'The return is filed.',
  )
  if (ok) fileKey = newIdempotencyKey()
}

function startPay(): void {
  Object.assign(payForm, { open: true, payment_date: '', amount: filed.value?.outstanding ?? '', penalty: '', penalty_account_id: 0, method: 'BANK_TRANSFER', reference: '', remarks: '' })
  payKey = newIdempotencyKey()
  error.value = null
}

async function pay(): Promise<void> {
  const r = filed.value
  if (!r) return
  const ok = await run(
    () => api.POST('/api/v1/properties/{propertyId}/tax/returns/{id}/payments', {
      params: { path: { ...base().path, id: r.id }, header: { 'Idempotency-Key': payKey } },
      body: {
        payment_date: payForm.payment_date, amount: payForm.amount.trim(), penalty: payForm.penalty.trim() || undefined, penalty_account_id: payForm.penalty_account_id || undefined,
        payment_method: payForm.method as 'CASH', reference_number: payForm.reference || undefined, remarks: payForm.remarks || undefined,
      },
    }),
    'The payment is recorded.',
  )
  if (ok) payForm.open = false
}

async function confirmVoid(approval: Approval): Promise<void> {
  const v = voiding.value
  if (!v) return
  busy.value = true
  dialogError.value = null
  try {
    const body = { reason: v.reason.trim(), approval }
    if (v.kind === 'return') await api.POST('/api/v1/properties/{propertyId}/tax/returns/{id}/void', { params: { path: { ...base().path, id: v.id } }, body })
    else await api.POST('/api/v1/properties/{propertyId}/tax/payments/{id}/void', { params: { path: { ...base().path, id: v.id } }, body })
    notice.value = v.kind === 'return' ? 'The return is voided.' : 'The payment is voided.'
    voiding.value = null
    await reload()
  } catch (e) {
    dialogError.value = e instanceof ApiError ? e : null
  } finally {
    busy.value = false
  }
}

async function showPdf(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null || !openMonth.value) return
  try {
    await openPdf(filed.value ? documentPath.taxReturn(propertyId, filed.value.id) : documentPath.taxWorksheet(propertyId, taxId.value, openMonth.value))
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  }
}

watch([() => pid.value, taxId], () => {
  periods.value = []
  worksheet.value = null
  filed.value = null
  openMonth.value = ''
  loaded.value = false
  void load()
}, { immediate: true })
</script>

<template>
  <div class="page-head">
    <h1 class="page-title">Tax returns</h1>
  </div>
  <p v-if="error" class="alert" role="alert" data-testid="tax-error">
    {{ error.message }} <code>{{ error.code }}</code>
    <template v-for="(f, i) in (error.fieldErrors ?? []).slice(0, 4)" :key="i"><br /><span class="muted">{{ f.field }}: {{ f.message }}</span></template>
  </p>
  <p v-if="notice" class="notice" role="status" data-testid="notice">{{ notice }}</p>
  <p v-if="pid === null" class="muted">Select a property first.</p>
  <p v-else-if="!can('tax.view')" class="muted" data-testid="no-access">Your role at this property cannot see tax returns: the <code>tax.view</code> permission is needed.</p>
  <template v-else>
    <form class="filters card" novalidate @submit.prevent>
      <label class="field">
        <span>Tax</span>
        <select v-model.number="taxId" name="tax">
          <option :value="0">Choose a tax</option>
          <option v-for="p in profiles" :key="p.tax_id" :value="p.tax_id">{{ p.tax_code }} · {{ p.tax_name }}</option>
        </select>
      </label>
    </form>
    <p v-if="loaded && !profiles.length" class="muted" data-testid="no-profiles">No tax is set up for filing yet: set one up under Tax → Filing profiles.</p>

    <section v-if="taxId" class="card">
      <p v-if="loaded && !periods.length" class="muted" data-testid="empty">The books have no month yet.</p>
      <table v-else class="list" data-testid="periods">
        <thead><tr><th>Month</th><th>Due</th><th class="num">Tax</th><th>Status</th><th class="num">Paid</th><th class="num">Owed</th></tr></thead>
        <tbody>
          <template v-for="per in periods" :key="per.period_start">
            <tr class="clickable" :class="{ selected: openMonth === per.period_start, late: per.overdue }" :data-testid="`period-${per.period_start}`" @click="open(per)">
              <td>{{ monthLabel(per.period_start) }}</td>
              <td>{{ per.due_date }}<small v-if="per.overdue" class="error-text"> · overdue</small></td>
              <td class="num">{{ per.tax_amount }}</td>
              <td>{{ STATUS[per.status] }}</td>
              <td class="num">{{ per.status === 'FILED' ? per.paid : '' }}</td>
              <td class="num">{{ per.status === 'FILED' ? per.outstanding : '' }}</td>
            </tr>
            <tr v-if="openMonth === per.period_start && worksheet" class="detail" data-testid="worksheet">
              <td colspan="6">
                <table class="list inner" data-testid="lines">
                  <thead><tr><th>Charge</th><th class="num">Rate</th><th class="num">Items</th><th class="num">Taxable base</th><th class="num">Tax</th></tr></thead>
                  <tbody>
                    <tr v-for="l in worksheet.lines" :key="`${l.charge_code}-${l.rate}`">
                      <td>{{ l.charge_code }}<small v-if="l.charge_name" class="muted"> · {{ l.charge_name }}</small></td>
                      <td class="num">{{ Number(l.rate) }}%</td><td class="num">{{ l.items }}</td><td class="num">{{ l.base_amount }}</td><td class="num">{{ l.tax_amount }}</td>
                    </tr>
                    <tr v-if="!worksheet.lines.length"><td colspan="5" class="muted">Nothing was collected in the month.</td></tr>
                  </tbody>
                  <tfoot><tr data-testid="totals"><th colspan="3">Total</th><th class="num">{{ worksheet.base_amount }}</th><th class="num">{{ worksheet.tax_amount }}</th></tr></tfoot>
                </table>
                <p class="muted" data-testid="books-check">
                  The books credited {{ worksheet.gl_collected }} for this tax in the month<template v-if="Number(worksheet.difference) !== 0"> · <b class="error-text">difference {{ money(worksheet.difference) }}</b></template><template v-else> · they agree</template>
                  · {{ worksheet.posted_days }} of {{ worksheet.days }} business days closed with their journal.
                </p>
                <button type="button" data-testid="pdf" @click="showPdf">PDF</button>

                <template v-if="filed">
                  <h3 data-testid="filed-header">
                    {{ filed.return_number }} · filed {{ filed.filed_on }}<small v-if="filed.filing_reference" class="muted"> · {{ filed.filing_reference }}</small> ·
                    {{ PAY[filed.payment_status] }} · owed {{ filed.outstanding }}
                  </h3>
                  <table v-if="filed.payments?.length" class="list inner" data-testid="payments">
                    <thead><tr><th>Date</th><th>Payment</th><th>Reference</th><th class="num">Amount</th><th class="num">Penalty</th><th /></tr></thead>
                    <tbody>
                      <tr v-for="p in filed.payments" :key="p.id" :class="{ voided: p.status === 'VOIDED' }" :data-testid="`payment-${p.payment_number}`">
                        <td>{{ p.payment_date }}</td><td>{{ p.payment_number }}</td><td>{{ p.reference_number ?? '' }}</td>
                        <td class="num">{{ p.amount }}</td><td class="num">{{ money(p.penalty) }}</td>
                        <td><button v-if="can('tax.file') && p.status === 'POSTED'" type="button" :data-testid="`void-payment-${p.payment_number}`" @click="voiding = { kind: 'payment', id: p.id, reason: '', asking: false }">Void…</button></td>
                      </tr>
                    </tbody>
                  </table>
                  <div v-if="can('tax.file') && filed.status === 'FILED'" class="actions">
                    <button v-if="!payForm.open && Number(filed.outstanding) > 0" type="button" class="btn-primary" data-testid="pay-open" @click="startPay">Record a payment</button>
                    <button v-if="!Number(filed.paid)" type="button" data-testid="void-return" @click="voiding = { kind: 'return', id: filed.id, reason: '', asking: false }">Void the return…</button>
                  </div>
                  <form v-if="payForm.open" class="card" novalidate data-testid="pay-form" @submit.prevent="pay">
                    <div class="form-grid">
                      <label class="field">
                        <span>Payment date</span>
                        <input v-model="payForm.payment_date" name="payment_date" type="date" :aria-invalid="!!fieldError('payment_date')" />
                        <small v-if="fieldError('payment_date')" class="error-text">{{ fieldError('payment_date') }}</small>
                      </label>
                      <label class="field">
                        <span>Amount</span>
                        <input v-model="payForm.amount" name="amount" inputmode="decimal" :aria-invalid="!!fieldError('amount')" />
                        <small v-if="fieldError('amount')" class="error-text">{{ fieldError('amount') }}</small>
                      </label>
                      <label class="field">
                        <span>Paid from</span>
                        <select v-model="payForm.method" name="method">
                          <option value="BANK_TRANSFER">Bank transfer</option>
                          <option value="CASH">Cash</option>
                          <option value="OTHER">Other</option>
                        </select>
                      </label>
                      <label class="field"><span>Billing code / receipt</span><input v-model="payForm.reference" name="reference" maxlength="100" /></label>
                      <label class="field"><span>Penalty</span><input v-model="payForm.penalty" name="penalty" inputmode="decimal" :aria-invalid="!!fieldError('penalty')" /></label>
                      <label v-if="payForm.penalty.trim() && Number(payForm.penalty) > 0" class="field">
                        <span>Penalty account</span>
                        <select v-model.number="payForm.penalty_account_id" name="penalty_account" :aria-invalid="!!fieldError('penalty_account_id')">
                          <option :value="0">Choose an account</option>
                          <option v-for="a in expenses" :key="a.id" :value="a.id">{{ a.code }} · {{ a.name }}</option>
                        </select>
                        <small v-if="fieldError('penalty_account_id')" class="error-text">{{ fieldError('penalty_account_id') }}</small>
                      </label>
                    </div>
                    <div class="form-actions">
                      <button type="button" @click="payForm.open = false">Cancel</button>
                      <button type="submit" class="btn-primary" :disabled="busy || !payForm.payment_date || !payForm.amount.trim()" data-testid="pay-post">Record the payment</button>
                    </div>
                  </form>
                </template>

                <template v-else>
                  <ul v-if="worksheet.blockers.length" class="blockers" data-testid="blockers"><li v-for="b in worksheet.blockers" :key="b">{{ b }}</li></ul>
                  <form v-if="can('tax.file') && worksheet.ready" class="card" novalidate data-testid="file-form" @submit.prevent="file">
                    <h3>File the return of {{ monthLabel(worksheet.period_start) }}</h3>
                    <p class="muted">The worksheet above is frozen as the return; due {{ worksheet.due_date }} at {{ worksheet.profile.authority }}.</p>
                    <div class="form-grid">
                      <label class="field"><span>Filed on</span><input v-model="fileForm.filed_on" name="filed_on" type="date" :aria-invalid="!!fieldError('filed_on')" /><small class="hint">Empty: the current business date.</small></label>
                      <label class="field"><span>Filing reference</span><input v-model="fileForm.reference" name="reference" maxlength="100" /></label>
                      <label class="field wide"><span>Notes</span><input v-model="fileForm.notes" name="notes" maxlength="500" /></label>
                    </div>
                    <div class="form-actions"><button type="submit" class="btn-primary" :disabled="busy" data-testid="file-post">File the return</button></div>
                  </form>
                </template>
              </td>
            </tr>
          </template>
        </tbody>
      </table>
    </section>
  </template>
  <div v-if="voiding && !voiding.asking" class="card" data-testid="void-form">
    <h3>Void the {{ voiding.kind }}</h3>
    <label class="field"><span>Reason</span><input v-model="voiding.reason" name="reason" maxlength="500" /></label>
    <div class="form-actions">
      <button type="button" @click="voiding = null">Cancel</button>
      <button type="button" class="btn-primary" :disabled="!voiding.reason.trim()" data-testid="void-ask" @click="voiding.asking = true">Continue</button>
    </div>
  </div>
  <ApprovalDialog v-if="voiding?.asking" title="Approve void" :busy="busy" :error="dialogError" @approve="confirmVoid" @cancel="voiding = null; dialogError = null" />
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
.late td:first-child {
  color: var(--danger, #b91c1c);
}
.voided td {
  color: var(--muted, #6b7280);
  text-decoration: line-through;
}
.blockers {
  color: var(--danger, #b91c1c);
  margin: 4px 0 8px;
}
.actions {
  display: flex;
  gap: 8px;
  margin: 8px 0;
}
</style>
