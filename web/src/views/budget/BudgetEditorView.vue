<script setup lang="ts">
import ErrorNotice from '@/components/app/ErrorNotice.vue'
import { computed, reactive, ref, watch } from 'vue'
import { RouterLink, useRoute, useRouter } from 'vue-router'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { Approval, Budget, BudgetRow, BudgetStatisticsRow, Department } from '@/api/types'
import ApprovalDialog from '@/components/ApprovalDialog.vue'
import DepartmentSelect from '@/components/app/DepartmentSelect.vue'
import FormField from '@/components/app/FormField.vue'
import PageHeader from '@/components/app/PageHeader.vue'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { NativeSelect } from '@/components/ui/native-select'
import { confirm } from '@/composables/useConfirm'
import { loadDepartments } from '@/composables/useDepartments'
import { t } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import { downloadCsv } from '../accounting/reportApi'
import { fromMilli, isAmount, monthLabel, sumAmounts, toMilli } from './budgetMath'

/**
 * One version of a budget. A draft is edited as a grid (an account a row, a month a column) and saved as a whole; an active or archived
 * version is only read, and a revision of it is a copy that becomes a new draft. Making a draft active needs an approval.
 */
const auth = useAuthStore()
const property = usePropertyStore()
const route = useRoute()
const router = useRouter()

interface GridRow { account_id: number; code: string; name: string; account_type: string; department_id: number | null; department_code: string; department_name: string; amounts: string[] }

/** What tells the rows of an account apart: its code, and the code of the department when the row is for one ("4110", "4110-REST"). */
const rowId = (r: Pick<GridRow, 'code' | 'department_code'>): string => (r.department_code ? `${r.code}-${r.department_code}` : r.code)

interface StatCell { available: string; sold: string; adr: string }

const budget = ref<Budget | null>(null)
const stats = ref<StatCell[]>([])
const statsSaved = ref('')
const grid = ref<GridRow[]>([])
const saved = ref('')
const loaded = ref(false)
const error = ref<ApiError | null>(null)
const dialogError = ref<ApiError | null>(null)
const notice = ref('')
const busy = ref(false)
const asking = ref(false)
const addId = ref('')
const addDept = ref<number | null>(null)
const deptList = ref<Department[]>([])
const details = reactive({ name: '', description: '' })
const spread = ref<{ account_id: number; department_id: number | null; label: string; total: string; method: 'EQUAL' | 'LAST_YEAR' } | null>(null)
const fill = reactive({ open: false, percent: '', replace: false })
const imp = reactive({ open: false, csv: '', checked: null as number | null })

const pid = computed(() => property.currentId)
const budgetId = computed(() => Number(route.params.id))
const can = (p: string) => auth.can(p, pid.value)
const editable = computed(() => budget.value?.status === 'DRAFT' && can('budget.manage'))
const months = computed(() => budget.value?.months ?? [])
const dirty = computed(() => JSON.stringify(grid.value) !== saved.value)
const statDirty = computed(() => JSON.stringify(stats.value) !== statsSaved.value)
const showStats = computed(() => editable.value || (budget.value?.statistics?.length ?? 0) > 0)

