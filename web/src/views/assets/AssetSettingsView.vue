<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import { fetchAll } from '@/api/paging'
import type { AssetAccountOption, AssetCategory, AssetFiscalGroup, AssetLocation, AssetSettings, AssetSetupCheck } from '@/api/types'
import DepartmentSelect from '@/components/app/DepartmentSelect.vue'
import ErrorNotice from '@/components/app/ErrorNotice.vue'
import FormField from '@/components/app/FormField.vue'
import PageHeader from '@/components/app/PageHeader.vue'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card } from '@/components/ui/card'
import { Dialog, DialogContent, DialogDescription, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { NativeSelect } from '@/components/ui/native-select'
import { useAssetAccess } from '@/composables/useAssetAccess'
import { toast } from '@/composables/useToast'
import { t } from '@/i18n'
import { formatDate, formatMoney } from '@/utils/format'

/**
 * Categories with their accounts, the settings of the property, the locations, the fiscal rates and the check of the register (docs/architecture/37-fixed-assets.md 4.1, 9; mockup 58).
 * Everything is read by who holds any asset permission and changed with asset.setup.
 */
const KINDS = ['LAND', 'BUILDING', 'FFE', 'OE', 'VEHICLE', 'IT', 'CIP', 'LICENSE', 'OTHER'] as const
const NO_DEPRECIATION = ['LAND', 'CIP']
const { pid, canRead, canSetup } = useAssetAccess()
const error = ref<ApiError | null>(null)
const busy = ref(false)

const categories = ref<AssetCategory[]>([])
const locations = ref<AssetLocation[]>([])
const fiscal = ref<AssetFiscalGroup[]>([])
const settings = ref<AssetSettings | null>(null)
const check = ref<AssetSetupCheck | null>(null)
const ISSUES = new Set(['REGISTER_NOT_STARTED', 'CATEGORY_ACCOUNT_UNUSABLE', 'CATEGORY_NEEDS_DEPARTMENT', 'LEDGER_DRIFT', 'ASSET_IN_INACTIVE_LOCATION', 'GL_DIFFERENCE', 'OUTSIDE_REGISTER_1590'])
/** An issue of the check in the language of the page, made from its code and its parameters; the text of the server is only for a code that this page does not know. */
function issueText(i: AssetSetupCheck['issues'][number]): string {
  if (!ISSUES.has(i.code)) return i.message
  const c = i.context ?? {}
  const text = (v: unknown): string => (v === undefined || v === null ? '' : String(v))
  const field = text(c.field)
  return t(`assets.settings.issue.${i.code}`, {
    category: text(c.category_name) || text(c.category), account: text(c.account), asset: text(c.asset), location: text(c.location),
    field: field ? t(`assets.settings.issueField.${field}`) : '', balance: c.balance === undefined ? '' : formatMoney(text(c.balance)),
  })
}
const assetAccounts = ref<AssetAccountOption[]>([])
const expenseAccounts = ref<AssetAccountOption[]>([])

async function load(): Promise<void> {
  const propertyId = pid.value
  error.value = null
  if (propertyId === null || !canRead.value) return
  const path = { params: { path: { propertyId } } }
  try {
    const [c, l, f, s, k] = await Promise.all([
      api.GET('/api/v1/properties/{propertyId}/assets/categories', path),
      api.GET('/api/v1/properties/{propertyId}/assets/locations', path),
      api.GET('/api/v1/properties/{propertyId}/assets/fiscal-groups', path),
      api.GET('/api/v1/properties/{propertyId}/assets/settings', path),
      api.GET('/api/v1/properties/{propertyId}/assets/setup-check', path),
    ])
    categories.value = c.data?.data ?? []
    locations.value = l.data?.data ?? []
    fiscal.value = f.data?.data ?? []
    settings.value = s.data ?? null
    check.value = k.data ?? null
    if (s.data) Object.assign(sForm, formOf(s.data))
    if (canSetup.value) {
      const [a, e] = await Promise.all([
        api.GET('/api/v1/properties/{propertyId}/assets/accounts', { params: { path: { propertyId }, query: { type: 'ASSET' } } }),
        api.GET('/api/v1/properties/{propertyId}/assets/accounts', { params: { path: { propertyId }, query: { type: 'EXPENSE' } } }),
      ])
      assetAccounts.value = a.data?.data ?? []
      expenseAccounts.value = e.data?.data ?? []
    }
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  }
}

// settings
const formOf = (s: AssetSettings) => ({ register_from: s.register_from ?? '', capitalization_threshold: s.capitalization_threshold, start_rule: s.start_rule, oe_policy: s.oe_policy, oe_default_months: s.oe_default_months, tax_year_start_month: s.tax_year_start_month ?? 0 })
const sForm = reactive({ register_from: '', capitalization_threshold: '0', start_rule: 'IN_SERVICE_MONTH' as AssetSettings['start_rule'], oe_policy: 'EXPENSE' as AssetSettings['oe_policy'], oe_default_months: 24, tax_year_start_month: 0 })
const sError = ref<ApiError | null>(null)
async function saveSettings(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null || !settings.value) return
  busy.value = true
  sError.value = null
  try {
    const { data } = await api.PUT('/api/v1/properties/{propertyId}/assets/settings', {
      params: { path: { propertyId } },
      body: {
        register_from: sForm.register_from || null, capitalization_threshold: String(sForm.capitalization_threshold).trim() || '0', start_rule: sForm.start_rule, oe_policy: sForm.oe_policy,
        oe_default_months: Number(sForm.oe_default_months), tax_year_start_month: sForm.tax_year_start_month || null, version: settings.value.version,
      },
    })
    if (data) {
      settings.value = data
      Object.assign(sForm, formOf(data))
    }
    toast.success(t('assets.settings.saved'))
  } catch (e) {
    sError.value = e instanceof ApiError ? e : null
  } finally {
    busy.value = false
  }
}

