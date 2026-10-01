<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { TrialBalance } from '@/api/types'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import { documentPath, openPdf } from '@/utils/documents'
import { downloadCsv, money } from './reportApi'

const auth = useAuthStore()
const property = usePropertyStore()

const report = ref<TrialBalance | null>(null)
const error = ref<ApiError | null>(null)
const busy = ref(false)
const range = reactive({ from: '', to: '' })

const pid = computed(() => property.currentId)
const can = (p: string) => auth.can(p, pid.value)
const query = () => ({ from: range.from || undefined, to: range.to || undefined })

async function load(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null || !can('accounting.view')) return
  busy.value = true
  error.value = null
  try {
    const { data } = await api.GET('/api/v1/properties/{propertyId}/accounting/trial-balance', { params: { path: { propertyId }, query: query() } })
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
    await downloadCsv('/api/v1/properties/{propertyId}/accounting/trial-balance', { path: { propertyId }, query: query() }, `trial-balance-${report.value.to}.csv`)
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  }
}

async function showPdf(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null) return
  try {
    await openPdf(documentPath.accounting(propertyId, 'trial-balance', query()))
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  }
}

watch(() => pid.value, () => {
  report.value = null
  void load()
}, { immediate: true })
</script>

<template>
  <div class="page-head">
    <h1 class="page-title">Trial balance</h1>
    <div v-if="report" class="head-actions">
      <button type="button" data-testid="pdf" @click="showPdf">PDF</button>
      <button type="button" data-testid="export" @click="exportCsv">Export CSV</button>
    </div>
  </div>
  <p v-if="error" class="alert" role="alert" data-testid="report-error">
    {{ error.message }} <code>{{ error.code }}</code>
    <template v-for="(f, i) in error.fieldErrors ?? []" :key="i"><br /><span class="muted">{{ f.field }}: {{ f.message }}</span></template>
  </p>
  <p v-if="pid === null" class="muted">Select a property first.</p>
  <p v-else-if="!can('accounting.view')" class="muted" data-testid="no-access">Your role at this property cannot see the trial balance: the <code>accounting.view</code> permission is needed.</p>
  <template v-else>
    <form class="filters card" novalidate @submit.prevent="load">
      <label class="field"><span>From</span><input v-model="range.from" name="from" type="date" /></label>
      <label class="field"><span>To</span><input v-model="range.to" name="to" type="date" /></label>
      <button type="submit" :disabled="busy" data-testid="apply">Show</button>
    </form>
    <section v-if="report" class="card">
      <p class="muted" data-testid="range">{{ report.from }} to {{ report.to }}</p>
      <p v-if="!report.rows.length" class="muted" data-testid="empty">No entries up to this date.</p>
      <table v-else class="list" data-testid="trial-balance">
        <thead>
          <tr><th rowspan="2">Account</th><th colspan="2">Opening</th><th colspan="2">Movement</th><th colspan="2">Closing</th></tr>
          <tr><th class="num">Debit</th><th class="num">Credit</th><th class="num">Debit</th><th class="num">Credit</th><th class="num">Debit</th><th class="num">Credit</th></tr>
        </thead>
        <tbody>
          <tr v-for="r in report.rows" :key="r.account_id" :data-testid="`row-${r.code}`">
            <td>{{ r.code }} · {{ r.name }}</td>
            <td class="num">{{ money(r.opening_debit) }}</td><td class="num">{{ money(r.opening_credit) }}</td>
            <td class="num">{{ money(r.debit) }}</td><td class="num">{{ money(r.credit) }}</td>
            <td class="num">{{ money(r.closing_debit) }}</td><td class="num">{{ money(r.closing_credit) }}</td>
          </tr>
        </tbody>
        <tfoot>
          <tr data-testid="totals">
            <th>Total</th>
            <th class="num">{{ money(report.totals.opening_debit) }}</th><th class="num">{{ money(report.totals.opening_credit) }}</th>
            <th class="num">{{ money(report.totals.debit) }}</th><th class="num">{{ money(report.totals.credit) }}</th>
            <th class="num">{{ money(report.totals.closing_debit) }}</th><th class="num">{{ money(report.totals.closing_credit) }}</th>
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
