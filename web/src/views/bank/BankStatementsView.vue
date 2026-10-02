<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { RouterLink, useRoute } from 'vue-router'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { BankAccount, BankStatement } from '@/api/types'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'

const auth = useAuthStore()
const property = usePropertyStore()
const route = useRoute()

const banks = ref<BankAccount[]>([])
const statements = ref<BankStatement[]>([])
const loaded = ref(false)
const error = ref<ApiError | null>(null)
const notice = ref('')
const busy = ref(false)
const bank = ref(Number(route.query.bank) || 0)
const importing = ref(false)
const form = reactive({ period_from: '', period_to: '', opening_balance: '', closing_balance: '', note: '', csv: '' })

const pid = computed(() => property.currentId)
const can = (p: string) => auth.can(p, pid.value)
const fieldError = (field: string) => error.value?.fieldMessage(field)
const rowErrors = computed(() => (error.value?.fieldErrors ?? []).filter((f) => f.field.startsWith('rows[')))

async function load(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null || !can('bank.view')) return
  try {
    if (!banks.value.length) {
      const res = await api.GET('/api/v1/properties/{propertyId}/bank/accounts', { params: { path: { propertyId } } })
      banks.value = res.data?.data ?? []
      if (!bank.value && banks.value.length === 1) bank.value = banks.value[0]?.id ?? 0
    }
    const { data } = await api.GET('/api/v1/properties/{propertyId}/bank/statements', { params: { path: { propertyId }, query: { bank_account_id: bank.value || undefined } } })
    statements.value = data?.data ?? []
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    loaded.value = true
  }
}

function startImport(): void {
  Object.assign(form, { period_from: '', period_to: '', opening_balance: '', closing_balance: '', note: '', csv: '' })
  // the next statement starts the day after the latest one and opens with its closing balance
  const last = statements.value.filter((s) => s.bank_account_id === bank.value).sort((a, b) => a.period_to.localeCompare(b.period_to)).at(-1)
  if (last) {
    const next = new Date(`${last.period_to}T00:00:00Z`)
    next.setUTCDate(next.getUTCDate() + 1)
    form.period_from = next.toISOString().slice(0, 10)
    form.opening_balance = last.closing_balance
  }
  error.value = null
  importing.value = true
}

async function readFile(ev: Event): Promise<void> {
  const file = (ev.target as HTMLInputElement).files?.[0]
  if (file) form.csv = await file.text()
}

async function runImport(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null) return
  busy.value = true
  error.value = null
  notice.value = ''
  try {
    const { data } = await api.POST('/api/v1/properties/{propertyId}/bank/statements', {
      params: { path: { propertyId } },
      body: {
        bank_account_id: bank.value, period_from: form.period_from, period_to: form.period_to, opening_balance: form.opening_balance.trim(), closing_balance: form.closing_balance.trim(),
        note: form.note || undefined, csv: form.csv,
      },
    })
    importing.value = false
    notice.value = `Statement imported with ${data?.lines.length ?? 0} line(s).`
    await load()
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    busy.value = false
  }
}

async function remove(s: BankStatement): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null || !window.confirm(`Delete the statement ${s.period_from} to ${s.period_to}? Its matchings are lost.`)) return
  busy.value = true
  error.value = null
  try {
    await api.DELETE('/api/v1/properties/{propertyId}/bank/statements/{id}', { params: { path: { propertyId, id: s.id } } })
    notice.value = 'Statement deleted.'
    await load()
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    busy.value = false
  }
}

watch(() => pid.value, () => {
  banks.value = []
  statements.value = []
  loaded.value = false
  void load()
}, { immediate: true })
</script>