// categories
const cDialog = ref(false)
const cError = ref<ApiError | null>(null)
const blank = () => ({ id: 0, code: '', name: '', kind: 'FFE' as AssetCategory['kind'], cost_account_id: 0, accumulated_account_id: 0, expense_account_id: 0, department_id: null as number | null, method: '' as '' | 'SL' | 'DB', life_months: '', residual_pct: '0', db_factor: '', fiscal_group: 'NONE', fiscal_method: '', is_active: true, version: 0, asset_count: 0 })
const cForm = reactive(blank())
const depreciable = computed(() => !NO_DEPRECIATION.includes(cForm.kind))
function newCategory(): void {
  Object.assign(cForm, blank())
  cError.value = null
  cDialog.value = true
}
function editCategory(c: AssetCategory): void {
  Object.assign(cForm, {
    id: c.id, code: c.code, name: c.name, kind: c.kind, cost_account_id: c.cost_account_id, accumulated_account_id: c.accumulated_account_id ?? 0, expense_account_id: c.expense_account_id ?? 0, department_id: c.department_id ?? null,
    method: c.method ?? '', life_months: c.life_months ? String(c.life_months) : '', residual_pct: c.residual_pct, db_factor: c.db_factor ?? '', fiscal_group: c.fiscal_group, fiscal_method: c.fiscal_method ?? '', is_active: c.is_active, version: c.version, asset_count: c.asset_count,
  })
  cError.value = null
  cDialog.value = true
}
async function saveCategory(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null) return
  busy.value = true
  cError.value = null
  const f = cForm
  const body = {
    code: f.code.trim(), name: f.name.trim(), kind: f.kind, cost_account_id: f.cost_account_id, accumulated_account_id: depreciable.value && f.accumulated_account_id ? f.accumulated_account_id : null,
    expense_account_id: depreciable.value && f.expense_account_id ? f.expense_account_id : null, department_id: f.department_id, method: depreciable.value && f.method ? f.method : null,
    life_months: depreciable.value && f.life_months ? Number(f.life_months) : null, residual_pct: f.residual_pct || '0', db_factor: f.method === 'DB' && f.db_factor ? f.db_factor : null,
    fiscal_group: f.fiscal_group, fiscal_method: f.fiscal_method || null, is_active: f.is_active, version: f.version || undefined,
  }
  try {
    if (f.id === 0) await api.POST('/api/v1/properties/{propertyId}/assets/categories', { params: { path: { propertyId } }, body })
    else await api.PATCH('/api/v1/properties/{propertyId}/assets/categories/{id}', { params: { path: { propertyId, id: f.id } }, body })
    cDialog.value = false
    toast.success(t('assets.settings.saved'))
    await load()
  } catch (e) {
    cError.value = e instanceof ApiError ? e : null
  } finally {
    busy.value = false
  }
}
const methodText = (c: AssetCategory): string => (c.is_depreciable && c.method ? `${t(`assets.method.${c.method}`)} · ${t('assets.life.months', { n: c.life_months ?? 0 })}` : t('assets.life.notDepreciated'))
const fiscalText = (code: string): string => (code === 'NONE' ? '–' : t(`assets.fiscalGroup.${code}`))
const groupCodes = computed(() => ['NONE', ...fiscal.value.map((g) => g.code)])