const isCount = (v: string): boolean => v.trim() === '' || /^\d{1,7}$/.test(v.trim())
const isRate = (v: string): boolean => isAmount(v) && !v.trim().startsWith('-')
function statInvalid(c: StatCell): number {
  let n = [!isCount(c.available), !isCount(c.sold), !isRate(c.adr)].filter(Boolean).length
  if (n === 0 && Number(c.sold || 0) > Number(c.available || 0)) n++
  return n
}
const invalidStats = computed(() => stats.value.reduce((n, c) => n + statInvalid(c), 0))
/** The occupancy a month shows while it is edited: sold over available, as the server will derive it. */
function occupancy(c: StatCell): string {
  const a = Number(c.available || 0)
  return a > 0 && statInvalid(c) === 0 ? `${(Math.round((Number(c.sold || 0) * 10000) / a) / 100).toFixed(2)}%` : ''
}
function roomRevenue(c: StatCell): string {
  const adr = toMilli(c.adr)
  return adr === null || statInvalid(c) > 0 || c.sold.trim() === '' ? '' : fromMilli(adr * BigInt(c.sold.trim()))
}
const daysOf = (start: string): number => {
  const m = /^(\d{4})-(\d{2})-/.exec(start)
  return m ? new Date(Date.UTC(Number(m[1]), Number(m[2]), 0)).getUTCDate() : 0
}
const invalidCells = computed(() => grid.value.reduce((n, r) => n + r.amounts.filter((a) => !isAmount(a)).length, 0))
const accountList = computed(() => budget.value?.available_accounts ?? [])
/** A row is an account and a department: an account may be added again for another department, not twice for the same one. */
const canAdd = computed(() => addId.value !== '' && !grid.value.some((r) => String(r.account_id) === addId.value && (r.department_id ?? 0) === (addDept.value ?? 0)))

const rowTotal = (r: GridRow): string => sumAmounts(r.amounts) ?? ''
function monthTotal(type: 'REVENUE' | 'EXPENSE', m: number): string {
  return sumAmounts(grid.value.filter((r) => r.account_type === type).map((r) => r.amounts[m] ?? '')) ?? ''
}
function yearTotal(type: 'REVENUE' | 'EXPENSE'): string {
  return sumAmounts(grid.value.filter((r) => r.account_type === type).map((r) => rowTotal(r))) ?? ''
}
const resultTotal = computed(() => {
  const rev = toMilli(yearTotal('REVENUE'))
  const exp = toMilli(yearTotal('EXPENSE'))
  return rev === null || exp === null ? '' : fromMilli(rev - exp)
})
function monthResult(m: number): string {
  const rev = toMilli(monthTotal('REVENUE', m))
  const exp = toMilli(monthTotal('EXPENSE', m))
  return rev === null || exp === null ? '' : fromMilli(rev - exp)
}

function applyGrid(b: Budget): void {
  grid.value = (b.rows ?? []).map((r: BudgetRow) => ({
    account_id: r.account_id, code: r.code, name: r.name, account_type: r.account_type, department_id: r.department_id ?? null, department_code: r.department_code ?? '', department_name: r.department_name ?? '',
    amounts: [...r.amounts],
  }))
  saved.value = JSON.stringify(grid.value)
}

function applyStats(b: Budget): void {
  const by = new Map((b.statistics ?? []).map((r: BudgetStatisticsRow) => [r.month, r]))
  stats.value = (b.months ?? []).map((m) => {
    const r = by.get(m.number)
    return r ? { available: String(r.rooms_available), sold: String(r.rooms_sold), adr: r.adr } : { available: '', sold: '', adr: '' }
  })
  statsSaved.value = JSON.stringify(stats.value)
}

function apply(b: Budget): void {
  budget.value = b
  applyGrid(b)
  applyStats(b)
  details.name = b.name
  details.description = b.description ?? ''
}

async function load(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null || !can('budget.view')) return
  try {
    const { data } = await api.GET('/api/v1/properties/{propertyId}/budgets/{id}', { params: { path: { propertyId, id: budgetId.value } } })
    if (data) apply(data)
    deptList.value = await loadDepartments(propertyId)
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    loaded.value = true
  }
}

/** Runs an action that talks to the server, showing what it answers; `false` when it was refused. */
async function run(action: () => Promise<void>, onDialog = false): Promise<boolean> {
  busy.value = true
  error.value = null
  dialogError.value = null
  notice.value = ''
  try {
    await action()
    return true
  } catch (e) {
    if (onDialog) dialogError.value = e instanceof ApiError ? e : null
    else error.value = e instanceof ApiError ? e : null
    return false
  } finally {
    busy.value = false
  }
}