<template>
  <div class="page-head">
    <h1 class="page-title">Bank statements</h1>
    <button v-if="can('bank.reconcile') && bank && !importing" type="button" class="btn-primary" data-testid="new-statement" @click="startImport">Import a statement</button>
  </div>
  <p v-if="error && !importing" class="alert" role="alert" data-testid="statement-error">{{ error.message }} <code>{{ error.code }}</code></p>
  <p v-if="notice" class="notice" role="status" data-testid="notice">{{ notice }}</p>
  <p v-if="pid === null" class="muted">Select a property first.</p>
  <p v-else-if="!can('bank.view')" class="muted" data-testid="no-access">Your role at this property cannot see bank statements: the <code>bank.view</code> permission is needed.</p>
  <template v-else>
    <form class="filters card" novalidate @submit.prevent="load">
      <label class="field">
        <span>Bank account</span>
        <select v-model.number="bank" name="bank" @change="load">
          <option :value="0">All</option>
          <option v-for="b in banks" :key="b.id" :value="b.id">{{ b.name }} ({{ b.account_code }})</option>
        </select>
      </label>
    </form>

    <form v-if="importing" class="card" novalidate data-testid="import-form" @submit.prevent="runImport">
      <h2>Import a statement</h2>
      <p class="muted">
        Paste the lines as CSV with a header row: a <code>date</code> column, <code>description</code>, optionally <code>reference</code>, and either <code>amount</code> (money in positive) or <code>credit</code> and <code>debit</code>
        columns. The lines must add up to the difference of the two balances printed on the statement.
      </p>
      <div class="form-grid">
        <label class="field">
          <span>From</span>
          <input v-model="form.period_from" name="period_from" type="date" :aria-invalid="!!fieldError('period_from')" />
          <small v-if="fieldError('period_from')" class="error-text">{{ fieldError('period_from') }}</small>
        </label>
        <label class="field">
          <span>To</span>
          <input v-model="form.period_to" name="period_to" type="date" :aria-invalid="!!fieldError('period_to')" />
          <small v-if="fieldError('period_to')" class="error-text">{{ fieldError('period_to') }}</small>
        </label>
        <label class="field">
          <span>Opening balance</span>
          <input v-model="form.opening_balance" name="opening_balance" inputmode="decimal" :aria-invalid="!!fieldError('opening_balance')" />
          <small v-if="fieldError('opening_balance')" class="error-text">{{ fieldError('opening_balance') }}</small>
        </label>
        <label class="field">
          <span>Closing balance</span>
          <input v-model="form.closing_balance" name="closing_balance" inputmode="decimal" :aria-invalid="!!fieldError('closing_balance')" />
          <small v-if="fieldError('closing_balance')" class="error-text">{{ fieldError('closing_balance') }}</small>
        </label>
        <label class="field wide"><span>Note</span><input v-model="form.note" name="note" maxlength="300" /></label>
      </div>
      <input type="file" accept=".csv,text/csv" data-testid="import-file" @change="readFile" />
      <textarea v-model="form.csv" name="csv" rows="8" class="csv" placeholder="date,description,reference,amount" />
      <small v-if="fieldError('csv')" class="error-text" data-testid="csv-error">{{ fieldError('csv') }}</small>
      <p v-if="error" class="alert" role="alert" data-testid="import-error">
        {{ error.message }} <code>{{ error.code }}</code>
        <template v-for="(f, i) in rowErrors.slice(0, 8)" :key="i"><br /><span class="muted">{{ f.field }}: {{ f.message }}</span></template>
      </p>
      <div class="form-actions">
        <button type="button" @click="importing = false">Cancel</button>
        <button type="submit" class="btn-primary" :disabled="busy || !form.csv.trim() || !form.period_from || !form.period_to || !form.closing_balance.trim()" data-testid="import-run">Import</button>
      </div>
    </form>

    <section class="card">
      <p v-if="loaded && !statements.length" class="muted" data-testid="empty">No statements yet.</p>
      <table v-else class="list" data-testid="statements">
        <thead><tr><th>Bank</th><th>Period</th><th class="num">Opening</th><th class="num">Closing</th><th>Matched</th><th>Status</th><th /></tr></thead>
        <tbody>
          <tr v-for="s in statements" :key="s.id" :data-testid="`statement-${s.id}`">
            <td>{{ s.bank_name }}</td>
            <td>{{ s.period_from }} – {{ s.period_to }}</td>
            <td class="num">{{ s.opening_balance }}</td>
            <td class="num">{{ s.closing_balance }}</td>
            <td>{{ s.matched_count }} of {{ s.line_count }}</td>
            <td>{{ s.status === 'RECONCILED' ? 'Reconciled' : 'Open' }}</td>
            <td class="row-actions">
              <RouterLink :to="`/bank/statements/${s.id}`" :data-testid="`open-${s.id}`">{{ s.status === 'OPEN' && can('bank.reconcile') ? 'Reconcile' : 'View' }}</RouterLink>
              <button v-if="s.status === 'OPEN' && can('bank.reconcile')" type="button" :data-testid="`delete-${s.id}`" @click="remove(s)">Delete</button>
            </td>
          </tr>
        </tbody>
      </table>
    </section>
  </template>
</template>

<style scoped>
.wide {
  grid-column: 1 / -1;
}
.csv {
  width: 100%;
  font-family: monospace;
  margin: 8px 0;
}
.num {
  text-align: right;
  white-space: nowrap;
}
.row-actions {
  display: flex;
  gap: 8px;
  align-items: center;
}
</style>
