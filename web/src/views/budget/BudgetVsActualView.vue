<script setup lang="ts">
import ErrorNotice from '@/components/app/ErrorNotice.vue'
import { computed, reactive, ref, watch } from 'vue'
import { RouterLink } from 'vue-router'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { Budget, BudgetCell, BudgetDepartmentVariance, BudgetDepartmentVsActual, BudgetStatisticsVsActual, BudgetVsActual } from '@/api/types'
import DepartmentSelect from '@/components/app/DepartmentSelect.vue'
import EmptyState from '@/components/app/EmptyState.vue'
import FormField from '@/components/app/FormField.vue'
import PageHeader from '@/components/app/PageHeader.vue'
import { Button } from '@/components/ui/button'
import { Card, CardContent } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { NativeSelect } from '@/components/ui/native-select'
import { formatMoney } from '@/utils/format'
import { t } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import { openPdf } from '@/utils/documents'
import { downloadCsv } from '../accounting/reportApi'
import { monthLabel, monthStart, monthValue } from './budgetMath'

/**
 * The budget against the actuals of the books, laid out like the income statement: a period (months of a fiscal year) and the year to date.
 * A variance is the actual less the budget; whether it is good is told in words as well as by colour.
 */
const auth = useAuthStore()
const property = usePropertyStore()

const report = ref<BudgetVsActual | null>(null)
const statistics = ref<BudgetStatisticsVsActual | null>(null)
const byDepartment = ref<BudgetDepartmentVsActual | null>(null)
const basis = ref<'period' | 'ytd'>('period')
const budgets = ref<Budget[]>([])
const loaded = ref(false)
const error = ref<ApiError | null>(null)
const busy = ref(false)
const form = reactive({ year_start: '', from: '', to: '', budget_id: '', department_id: null as number | null })

const pid = computed(() => property.currentId)
const can = (p: string) => auth.can(p, pid.value)
const years = computed(() => [...new Map(budgets.value.map((b) => [b.year_start, b.year_label])).entries()].map(([value, label]) => ({ value, label })))
const versions = computed(() => budgets.value.filter((b) => !form.year_start || b.year_start === form.year_start))

function query(): Record<string, string | number | undefined> {
  return {
    year_start: form.year_start || undefined,
    from: monthStart(form.from) || undefined,
    to: monthStart(form.to) || undefined,
    budget_id: form.budget_id ? Number(form.budget_id) : undefined,
  }
}

/** The same year, range and version, and the department the report by department is narrowed to. */
const departmentQuery = () => ({ ...query(), department_id: form.department_id ?? undefined })

async function loadBudgets(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null || !can('budget.view')) return
  try {
    const { data } = await api.GET('/api/v1/properties/{propertyId}/budgets', { params: { path: { propertyId } } })
    budgets.value = data?.data ?? []
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  }
}

async function show(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null || !can('budget.view')) return
  busy.value = true
  error.value = null
  try {
    const { data } = await api.GET('/api/v1/properties/{propertyId}/budgets/vs-actual', { params: { path: { propertyId }, query: query() } })
    report.value = (data as BudgetVsActual | undefined) ?? null
    // the statistics of the same version and range; the money report alone is not lost when they cannot be read
    statistics.value = null
    if (report.value) {
      const st = await api.GET('/api/v1/properties/{propertyId}/budgets/statistics-vs-actual', { params: { path: { propertyId }, query: query() } })
      statistics.value = st.data ?? null
      byDepartment.value = null
      const dv = await api.GET('/api/v1/properties/{propertyId}/budgets/department-vs-actual', { params: { path: { propertyId }, query: departmentQuery() } })
      byDepartment.value = dv.data ?? null
    }
  } catch (e) {
    report.value = null
    statistics.value = null
    byDepartment.value = null
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
    await downloadCsv('/api/v1/properties/{propertyId}/budgets/vs-actual', { path: { propertyId }, query: query() }, 'budget-vs-actual.csv')
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  }
}

async function showPdf(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null) return
  const q = new URLSearchParams()
  for (const [k, v] of Object.entries(query())) if (v !== undefined) q.set(k, String(v))
  try {
    await openPdf(`/api/v1/properties/${propertyId}/budgets/vs-actual.pdf${q.toString() ? `?${q}` : ''}`)
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  }
}