// locations
const lDialog = ref(false)
const lError = ref<ApiError | null>(null)
const lForm = reactive({ code: '', name: '' })
async function saveLocation(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null) return
  busy.value = true
  lError.value = null
  try {
    await api.POST('/api/v1/properties/{propertyId}/assets/locations', { params: { path: { propertyId } }, body: { code: lForm.code.trim(), name: lForm.name.trim() } })
    lDialog.value = false
    toast.success(t('assets.settings.saved'))
    await load()
  } catch (e) {
    lError.value = e instanceof ApiError ? e : null
  } finally {
    busy.value = false
  }
}
async function toggleLocation(l: AssetLocation): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null) return
  try {
    await api.PATCH('/api/v1/properties/{propertyId}/assets/locations/{id}', { params: { path: { propertyId, id: l.id } }, body: { is_active: !l.is_active } })
    await load()
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  }
}
// "ensure": the location of a room, a meeting room or a warehouse, made when it is first used
const ensureKind = ref<'ROOM' | 'BANQUET_ROOM' | 'WAREHOUSE'>('ROOM')
const ensureRef = ref(0)
const ensureOptions = ref<{ id: number; label: string }[]>([])
async function loadEnsure(): Promise<void> {
  const propertyId = pid.value
  ensureRef.value = 0
  ensureOptions.value = []
  if (propertyId === null || !canSetup.value) return
  const path = { params: { path: { propertyId } } }
  try {
    if (ensureKind.value === 'ROOM') {
      const rooms = await fetchAll((cursor) => api.GET('/api/v1/properties/{propertyId}/rooms', { params: { path: { propertyId }, query: { cursor } as never } }) as never)
      ensureOptions.value = (rooms as { id: number; room_number: string }[]).map((r) => ({ id: r.id, label: r.room_number }))
    } else if (ensureKind.value === 'BANQUET_ROOM') {
      const { data } = await api.GET('/api/v1/properties/{propertyId}/banquet/rooms', path)
      ensureOptions.value = (data?.data ?? []).map((r) => ({ id: r.id, label: `${r.code} · ${r.name}` }))
    } else {
      const { data } = await api.GET('/api/v1/properties/{propertyId}/inventory/warehouses', path)
      ensureOptions.value = (data?.data ?? []).map((w) => ({ id: w.id, label: `${w.code} · ${w.name}` }))
    }
  } catch {
    ensureOptions.value = [] // not allowed to read that master, or its module is off: nothing to offer
  }
}
async function ensureLocation(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null || !ensureRef.value) return
  busy.value = true
  try {
    await api.POST('/api/v1/properties/{propertyId}/assets/locations/ensure', { params: { path: { propertyId } }, body: { kind: ensureKind.value, ref_id: ensureRef.value } })
    toast.success(t('assets.settings.saved'))
    ensureRef.value = 0
    await load()
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    busy.value = false
  }
}

// fiscal rates
const rates = reactive<Record<string, { life_years: string; sl_rate: string; db_rate: string }>>({})
watch(fiscal, (list) => {
  for (const g of list) rates[g.code] = { life_years: String(g.life_years), sl_rate: g.sl_rate, db_rate: g.db_rate ?? '' }
}, { immediate: true })
async function saveRate(g: AssetFiscalGroup): Promise<void> {
  const propertyId = pid.value
  const r = rates[g.code]
  if (propertyId === null || !r) return
  busy.value = true
  try {
    await api.PUT('/api/v1/properties/{propertyId}/assets/fiscal-groups/{code}', { params: { path: { propertyId, code: g.code } }, body: { life_years: Number(r.life_years), sl_rate: r.sl_rate, db_rate: r.db_rate || null } })
    toast.success(t('assets.settings.saved'))
    await load()
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    busy.value = false
  }
}

