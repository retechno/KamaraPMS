<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { RouterLink, useRouter } from 'vue-router'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { AssetCategory, AssetLocation, AssetOpeningImport, AssetOpeningResult, AssetOpeningRow, Department } from '@/api/types'
import ErrorNotice from '@/components/app/ErrorNotice.vue'
import FormField from '@/components/app/FormField.vue'
import PageHeader from '@/components/app/PageHeader.vue'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { NativeSelect } from '@/components/ui/native-select'
import { useAssetAccess } from '@/composables/useAssetAccess'
import { loadDepartments } from '@/composables/useDepartments'
import { toast } from '@/composables/useToast'
import { t, te } from '@/i18n'
import { formatDate, formatMoney, formatMonth } from '@/utils/format'

/**
 * The opening balances of the register (docs/architecture/37-fixed-assets.md 4.4, mockup 55): old assets from a file or one by one, with the dry run before anything is written, the mode (with a
 * journal against 3900, or the register only, tied to the ledger), the fiscal columns, and the list of the imports made.
 */
const router = useRouter()
const { pid, canRead, canSetup } = useAssetAccess()
const TEMPLATE = [
  'name', 'category', 'location', 'department', 'supplier', 'acquisition_date', 'in_service_date', 'cost', 'residual_value', 'life_months', 'method', 'accumulated_depreciation', 'months_done',
  'unit_count', 'serial_no', 'warranty_until', 'start_rule', 'fiscal_group', 'fiscal_method', 'fiscal_accumulated_depreciation', 'fiscal_months_done',
]
const SAMPLE = 'Lemari kayu kamar,FF&E,,,,2020-01-10,2020-01-10,36000000,0,96,SL,30375000,81,1,,,,G1,SL,36000000,48'

const categories = ref<AssetCategory[]>([])
const locations = ref<AssetLocation[]>([])
const departments = ref<Department[]>([])
const imports = ref<AssetOpeningImport[]>([])
const error = ref<ApiError | null>(null)
const busy = ref(false)

const as_of = ref('')
const journal_date = ref('')
const mode = ref<'WITH_JOURNAL' | 'REGISTER_ONLY'>('WITH_JOURNAL')
const csv = ref('')
const single = ref(false)
const row = reactive({
  name: '', category: '', location: '', department: '', acquisition_date: '', in_service_date: '', cost: '', residual_value: '', life_months: '', method: '', accumulated_depreciation: '', months_done: '', unit_count: '', serial_no: '', warranty_until: '', start_rule: '',
  fiscal_group: '', fiscal_method: '', fiscal_accumulated_depreciation: '', fiscal_months_done: '',
})
const result = ref<AssetOpeningResult | null>(null)
const resultFrom = ref<'csv' | 'single'>('csv')
let key = crypto.randomUUID()

async function loadAll(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null || !canRead.value) return
  try {
    const [c, l, i] = await Promise.all([
      api.GET('/api/v1/properties/{propertyId}/assets/categories', { params: { path: { propertyId } } }),
      api.GET('/api/v1/properties/{propertyId}/assets/locations', { params: { path: { propertyId } } }),
      api.GET('/api/v1/properties/{propertyId}/assets/opening/imports', { params: { path: { propertyId } } }),
    ])
    categories.value = c.data?.data ?? []
    locations.value = l.data?.data ?? []
    imports.value = i.data?.data ?? []
    departments.value = await loadDepartments(propertyId)
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  }
}
watch(pid, loadAll, { immediate: true })

function onFile(e: Event): void {
  const f = (e.target as HTMLInputElement).files?.[0]
  if (!f) return
  void f.text().then((text) => { csv.value = text; result.value = null })
}

function templateDownload(): void {
  const text = `${TEMPLATE.join(',')}\n${SAMPLE}\n`
  const url = URL.createObjectURL(new Blob([text], { type: 'text/csv' }))
  const a = document.createElement('a')
  a.href = url
  a.download = 'aset-lama.csv'
  document.body.appendChild(a)
  a.click()
  a.remove()
  window.setTimeout(() => URL.revokeObjectURL(url), 60 * 1000)
}