/** A variance with its sign, and in words whether it is good ("favourable") or bad: colour alone is not enough. */
const verdict = (c: BudgetCell): string => (c.favourable === null ? '' : c.favourable ? t('budget.fav') : t('budget.unfav'))
const tone = (c: BudgetCell): string => (c.favourable === null ? '' : c.favourable ? 'text-success-text' : 'text-destructive')
const amount = (v: string): string => (Number(v) === 0 ? '–' : formatMoney(v))
const percent = (c: BudgetCell): string => (c.variance_percent === null ? '' : `${formatMoney(c.variance_percent)}%`)
/** A statistic in its unit: room nights as they are, a percent with its sign, money with separators. */
function stat(unit: string, v: string): string {
  if (unit === 'PERCENT') return `${formatMoney(v)}%`
  return unit === 'MONEY' ? amount(v) : formatMoney(v)
}
/** The departments of the report by department as rows: each department followed by its sub-departments. */
const departmentRows = computed<BudgetDepartmentVariance[]>(() => {
  const list = byDepartment.value?.departments
  return Array.isArray(list) ? list.flatMap((d) => [d, ...(d.children ?? [])]) : []
})
const hasUnassigned = computed(() => {
  const u = byDepartment.value?.unassigned
  return !!u && [u.revenue, u.expense].some((p) => p && !(Number(p.period.actual) === 0 && Number(p.period.budget) === 0 && Number(p.ytd.actual) === 0 && Number(p.ytd.budget) === 0))
})
const deptCell = (p: { period: BudgetCell; ytd: BudgetCell }): BudgetCell => (basis.value === 'ytd' ? p.ytd : p.period)
const bothZero = (c: BudgetCell): boolean => Number(c.actual) === 0 && Number(c.budget) === 0

watch(() => pid.value, () => {
  report.value = null
  loaded.value = false
  void loadBudgets()
  void show()
}, { immediate: true })
watch(() => form.year_start, () => { form.budget_id = '' })
</script>

<template>
  <PageHeader :title="t('budget.vsActualTitle')" :description="t('budget.vsActualIntro')">
    <template #actions>
      <RouterLink to="/budget" class="text-sm text-primary hover:underline">{{ t('budget.title') }}</RouterLink>
      <template v-if="report">
        <Button type="button" variant="outline" data-testid="pdf" @click="showPdf">{{ t('statements.pdf') }}</Button>
        <Button type="button" variant="outline" data-testid="export" @click="exportCsv">{{ t('statements.export') }}</Button>
      </template>
    </template>
  </PageHeader>
  <ErrorNotice v-if="error" :error="error" inline data-testid="report-error">
