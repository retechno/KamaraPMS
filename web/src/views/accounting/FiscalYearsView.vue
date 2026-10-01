<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { RouterLink } from 'vue-router'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { Approval, FiscalYear } from '@/api/types'
import ApprovalDialog from '@/components/ApprovalDialog.vue'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'

const auth = useAuthStore()
const property = usePropertyStore()

const years = ref<FiscalYear[]>([])
const loaded = ref(false)
const error = ref<ApiError | null>(null)
const dialogError = ref<ApiError | null>(null)
const notice = ref('')
const busy = ref(false)
const reopening = ref<{ start: string; label: string; reason: string; asking: boolean } | null>(null)

const pid = computed(() => property.currentId)
const can = (p: string) => auth.can(p, pid.value)
const path = { close: '/api/v1/properties/{propertyId}/accounting/fiscal-years/{start}/close', reopen: '/api/v1/properties/{propertyId}/accounting/fiscal-years/{start}/reopen' } as const

async function load(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null || !can('accounting.view')) return
  try {
    const { data } = await api.GET('/api/v1/properties/{propertyId}/accounting/fiscal-years', { params: { path: { propertyId } } })
    years.value = data?.data ?? []
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    loaded.value = true
  }
}

async function close(y: FiscalYear): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null) return
  const msg = `Close ${y.label}? The result of the year (${y.net_income}) is moved to retained earnings and every month of the year stays closed until the year is reopened.`
  if (!window.confirm(msg)) return
  busy.value = true
  error.value = null
  notice.value = ''
  try {
    await api.POST(path.close, { params: { path: { propertyId, start: y.year_start } } })
    notice.value = `${y.label} is closed.`
    await load()
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    busy.value = false
  }
}

async function reopen(approval: Approval): Promise<void> {
  const propertyId = pid.value
  const r = reopening.value
  if (propertyId === null || r === null) return
  busy.value = true
  dialogError.value = null
  try {
    await api.POST(path.reopen, { params: { path: { propertyId, start: r.start } }, body: { reason: r.reason.trim(), approval } })
    notice.value = `${r.label} is open again.`
    reopening.value = null
    await load()
  } catch (e) {
    dialogError.value = e instanceof ApiError ? e : null
  } finally {
    busy.value = false
  }
}

watch(() => pid.value, () => {
  years.value = []
  loaded.value = false
  void load()
}, { immediate: true })
</script>

<template>
  <div class="page-head">
    <h1 class="page-title">Fiscal years</h1>
  </div>
  <p v-if="error" class="alert" role="alert" data-testid="year-error">{{ error.message }} <code>{{ error.code }}</code></p>
  <p v-if="notice" class="notice" role="status" data-testid="notice">{{ notice }}</p>
  <p v-if="pid === null" class="muted">Select a property first.</p>
  <p v-else-if="!can('accounting.view')" class="muted" data-testid="no-access">Your role at this property cannot see fiscal years: the <code>accounting.view</code> permission is needed.</p>
  <template v-else>
    <p class="muted">
      Closing a fiscal year posts a closing journal on its last day that moves the revenue and expense balances to retained earnings, so the next year's income statement starts from zero.
      A year closes when it has ended, the year before it is closed and every month of it is closed in <RouterLink to="/accounting/periods">Periods</RouterLink>.
    </p>
    <form v-if="reopening && !reopening.asking" class="card" novalidate data-testid="reopen-form" @submit.prevent="reopening.asking = true">
      <h2>Reopen {{ reopening.label }}</h2>
      <p class="muted">The closing journal is reversed. Reopening needs the approval of someone who may approve corrections.</p>
      <label class="field">
        <span>Reason</span>
        <input v-model="reopening.reason" name="reason" maxlength="500" />
      </label>
      <div class="form-actions">
        <button type="button" @click="reopening = null">Cancel</button>
        <button type="submit" class="btn-primary" :disabled="!reopening.reason.trim()" data-testid="reopen-ask">Continue</button>
      </div>
    </form>
    <section class="card">
      <p v-if="loaded && !years.length" class="muted" data-testid="empty">No fiscal year yet.</p>
      <table v-else class="list" data-testid="years">
        <thead><tr><th>Year</th><th>From – to</th><th>Months closed</th><th class="num">Result</th><th>Status</th><th /></tr></thead>
        <tbody>
          <tr v-for="y in years" :key="y.year_start" :data-testid="`year-${y.label}`">
            <td><b>{{ y.label }}</b></td>
            <td>{{ y.year_start }} – {{ y.year_end }}</td>
            <td>{{ y.closed_months }} of {{ y.months }}</td>
            <td class="num">{{ y.net_income }}</td>
            <td>
              {{ y.status === 'CLOSED' ? 'Closed' : 'Open' }}
              <small v-if="y.closing_journal_number" class="muted"> · journal {{ y.closing_journal_number }}</small>
              <small v-if="y.reopen_reason && y.status === 'OPEN'" class="muted"> · reopened: {{ y.reopen_reason }}</small>
            </td>
            <td class="row-actions">
              <button v-if="can('accounting.close') && y.closable" type="button" class="btn-primary" :disabled="busy" :data-testid="`close-${y.label}`" @click="close(y)">Close year</button>
              <button v-if="can('accounting.close') && y.reopenable" type="button" :data-testid="`reopen-${y.label}`" @click="reopening = { start: y.year_start, label: y.label, reason: '', asking: false }">Reopen</button>
            </td>
          </tr>
        </tbody>
      </table>
    </section>
  </template>
  <ApprovalDialog v-if="reopening?.asking" title="Approve reopening" :busy="busy" :error="dialogError" @approve="reopen" @cancel="reopening = null; dialogError = null" />
</template>

<style scoped>
.num {
  text-align: right;
  white-space: nowrap;
}
</style>