watch([pid, canRead], load, { immediate: true })
watch([ensureKind, canSetup], loadEnsure, { immediate: true })
</script>

<template>
  <PageHeader :title="t('assets.settings.title')" :description="t('assets.settings.intro')" />
  <ErrorNotice v-if="error" :error="error" inline />
  <p v-if="pid === null" class="muted">{{ t('setup.selectProperty') }}</p>
  <p v-else-if="!canRead" class="muted" data-testid="no-access">{{ t('assets.noAccess') }}</p>
  <template v-else>
    <Card class="flex flex-col gap-3 p-4" data-testid="categories-card">
      <div class="flex items-center gap-2"><h2 class="m-0 flex-1 text-base font-semibold">{{ t('assets.settings.categories') }}</h2><Button v-if="canSetup" variant="outline" data-testid="category-new" @click="newCategory">{{ t('assets.settings.newCategory') }}</Button></div>
      <div class="overflow-x-auto">
        <table class="w-full border-collapse text-sm" data-testid="categories-table">
          <thead><tr class="text-left text-xs uppercase text-muted-foreground"><th class="p-2">{{ t('assets.col.category') }}</th><th class="p-2">{{ t('assets.settings.costAccount') }}</th><th class="p-2">{{ t('assets.settings.accumAccount') }}</th><th class="p-2">{{ t('assets.settings.expenseAccount') }}</th><th class="p-2">{{ t('assets.settings.methodLife') }}</th><th class="p-2">{{ t('assets.col.department') }}</th><th class="p-2">{{ t('assets.asset.fiscalGroup') }}</th><th class="p-2 text-right">{{ t('assets.settings.assets') }}</th><th class="p-2" /></tr></thead>
          <tbody>
            <tr v-for="c in categories" :key="c.id" class="border-t border-border" :data-testid="`category-${c.code}`">
              <td class="p-2 font-medium">{{ c.name }}<Badge v-if="!c.is_active" variant="outline" class="ml-2">{{ t('common.inactive') }}</Badge></td>
              <td class="p-2">{{ c.cost_account_code }}</td>
              <td class="p-2">{{ c.accumulated_account_code ?? '–' }}</td>
              <td class="p-2">{{ c.expense_account_code ?? '–' }}</td>
              <td class="p-2" :class="c.is_depreciable ? '' : 'text-muted-foreground'">{{ methodText(c) }}</td>
              <td class="p-2">{{ c.department_name ?? '–' }}</td>
              <td class="p-2">{{ fiscalText(c.fiscal_group) }}</td>
              <td class="p-2 text-right tabular-nums">{{ c.asset_count }}</td>
              <td class="p-2 text-right"><Button v-if="canSetup" size="sm" variant="outline" :data-testid="`category-edit-${c.code}`" @click="editCategory(c)">{{ t('common.edit') }}</Button></td>
            </tr>
          </tbody>
        </table>
      </div>
      <p class="m-0 text-xs text-muted-foreground">{{ t('assets.settings.categoriesNote') }}</p>
    </Card>

    <Card v-if="settings" class="flex flex-col gap-4 p-4" data-testid="settings-card">
      <h2 class="m-0 text-base font-semibold">{{ t('assets.settings.property') }}</h2>
      <ErrorNotice v-if="sError" :error="sError" inline />
      <form novalidate class="grid gap-4 lg:grid-cols-3" @submit.prevent="saveSettings">
        <fieldset class="m-0 flex flex-col gap-1 border-0 p-0">
          <legend class="mb-1 text-sm font-semibold">{{ t('assets.settings.startRule') }}</legend>
          <label v-for="r in ['IN_SERVICE_MONTH', 'NEXT_MONTH']" :key="r" class="flex min-h-9 items-center gap-2 text-sm"><input v-model="sForm.start_rule" type="radio" name="start_rule" :value="r" :disabled="!canSetup" class="accent-primary" />{{ t(`assets.startRule.${r}`) }}</label>
          <span class="text-xs text-muted-foreground">{{ t('assets.settings.startRuleHint') }}</span>
        </fieldset>
        <fieldset class="m-0 flex flex-col gap-1 border-0 p-0">
          <legend class="mb-1 text-sm font-semibold">{{ t('assets.settings.oePolicy') }}</legend>
          <label v-for="r in ['BATCH', 'EXPENSE']" :key="r" class="flex min-h-9 items-center gap-2 text-sm"><input v-model="sForm.oe_policy" type="radio" name="oe_policy" :value="r" :disabled="!canSetup" class="accent-primary" />{{ t(`assets.oePolicy.${r}`) }}</label>
          <FormField :label="t('assets.settings.oeMonths')"><template #default="{ id }"><Input :id="id" v-model.number="sForm.oe_default_months" name="oe_default_months" type="number" min="1" max="120" :disabled="!canSetup" /></template></FormField>
          <span class="text-xs text-muted-foreground">{{ t('assets.settings.oePolicyHint') }}</span>
        </fieldset>
        <div class="flex flex-col gap-3">
          <FormField :label="t('assets.settings.threshold')" :hint="t('assets.settings.thresholdHint')" :error="sError?.fieldMessage('capitalization_threshold')"><template #default="{ id }"><Input :id="id" v-model="sForm.capitalization_threshold" name="capitalization_threshold" inputmode="decimal" :disabled="!canSetup" /></template></FormField>
          <FormField :label="t('assets.settings.registerFrom')" :hint="settings.started ? t('assets.settings.registerLocked') : t('assets.settings.registerFromHint')" :error="sError?.fieldMessage('register_from')">
            <template #default="{ id }"><Input :id="id" v-model="sForm.register_from" name="register_from" type="date" :disabled="!canSetup || settings.started" /></template>
          </FormField>
          <FormField :label="t('assets.settings.taxYear')" :hint="t('assets.settings.taxYearHint')">
            <template #default="{ id }">
              <NativeSelect :id="id" v-model.number="sForm.tax_year_start_month" name="tax_year_start_month" :disabled="!canSetup">
                <option :value="0">{{ t('assets.settings.taxYearBooks') }}</option>
                <option v-for="m in 12" :key="m" :value="m">{{ m }}</option>
              </NativeSelect>
            </template>
          </FormField>
          <div v-if="canSetup" class="flex justify-end"><Button type="submit" :disabled="busy" data-testid="settings-save">{{ t('common.save') }}</Button></div>
        </div>
      </form>
    </Card>

    <Card class="flex flex-col gap-3 p-4" data-testid="locations-card">
      <div class="flex items-center gap-2"><h2 class="m-0 flex-1 text-base font-semibold">{{ t('assets.settings.locations') }}</h2><Button v-if="canSetup" variant="outline" data-testid="location-new" @click="lForm.code = ''; lForm.name = ''; lError = null; lDialog = true">{{ t('assets.settings.newArea') }}</Button></div>
      <ul class="m-0 flex list-none flex-col gap-1 p-0 text-sm">
        <li v-for="l in locations" :key="l.id" class="flex flex-wrap items-center gap-x-3 border-b border-border py-1" :data-testid="`location-${l.code}`">
          <b class="w-24 font-medium">{{ l.code }}</b><span class="flex-1">{{ l.name }}<span v-if="l.reference_label" class="text-muted-foreground"> · {{ l.reference_label }}</span></span>
          <Badge variant="outline">{{ t(`assets.locationKind.${l.kind}`) }}</Badge><span class="tabular-nums text-muted-foreground">{{ t('assets.settings.assetCount', { n: l.asset_count }) }}</span>
          <Badge :variant="l.is_active ? 'success' : 'outline'">{{ l.is_active ? t('common.active') : t('common.inactive') }}</Badge>
          <Button v-if="canSetup" size="sm" variant="outline" :data-testid="`location-toggle-${l.code}`" @click="toggleLocation(l)">{{ l.is_active ? t('assets.settings.deactivate') : t('assets.settings.activate') }}</Button>
        </li>
      </ul>
      <form v-if="canSetup" class="flex flex-wrap items-end gap-2" data-testid="ensure-form" @submit.prevent="ensureLocation">
        <FormField :label="t('assets.settings.ensureKind')"><template #default="{ id }"><NativeSelect :id="id" v-model="ensureKind" name="kind"><option v-for="k in ['ROOM', 'BANQUET_ROOM', 'WAREHOUSE']" :key="k" :value="k">{{ t(`assets.locationKind.${k}`) }}</option></NativeSelect></template></FormField>
        <FormField :label="t('assets.settings.ensureRef')"><template #default="{ id }"><NativeSelect :id="id" v-model.number="ensureRef" name="ref_id"><option :value="0">–</option><option v-for="o in ensureOptions" :key="o.id" :value="o.id">{{ o.label }}</option></NativeSelect></template></FormField>
        <Button type="submit" variant="outline" :disabled="busy || !ensureRef" data-testid="ensure-submit">{{ t('assets.settings.ensure') }}</Button>
      </form>
    </Card>

    <Card class="flex flex-col gap-3 p-4" data-testid="fiscal-card">
      <h2 class="m-0 text-base font-semibold">{{ t('assets.settings.fiscalRates') }}</h2>
      <p class="m-0 text-xs text-muted-foreground">{{ t('assets.settings.fiscalNote') }}</p>
      <div v-for="g in fiscal" :key="g.code" class="flex flex-wrap items-end gap-2" :data-testid="`fiscal-${g.code}`">
        <b class="w-44 pb-2 text-sm font-medium">{{ t(`assets.fiscalGroup.${g.code}`) }}</b>
        <FormField :label="t('assets.settings.lifeYears')"><template #default="{ id }"><Input :id="id" v-model="rates[g.code]!.life_years" class="w-24" :disabled="!canSetup" /></template></FormField>
        <FormField :label="t('assets.settings.slRate')"><template #default="{ id }"><Input :id="id" v-model="rates[g.code]!.sl_rate" class="w-24" :disabled="!canSetup" /></template></FormField>
        <FormField :label="t('assets.settings.dbRate')"><template #default="{ id }"><Input :id="id" v-model="rates[g.code]!.db_rate" class="w-24" :disabled="!canSetup" /></template></FormField>
        <Button v-if="canSetup" size="sm" variant="outline" :disabled="busy" :data-testid="`fiscal-save-${g.code}`" @click="saveRate(g)">{{ t('common.save') }}</Button>
      </div>
    </Card>

    <Card v-if="check" class="flex flex-col gap-3 p-4" data-testid="check-card">
      <h2 class="m-0 text-base font-semibold">{{ t('assets.settings.check') }} <span class="text-sm font-normal text-muted-foreground">{{ formatDate(check.as_of) }}</span></h2>
      <p v-if="!check.issues.length" class="m-0 text-sm text-success-text" data-testid="check-ok">{{ t('assets.settings.checkOk') }}</p>
      <ul v-else class="m-0 flex list-none flex-col gap-1 p-0 text-sm" data-testid="check-issues"><li v-for="(i, n) in check.issues" :key="n"><Badge :variant="i.severity === 'ERROR' ? 'destructive' : 'warning'" class="mr-2">{{ t(`assets.severity.${i.severity}`) }}</Badge>{{ issueText(i) }}</li></ul>
      <div v-if="check.reconciliation.length" class="overflow-x-auto">
        <table class="w-full border-collapse text-sm tabular-nums" data-testid="check-reconciliation">
          <thead><tr class="text-left text-xs uppercase text-muted-foreground"><th class="p-2">{{ t('assets.col.category') }}</th><th class="p-2 text-right">{{ t('assets.settings.registerCost') }}</th><th class="p-2 text-right">{{ t('assets.settings.ledgerCost') }}</th><th class="p-2 text-right">{{ t('assets.settings.registerAccum') }}</th><th class="p-2 text-right">{{ t('assets.settings.ledgerAccum') }}</th></tr></thead>
          <tbody>
            <template v-for="r in check.reconciliation" :key="r.category_id">
            <tr class="border-t border-border">
              <td class="p-2">{{ r.category_name }}</td><td class="p-2 text-right">{{ formatMoney(r.register_cost) }}</td><td class="p-2 text-right" :class="Number(r.cost_difference) !== 0 ? 'text-destructive' : ''">{{ formatMoney(r.ledger_cost) }}</td>
              <td class="p-2 text-right">{{ formatMoney(r.register_accum) }}</td><td class="p-2 text-right" :class="Number(r.accum_difference) !== 0 ? 'text-destructive' : ''">{{ formatMoney(r.ledger_accum) }}</td>
            </tr>
            <tr v-if="(r.cost_causes?.length ?? 0) + (r.accum_causes?.length ?? 0)" :data-testid="`check-causes-${r.category_code}`">
              <td colspan="5" class="px-2 pb-2 text-xs text-muted-foreground">
                <span v-for="c in [...(r.cost_causes ?? []), ...(r.accum_causes ?? [])]" :key="`${c.code}-${c.amount}`" class="mr-3">{{ t(`assets.reports.cause.${c.code}`) }} {{ formatMoney(c.amount) }}</span>
              </td>
            </tr>
            </template>
          </tbody>
        </table>
      </div>
    </Card>
  </template>

  <Dialog :open="cDialog" @update:open="(v) => (cDialog = v)">
    <DialogContent class="max-h-[90dvh] max-w-2xl overflow-y-auto p-5" data-testid="category-dialog" @interact-outside.prevent>
      <form novalidate class="flex flex-col gap-3" @submit.prevent="saveCategory">
        <DialogTitle>{{ cForm.id === 0 ? t('assets.settings.newCategory') : t('assets.settings.editCategory') }}</DialogTitle>
        <DialogDescription>{{ cForm.asset_count > 0 ? t('assets.settings.categoryInUse') : t('assets.settings.categoryHint') }}</DialogDescription>
        <ErrorNotice v-if="cError" :error="cError" inline />
        <div class="grid gap-3 sm:grid-cols-3">
          <FormField :label="t('assets.settings.code')" required :error="cError?.fieldMessage('code')"><template #default="{ id }"><Input :id="id" v-model="cForm.code" name="code" maxlength="20" :disabled="cForm.id !== 0" /></template></FormField>
          <FormField :label="t('assets.asset.name')" required :error="cError?.fieldMessage('name')"><template #default="{ id }"><Input :id="id" v-model="cForm.name" name="name" maxlength="100" /></template></FormField>
          <FormField :label="t('assets.settings.kind')"><template #default="{ id }"><NativeSelect :id="id" v-model="cForm.kind" name="kind" :disabled="cForm.asset_count > 0"><option v-for="k in KINDS" :key="k" :value="k">{{ t(`assets.kind.${k}`) }}</option></NativeSelect></template></FormField>
        </div>
        <div class="grid gap-3 sm:grid-cols-3">
          <FormField :label="t('assets.settings.costAccount')" required :error="cError?.fieldMessage('cost_account_id')">
            <template #default="{ id }"><NativeSelect :id="id" v-model.number="cForm.cost_account_id" name="cost_account_id" :disabled="cForm.asset_count > 0"><option :value="0">–</option><option v-for="a in assetAccounts" :key="a.id" :value="a.id">{{ a.code }} · {{ a.name }}</option></NativeSelect></template>
          </FormField>
          <template v-if="depreciable">
            <FormField :label="t('assets.settings.accumAccount')" :error="cError?.fieldMessage('accumulated_account_id')">
              <template #default="{ id }"><NativeSelect :id="id" v-model.number="cForm.accumulated_account_id" name="accumulated_account_id" :disabled="cForm.asset_count > 0"><option :value="0">–</option><option v-for="a in assetAccounts" :key="a.id" :value="a.id">{{ a.code }} · {{ a.name }}</option></NativeSelect></template>
            </FormField>
            <FormField :label="t('assets.settings.expenseAccount')" :error="cError?.fieldMessage('expense_account_id')">
              <template #default="{ id }"><NativeSelect :id="id" v-model.number="cForm.expense_account_id" name="expense_account_id" :disabled="cForm.asset_count > 0"><option :value="0">–</option><option v-for="a in expenseAccounts" :key="a.id" :value="a.id">{{ a.code }} · {{ a.name }}</option></NativeSelect></template>
            </FormField>
          </template>
        </div>
        <div v-if="depreciable" class="grid gap-3 sm:grid-cols-4">
          <FormField :label="t('assets.settings.method')"><template #default="{ id }"><NativeSelect :id="id" v-model="cForm.method" name="method"><option value="">–</option><option value="SL">{{ t('assets.method.SL') }}</option><option value="DB">{{ t('assets.method.DB') }}</option></NativeSelect></template></FormField>
          <FormField :label="t('assets.settings.lifeMonths')" :error="cError?.fieldMessage('life_months')"><template #default="{ id }"><Input :id="id" v-model="cForm.life_months" name="life_months" inputmode="numeric" /></template></FormField>
          <FormField :label="t('assets.settings.residualPct')"><template #default="{ id }"><Input :id="id" v-model="cForm.residual_pct" name="residual_pct" inputmode="decimal" /></template></FormField>
          <FormField v-if="cForm.method === 'DB'" :label="t('assets.settings.dbFactor')"><template #default="{ id }"><Input :id="id" v-model="cForm.db_factor" name="db_factor" inputmode="decimal" /></template></FormField>
        </div>
        <div class="grid gap-3 sm:grid-cols-3">
          <FormField :label="t('assets.settings.defaultDepartment')"><template #default="{ id }"><DepartmentSelect :id="id" v-model="cForm.department_id" /></template></FormField>
          <FormField :label="t('assets.asset.fiscalGroup')"><template #default="{ id }"><NativeSelect :id="id" v-model="cForm.fiscal_group" name="fiscal_group"><option v-for="g in groupCodes" :key="g" :value="g">{{ fiscalText(g) }}</option></NativeSelect></template></FormField>
          <FormField :label="t('assets.opening.fiscalMethod')"><template #default="{ id }"><NativeSelect :id="id" v-model="cForm.fiscal_method" name="fiscal_method"><option value="">–</option><option value="SL">{{ t('assets.method.SL') }}</option><option value="DB">{{ t('assets.method.DB') }}</option></NativeSelect></template></FormField>
        </div>
        <label class="flex min-h-11 cursor-pointer items-center gap-2 text-sm"><input v-model="cForm.is_active" type="checkbox" name="is_active" class="size-4 accent-primary" />{{ t('common.active') }}</label>
        <div class="flex justify-end gap-2"><Button type="button" variant="outline" @click="cDialog = false">{{ t('common.cancel') }}</Button><Button type="submit" :disabled="busy || !cForm.code.trim() || !cForm.name.trim() || !cForm.cost_account_id" data-testid="category-save">{{ t('common.save') }}</Button></div>
      </form>
    </DialogContent>
  </Dialog>

  <Dialog :open="lDialog" @update:open="(v) => (lDialog = v)">
    <DialogContent class="max-w-md p-5" data-testid="location-dialog" @interact-outside.prevent>
      <form novalidate class="flex flex-col gap-3" @submit.prevent="saveLocation">
        <DialogTitle>{{ t('assets.settings.newArea') }}</DialogTitle>
        <DialogDescription>{{ t('assets.settings.areaHint') }}</DialogDescription>
        <ErrorNotice v-if="lError" :error="lError" inline />
        <FormField :label="t('assets.settings.code')" required :error="lError?.fieldMessage('code')"><template #default="{ id }"><Input :id="id" v-model="lForm.code" name="code" maxlength="20" /></template></FormField>
        <FormField :label="t('assets.asset.name')" required :error="lError?.fieldMessage('name')"><template #default="{ id }"><Input :id="id" v-model="lForm.name" name="name" maxlength="100" /></template></FormField>
        <div class="flex justify-end gap-2"><Button type="button" variant="outline" @click="lDialog = false">{{ t('common.cancel') }}</Button><Button type="submit" :disabled="busy || !lForm.code.trim() || !lForm.name.trim()" data-testid="location-save">{{ t('common.save') }}</Button></div>
      </form>
    </DialogContent>
  </Dialog>
</template>
