<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { Approval, GlAccount, Journal } from '@/api/types'
import ApprovalDialog from '@/components/ApprovalDialog.vue'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import { newIdempotencyKey } from '@/utils/reservations'
import { listAccounts } from './accountApi'
import { fromMilli, totals } from './accountMeta'

const auth = useAuthStore()
const property = usePropertyStore()

const journals = ref<Journal[]>([])
const accounts = ref<GlAccount[]>([])
const opened = ref<Journal | null>(null)
const loaded = ref(false)
const error = ref<ApiError | null>(null)
const dialogError = ref<ApiError | null>(null)
const notice = ref('')
const busy = ref(false)
const filter = reactive({ from: '', to: '', type: '', q: '' })
const creating = ref(false)
const form = reactive({ date: '', description: '', reference: '', lines: [] as { account_id: number; debit: string; credit: string; description: string }[] })
const reversing = ref<{ reason: string; asking: boolean } | null>(null)
let postKey = newIdempotencyKey()

const pid = computed(() => property.currentId)
const can = (p: string) => auth.can(p, pid.value)
const fieldError = (field: string) => error.value?.fieldMessage(field)
const postable = computed(() => accounts.value.filter((a) => a.is_postable && a.is_active))
const sums = computed(() => totals(form.lines))
const balanced = computed(() => sums.value.valid && sums.value.debit === sums.value.credit && sums.value.debit > 0n)
/** The ledger of an account for the month of the journal, so the line can be seen among its neighbours. */
function ledgerLink(accountId: number, date: string): { path: string; query: Record<string, string> } {
  const [y, m] = date.split('-').map(Number) as [number, number]
  const last = new Date(Date.UTC(y, m, 0)).getUTCDate()
  const mm = String(m).padStart(2, '0')
  return { path: '/accounting/ledger', query: { account: String(accountId), from: `${y}-${mm}-01`, to: `${y}-${mm}-${String(last).padStart(2, '0')}` } }
}
const TYPE_LABEL: Record<string, string> = { DAY_CLOSE: 'Day close', MANUAL: 'Manual', REVERSAL: 'Reversal', CLOSING: 'Year-end closing', PAYABLES: 'Payables' }

async function load(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null || !can('accounting.view')) return
  try {
    const { data } = await api.GET('/api/v1/properties/{propertyId}/accounting/journals', {
      params: {
        path: { propertyId },
        query: { from: filter.from || undefined, to: filter.to || undefined, type: (filter.type || undefined) as 'MANUAL' | undefined, q: filter.q.trim() || undefined },
      },
    })
    journals.value = data?.data ?? []
    if (!accounts.value.length) accounts.value = await listAccounts(propertyId, { active: true })
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    loaded.value = true
  }
}

async function open(j: Journal): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null) return
  if (opened.value?.id === j.id) {
    opened.value = null
    return
  }
  try {
    const { data } = await api.GET('/api/v1/properties/{propertyId}/accounting/journals/{id}', { params: { path: { propertyId, id: j.id } } })
    opened.value = data ?? null
    reversing.value = null
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  }
}

function startNew(): void {
  Object.assign(form, { date: '', description: '', reference: '' })
  form.lines = [{ account_id: 0, debit: '', credit: '', description: '' }, { account_id: 0, debit: '', credit: '', description: '' }]
  error.value = null
  postKey = newIdempotencyKey()
  creating.value = true
}

async function post(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null) return
  busy.value = true
  error.value = null
  notice.value = ''
  try {
    const { data } = await api.POST('/api/v1/properties/{propertyId}/accounting/journals', {
      params: { path: { propertyId }, header: { 'Idempotency-Key': postKey } },
      body: {
        journal_date: form.date, description: form.description, reference: form.reference || undefined,
        lines: form.lines.map((l) => ({ account_id: l.account_id, debit: l.debit.trim() || undefined, credit: l.credit.trim() || undefined, description: l.description || undefined })),
      },
    })
    postKey = newIdempotencyKey()
    creating.value = false
    notice.value = `Journal ${data?.journal_number ?? ''} posted.`
    await load()
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    busy.value = false
  }
}

async function reverse(approval: Approval): Promise<void> {
  const propertyId = pid.value
  const j = opened.value
  if (propertyId === null || j === null || reversing.value === null) return
  busy.value = true
  dialogError.value = null
  try {
    const { data } = await api.POST('/api/v1/properties/{propertyId}/accounting/journals/{id}/reverse', {
      params: { path: { propertyId, id: j.id } }, body: { reason: reversing.value.reason.trim(), approval },
    })
    notice.value = `${j.journal_number} reversed by ${data?.journal_number ?? ''}.`
    reversing.value = null
    opened.value = null
    await load()
  } catch (e) {
    dialogError.value = e instanceof ApiError ? e : null
  } finally {
    busy.value = false
  }
}