async function saveGrid(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null || budget.value === null) return
  const rows = grid.value.map((r) => ({ account_id: r.account_id, department_id: r.department_id ?? undefined, amounts: r.amounts.map((a) => a.trim()) }))
  const { data } = await api.PUT('/api/v1/properties/{propertyId}/budgets/{id}/grid', { params: { path: { propertyId, id: budget.value.id } }, body: { rows } })
  if (data) {
    budget.value = data
    applyGrid(data) // what is typed in the statistics stays
  }
}

async function saveStatistics(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null || budget.value === null) return
  const rows = stats.value
    .map((c, i) => ({ month: i + 1, c }))
    .filter(({ c }) => c.available.trim() !== '' || c.sold.trim() !== '' || c.adr.trim() !== '')
    .map(({ month, c }) => ({ month, rooms_available: Number(c.available || 0), rooms_sold: Number(c.sold || 0), adr: c.adr.trim() || '0' }))
  const { data } = await api.PUT('/api/v1/properties/{propertyId}/budgets/{id}/statistics', { params: { path: { propertyId, id: budget.value.id } }, body: { rows } })
  if (data) {
    budget.value = data
    applyStats(data) // what is typed in the grid stays
  }
}

const saveStats = () => run(async () => {
  await saveStatistics()
  notice.value = t('budget.statsSaved')
})

function suggestRooms(): void {
  const count = budget.value?.room_count ?? 0
  stats.value.forEach((c, i) => {
    if (c.available.trim() === '') c.available = String(count * daysOf(months.value[i]?.start ?? ''))
  })
}

const save = () => run(async () => {
  await saveGrid()
  notice.value = t('budget.savedGrid')
})

/** What an action works on is on the server: unsaved figures are saved first, and the action stops if they are refused. */
async function saveIfDirty(): Promise<void> {
  if (dirty.value) await saveGrid()
  if (statDirty.value) await saveStatistics()
}

async function saveDetails(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null || budget.value === null) return
  await run(async () => {
    const { data } = await api.PATCH('/api/v1/properties/{propertyId}/budgets/{id}', {
      params: { path: { propertyId, id: budget.value!.id } }, body: { name: details.name.trim(), description: details.description.trim() },
    })
    if (data) {
      budget.value = { ...budget.value!, name: data.name, description: data.description }
      notice.value = t('budget.savedDetails')
    }
  })
}

function addRow(): void {
  const a = accountList.value.find((x) => String(x.id) === addId.value)
  if (!a || !canAdd.value) return
  const d = deptList.value.find((x) => x.id === addDept.value)
  grid.value.push({
    account_id: a.id, code: a.code, name: a.name, account_type: a.account_type, department_id: addDept.value, department_code: d?.code ?? '', department_name: d?.name ?? '', amounts: months.value.map(() => ''),
  })
  grid.value.sort((x, y) => x.code.localeCompare(y.code) || x.department_code.localeCompare(y.department_code))
  addId.value = ''
}

function removeRow(r: GridRow): void {
  grid.value = grid.value.filter((x) => x !== r)
  if (spread.value?.account_id === r.account_id && spread.value.department_id === r.department_id) spread.value = null
}

function openSpread(r: GridRow): void {
  spread.value = { account_id: r.account_id, department_id: r.department_id, label: `${r.code} ${r.name}${r.department_code ? ` · ${r.department_code}` : ''}`, total: rowTotal(r) === '0' ? '' : rowTotal(r), method: 'EQUAL' }
}

async function applySpread(): Promise<void> {
  const propertyId = pid.value
  const s = spread.value
  if (propertyId === null || budget.value === null || s === null) return
  const ok = await run(async () => {
    await saveIfDirty()
    const { data } = await api.POST('/api/v1/properties/{propertyId}/budgets/{id}/spread', {
      params: { path: { propertyId, id: budget.value!.id } }, body: { account_id: s.account_id, department_id: s.department_id ?? undefined, total: s.total.trim(), method: s.method },
    })
    if (data) apply(data)
    notice.value = t('budget.spreadDone', { account: s.label })
  })
  if (ok) spread.value = null
}