function rowBody(): AssetOpeningRow {
  const r = row
  const num = (v: string): number | undefined => (v.trim() === '' ? undefined : Number(v))
  const str = (v: string): string | undefined => (v.trim() === '' ? undefined : v.trim())
  return {
    name: r.name.trim(), category: r.category, location: str(r.location), department: str(r.department), acquisition_date: str(r.acquisition_date) ?? null, in_service_date: str(r.in_service_date) ?? null,
    cost: r.cost.trim(), residual_value: str(r.residual_value) ?? null, life_months: num(r.life_months) ?? null, method: str(r.method), accumulated_depreciation: str(r.accumulated_depreciation) ?? null,
    months_done: num(r.months_done) ?? null, unit_count: num(r.unit_count) ?? null, serial_no: str(r.serial_no), warranty_until: str(r.warranty_until) ?? null, start_rule: str(r.start_rule),
    fiscal_group: str(r.fiscal_group), fiscal_method: str(r.fiscal_method), fiscal_accumulated_depreciation: str(r.fiscal_accumulated_depreciation) ?? null, fiscal_months_done: num(r.fiscal_months_done) ?? null,
  }
}

const ready = computed(() => as_of.value !== '' && (single.value ? row.name.trim() !== '' && row.category !== '' && row.cost.trim() !== '' : csv.value.trim() !== ''))

async function run(dry: boolean): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null) return
  busy.value = true
  error.value = null
  const common = { as_of: as_of.value, journal_date: journal_date.value || null, post_journal: mode.value === 'WITH_JOURNAL', dry_run: dry }
  try {
    const header = dry ? {} : { 'Idempotency-Key': key }
    const res = single.value
      ? await api.POST('/api/v1/properties/{propertyId}/assets/opening/single', { params: { path: { propertyId }, query: { dry_run: dry }, header } as never, body: { ...common, row: rowBody() } })
      : await api.POST('/api/v1/properties/{propertyId}/assets/opening/import', { params: { path: { propertyId }, query: { dry_run: dry }, header } as never, body: { ...common, csv: csv.value } })
    result.value = res.data ?? null
    resultFrom.value = single.value ? 'single' : 'csv'
    if (!dry && res.data) {
      key = crypto.randomUUID()
      toast.success(t('assets.opening.done', { number: res.data.number ?? '', n: res.data.rows.length }))
      await loadAll()
      await router.push('/assets')
    }
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    busy.value = false
  }
}

// a change of the input is a new request
watch([as_of, journal_date, mode, csv, single, () => ({ ...row })], () => { result.value = null; key = crypto.randomUUID() })

/** A finding of the import in the language of the page: by its field and code, then by its code, then as an error of the API. The figures it is made of come from the server. */
const finding = (f: { field?: string; code: string; message: string; params?: Record<string, string> }): string => {
  const params: Record<string, string> = { ...(f.params ?? {}) }
  if (params.month) params.month = formatMonth(params.month)
  for (const key of [`assets.findings.${f.field ?? ''}.${f.code}`, `assets.findings.${f.code}`, `errors.${f.code}`, `fieldErrors.${f.code}`]) if (te(key)) return t(key, params)
  return f.message
}
const canImport = computed(() => !!result.value && result.value.valid && result.value.dry_run && result.value.rows.length > 0)
</script>