async function postPending(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null) return
  busy.value = true
  error.value = null
  try {
    const { data } = await api.POST('/api/v1/properties/{propertyId}/accounting/journals/post-pending', { params: { path: { propertyId } } })
    notice.value = data?.posted ? `${data.posted} business day(s) journaled.` : 'No business day is waiting for its journal.'
    await load()
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    busy.value = false
  }
}

watch(() => pid.value, () => {
  journals.value = []
  accounts.value = []
  opened.value = null
  loaded.value = false
  void load()
}, { immediate: true })
</script>

<template>
  <div class="page-head">
    <h1 class="page-title">Journals</h1>
    <div class="head-actions">
      <button v-if="can('accounting.close')" type="button" :disabled="busy" data-testid="post-pending" @click="postPending">Journal missing days</button>
      <button v-if="can('accounting.post') && !creating" type="button" class="btn-primary" data-testid="new-journal" @click="startNew">New journal</button>
    </div>
  </div>

  <p v-if="error" class="alert" role="alert" data-testid="journal-error">
    {{ error.message }} <code>{{ error.code }}</code>
    <template v-if="error.fieldErrors?.length">
      <br />
      <span v-for="(f, i) in error.fieldErrors.slice(0, 8)" :key="i" class="muted">{{ f.field }}: {{ f.message }}<br /></span>
    </template>
  </p>
  <p v-if="notice" class="notice" role="status" data-testid="notice">{{ notice }}</p>
  <p v-if="pid === null" class="muted">Select a property first.</p>
  <p v-else-if="!can('accounting.view')" class="muted" data-testid="no-access">Your role at this property cannot see journals: the <code>accounting.view</code> permission is needed.</p>

  <template v-else>
    <form v-if="creating" class="card" novalidate data-testid="journal-form" @submit.prevent="post">
      <h2>New manual journal</h2>
      <div class="form-grid">
        <label class="field">
          <span>Date</span>
          <input v-model="form.date" name="date" type="date" :aria-invalid="!!fieldError('journal_date')" />
          <small v-if="fieldError('journal_date')" class="error-text">{{ fieldError('journal_date') }}</small>
        </label>
        <label class="field">
          <span>Reference</span>
          <input v-model="form.reference" name="reference" maxlength="100" />
        </label>
        <label class="field wide">
          <span>Description</span>
          <input v-model="form.description" name="description" maxlength="300" :aria-invalid="!!fieldError('description')" />
          <small v-if="fieldError('description')" class="error-text">{{ fieldError('description') }}</small>
        </label>
      </div>
      <table class="list lines">
        <thead><tr><th>Account</th><th class="num">Debit</th><th class="num">Credit</th><th>Note</th><th /></tr></thead>
        <tbody>
          <tr v-for="(l, i) in form.lines" :key="i" :data-testid="`line-${i}`">
            <td>
              <select v-model.number="l.account_id" :name="`account_${i}`" :aria-invalid="!!fieldError(`lines[${i}].account_id`)">
                <option :value="0">Choose an account</option>
                <option v-for="a in postable" :key="a.id" :value="a.id">{{ a.code }} · {{ a.name }}</option>
              </select>
              <small v-if="fieldError(`lines[${i}].account_id`)" class="error-text">{{ fieldError(`lines[${i}].account_id`) }}</small>
            </td>
            <td class="num"><input v-model="l.debit" :name="`debit_${i}`" inputmode="decimal" :disabled="l.credit.trim() !== ''" /></td>
            <td class="num"><input v-model="l.credit" :name="`credit_${i}`" inputmode="decimal" :disabled="l.debit.trim() !== ''" /></td>
            <td><input v-model="l.description" :name="`note_${i}`" maxlength="300" /></td>
            <td><button v-if="form.lines.length > 2" type="button" :data-testid="`remove-line-${i}`" @click="form.lines.splice(i, 1)">Remove</button></td>
          </tr>
        </tbody>
        <tfoot>
          <tr data-testid="line-totals">
            <td><button type="button" data-testid="add-line" @click="form.lines.push({ account_id: 0, debit: '', credit: '', description: '' })">Add a line</button></td>
            <td class="num">{{ fromMilli(sums.debit) }}</td>
            <td class="num">{{ fromMilli(sums.credit) }}</td>
            <td colspan="2">
              <span v-if="!sums.valid" class="error-text">An amount is not a number.</span>
              <span v-else-if="sums.debit !== sums.credit" class="error-text" data-testid="difference">Out of balance by {{ fromMilli(sums.debit > sums.credit ? sums.debit - sums.credit : sums.credit - sums.debit) }}</span>
              <span v-else class="muted">Balanced</span>
            </td>
          </tr>
        </tfoot>
      </table>
      <small v-if="fieldError('lines')" class="error-text">{{ fieldError('lines') }}</small>
      <div class="form-actions">
        <button type="button" @click="creating = false">Cancel</button>
        <button type="submit" class="btn-primary" :disabled="busy || !balanced || !form.date || !form.description.trim() || form.lines.some((l) => !l.account_id)" data-testid="journal-post">Post</button>
      </div>
    </form>

    <section class="card">
      <form class="filters" novalidate @submit.prevent="load">
        <label class="field"><span>From</span><input v-model="filter.from" name="from" type="date" /></label>
        <label class="field"><span>To</span><input v-model="filter.to" name="to" type="date" /></label>
        <label class="field">
          <span>Type</span>
          <select v-model="filter.type" name="type">
            <option value="">All</option>
            <option value="DAY_CLOSE">Day close</option>
            <option value="MANUAL">Manual</option>
            <option value="REVERSAL">Reversal</option>
            <option value="CLOSING">Year-end closing</option>
            <option value="PAYABLES">Payables</option>
          </select>
        </label>
        <label class="field"><span>Search</span><input v-model="filter.q" name="q" type="search" placeholder="Number, text or reference" /></label>
        <button type="submit" data-testid="apply">Apply</button>
      </form>
      <p v-if="loaded && !journals.length" class="muted" data-testid="empty">No journals match.</p>
      <table v-else class="list" data-testid="journals">
        <thead><tr><th>Date</th><th>Number</th><th>Type</th><th>Description</th><th class="num">Total</th><th /></tr></thead>
        <tbody>
          <template v-for="j in journals" :key="j.id">
            <tr class="clickable" :class="{ selected: opened?.id === j.id }" :data-testid="`journal-${j.journal_number}`" @click="open(j)">
              <td>{{ j.journal_date }}</td>
              <td>{{ j.journal_number }}</td>
              <td>{{ TYPE_LABEL[j.journal_type] }}</td>
              <td>{{ j.description }}<small v-if="j.reference" class="muted"> · {{ j.reference }}</small></td>
              <td class="num">{{ j.total }}</td>
              <td><small v-if="j.reversed_by_number" class="muted">Reversed by {{ j.reversed_by_number }}</small></td>
            </tr>
            <tr v-if="opened?.id === j.id" class="detail" data-testid="journal-detail">
              <td colspan="6">
                <table class="list inner">
                  <thead><tr><th>#</th><th>Account</th><th>Detail</th><th class="num">Debit</th><th class="num">Credit</th></tr></thead>
                  <tbody>
                    <tr v-for="l in opened.lines" :key="l.line_no">
                      <td>{{ l.line_no }}</td>
                      <td><RouterLink :to="ledgerLink(l.account_id, j.journal_date)" :data-testid="`ledger-link-${l.line_no}`">{{ l.account_code }} · {{ l.account_name }}</RouterLink></td>
                      <td><small class="muted">{{ l.description }}</small></td>
                      <td class="num">{{ Number(l.debit) ? l.debit : '' }}</td>
                      <td class="num">{{ Number(l.credit) ? l.credit : '' }}</td>
                    </tr>
                  </tbody>
                </table>
                <p v-if="opened.reason" class="muted">Reason: {{ opened.reason }}</p>
                <div v-if="can('accounting.post') && opened.journal_type === 'MANUAL' && !opened.reversed_by_journal_id" class="reverse">
                  <button v-if="!reversing" type="button" data-testid="reverse" @click="reversing = { reason: '', asking: false }">Reverse…</button>
                  <form v-else novalidate @submit.prevent="reversing.asking = true">
                    <label class="field">
                      <span>Reason</span>
                      <input v-model="reversing.reason" name="reason" maxlength="500" />
                    </label>
                    <button type="button" @click="reversing = null">Cancel</button>
                    <button type="submit" class="btn-primary" :disabled="!reversing.reason.trim()" data-testid="reverse-ask">Reverse with approval</button>
                  </form>
                </div>
              </td>
            </tr>
          </template>
        </tbody>
      </table>
    </section>
  </template>
  <ApprovalDialog v-if="reversing?.asking" title="Approve reversal" :busy="busy" :error="dialogError" @approve="reverse" @cancel="reversing = null; dialogError = null" />
</template>

<style scoped>
.head-actions {
  display: flex;
  gap: 8px;
}
.wide {
  grid-column: 1 / -1;
}
.num {
  text-align: right;
}
.clickable {
  cursor: pointer;
}
.selected td {
  background: var(--accent-soft);
}
.lines input,
.lines select {
  width: 100%;
}
.reverse {
  margin-top: 8px;
}
</style>
