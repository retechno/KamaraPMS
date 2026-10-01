<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { GeneralLedger, GlAccount } from '@/api/types'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import { listAccounts } from './accountApi'
import { documentPath, openPdf } from '@/utils/documents'
import { downloadCsv, money } from './reportApi'

const auth = useAuthStore()
const property = usePropertyStore()
const route = useRoute()

const accounts = ref<GlAccount[]>([])
const report = ref<GeneralLedger | null>(null)
const error = ref<ApiError | null>(null)
const busy = ref(false)
const queryDate = (v: unknown): string => (typeof v === 'string' && /^\d{4}-\d{2}-\d{2}$/.test(v) ? v : '')
const form = reactive({ account: Number(route.query.account) || 0, from: queryDate(route.query.from), to: queryDate(route.query.to) })

const pid = computed(() => property.currentId)
const can = (p: string) => auth.can(p, pid.value)
const postable = computed(() => accounts.value.filter((a) => a.is_postable))
const query = () => ({ from: form.from || undefined, to: form.to || undefined })

async function loadAccounts(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null || !can('accounting.view')) return
  try {
    accounts.value = await listAccounts(propertyId)
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  }
}

async function load(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null || !form.account) return
  busy.value = true
  error.value = null
  try {
    const { data } = await api.GET('/api/v1/properties/{propertyId}/accounting/accounts/{id}/ledger', { params: { path: { propertyId, id: form.account }, query: query() } })
    report.value = data ?? null
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    busy.value = false
  }
}

async function exportCsv(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null || !report.value) return
  try {
    await downloadCsv('/api/v1/properties/{propertyId}/accounting/accounts/{id}/ledger', { path: { propertyId, id: form.account }, query: query() }, `ledger-${report.value.account.code}.csv`)
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  }
}

async function showPdf(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null || !form.account) return
  try {
    await openPdf(documentPath.ledger(propertyId, form.account, query()))
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  }
}

watch(() => pid.value, () => {
  accounts.value = []
  report.value = null
  void loadAccounts().then(load)
}, { immediate: true })
</script>

<template>
  <div class="page-head">
    <h1 class="page-title">General ledger</h1>
    <div v-if="report" class="head-actions">
      <button type="button" data-testid="pdf" @click="showPdf">PDF</button>
      <button type="button" data-testid="export" @click="exportCsv">Export CSV</button>
    </div>
  </div>
  <p v-if="error" class="alert" role="alert" data-testid="report-error">{{ error.message }} <code>{{ error.code }}</code></p>
  <p v-if="pid === null" class="muted">Select a property first.</p>
  <p v-else-if="!can('accounting.view')" class="muted" data-testid="no-access">Your role at this property cannot see the general ledger: the <code>accounting.view</code> permission is needed.</p>
  <template v-else>
    <form class="filters card" novalidate @submit.prevent="load">
      <label class="field">
        <span>Account</span>
        <select v-model.number="form.account" name="account">
          <option :value="0">Choose an account</option>
          <option v-for="a in postable" :key="a.id" :value="a.id">{{ a.code }} · {{ a.name }}</option>
        </select>
      </label>
      <label class="field"><span>From</span><input v-model="form.from" name="from" type="date" /></label>
      <label class="field"><span>To</span><input v-model="form.to" name="to" type="date" /></label>
      <button type="submit" :disabled="busy || !form.account" data-testid="apply">Show</button>
    </form>
    <section v-if="report" class="card">
      <h2>{{ report.account.code }} · {{ report.account.name }}</h2>
      <p class="muted" data-testid="range">{{ report.from }} to {{ report.to }} · balances on the {{ report.account.normal_side.toLowerCase() }} side</p>
      <p v-if="report.truncated" class="alert" data-testid="truncated">Only the first 5000 entries are shown: narrow the range.</p>
      <table class="list" data-testid="ledger">
        <thead><tr><th>Date</th><th>Journal</th><th>Detail</th><th class="num">Debit</th><th class="num">Credit</th><th class="num">Balance</th></tr></thead>
        <tbody>
          <tr data-testid="opening"><td colspan="5">Opening balance</td><td class="num">{{ report.opening_balance }}</td></tr>
          <tr v-for="(l, i) in report.lines" :key="i" :data-testid="`entry-${i}`">
            <td>{{ l.journal_date }}</td>
            <td>{{ l.journal_number }}</td>
            <td>{{ l.description }}</td>
            <td class="num">{{ money(l.debit) }}</td>
            <td class="num">{{ money(l.credit) }}</td>
            <td class="num">{{ l.balance }}</td>
          </tr>
        </tbody>
        <tfoot>
          <tr data-testid="closing">
            <th colspan="3">Closing balance</th>
            <th class="num">{{ money(report.total_debit) }}</th>
            <th class="num">{{ money(report.total_credit) }}</th>
            <th class="num">{{ report.closing_balance }}</th>
          </tr>
        </tfoot>
      </table>
    </section>
  </template>
</template>

<style scoped>
.head-actions {
  display: flex;
  gap: 8px;
}
.num {
  text-align: right;
  white-space: nowrap;
}
</style>
