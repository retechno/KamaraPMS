<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { StatementLine } from '@/api/types'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import { documentPath, openPdf } from '@/utils/documents'
import { downloadCsv } from './reportApi'
import StatementTable from './StatementTable.vue'

/** The income statement (USALI layout, over a range) and the balance sheet (as of a date): one screen, told apart by the route. */
const auth = useAuthStore()
const property = usePropertyStore()
const route = useRoute()

const income = computed(() => route.name === 'accounting-income-statement')
const title = computed(() => (income.value ? 'Income statement' : 'Balance sheet'))
const lines = ref<StatementLine[]>([])
const heading = ref('')
const imbalance = ref('')
const loaded = ref(false)
const error = ref<ApiError | null>(null)
const busy = ref(false)
const form = reactive({ from: '', to: '', as_of: '' })

const pid = computed(() => property.currentId)
const can = (p: string) => auth.can(p, pid.value)

async function load(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null || !can('accounting.view')) return
  busy.value = true
  error.value = null
  try {
    if (income.value) {
      const { data } = await api.GET('/api/v1/properties/{propertyId}/accounting/income-statement', {
        params: { path: { propertyId }, query: { from: form.from || undefined, to: form.to || undefined } },
      })
      lines.value = data?.lines ?? []
      heading.value = data ? `${data.from} to ${data.to}` : ''
      imbalance.value = ''
    } else {
      const { data } = await api.GET('/api/v1/properties/{propertyId}/accounting/balance-sheet', { params: { path: { propertyId }, query: { as_of: form.as_of || undefined } } })
      lines.value = data?.lines ?? []
      heading.value = data ? `As of ${data.as_of}` : ''
      imbalance.value = data && Number(data.difference) !== 0 ? data.difference : ''
    }
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    busy.value = false
    loaded.value = true
  }
}

async function exportCsv(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null) return
  try {
    if (income.value) {
      await downloadCsv('/api/v1/properties/{propertyId}/accounting/income-statement', { path: { propertyId }, query: { from: form.from || undefined, to: form.to || undefined } }, 'income-statement.csv')
    } else {
      await downloadCsv('/api/v1/properties/{propertyId}/accounting/balance-sheet', { path: { propertyId }, query: { as_of: form.as_of || undefined } }, 'balance-sheet.csv')
    }
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  }
}

async function showPdf(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null) return
  try {
    if (income.value) await openPdf(documentPath.accounting(propertyId, 'income-statement', { from: form.from, to: form.to }))
    else await openPdf(documentPath.accounting(propertyId, 'balance-sheet', { as_of: form.as_of }))
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  }
}

watch([() => pid.value, income], () => {
  lines.value = []
  loaded.value = false
  void load()
}, { immediate: true })
</script>

<template>
  <div class="page-head">
    <h1 class="page-title">{{ title }}</h1>
    <div v-if="loaded && lines.length" class="head-actions">
      <button type="button" data-testid="pdf" @click="showPdf">PDF</button>
      <button type="button" data-testid="export" @click="exportCsv">Export CSV</button>
    </div>
  </div>
  <p v-if="error" class="alert" role="alert" data-testid="report-error">
    {{ error.message }} <code>{{ error.code }}</code>
    <template v-for="(f, i) in error.fieldErrors ?? []" :key="i"><br /><span class="muted">{{ f.field }}: {{ f.message }}</span></template>
  </p>
  <p v-if="pid === null" class="muted">Select a property first.</p>
  <p v-else-if="!can('accounting.view')" class="muted" data-testid="no-access">Your role at this property cannot see financial statements: the <code>accounting.view</code> permission is needed.</p>
  <template v-else>
    <form class="filters card" novalidate @submit.prevent="load">
      <template v-if="income">
        <label class="field"><span>From</span><input v-model="form.from" name="from" type="date" /></label>
        <label class="field"><span>To</span><input v-model="form.to" name="to" type="date" /></label>
      </template>
      <label v-else class="field"><span>As of</span><input v-model="form.as_of" name="as_of" type="date" /></label>
      <button type="submit" :disabled="busy" data-testid="apply">Show</button>
    </form>
    <section v-if="loaded" class="card">
      <p class="muted" data-testid="range">{{ heading }}<template v-if="income"> · USALI layout: departments, gross operating profit (GOP), EBITDA, net income</template></p>
      <p v-if="imbalance" class="alert" data-testid="imbalance">The books are out of balance by {{ imbalance }}.</p>
      <p v-if="!lines.length" class="muted" data-testid="empty">Nothing is posted yet.</p>
      <StatementTable v-else :lines="lines" />
      <p v-if="!income" class="muted">Equity includes the earnings of all periods to date: there is no year-end closing entry.</p>
    </section>
  </template>
</template>

<style scoped>
.head-actions {
  display: flex;
  gap: 8px;
}
</style>