async function applyFill(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null || budget.value === null) return
  const ok = await run(async () => {
    await saveIfDirty()
    const { data } = await api.POST('/api/v1/properties/{propertyId}/budgets/{id}/fill-from-actuals', {
      params: { path: { propertyId, id: budget.value!.id } }, body: { percent_change: fill.percent.trim(), replace: fill.replace },
    })
    if (data) apply(data)
    notice.value = t('budget.filled')
  })
  if (ok) fill.open = false
}

function readFile(event: Event): void {
  const file = (event.target as HTMLInputElement).files?.[0]
  if (!file) return
  const reader = new FileReader()
  reader.onload = () => {
    imp.csv = String(reader.result ?? '')
    imp.checked = null
  }
  reader.readAsText(file)
}

async function importCsv(dryRun: boolean): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null || budget.value === null) return
  const ok = await run(async () => {
    const { data } = await api.POST('/api/v1/properties/{propertyId}/budgets/{id}/import', {
      params: { path: { propertyId, id: budget.value!.id } }, body: { csv: imp.csv, dry_run: dryRun },
    })
    if (dryRun) {
      imp.checked = data?.accounts ?? 0
      return
    }
    await load()
    notice.value = t('budget.imported', { n: data?.accounts ?? 0 })
  })
  if (!ok) imp.checked = null
  else if (!dryRun) {
    imp.open = false
    imp.csv = ''
    imp.checked = null
  }
}

async function exportCsv(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null || budget.value === null) return
  const b = budget.value
  await run(() => downloadCsv('/api/v1/properties/{propertyId}/budgets/{id}/export', { path: { propertyId, id: b.id } }, `budget-${b.year_label}-v${b.version}.csv`))
}

async function remove(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null || budget.value === null) return
  if (!(await confirm({ title: t('budget.deleteTitle'), description: t('budget.deleteHint', { name: budget.value.name }), destructive: true }))) return
  const id = budget.value.id
  const ok = await run(async () => {
    await api.DELETE('/api/v1/properties/{propertyId}/budgets/{id}', { params: { path: { propertyId, id } } })
  })
  if (ok) await router.push('/budget')
}

async function revise(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null || budget.value === null) return
  const b = budget.value
  let created: Budget | undefined
  const ok = await run(async () => {
    const res = await api.POST('/api/v1/properties/{propertyId}/budgets', {
      params: { path: { propertyId } }, body: { name: t('budget.revisionName', { name: b.name.slice(0, 80) }), copy_from_id: b.id },
    })
    created = res.data
  })
  if (ok && created) await router.push(`/budget/${created.id}`)
}

async function askApproval(): Promise<void> {
  if (!(await run(saveIfDirty))) return
  dialogError.value = null
  asking.value = true
}

async function activate(approval: Approval): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null || budget.value === null) return
  const id = budget.value.id
  const ok = await run(async () => {
    const { data } = await api.POST('/api/v1/properties/{propertyId}/budgets/{id}/activate', { params: { path: { propertyId, id } }, body: { approval } })
    if (data) apply(data)
    notice.value = t('budget.activated', { year: data?.year_label ?? '', version: data?.version ?? '' })
  }, true)
  if (ok) asking.value = false
}

watch([() => pid.value, budgetId], () => {
  budget.value = null
  loaded.value = false
  asking.value = false
  spread.value = null
  void load()
}, { immediate: true })
</script>

<template>
  <PageHeader :title="budget ? t('budget.versionLabel', { year: budget.year_label, version: budget.version, name: budget.name }) : t('budget.title')">
    <template v-if="budget" #marks>
      <Badge :variant="budget.status === 'ACTIVE' ? 'success' : budget.status === 'DRAFT' ? 'secondary' : 'outline'" :data-status="budget.status" data-testid="status">{{ t(`budget.status.${budget.status}`) }}</Badge>
    </template>
    <template #actions>
      <RouterLink to="/budget" class="text-sm text-primary hover:underline" data-testid="back">{{ t('budget.backToList') }}</RouterLink>
      <template v-if="budget">
        <Button type="button" variant="outline" :disabled="busy" data-testid="export" @click="exportCsv">{{ t('budget.export') }}</Button>
        <Button v-if="can('budget.manage') && budget.status !== 'DRAFT'" type="button" :disabled="busy" data-testid="revise" @click="revise">{{ t('budget.revise') }}</Button>
      </template>
    </template>
  </PageHeader>
  <ErrorNotice v-if="error" :error="error" inline data-testid="budget-error">
