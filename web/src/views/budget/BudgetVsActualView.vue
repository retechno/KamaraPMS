<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { RouterLink } from 'vue-router'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { Budget, BudgetCell, BudgetVsActual } from '@/api/types'
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
const budgets = ref<Budget[]>([])
const loaded = ref(false)
const error = ref<ApiError | null>(null)
const busy = ref(false)
const form = reactive({ year_start: '', from: '', to: '', budget_id: '' })

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
  } catch (e) {
    report.value = null
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
const tone = (c: BudgetCell): string => (c.favourable === null ? '' : c.favourable ? 'text-success' : 'text-destructive')
const amount = (v: string): string => (Number(v) === 0 ? '–' : formatMoney(v))
const percent = (c: BudgetCell): string => (c.variance_percent === null ? '' : `${formatMoney(c.variance_percent)}%`)
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
  <p v-if="error" class="alert" role="alert" data-testid="report-error">
    {{ error.message }} <code>{{ error.code }}</code>
    <template v-for="(f, i) in error.fieldErrors ?? []" :key="i"><br /><span class="muted">{{ f.field }}: {{ f.message }}</span></template>
  </p>
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
                    <tr v-for="a in l.kind === 'GROUP' ? l.accounts : []" :key="a.account_id" class="text-muted-foreground" :data-testid="`account-${a.code}`">
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
                  </template>
                </template>
              </tbody>
            </table>
          </div>
          <p class="mb-0 mt-3 text-sm text-muted-foreground">{{ t('budget.varianceNote') }}</p>
        </template>
      </CardContent>
    </Card>
  </template>
</template>
