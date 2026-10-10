<script setup lang="ts">
import ErrorNotice from '@/components/app/ErrorNotice.vue'
import { computed, reactive, ref, watch } from 'vue'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import EmptyState from '@/components/app/EmptyState.vue'
import FormField from '@/components/app/FormField.vue'
import PageHeader from '@/components/app/PageHeader.vue'
import { Button } from '@/components/ui/button'
import { Card, CardContent } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { NativeSelect } from '@/components/ui/native-select'
import { currentLocale, t } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import { addDays } from '@/utils/dates'
import { type ReportDef, type ReportKey, type Table, reports } from './reportDefs'

const auth = useAuthStore()
const property = usePropertyStore()

const key = ref<ReportKey>('revenue')
const range = reactive({ from: '', to: '', date: '', minHours: 0 })
// The answer is kept as the server sent it and laid out on demand, so a change of language re-labels the table.
const data = ref<unknown>(null)
const error = ref<ApiError | null>(null)
const busy = ref(false)

const pid = computed(() => property.currentId)
const businessDate = computed(() => property.clock?.business_date ?? '')
const allowed = computed(() => auth.can('report.view', pid.value))
const def = computed<ReportDef>(() => reports.find((r) => r.key === key.value) ?? reports[0]!)
const table = computed<Table | null>(() => (data.value ? def.value.table(data.value as never) : null))

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
  if (def.value.input === 'hours') q.min_hours = String(Math.max(0, Math.trunc(Number(range.minHours) || 0)))
  if (format) {
    q.format = format
    q.lang = currentLocale() // the column names of the CSV follow the language of the page
  }
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
    data.value = (await fetchReport()) ?? null
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
    data.value = null
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
  data.value = null
  error.value = null
})
</script>

<template>
  <PageHeader :title="t('reports.title')" />

  <ErrorNotice v-if="error" :error="error" inline data-testid="form-error" />
  <p v-if="pid === null" class="muted">{{ t('setup.selectProperty') }}</p>
  <p v-else-if="!allowed" class="muted" data-testid="no-access">{{ t('reports.noAccess', { permission: 'report.view' }) }}</p>

  <template v-else>
    <Card class="mb-4">
      <form class="flex flex-wrap items-end gap-x-4 gap-y-6 px-4 pb-8 pt-4" novalidate data-testid="filters" @submit.prevent="run">
        <FormField class="w-72" :label="t('reports.report')">
          <template #default="{ id }">
            <NativeSelect :id="id" v-model="key" name="report">
              <option v-for="r in reports" :key="r.key" :value="r.key">{{ r.title() }}</option>
            </NativeSelect>
          </template>
        </FormField>
        <template v-if="def.input === 'range'">
          <FormField float-hint :hint="$weekday(range.from)" :label="t('reports.from')"><template #default="{ id }"><Input :id="id" v-model="range.from" name="from" type="date" /></template></FormField>
          <FormField float-hint :hint="$weekday(range.to)" :label="t('reports.to')"><template #default="{ id }"><Input :id="id" v-model="range.to" name="to" type="date" /></template></FormField>
        </template>
        <FormField float-hint :hint="$weekday(range.date)" v-else-if="def.input === 'date'" :label="t('reports.date')"><template #default="{ id }"><Input :id="id" v-model="range.date" name="date" type="date" /></template></FormField>
        <FormField v-else-if="def.input === 'hours'" :label="t('reports.minHours')"><template #default="{ id }"><Input :id="id" v-model.number="range.minHours" name="min_hours" type="number" min="0" /></template></FormField>
        <Button type="submit" :disabled="busy" data-testid="run">{{ t('reports.run') }}</Button>
        <Button type="button" variant="outline" :disabled="busy || !table" data-testid="csv" @click="download">{{ t('reports.csv') }}</Button>
        <small class="basis-full text-xs text-muted-foreground">{{ def.hint() }} {{ t('reports.businessDates') }}</small>
      </form>
    </Card>

    <Card v-if="table" data-testid="result">
      <CardContent class="pt-4">
        <p v-if="table.note" class="mb-3 mt-0 text-sm text-muted-foreground" data-testid="note">{{ table.note }}</p>
        <EmptyState v-if="!table.rows.length" :description="t('emptyState.changeSelection')" :title="t('reports.empty')" data-testid="empty" />
        <div v-else class="overflow-x-auto">
          <table class="w-full border-collapse text-sm">
            <thead>
              <tr>
                <th v-for="(c, i) in table.columns" :key="c" :class="['whitespace-nowrap border-b border-border px-3 py-2 text-xs font-semibold uppercase tracking-wide text-muted-foreground', table.numeric.includes(i) ? 'text-right' : 'text-left']">{{ c }}</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="(r, ri) in table.rows" :key="ri" class="border-b border-border hover:bg-accent/50">
                <td v-for="(c, i) in r" :key="i" :class="['px-3 py-2', table.numeric.includes(i) && 'text-right tabular-nums']">{{ table.numeric.includes(i) ? $money(c) : $date(c) }}</td>
              </tr>
            </tbody>
            <tfoot v-if="table.footer">
              <tr data-testid="footer" class="font-semibold">
                <th v-for="(c, i) in table.footer" :key="i" :class="['px-3 py-2', table.numeric.includes(i) ? 'text-right tabular-nums' : 'text-left']">{{ table.numeric.includes(i) ? $money(c) : c }}</th>
              </tr>
            </tfoot>
          </table>
        </div>
      </CardContent>
    </Card>
  </template>
</template>