<template v-for="(f, i) in error.fieldErrors ?? []" :key="i"><br /><span class="muted">{{ f.field }}: {{ f.message }}</span></template>
</ErrorNotice>
  <p v-if="notice" class="notice" role="status" data-testid="notice">{{ notice }}</p>
  <p v-if="pid === null" class="muted">{{ t('setup.selectProperty') }}</p>
  <p v-else-if="!can('budget.view')" class="muted" data-testid="no-access">{{ t('budget.noAccess', { permission: 'budget.view' }) }}</p>
  <template v-else-if="budget">
    <p v-if="budget.status === 'ACTIVE'" class="mb-4 text-sm text-muted-foreground" data-testid="active-note">{{ t('budget.activeNote') }}</p>
    <p v-else-if="budget.status === 'ARCHIVED'" class="mb-4 text-sm text-muted-foreground" data-testid="archived-note">{{ t('budget.archivedNote') }}</p>

    <Card v-if="editable" class="mb-4">
      <form novalidate data-testid="details-form" @submit.prevent="saveDetails">
        <CardContent class="flex flex-wrap items-end gap-4 pt-4">
          <FormField class="min-w-64 flex-1" :label="t('budget.name')" required>
            <template #default="{ id }"><Input :id="id" v-model="details.name" name="name" maxlength="100" /></template>
          </FormField>
          <FormField class="min-w-64 flex-1" :label="t('budget.description')">
            <template #default="{ id }"><Input :id="id" v-model="details.description" name="description" maxlength="500" /></template>
          </FormField>
          <Button type="submit" variant="outline" :disabled="busy || !details.name.trim()" data-testid="save-details">{{ t('budget.saveDetails') }}</Button>
        </CardContent>
      </form>
    </Card>

    <Card v-if="editable && spread" class="mb-4">
      <form novalidate data-testid="spread-form" @submit.prevent="applySpread">
        <CardHeader><CardTitle>{{ t('budget.spreadTitle', { account: spread.label }) }}</CardTitle></CardHeader>
        <CardContent class="flex flex-wrap items-end gap-4">
          <FormField :label="t('budget.spreadTotal')" :hint="t('budget.spreadHint')">
            <template #default="{ id }"><Input :id="id" v-model="spread.total" name="spread_total" inputmode="decimal" /></template>
          </FormField>
          <FormField :label="t('budget.spreadMethod')">
            <template #default="{ id }">
              <NativeSelect :id="id" v-model="spread.method" name="spread_method">
                <option value="EQUAL">{{ t('budget.spreadEqual') }}</option>
                <option value="LAST_YEAR">{{ t('budget.spreadLastYear') }}</option>
              </NativeSelect>
            </template>
          </FormField>
          <Button type="button" variant="outline" @click="spread = null">{{ t('common.cancel') }}</Button>
          <Button type="submit" :disabled="busy || !spread.total.trim() || !isAmount(spread.total)" data-testid="spread-apply">{{ t('budget.spreadApply') }}</Button>
        </CardContent>
      </form>
    </Card>

    <Card v-if="editable && fill.open" class="mb-4">
      <form novalidate data-testid="fill-form" @submit.prevent="applyFill">
        <CardHeader>
          <CardTitle>{{ t('budget.fillTitle') }}</CardTitle>
          <p class="m-0 text-sm text-muted-foreground">{{ t('budget.fillHint') }}</p>
        </CardHeader>
        <CardContent class="flex flex-wrap items-end gap-4">
          <FormField :label="t('budget.fillPercent')" :hint="t('budget.fillPercentHint')">
            <template #default="{ id }"><Input :id="id" v-model="fill.percent" name="percent_change" inputmode="decimal" /></template>
          </FormField>
          <label class="flex items-center gap-2 pb-2 text-sm"><input v-model="fill.replace" type="checkbox" name="replace" />{{ t('budget.fillReplace') }}</label>
          <Button type="button" variant="outline" @click="fill.open = false">{{ t('common.cancel') }}</Button>
          <Button type="submit" :disabled="busy" data-testid="fill-apply">{{ t('budget.fillApply') }}</Button>
        </CardContent>
      </form>
    </Card>

    <Card v-if="editable && imp.open" class="mb-4">
      <form novalidate data-testid="import-form" @submit.prevent="importCsv(false)">
        <CardHeader>
          <CardTitle>{{ t('budget.importTitle') }}</CardTitle>
          <p class="m-0 text-sm text-muted-foreground">{{ t('budget.importHint') }}</p>
        </CardHeader>
        <CardContent class="flex flex-col gap-3">
          <input type="file" accept=".csv,text/csv" name="file" :aria-label="t('budget.importFile')" data-testid="import-file" @change="readFile" />
          <textarea v-model="imp.csv" name="csv" rows="6" class="w-full rounded-md border border-border bg-card p-2 font-mono text-xs" :aria-label="t('budget.importText')" @input="imp.checked = null" />
          <p v-if="imp.checked !== null" class="notice m-0" data-testid="import-checked">{{ t('budget.importChecked', { n: imp.checked }) }}</p>
          <div class="flex justify-end gap-2">
            <Button type="button" variant="outline" @click="imp.open = false">{{ t('common.cancel') }}</Button>
            <Button type="button" variant="outline" :disabled="busy || !imp.csv.trim()" data-testid="import-check" @click="importCsv(true)">{{ t('budget.importCheck') }}</Button>
            <Button type="submit" :disabled="busy || imp.checked === null" data-testid="import-apply">{{ t('budget.importApply') }}</Button>
          </div>
        </CardContent>
      </form>
    </Card>

    <Card>
      <CardHeader class="flex-row flex-wrap items-center justify-between gap-2">
        <CardTitle>{{ t('budget.gridTitle') }}</CardTitle>
        <div v-if="editable" class="flex flex-wrap items-center gap-2">
          <NativeSelect v-model="addId" name="add_account" class="w-64" :aria-label="t('budget.chooseAccount')" data-testid="add-account">
            <option value="">{{ t('budget.chooseAccount') }}</option>
            <option v-for="a in accountList" :key="a.id" :value="String(a.id)">{{ a.code }} {{ a.name }}</option>
          </NativeSelect>
          <DepartmentSelect v-model="addDept" name="add_department" class="w-56" :aria-label="t('budget.department')" />
          <Button type="button" variant="outline" size="sm" :disabled="!canAdd" data-testid="add-row" @click="addRow">{{ t('budget.addAccount') }}</Button>
          <Button type="button" variant="outline" size="sm" data-testid="fill-open" @click="fill.open = !fill.open">{{ t('budget.fillTitle') }}</Button>
          <Button type="button" variant="outline" size="sm" data-testid="import-open" @click="imp.open = !imp.open">{{ t('budget.importTitle') }}</Button>
        </div>
      </CardHeader>
      <CardContent>
        <p v-if="!grid.length" class="muted" data-testid="empty">{{ editable ? t('budget.gridEmptyEdit') : t('budget.gridEmpty') }}</p>
        <div v-else class="overflow-x-auto">
          <table class="w-full border-collapse text-sm" data-testid="grid">
            <thead>
              <tr class="border-b border-border text-left text-xs uppercase tracking-wide text-muted-foreground">
                <th class="sticky left-0 z-10 min-w-56 bg-card py-2 pr-2">{{ t('budget.account') }}</th>
                <th v-for="m in months" :key="m.number" class="px-1 py-2 text-right">{{ monthLabel(m.start) }}</th>
                <th class="px-2 py-2 text-right">{{ t('budget.total') }}</th>
                <th v-if="editable" class="py-2"><span class="sr-only">{{ t('budget.actions') }}</span></th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="r in grid" :key="`${r.account_id}-${r.department_id ?? 0}`" class="border-b border-border" :data-testid="`row-${rowId(r)}`">
                <th scope="row" class="sticky left-0 z-10 bg-card py-1 pr-2 text-left font-normal">
                  <span class="tabular-nums text-muted-foreground">{{ r.code }}</span> {{ r.name }}<small v-if="r.department_code" class="ml-1 rounded bg-muted px-1 text-muted-foreground" :title="r.department_name" :data-testid="`department-${rowId(r)}`">{{ r.department_code }}</small>
                </th>
                <td v-for="(a, m) in r.amounts" :key="m" class="px-1 py-1 text-right tabular-nums">
                  <Input
                    v-if="editable" v-model="r.amounts[m]" :name="`a-${rowId(r)}-${m + 1}`" inputmode="decimal" class="h-8 w-28 text-right tabular-nums"
                    :aria-invalid="!isAmount(a)" :aria-label="`${rowId(r)} ${monthLabel(months[m]?.start ?? '')}`"
                  />
                  <template v-else>{{ $money(a) }}</template>
                </td>
                <td class="px-2 py-1 text-right font-medium tabular-nums" :data-testid="`total-${rowId(r)}`">{{ $money(rowTotal(r)) }}</td>
                <td v-if="editable" class="whitespace-nowrap py-1 pl-2 text-right">
                  <Button type="button" variant="ghost" size="sm" :data-testid="`spread-${rowId(r)}`" @click="openSpread(r)">{{ t('budget.spread') }}</Button>
                  <Button type="button" variant="ghost" size="sm" :data-testid="`remove-${rowId(r)}`" :aria-label="t('budget.remove', { account: rowId(r) })" @click="removeRow(r)">×</Button>
                </td>
              </tr>
            </tbody>
            <tfoot>
              <tr v-for="row in [{ key: 'REVENUE', label: t('budget.revenue') }, { key: 'EXPENSE', label: t('budget.expense') }] as const" :key="row.key" class="border-t border-border" :data-testid="`sum-${row.key}`">
                <th scope="row" class="sticky left-0 z-10 bg-card py-1.5 pr-2 text-left">{{ row.label }}</th>
                <td v-for="(_, m) in months" :key="m" class="px-1 py-1.5 text-right tabular-nums">{{ $money(monthTotal(row.key, m)) }}</td>
                <td class="px-2 py-1.5 text-right font-semibold tabular-nums">{{ $money(yearTotal(row.key)) }}</td>
                <td v-if="editable" />
              </tr>
              <tr class="border-t-2 border-foreground" data-testid="sum-RESULT">
                <th scope="row" class="sticky left-0 z-10 bg-card py-1.5 pr-2 text-left">{{ t('budget.result') }}</th>
                <td v-for="(_, m) in months" :key="m" class="px-1 py-1.5 text-right tabular-nums">{{ $money(monthResult(m)) }}</td>
                <td class="px-2 py-1.5 text-right font-semibold tabular-nums">{{ $money(resultTotal) }}</td>
                <td v-if="editable" />
              </tr>
            </tfoot>
          </table>
        </div>
        <p v-if="invalidCells" class="alert mt-3" role="alert" data-testid="invalid">{{ t('budget.invalidCells', { n: invalidCells }) }}</p>
        <div v-if="editable" class="mt-4 flex flex-wrap items-center justify-between gap-2">
          <Button type="button" variant="outline" class="text-destructive" :disabled="busy" data-testid="delete" @click="remove">{{ t('budget.delete') }}</Button>
          <div class="flex flex-wrap items-center gap-2">
            <span v-if="dirty" class="text-sm text-muted-foreground" data-testid="unsaved">{{ t('budget.unsaved') }}</span>
            <Button type="button" variant="outline" :disabled="busy || !dirty || invalidCells > 0" data-testid="save" @click="save">{{ t('budget.saveGrid') }}</Button>
            <Button type="button" :disabled="busy || invalidCells > 0 || !grid.length" data-testid="activate" @click="askApproval">{{ t('budget.activate') }}</Button>
          </div>
        </div>
      </CardContent>
    </Card>

    <Card v-if="showStats" class="mt-4" data-testid="statistics">
      <CardHeader>
        <CardTitle>{{ t('budget.statsTitle') }}</CardTitle>
        <p class="m-0 text-sm text-muted-foreground">{{ t('budget.statsHint') }}</p>
      </CardHeader>
      <CardContent>
        <div class="overflow-x-auto">
          <table class="w-full border-collapse text-sm" data-testid="stats-grid">
            <thead>
              <tr class="border-b border-border text-left text-xs uppercase tracking-wide text-muted-foreground">
                <th class="sticky left-0 z-10 min-w-48 bg-card py-2 pr-2" />
                <th v-for="m in months" :key="m.number" class="px-1 py-2 text-right">{{ monthLabel(m.start) }}</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="f in [{ key: 'available', label: t('budget.statsAvailable') }, { key: 'sold', label: t('budget.statsSold') }, { key: 'adr', label: t('budget.statsAdr') }] as const" :key="f.key" class="border-b border-border" :data-testid="`stats-${f.key}`">
                <th scope="row" class="sticky left-0 z-10 bg-card py-1 pr-2 text-left font-normal">{{ f.label }}</th>
                <td v-for="(c, i) in stats" :key="i" class="px-1 py-1 text-right tabular-nums">
                  <Input
                    v-if="editable" v-model="c[f.key]" :name="`s-${f.key}-${i + 1}`" inputmode="decimal" class="h-8 w-24 text-right tabular-nums"
                    :aria-invalid="statInvalid(c) > 0" :aria-label="`${f.label} ${monthLabel(months[i]?.start ?? '')}`"
                  />
                  <template v-else>{{ c[f.key] === '' ? '' : f.key === 'adr' ? $money(c[f.key]) : c[f.key] }}</template>
                </td>
              </tr>
              <tr class="border-b border-border text-muted-foreground" data-testid="stats-occupancy">
                <th scope="row" class="sticky left-0 z-10 bg-card py-1.5 pr-2 text-left font-normal">{{ t('budget.statsOccupancy') }}</th>
                <td v-for="(c, i) in stats" :key="i" class="px-1 py-1.5 text-right tabular-nums">{{ occupancy(c) }}</td>
              </tr>
              <tr class="text-muted-foreground" data-testid="stats-revenue">
                <th scope="row" class="sticky left-0 z-10 bg-card py-1.5 pr-2 text-left font-normal">{{ t('budget.statsRevenue') }}</th>
                <td v-for="(c, i) in stats" :key="i" class="px-1 py-1.5 text-right tabular-nums">{{ $money(roomRevenue(c)) }}</td>
              </tr>
            </tbody>
          </table>
        </div>
        <p v-if="invalidStats" class="alert mt-3" role="alert" data-testid="stats-invalid">{{ t('budget.statsInvalid', { n: invalidStats }) }}</p>
        <div v-if="editable" class="mt-4 flex flex-wrap items-center justify-end gap-2">
          <Button type="button" variant="outline" :title="t('budget.statsSuggestHint')" data-testid="stats-suggest" @click="suggestRooms">{{ t('budget.statsSuggest') }}</Button>
          <span v-if="statDirty" class="text-sm text-muted-foreground" data-testid="stats-unsaved">{{ t('budget.unsaved') }}</span>
          <Button type="button" :disabled="busy || !statDirty || invalidStats > 0" data-testid="stats-save" @click="saveStats">{{ t('budget.statsSave') }}</Button>
        </div>
      </CardContent>
    </Card>
  </template>
  <ApprovalDialog v-if="asking" :title="t('budget.approveActivate')" :message="t('budget.approveActivateHint')" :busy="busy" :error="dialogError" @approve="activate" @cancel="asking = false; dialogError = null" />
</template>
