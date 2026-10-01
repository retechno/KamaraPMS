<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import { addDays } from '@/utils/dates'
import { type ReportDef, type ReportKey, type Table, reports } from './reportDefs'

const auth = useAuthStore()
const property = usePropertyStore()

const key = ref<ReportKey>('revenue')
const range = reactive({ from: '', to: '', date: '' })
const table = ref<Table | null>(null)
const error = ref<ApiError | null>(null)
const busy = ref(false)

const pid = computed(() => property.currentId)
const businessDate = computed(() => property.clock?.business_date ?? '')
const allowed = computed(() => auth.can('report.view', pid.value))
const def = computed<ReportDef>(() => reports.find((r) => r.key === key.value) ?? reports[0]!)

watch(businessDate, (bd) => {
  if (!bd) return
  range.date ||= bd
  range.to ||= bd
  range.from ||= addDays(bd, -6)
}, { immediate: true })

function query(format?: 'csv'): Record<string, string | undefined> {
  const q: Record<string, string | undefined> = {}
  if (def.value.input === 'range') Object.assign(q, { from: range.from, to: range.to })
  if (def.value.input === 'date') q.date = range.date
  if (format) q.format = format
  return q
}

// The report paths are fixed strings; the typed client needs the literal, so the call goes through one cast.
async function fetchReport(format?: 'csv'): Promise<unknown> {
  const propertyId = pid.value
  if (propertyId === null) return undefined
  const get = api.GET as unknown as (path: string, init: object) => Promise<{ data?: unknown }>
  const init = { params: { path: { propertyId }, query: query(format) }, ...(format ? { parseAs: 'text' } : {}) }
  const { data } = await get(`/api/v1/properties/{propertyId}/reports/${key.value}`, init)
  return data
}

async function run(): Promise<void> {
  busy.value = true
  error.value = null
  try {
    const data = await fetchReport()
    table.value = data ? def.value.table(data as never) : null
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
    table.value = null
  } finally {
    busy.value = false
  }
}

async function download(): Promise<void> {
  busy.value = true
  error.value = null
  try {
    const text = await fetchReport('csv')
    const url = URL.createObjectURL(new Blob([String(text ?? '')], { type: 'text/csv;charset=utf-8' }))
    const a = document.createElement('a')
    a.href = url
    a.download = `${key.value}-${range.from || range.date || businessDate.value}${def.value.input === 'range' ? `_${range.to}` : ''}.csv`
    a.click()
    URL.revokeObjectURL(url)
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    busy.value = false
  }
}

watch([key, pid], () => {
  table.value = null
  error.value = null
})
</script>

<template>
  <div class="page-head">
    <h1 class="page-title">Reports</h1>
  </div>

  <p v-if="error" class="alert" role="alert" data-testid="form-error">{{ error.message }} <code>{{ error.code }}</code></p>
  <p v-if="pid === null" class="muted">Select a property first.</p>
  <p v-else-if="!allowed" class="muted" data-testid="no-access">Your role at this property does not allow viewing reports (the <code>report.view</code> permission).</p>

  <template v-else>
    <form class="card filters" novalidate data-testid="filters" @submit.prevent="run">
      <label class="field">
        <span>Report</span>
        <select v-model="key" name="report">
          <option v-for="r in reports" :key="r.key" :value="r.key">{{ r.title }}</option>
        </select>
      </label>
      <template v-if="def.input === 'range'">
        <label class="field"><span>From</span><input v-model="range.from" name="from" type="date" /></label>
        <label class="field"><span>To</span><input v-model="range.to" name="to" type="date" /></label>
      </template>
      <label v-else-if="def.input === 'date'" class="field"><span>Date</span><input v-model="range.date" name="date" type="date" /></label>
      <button type="submit" class="btn-primary" :disabled="busy" data-testid="run">Run</button>
      <button type="button" :disabled="busy || !table" data-testid="csv" @click="download">Download CSV</button>
      <small class="muted hint">{{ def.hint }} Business dates, not calendar dates.</small>
    </form>

    <section v-if="table" class="card" data-testid="result">
      <p v-if="table.note" class="muted" data-testid="note">{{ table.note }}</p>
      <p v-if="!table.rows.length" class="muted" data-testid="empty">Nothing to report for this selection.</p>
      <table v-else class="list">
        <thead><tr><th v-for="(c, i) in table.columns" :key="c" :class="{ num: table.numeric.includes(i) }">{{ c }}</th></tr></thead>
        <tbody>
          <tr v-for="(r, ri) in table.rows" :key="ri">
            <td v-for="(c, i) in r" :key="i" :class="{ num: table.numeric.includes(i) }">{{ c }}</td>
          </tr>
        </tbody>
        <tfoot v-if="table.footer">
          <tr data-testid="footer"><th v-for="(c, i) in table.footer" :key="i" :class="{ num: table.numeric.includes(i) }">{{ c }}</th></tr>
        </tfoot>
      </table>
    </section>
  </template>
</template>

<style scoped>
.filters {
  display: flex;
  gap: 12px;
  align-items: flex-end;
  flex-wrap: wrap;
}
.hint {
  flex-basis: 100%;
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
.list .num {
  text-align: right;
  font-variant-numeric: tabular-nums;
}
</style>