<template>
  <RouterLink to="/assets" class="text-sm text-primary hover:underline">← {{ t('assets.register.title') }}</RouterLink>
  <PageHeader :title="t('assets.opening.title')" :description="t('assets.opening.intro')">
    <template #actions>
      <Button variant="outline" data-testid="template-download" @click="templateDownload">{{ t('assets.opening.template') }}</Button>
      <Button variant="outline" :aria-pressed="single" data-testid="toggle-single" @click="single = !single">{{ single ? t('assets.opening.useFile') : t('assets.opening.addOne') }}</Button>
    </template>
  </PageHeader>
  <ErrorNotice v-if="error" :error="error" inline />
  <p v-if="pid === null" class="muted">{{ t('setup.selectProperty') }}</p>
  <p v-else-if="!canRead" class="muted" data-testid="no-access">{{ t('assets.noAccess') }}</p>
  <p v-else-if="!canSetup" class="muted" data-testid="no-setup">{{ t('assets.opening.needSetup') }}</p>
  <template v-else>
    <Card class="flex flex-col gap-4 p-4" data-testid="opening-form">
      <div class="grid gap-3 sm:grid-cols-3">
        <FormField :label="t('assets.opening.asOf')" :hint="t('assets.opening.asOfHint')" required><template #default="{ id }"><Input :id="id" v-model="as_of" name="as_of" type="date" /></template></FormField>
        <FormField :label="t('assets.opening.journalDate')" :hint="t('assets.opening.journalDateHint')"><template #default="{ id }"><Input :id="id" v-model="journal_date" name="journal_date" type="date" :disabled="mode === 'REGISTER_ONLY'" /></template></FormField>
      </div>
      <fieldset class="m-0 grid gap-2 border-0 p-0 sm:grid-cols-2" :aria-label="t('assets.opening.mode')">
        <label class="flex cursor-pointer gap-2 rounded-lg border border-border p-3 text-sm" :class="mode === 'WITH_JOURNAL' ? 'border-primary bg-primary/5' : ''">
          <input v-model="mode" type="radio" name="mode" value="WITH_JOURNAL" class="mt-1 accent-primary" /><span><strong>{{ t('assets.opening.withJournal') }}</strong><br /><span class="text-xs text-muted-foreground">{{ t('assets.opening.withJournalHint') }}</span></span>
        </label>
        <label class="flex cursor-pointer gap-2 rounded-lg border border-border p-3 text-sm" :class="mode === 'REGISTER_ONLY' ? 'border-primary bg-primary/5' : ''">
          <input v-model="mode" type="radio" name="mode" value="REGISTER_ONLY" class="mt-1 accent-primary" /><span><strong>{{ t('assets.opening.registerOnly') }}</strong><br /><span class="text-xs text-muted-foreground">{{ t('assets.opening.registerOnlyHint') }}</span></span>
        </label>
      </fieldset>

      <template v-if="!single">
        <FormField :label="t('assets.opening.file')" :hint="t('assets.opening.fileHint')"><template #default="{ id }"><input :id="id" type="file" accept=".csv,text/csv,text/plain" class="text-sm" data-testid="csv-file" @change="onFile" /></template></FormField>
        <FormField :label="t('assets.opening.paste')"><template #default="{ id }"><textarea :id="id" v-model="csv" name="csv" rows="5" class="w-full rounded-md border border-input bg-background p-2 font-mono text-xs" data-testid="csv-text" /></template></FormField>
      </template>
      <form v-else novalidate class="flex flex-col gap-3" data-testid="single-form" @submit.prevent="run(true)">
        <div class="grid gap-3 sm:grid-cols-3">
          <FormField :label="t('assets.asset.name')" required><template #default="{ id }"><Input :id="id" v-model="row.name" name="name" maxlength="150" /></template></FormField>
          <FormField :label="t('assets.col.category')" required>
            <template #default="{ id }"><NativeSelect :id="id" v-model="row.category" name="category"><option value="">–</option><option v-for="c in categories" :key="c.id" :value="c.code">{{ c.code }} · {{ c.name }}</option></NativeSelect></template>
          </FormField>
          <FormField :label="t('assets.col.cost')" required><template #default="{ id }"><Input :id="id" v-model="row.cost" name="cost" inputmode="decimal" /></template></FormField>
          <FormField :label="t('assets.col.place')">
            <template #default="{ id }"><NativeSelect :id="id" v-model="row.location" name="location"><option value="">–</option><option v-for="l in locations" :key="l.id" :value="l.code">{{ l.code }} · {{ l.name }}</option></NativeSelect></template>
          </FormField>
          <FormField :label="t('assets.col.department')">
            <template #default="{ id }"><NativeSelect :id="id" v-model="row.department" name="department"><option value="">–</option><option v-for="d in departments" :key="d.id" :value="d.code">{{ d.code }} · {{ d.name }}</option></NativeSelect></template>
          </FormField>
          <FormField :label="t('assets.asset.acquired')"><template #default="{ id }"><Input :id="id" v-model="row.acquisition_date" name="acquisition_date" type="date" /></template></FormField>
          <FormField :label="t('assets.col.inService')" required><template #default="{ id }"><Input :id="id" v-model="row.in_service_date" name="in_service_date" type="date" /></template></FormField>
          <FormField :label="t('assets.opening.accumulated')"><template #default="{ id }"><Input :id="id" v-model="row.accumulated_depreciation" name="accumulated_depreciation" inputmode="decimal" /></template></FormField>
          <FormField :label="t('assets.opening.monthsDone')"><template #default="{ id }"><Input :id="id" v-model="row.months_done" name="months_done" inputmode="numeric" /></template></FormField>
        </div>
        <details class="text-sm">
          <summary class="cursor-pointer font-medium">{{ t('assets.opening.moreFields') }}</summary>
          <div class="mt-3 grid gap-3 sm:grid-cols-3">
            <FormField :label="t('assets.asset.residual')"><template #default="{ id }"><Input :id="id" v-model="row.residual_value" name="residual_value" inputmode="decimal" /></template></FormField>
            <FormField :label="t('assets.opening.lifeMonths')"><template #default="{ id }"><Input :id="id" v-model="row.life_months" name="life_months" inputmode="numeric" /></template></FormField>
            <FormField :label="t('assets.settings.method')"><template #default="{ id }"><NativeSelect :id="id" v-model="row.method" name="method"><option value="">–</option><option value="SL">{{ t('assets.method.SL') }}</option><option value="DB">{{ t('assets.method.DB') }}</option></NativeSelect></template></FormField>
            <FormField :label="t('assets.opening.unitCount')"><template #default="{ id }"><Input :id="id" v-model="row.unit_count" name="unit_count" inputmode="numeric" /></template></FormField>
            <FormField :label="t('assets.asset.serial')"><template #default="{ id }"><Input :id="id" v-model="row.serial_no" name="serial_no" /></template></FormField>
            <FormField :label="t('assets.asset.warranty')"><template #default="{ id }"><Input :id="id" v-model="row.warranty_until" name="warranty_until" type="date" /></template></FormField>
            <FormField :label="t('assets.asset.fiscalGroup')"><template #default="{ id }"><Input :id="id" v-model="row.fiscal_group" name="fiscal_group" placeholder="G1" /></template></FormField>
            <FormField :label="t('assets.opening.fiscalMethod')"><template #default="{ id }"><NativeSelect :id="id" v-model="row.fiscal_method" name="fiscal_method"><option value="">–</option><option value="SL">{{ t('assets.method.SL') }}</option><option value="DB">{{ t('assets.method.DB') }}</option></NativeSelect></template></FormField>
            <FormField :label="t('assets.opening.fiscalAccum')"><template #default="{ id }"><Input :id="id" v-model="row.fiscal_accumulated_depreciation" name="fiscal_accumulated_depreciation" inputmode="decimal" /></template></FormField>
            <FormField :label="t('assets.opening.fiscalMonths')"><template #default="{ id }"><Input :id="id" v-model="row.fiscal_months_done" name="fiscal_months_done" inputmode="numeric" /></template></FormField>
          </div>
        </details>
      </form>
      <div class="flex justify-end gap-2">
        <Button type="button" :disabled="busy || !ready" data-testid="dry-run" @click="run(true)">{{ t('assets.opening.check') }}</Button>
      </div>
    </Card>

    <section v-if="result" class="flex flex-col gap-3" data-testid="opening-result">
      <p v-if="!result.valid" class="notice" role="alert" data-testid="result-invalid">{{ t('assets.opening.invalid') }}</p>
      <ul v-if="result.errors.length" class="m-0 flex list-none flex-col gap-1 p-0 text-sm text-destructive" data-testid="result-errors"><li v-for="(f, i) in result.errors" :key="i">{{ finding(f) }}</li></ul>
      <ul v-if="result.warnings.length" class="m-0 flex list-none flex-col gap-1 p-0 text-sm" data-testid="result-warnings"><li v-for="(f, i) in result.warnings" :key="i">{{ finding(f) }}</li></ul>
      <Card class="overflow-x-auto p-0">
        <table class="w-full border-collapse text-sm" data-testid="result-rows">
          <thead><tr class="text-left text-xs uppercase text-muted-foreground">
            <th class="p-2">#</th><th class="p-2">{{ t('assets.col.asset') }}</th><th class="p-2">{{ t('assets.col.category') }}</th><th class="p-2 text-right">{{ t('assets.col.cost') }}</th><th class="p-2 text-right">{{ t('assets.col.accum') }}</th>
            <th class="p-2 text-right">{{ t('assets.opening.monthsDone') }}</th><th class="p-2 text-right">{{ t('assets.opening.fiscalAccum') }}</th><th class="p-2">{{ t('assets.opening.checkResult') }}</th>
          </tr></thead>
          <tbody>
            <tr v-for="r in result.rows" :key="r.row_no" class="border-t border-border" :data-testid="`result-row-${r.row_no}`">
              <td class="p-2">{{ r.row_no }}</td>
              <td class="p-2 font-medium">{{ r.name }}<Badge v-if="r.fully_depreciated" variant="outline" class="ml-2">{{ t('assets.life.fullyDepreciated') }}</Badge><div v-if="r.asset_number" class="text-xs text-muted-foreground">{{ r.asset_number }}</div></td>
              <td class="p-2">{{ r.category_code }}</td>
              <td class="p-2 text-right tabular-nums">{{ formatMoney(r.cost) }}</td>
              <td class="p-2 text-right tabular-nums">{{ formatMoney(r.accum) }}</td>
              <td class="p-2 text-right tabular-nums">{{ r.months_done }}</td>
              <td class="p-2 text-right tabular-nums">{{ r.fiscal_accum === null || r.fiscal_accum === undefined ? '–' : formatMoney(r.fiscal_accum) }}</td>
              <td class="p-2">
                <span v-if="r.errors.length" class="text-destructive">{{ r.errors.map(finding).join('; ') }}</span>
                <span v-else-if="r.warnings.length" class="text-warning-text">{{ r.warnings.map(finding).join('; ') }}</span>
                <span v-else class="text-success-text">{{ t('assets.opening.ready') }}</span>
              </td>
            </tr>
          </tbody>
        </table>
      </Card>
      <div class="grid gap-4 lg:grid-cols-2">
        <Card class="flex flex-col gap-1 p-4" data-testid="result-totals">
          <h2 class="m-0 text-base font-semibold">{{ t('assets.opening.totals') }}</h2>
          <div v-for="g in result.totals" :key="g.category_code" class="flex justify-between gap-3 text-sm tabular-nums"><span>{{ g.category_name }} · {{ g.count }}</span><span>{{ formatMoney(g.cost) }} / {{ formatMoney(g.accum) }}</span></div>
          <div class="mt-1 flex justify-between gap-3 border-t border-border pt-1 text-sm font-semibold tabular-nums"><span>{{ t('assets.opening.total') }}</span><span>{{ formatMoney(result.total_cost) }} / {{ formatMoney(result.total_accum) }}</span></div>
        </Card>
        <Card v-if="result.mode === 'WITH_JOURNAL'" class="flex flex-col gap-1 p-4" data-testid="result-journal">
          <h2 class="m-0 text-base font-semibold">{{ t('assets.opening.journalPreview', { date: formatDate(result.journal_date) }) }}</h2>
          <div v-for="(l, i) in result.journal_preview" :key="i" class="flex justify-between gap-3 text-sm tabular-nums">
            <span>{{ Number(l.debit) > 0 ? 'Dr' : 'Cr' }} {{ l.account_code }} {{ l.account_name }}</span><span>{{ formatMoney(Number(l.debit) > 0 ? l.debit : l.credit) }}</span>
          </div>
        </Card>
        <Card v-else class="flex flex-col gap-1 p-4" data-testid="result-glcheck">
          <h2 class="m-0 text-base font-semibold">{{ t('assets.opening.glCheck') }}</h2>
          <div v-for="g in result.gl_check" :key="g.category_code" class="text-sm tabular-nums">
            <div class="flex justify-between gap-3"><span>{{ g.category_code }} · {{ g.cost_account_code }}</span><span>{{ formatMoney(g.register_cost) }} / {{ formatMoney(g.ledger_cost) }}</span></div>
            <div v-if="Number(g.cost_difference) !== 0 || Number(g.accum_difference) !== 0" class="text-xs text-destructive">{{ t('assets.opening.difference') }}: {{ formatMoney(g.cost_difference) }} / {{ formatMoney(g.accum_difference) }}</div>
          </div>
        </Card>
      </div>
      <div class="flex justify-end gap-2">
        <Button type="button" :disabled="busy || !canImport" data-testid="confirm-import" @click="run(false)">{{ t('assets.opening.import', { n: result.rows.length }) }}</Button>
      </div>
    </section>

    <Card class="flex flex-col gap-2 p-4" data-testid="past-imports">
      <h2 class="m-0 text-base font-semibold">{{ t('assets.opening.past') }}</h2>
      <p v-if="!imports.length" class="muted m-0">{{ t('assets.opening.noPast') }}</p>
      <ul v-else class="m-0 flex list-none flex-col gap-1 p-0 text-sm">
        <li v-for="i in imports" :key="i.id" class="flex flex-wrap gap-x-3 border-b border-border py-1 tabular-nums" :data-testid="`import-${i.number}`">
          <b class="w-28">{{ i.number }}</b><span>{{ formatDate(i.as_of) }}</span><span>{{ t(`assets.opening.modeName.${i.mode}`) }}</span><span>{{ t('assets.opening.assetsCount', { n: i.asset_count }) }}</span>
          <span class="flex-1 text-right">{{ formatMoney(i.total_cost) }} / {{ formatMoney(i.total_accum) }}</span><span v-if="i.journal_number">{{ i.journal_number }}</span>
        </li>
      </ul>
    </Card>
  </template>
</template>