<template v-for="(f, i) in error.fieldErrors ?? []" :key="i"><br /><span class="muted">{{ f.field }}: {{ f.message }}</span></template>
</ErrorNotice>
  <p v-if="pid === null" class="muted">{{ t('setup.selectProperty') }}</p>
  <p v-else-if="!can('budget.view')" class="muted" data-testid="no-access">{{ t('budget.noAccess', { permission: 'budget.view' }) }}</p>
  <template v-else>
    <Card class="mb-4">
      <form class="flex flex-wrap items-end gap-4 p-4" novalidate data-testid="filters" @submit.prevent="show">
        <FormField :label="t('budget.year')">
          <template #default="{ id }">
            <NativeSelect :id="id" v-model="form.year_start" name="year_start">
              <option value="">{{ t('budget.currentYear') }}</option>
              <option v-for="y in years" :key="y.value" :value="y.value">{{ y.label }}</option>
            </NativeSelect>
          </template>
        </FormField>
        <FormField :label="t('budget.fromMonth')">
          <template #default="{ id }"><Input :id="id" v-model="form.from" name="from" type="month" /></template>
        </FormField>
        <FormField :label="t('budget.toMonth')">
          <template #default="{ id }"><Input :id="id" v-model="form.to" name="to" type="month" /></template>
        </FormField>
        <FormField :label="t('budget.versionPick')">
          <template #default="{ id }">
            <NativeSelect :id="id" v-model="form.budget_id" name="budget_id">
              <option value="">{{ t('budget.activeVersion') }}</option>
              <option v-for="b in versions" :key="b.id" :value="String(b.id)">{{ t('budget.versionLabel', { year: b.year_label, version: b.version, name: b.name }) }} · {{ t(`budget.status.${b.status}`) }}</option>
            </NativeSelect>
          </template>
        </FormField>
        <FormField :label="t('budget.department')">
          <template #default="{ id }"><DepartmentSelect :id="id" v-model="form.department_id" name="department_id" :none-label="t('departments.all')" /></template>
        </FormField>
        <Button type="submit" variant="outline" :disabled="busy" data-testid="apply">{{ t('statements.show') }}</Button>
      </form>
    </Card>
    <Card v-if="loaded">
      <CardContent class="pt-4">
        <EmptyState v-if="!report" :title="t('budget.noReport')" :description="t('budget.noReportHint')" data-testid="empty" />
        <template v-else>
          <p class="mb-3 mt-0 text-sm text-muted-foreground" data-testid="range">
            {{ report.year_label }} · {{ report.budget.name }} (v{{ report.budget.version }}, {{ t(`budget.status.${report.budget.status}`) }}) ·
            {{ monthLabel(report.from, true) }} – {{ monthLabel(report.to, true) }} · {{ t('statements.usali') }}
          </p>
          <div class="overflow-x-auto">
            <table class="w-full border-collapse text-sm" data-testid="report">
              <thead>
                <tr class="text-xs uppercase tracking-wide text-muted-foreground">
                  <th rowspan="2" class="py-2 pr-2 text-left align-bottom">{{ t('budget.line') }}</th>
                  <th colspan="4" class="border-l border-border px-2 py-1 text-center">{{ t('budget.period') }}: {{ monthValue(report.from) }} – {{ monthValue(report.to) }}</th>
                  <th colspan="4" class="border-l border-border px-2 py-1 text-center">{{ t('budget.ytd') }}</th>
                </tr>
                <tr class="border-b border-border text-xs uppercase tracking-wide text-muted-foreground">
                  <template v-for="g in ['p', 'y']" :key="g">
                    <th class="border-l border-border px-2 py-1 text-right">{{ t('budget.actual') }}</th>
                    <th class="px-2 py-1 text-right">{{ t('budget.budgetCol') }}</th>
                    <th class="px-2 py-1 text-right">{{ t('budget.variance') }}</th>
                    <th class="px-2 py-1 text-right">%</th>
                  </template>
                </tr>
              </thead>
              <tbody>
                <template v-for="l in report.lines" :key="l.key">
                  <tr v-if="l.kind === 'HEADING'" :data-testid="`line-${l.key}`">
                    <th colspan="9" class="pb-1 pt-4 text-left text-xs font-semibold uppercase tracking-wide text-muted-foreground">{{ l.title }}</th>
                  </tr>
                  <template v-else>
                    <template v-if="l.kind === 'GROUP'">
                      <tr :data-testid="`line-${l.key}`" class="border-b border-border">
                        <td class="py-1.5 pr-2 font-medium">{{ l.title }}</td>
                        <td v-if="l.accounts.length === 1" colspan="8" class="border-l border-border" />
                        <template v-else>
                          <td class="border-l border-border px-2 text-right tabular-nums">{{ bothZero(l.period) ? '' : amount(l.period.actual) }}</td>
                          <td class="px-2 text-right tabular-nums">{{ bothZero(l.period) ? '' : amount(l.period.budget) }}</td>
                          <td class="px-2 text-right tabular-nums" :class="tone(l.period)">{{ bothZero(l.period) ? '' : amount(l.period.variance) }}</td>
                          <td class="px-2 text-right tabular-nums text-muted-foreground">{{ percent(l.period) }}</td>
                          <td class="border-l border-border px-2 text-right tabular-nums">{{ bothZero(l.ytd) ? '' : amount(l.ytd.actual) }}</td>
                          <td class="px-2 text-right tabular-nums">{{ bothZero(l.ytd) ? '' : amount(l.ytd.budget) }}</td>
                          <td class="px-2 text-right tabular-nums" :class="tone(l.ytd)">{{ bothZero(l.ytd) ? '' : amount(l.ytd.variance) }}</td>
                          <td class="px-2 text-right tabular-nums text-muted-foreground">{{ percent(l.ytd) }}</td>
                        </template>
                      </tr>
                    </template>
                    <template v-else>
                      <tr :class="l.kind === 'TOTAL' ? 'border-t-2 border-foreground' : 'border-t border-foreground'" :data-testid="`line-${l.key}`">
                        <td class="py-1.5 pr-2"><b>{{ l.title }}</b></td>
                        <td class="border-l border-border px-2 text-right tabular-nums"><b>{{ amount(l.period.actual) }}</b></td>
                        <td class="px-2 text-right tabular-nums"><b>{{ amount(l.period.budget) }}</b></td>
                        <td class="px-2 text-right tabular-nums" :class="tone(l.period)" :data-testid="`variance-${l.key}`"><b>{{ amount(l.period.variance) }}</b> <small v-if="verdict(l.period)">{{ verdict(l.period) }}</small></td>
                        <td class="px-2 text-right tabular-nums text-muted-foreground">{{ percent(l.period) }}</td>
                        <td class="border-l border-border px-2 text-right tabular-nums"><b>{{ amount(l.ytd.actual) }}</b></td>
                        <td class="px-2 text-right tabular-nums"><b>{{ amount(l.ytd.budget) }}</b></td>
                        <td class="px-2 text-right tabular-nums" :class="tone(l.ytd)"><b>{{ amount(l.ytd.variance) }}</b> <small v-if="verdict(l.ytd)">{{ verdict(l.ytd) }}</small></td>
                        <td class="px-2 text-right tabular-nums text-muted-foreground">{{ percent(l.ytd) }}</td>
                      </tr>
                    </template>
                    <template v-for="a in l.kind === 'GROUP' ? l.accounts : []" :key="a.account_id">
                    <tr class="text-muted-foreground" :data-testid="`account-${a.code}`">
                      <td class="py-1 pl-4 pr-2"><span class="tabular-nums">{{ a.code }}</span> {{ a.name }}</td>
                      <td class="border-l border-border px-2 text-right tabular-nums">{{ bothZero(a.period) ? '' : amount(a.period.actual) }}</td>
                      <td class="px-2 text-right tabular-nums">{{ bothZero(a.period) ? '' : amount(a.period.budget) }}</td>
                      <td class="px-2 text-right tabular-nums" :class="tone(a.period)">{{ bothZero(a.period) ? '' : amount(a.period.variance) }} <small v-if="verdict(a.period)">{{ verdict(a.period) }}</small></td>
                      <td class="px-2 text-right tabular-nums">{{ percent(a.period) }}</td>
                      <td class="border-l border-border px-2 text-right tabular-nums">{{ bothZero(a.ytd) ? '' : amount(a.ytd.actual) }}</td>
                      <td class="px-2 text-right tabular-nums">{{ bothZero(a.ytd) ? '' : amount(a.ytd.budget) }}</td>
                      <td class="px-2 text-right tabular-nums" :class="tone(a.ytd)">{{ bothZero(a.ytd) ? '' : amount(a.ytd.variance) }} <small v-if="verdict(a.ytd)">{{ verdict(a.ytd) }}</small></td>
                      <td class="px-2 text-right tabular-nums">{{ percent(a.ytd) }}</td>
                    </tr>
                    <tr v-for="d in a.departments ?? []" :key="d.department_id ?? 0" class="text-xs italic text-muted-foreground" :data-testid="`account-${a.code}-dept-${d.department_code || 'none'}`">
                      <td class="py-0.5 pl-8 pr-2">{{ d.department_id === null ? t('budget.noDepartment') : `${d.department_code} · ${d.department_name}` }}</td>
                      <td class="border-l border-border px-2 text-right tabular-nums">{{ bothZero(d.period) ? '' : amount(d.period.actual) }}</td>
                      <td class="px-2 text-right tabular-nums">{{ bothZero(d.period) ? '' : amount(d.period.budget) }}</td>
                      <td class="px-2 text-right tabular-nums" :class="tone(d.period)">{{ bothZero(d.period) ? '' : amount(d.period.variance) }} <small v-if="verdict(d.period)">{{ verdict(d.period) }}</small></td>
                      <td class="px-2 text-right tabular-nums">{{ percent(d.period) }}</td>
                      <td class="border-l border-border px-2 text-right tabular-nums">{{ bothZero(d.ytd) ? '' : amount(d.ytd.actual) }}</td>
                      <td class="px-2 text-right tabular-nums">{{ bothZero(d.ytd) ? '' : amount(d.ytd.budget) }}</td>
                      <td class="px-2 text-right tabular-nums" :class="tone(d.ytd)">{{ bothZero(d.ytd) ? '' : amount(d.ytd.variance) }} <small v-if="verdict(d.ytd)">{{ verdict(d.ytd) }}</small></td>
                      <td class="px-2 text-right tabular-nums">{{ percent(d.ytd) }}</td>
                    </tr>
                    </template>
                  </template>
                </template>
              </tbody>
            </table>
          </div>
          <p class="mb-0 mt-3 text-sm text-muted-foreground">{{ t('budget.varianceNote') }}</p>
        </template>
      </CardContent>
    </Card>
    <Card v-if="loaded && report && byDepartment && Array.isArray(byDepartment.departments)" class="mt-4" data-testid="by-department">
      <CardContent class="pt-4">
        <div class="flex flex-wrap items-center justify-between gap-2">
          <h2 class="m-0 text-lg font-semibold">{{ t('budget.byDepartmentTitle') }}</h2>
          <NativeSelect v-model="basis" name="basis" class="w-48" :aria-label="t('budget.basis')" data-testid="basis">
            <option value="period">{{ t('budget.period') }}</option>
            <option value="ytd">{{ t('budget.ytd') }}</option>
          </NativeSelect>
        </div>
        <p class="mb-3 mt-1 text-sm text-muted-foreground">{{ t('budget.byDepartmentHint') }}</p>
        <div class="overflow-x-auto">
          <table class="w-full border-collapse text-sm" data-testid="department-report">
            <thead>
              <tr class="text-xs uppercase tracking-wide text-muted-foreground">
                <th rowspan="2" class="py-2 pr-2 text-left align-bottom">{{ t('budget.department') }}</th>
                <th v-for="g in ['revenue', 'expense', 'profit']" :key="g" colspan="3" class="border-l border-border px-2 py-1 text-center">{{ t(`departments.${g}`) }}</th>
              </tr>
              <tr class="border-b border-border text-xs uppercase tracking-wide text-muted-foreground">
                <template v-for="g in ['revenue', 'expense', 'profit']" :key="g">
                  <th class="border-l border-border px-2 py-1 text-right">{{ t('budget.actual') }}</th>
                  <th class="px-2 py-1 text-right">{{ t('budget.budgetCol') }}</th>
                  <th class="px-2 py-1 text-right">{{ t('budget.variance') }}</th>
                </template>
              </tr>
            </thead>
            <tbody>
              <tr v-for="d in departmentRows" :key="d.id" class="border-b border-border" :class="d.level === 1 ? 'font-semibold' : ''" :data-testid="`dept-${d.code}`">
                <td class="py-1.5 pr-2" :class="d.level === 2 ? 'pl-6' : ''">{{ d.code }} · {{ d.name }}</td>
                <template v-for="g in (['revenue', 'expense', 'profit'] as const)" :key="g">
                  <td class="border-l border-border px-2 text-right tabular-nums">{{ amount(deptCell(d[g]).actual) }}</td>
                  <td class="px-2 text-right tabular-nums">{{ amount(deptCell(d[g]).budget) }}</td>
                  <td class="px-2 text-right tabular-nums" :class="tone(deptCell(d[g]))">{{ amount(deptCell(d[g]).variance) }} <small v-if="verdict(deptCell(d[g]))">{{ verdict(deptCell(d[g])) }}</small></td>
                </template>
              </tr>
              <tr v-if="hasUnassigned" class="border-b border-border italic" data-testid="dept-unassigned">
                <td class="py-1.5 pr-2">{{ t('budget.noDepartment') }}</td>
                <template v-for="g in (['revenue', 'expense', 'profit'] as const)" :key="g">
                  <td class="border-l border-border px-2 text-right tabular-nums">{{ amount(deptCell(byDepartment.unassigned[g]).actual) }}</td>
                  <td class="px-2 text-right tabular-nums">{{ amount(deptCell(byDepartment.unassigned[g]).budget) }}</td>
                  <td class="px-2 text-right tabular-nums" :class="tone(deptCell(byDepartment.unassigned[g]))">{{ amount(deptCell(byDepartment.unassigned[g]).variance) }}</td>
                </template>
              </tr>
            </tbody>
            <tfoot>
              <tr class="border-t-2 border-foreground font-semibold" data-testid="dept-totals">
                <td class="py-1.5 pr-2">{{ t('departments.total') }}</td>
                <template v-for="g in (['revenue', 'expense', 'profit'] as const)" :key="g">
                  <td class="border-l border-border px-2 text-right tabular-nums">{{ amount(deptCell(byDepartment.totals[g]).actual) }}</td>
                  <td class="px-2 text-right tabular-nums">{{ amount(deptCell(byDepartment.totals[g]).budget) }}</td>
                  <td class="px-2 text-right tabular-nums" :class="tone(deptCell(byDepartment.totals[g]))">{{ amount(deptCell(byDepartment.totals[g]).variance) }} <small v-if="verdict(deptCell(byDepartment.totals[g]))">{{ verdict(deptCell(byDepartment.totals[g])) }}</small></td>
                </template>
              </tr>
            </tfoot>
          </table>
        </div>
      </CardContent>
    </Card>
    <Card v-if="loaded && report && statistics" class="mt-4" data-testid="statistics">
      <CardContent class="pt-4">
        <h2 class="m-0 text-lg font-semibold">{{ t('budget.statsReportTitle') }}</h2>
        <p class="mb-3 mt-1 text-sm text-muted-foreground">{{ t('budget.statsReportHint') }} {{ t('budget.statsClosedDays', { n: statistics.closed_days }) }}</p>
        <p v-if="!statistics.has_statistics" class="muted" data-testid="stats-none">{{ t('budget.statsNone') }}</p>
        <template v-else>
          <div class="overflow-x-auto">
            <table class="w-full border-collapse text-sm" data-testid="stats-report">
              <thead>
                <tr class="border-b border-border text-xs uppercase tracking-wide text-muted-foreground">
                  <th class="py-2 pr-2 text-left" />
                  <template v-for="g in ['p', 'y']" :key="g">
                    <th class="border-l border-border px-2 py-1 text-right">{{ g === 'p' ? t('budget.period') : t('budget.ytd') }} · {{ t('budget.actual') }}</th>
                    <th class="px-2 py-1 text-right">{{ t('budget.budgetCol') }}</th>
                    <th class="px-2 py-1 text-right">{{ t('budget.variance') }}</th>
                  </template>
                </tr>
              </thead>
              <tbody>
                <tr v-for="m in statistics.metrics" :key="m.key" class="border-b border-border" :data-testid="`metric-${m.key}`">
                  <th scope="row" class="py-1.5 pr-2 text-left font-medium">{{ t(`budget.metric.${m.key}`) }}</th>
                  <template v-for="c in [m.period, m.ytd]" :key="c === m.period ? 'p' : 'y'">
                    <td class="border-l border-border px-2 text-right tabular-nums">{{ stat(m.unit, c.actual) }}</td>
                    <td class="px-2 text-right tabular-nums">{{ stat(m.unit, c.budget) }}</td>
                    <td class="px-2 text-right tabular-nums" :class="tone(c)">{{ stat(m.unit, c.variance) }} <small v-if="verdict(c)">{{ verdict(c) }}</small></td>
                  </template>
                </tr>
              </tbody>
            </table>
          </div>
          <p v-if="statistics.room_revenue_check.agrees" class="mb-0 mt-3 text-sm text-muted-foreground" data-testid="check-agrees">{{ t('budget.statsCheckAgrees') }}</p>
          <p v-else class="alert mb-0 mt-3" data-testid="check-differs">{{ t('budget.statsCheckDiffers', { money: formatMoney(statistics.room_revenue_check.ytd_money), stats: formatMoney(statistics.room_revenue_check.ytd_statistics) }) }}</p>
        </template>
      </CardContent>
    </Card>
  </template>
</template>
