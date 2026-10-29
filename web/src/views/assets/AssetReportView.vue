<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { RouterLink, useRoute } from 'vue-router'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { AssetAuditDifference, AssetCategory, AssetExpiringRow, AssetLocation, AssetPlacementReport, AssetPlacementRow, AssetRegisterReport, AssetSchedule } from '@/api/types'
import ErrorNotice from '@/components/app/ErrorNotice.vue'
import FormField from '@/components/app/FormField.vue'
import PageHeader from '@/components/app/PageHeader.vue'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { NativeSelect } from '@/components/ui/native-select'
import { useAssetAccess } from '@/composables/useAssetAccess'
import { t } from '@/i18n'
import { formatDate, formatMoney, formatMonth } from '@/utils/format'
import { downloadCsv, money } from '@/views/accounting/reportApi'

/**
 * The simple reports of the fixed assets (docs/architecture/37-fixed-assets.md 4.12): the register as at a date, the schedules, the assets by place, the warranties and lives that end, and the differences of the stock-takes.
 * Each one is a table with a CSV file and a print page; the recap and the fiscal correction have their own pages.
 */
type Kind = 'register' | 'schedule' | 'locations' | 'expiring' | 'audit-differences'
const route = useRoute()
const kind = computed(() => String(route.params.kind) as Kind)
const { pid, canRead } = useAssetAccess()

const categories = ref<AssetCategory[]>([])
const locations = ref<AssetLocation[]>([])
const f = ref({ as_of: '', category_id: 0, location_id: 0, days: '90', months: '6', result: '', date_from: '', date_to: '' })

const register = ref<AssetRegisterReport | null>(null)
const schedules = ref<AssetSchedule[]>([])
const placement = ref<AssetPlacementReport | null>(null)
const expiring = ref<AssetExpiringRow[]>([])
const differences = ref<AssetAuditDifference[]>([])
const loaded = ref(false)
const busy = ref(false)
const error = ref<ApiError | null>(null)

const PATHS: Record<Kind, string> = {
  register: '/api/v1/properties/{propertyId}/assets/reports/register',
  schedule: '/api/v1/properties/{propertyId}/assets/reports/schedule',
  locations: '/api/v1/properties/{propertyId}/assets/reports/locations',
  expiring: '/api/v1/properties/{propertyId}/assets/reports/expiring',
  'audit-differences': '/api/v1/properties/{propertyId}/assets/reports/audit-differences',
}
/** The schedule is of one asset or of one category: without either the server refuses it, so nothing is asked and the button stays off. */
const canRun = computed(() => kind.value !== 'schedule' || Number(route.query.asset_id) > 0 || f.value.category_id > 0)
/** The query of the report as it stands in the form: empty values are left out. */
function query(): Record<string, unknown> {
  const v = f.value
  const assetId = Number(route.query.asset_id) || undefined
  switch (kind.value) {
    case 'register': return { as_of: v.as_of || undefined, category_id: v.category_id || undefined, location_id: v.location_id || undefined }
    case 'schedule': return { asset_id: assetId, category_id: assetId ? undefined : v.category_id || undefined }
    case 'expiring': return { days: Number(v.days) || undefined, months: Number(v.months) || undefined }
    case 'audit-differences': return { result: v.result || undefined, date_from: v.date_from || undefined, date_to: v.date_to || undefined }
    default: return {}
  }
}

async function loadMasters(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null || !canRead.value) return
  try {
    const [c, l] = await Promise.all([
      api.GET('/api/v1/properties/{propertyId}/assets/categories', { params: { path: { propertyId } } }),
      api.GET('/api/v1/properties/{propertyId}/assets/locations', { params: { path: { propertyId } } }),
    ])
    categories.value = c.data?.data ?? []
    locations.value = l.data?.data ?? []
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  }
}

async function load(): Promise<void> {
  const propertyId = pid.value
  error.value = null
  if (propertyId === null || !canRead.value) {
    loaded.value = true
    return
  }
  const k = kind.value
  if (!canRun.value) {
    schedules.value = []
    loaded.value = true
    return
  }
  const get = api.GET as unknown as (path: string, init: object) => Promise<{ data?: Record<string, unknown> }>
  busy.value = true
  try {
    const { data } = await get(PATHS[k], { params: { path: { propertyId }, query: query() } })
    register.value = k === 'register' ? ((data as unknown as AssetRegisterReport) ?? null) : null
    schedules.value = k === 'schedule' ? ((data?.data as AssetSchedule[]) ?? []) : []
    placement.value = k === 'locations' ? ((data as unknown as AssetPlacementReport) ?? null) : null
    expiring.value = k === 'expiring' ? ((data?.data as AssetExpiringRow[]) ?? []) : []
    differences.value = k === 'audit-differences' ? ((data?.data as AssetAuditDifference[]) ?? []) : []
    if (k === 'register' && register.value && !f.value.as_of) f.value.as_of = register.value.as_of
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    busy.value = false
    loaded.value = true
  }
}
watch([pid, kind], () => { void loadMasters(); void load() }, { immediate: true })

async function csv(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null) return
  try {
    await downloadCsv(PATHS[kind.value], { path: { propertyId }, query: query() }, `asset-${kind.value}.csv`)
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  }
}
function print(): void {
  window.print()
}

const placeRows = (rows: AssetPlacementRow[] | undefined): AssetPlacementRow[] => rows ?? []
const th = 'p-2 text-xs font-medium uppercase text-muted-foreground'
</script>

<template>
  <section class="flex flex-col gap-4" :data-testid="`asset-report-${kind}`">
    <PageHeader :title="t(`assets.reports.kind.${kind}.name`)" :description="t(`assets.reports.kind.${kind}.about`)">
      <div class="flex flex-wrap items-center gap-2 print:hidden">
        <Button as-child variant="outline" size="sm"><RouterLink to="/assets/reports">{{ t('assets.reports.back') }}</RouterLink></Button>
        <Button variant="outline" size="sm" data-testid="print" @click="print">{{ t('assets.reports.print') }}</Button>
        <Button variant="outline" size="sm" data-testid="export" :disabled="!canRun" @click="csv">{{ t('assets.reports.csv') }}</Button>
      </div>
    </PageHeader>
    <ErrorNotice :error="error" />
    <p v-if="loaded && !canRead" class="muted m-0">{{ t('assets.reports.denied') }}</p>
    <template v-else>
      <form v-if="kind !== 'locations'" class="flex flex-wrap items-end gap-3 print:hidden" data-testid="report-filters" @submit.prevent="load">
        <template v-if="kind === 'register'">
          <FormField :label="t('assets.reports.asOf')"><template #default="{ id }"><Input :id="id" v-model="f.as_of" type="date" name="as_of" data-testid="f-as-of" /></template></FormField>
          <FormField :label="t('assets.reports.category')"><template #default="{ id }"><NativeSelect :id="id" v-model.number="f.category_id" name="category_id" data-testid="f-category"><option :value="0">{{ t('assets.reports.all') }}</option><option v-for="c in categories" :key="c.id" :value="c.id">{{ c.name }}</option></NativeSelect></template></FormField>
          <FormField :label="t('assets.reports.location')"><template #default="{ id }"><NativeSelect :id="id" v-model.number="f.location_id" name="location_id" data-testid="f-location"><option :value="0">{{ t('assets.reports.all') }}</option><option v-for="l in locations" :key="l.id" :value="l.id">{{ l.name }}</option></NativeSelect></template></FormField>
        </template>
        <template v-else-if="kind === 'schedule'">
          <FormField :label="t('assets.reports.category')"><template #default="{ id }"><NativeSelect :id="id" v-model.number="f.category_id" name="category_id" data-testid="f-category"><option :value="0">{{ t('assets.reports.choose') }}</option><option v-for="c in categories" :key="c.id" :value="c.id">{{ c.name }}</option></NativeSelect></template></FormField>
        </template>
        <template v-else-if="kind === 'expiring'">
          <FormField :label="t('assets.reports.days')"><template #default="{ id }"><Input :id="id" v-model="f.days" type="number" min="1" name="days" class="w-24" data-testid="f-days" /></template></FormField>
          <FormField :label="t('assets.reports.months')"><template #default="{ id }"><Input :id="id" v-model="f.months" type="number" min="1" name="months" class="w-24" data-testid="f-months" /></template></FormField>
        </template>
        <template v-else>
          <FormField :label="t('assets.reports.result')"><template #default="{ id }"><NativeSelect :id="id" v-model="f.result" name="result" data-testid="f-result"><option value="">{{ t('assets.reports.all') }}</option><option v-for="r in ['MOVED', 'UNEXPECTED', 'DAMAGED', 'MISSING']" :key="r" :value="r">{{ t(`assets.audit.result.${r}`) }}</option></NativeSelect></template></FormField>
          <FormField :label="t('assets.reports.from')"><template #default="{ id }"><Input :id="id" v-model="f.date_from" type="date" name="date_from" /></template></FormField>
          <FormField :label="t('assets.reports.to')"><template #default="{ id }"><Input :id="id" v-model="f.date_to" type="date" name="date_to" /></template></FormField>
        </template>
        <Button type="submit" size="sm" :disabled="busy || !canRun" data-testid="report-run">{{ t('assets.reports.run') }}</Button>
      </form>

      <!-- the register as at a date -->
      <Card v-if="kind === 'register' && register" class="overflow-x-auto p-0">
        <table class="w-full border-collapse text-sm tabular-nums" data-testid="register-table">
          <thead><tr class="border-b border-border text-left"><th :class="th">{{ t('assets.reports.col.number') }}</th><th :class="th">{{ t('assets.reports.col.name') }}</th><th :class="th">{{ t('assets.reports.col.category') }}</th><th :class="th">{{ t('assets.reports.col.location') }}</th><th :class="th" class="text-right">{{ t('assets.reports.col.cost') }}</th><th :class="th" class="text-right">{{ t('assets.reports.col.accum') }}</th><th :class="th" class="text-right">{{ t('assets.reports.col.carrying') }}</th><th :class="th">{{ t('assets.reports.col.status') }}</th></tr></thead>
          <tbody>
            <tr v-for="r in register.rows" :key="r.asset_id" class="border-b border-border" :data-testid="`register-row-${r.number}`">
              <td class="p-2"><RouterLink :to="`/assets/${r.asset_id}`">{{ r.number }}</RouterLink></td><td class="p-2">{{ r.name }}</td><td class="p-2">{{ r.category_name }}</td><td class="p-2">{{ r.location_name ?? '–' }}</td>
              <td class="p-2 text-right">{{ formatMoney(r.cost) }}</td><td class="p-2 text-right">{{ formatMoney(r.accum_depr) }}</td><td class="p-2 text-right">{{ formatMoney(r.carrying_value) }}</td><td class="p-2">{{ r.status }}</td>
            </tr>
          </tbody>
          <tfoot><tr class="font-semibold" data-testid="register-total"><td class="p-2" colspan="4">{{ t('assets.reports.totalOf', { n: register.count }) }} · {{ formatDate(register.as_of) }}</td><td class="p-2 text-right">{{ formatMoney(register.cost) }}</td><td class="p-2 text-right">{{ formatMoney(register.accum_depr) }}</td><td class="p-2 text-right">{{ formatMoney(register.carrying_value) }}</td><td /></tr></tfoot>
        </table>
      </Card>

      <!-- the depreciation schedules, commercial and fiscal -->
      <template v-if="kind === 'schedule'">
        <p v-if="loaded && !schedules.length" class="muted m-0" data-testid="schedule-empty">{{ t('assets.reports.scheduleEmpty') }}</p>
        <Card v-for="s in schedules" :key="s.asset_id" class="flex flex-col gap-2 p-4" :data-testid="`schedule-${s.number}`">
          <h2 class="m-0 text-base font-semibold"><RouterLink :to="`/assets/${s.asset_id}`">{{ s.number }}</RouterLink> · {{ s.name }}</h2>
          <div class="overflow-x-auto">
            <table class="w-full border-collapse text-sm tabular-nums">
              <thead><tr class="border-b border-border text-left"><th :class="th">{{ t('assets.schedule.month') }}</th><th :class="th">{{ t('assets.reports.col.status') }}</th><th :class="th" class="text-right">{{ t('assets.schedule.depreciation') }}</th><th :class="th" class="text-right">{{ t('assets.reports.col.accum') }}</th><th v-if="s.fiscal_group" :class="th" class="text-right">{{ t('assets.schedule.fiscalAmount') }}</th><th v-if="s.fiscal_group" :class="th" class="text-right">{{ t('assets.schedule.fiscalAccum') }}</th></tr></thead>
              <tbody>
                <tr v-for="r in s.rows" :key="`${r.kind}-${r.month}`" class="border-b border-border">
                  <td class="p-2">{{ formatMonth(r.month.slice(0, 7)) }}</td><td class="p-2">{{ t(`assets.schedule.kind.${r.kind}`) }}</td><td class="p-2 text-right">{{ formatMoney(r.amount) }}</td><td class="p-2 text-right">{{ formatMoney(r.accum_after) }}</td>
                  <td v-if="s.fiscal_group" class="p-2 text-right">{{ r.fiscal_amount ? formatMoney(r.fiscal_amount) : '–' }}</td><td v-if="s.fiscal_group" class="p-2 text-right">{{ r.fiscal_accum_after ? formatMoney(r.fiscal_accum_after) : '–' }}</td>
                </tr>
              </tbody>
            </table>
          </div>
        </Card>
      </template>

      <!-- by location and by department -->
      <template v-if="kind === 'locations' && placement">
        <Card v-for="part in [{ key: 'locations', rows: placeRows(placement.locations), none: placement.no_location }, { key: 'departments', rows: placeRows(placement.departments), none: placement.no_department }]" :key="part.key" class="flex flex-col gap-2 p-4" :data-testid="`placement-${part.key}`">
          <h2 class="m-0 text-base font-semibold">{{ t(`assets.reports.placement.${part.key}`) }}</h2>
          <div class="overflow-x-auto">
            <table class="w-full border-collapse text-sm tabular-nums">
              <thead><tr class="border-b border-border text-left"><th :class="th">{{ t('assets.reports.placement.name') }}</th><th :class="th" class="text-right">{{ t('assets.reports.placement.assets') }}</th><th :class="th" class="text-right">{{ t('assets.reports.col.cost') }}</th><th :class="th" class="text-right">{{ t('assets.reports.col.accum') }}</th><th :class="th" class="text-right">{{ t('assets.reports.col.carrying') }}</th></tr></thead>
              <tbody>
                <tr v-for="r in part.rows" :key="r.id ?? r.code" class="border-b border-border" :class="r.is_active ? '' : 'text-muted-foreground'">
                  <td class="p-2">{{ r.code }} · {{ r.name }}</td><td class="p-2 text-right">{{ part.key === 'departments' ? r.total_assets : r.assets }}</td>
                  <td class="p-2 text-right">{{ money(part.key === 'departments' ? r.total_cost : r.cost) }}</td><td class="p-2 text-right">{{ money(part.key === 'departments' ? r.total_accum_depr : r.accum_depr) }}</td><td class="p-2 text-right">{{ money(part.key === 'departments' ? r.total_carrying_value : r.carrying_value) }}</td>
                </tr>
                <tr class="border-b border-border text-muted-foreground"><td class="p-2">{{ t(`assets.reports.placement.${part.key === 'locations' ? 'noLocation' : 'noDepartment'}`) }}</td><td class="p-2 text-right">{{ part.none.assets }}</td><td class="p-2 text-right">{{ money(part.none.cost) }}</td><td class="p-2 text-right">{{ money(part.none.accum_depr) }}</td><td class="p-2 text-right">{{ money(part.none.carrying_value) }}</td></tr>
              </tbody>
            </table>
          </div>
        </Card>
      </template>

      <!-- warranties and lives that end -->
      <Card v-if="kind === 'expiring'" class="overflow-x-auto p-0">
        <p v-if="loaded && !expiring.length" class="muted m-3" data-testid="expiring-empty">{{ t('assets.reports.expiringEmpty') }}</p>
        <table v-else class="w-full border-collapse text-sm tabular-nums" data-testid="expiring-table">
          <thead><tr class="border-b border-border text-left"><th :class="th">{{ t('assets.reports.col.number') }}</th><th :class="th">{{ t('assets.reports.col.name') }}</th><th :class="th">{{ t('assets.reports.col.location') }}</th><th :class="th">{{ t('assets.reports.expiring.warranty') }}</th><th :class="th" class="text-right">{{ t('assets.reports.expiring.monthsLeft') }}</th><th :class="th" class="text-right">{{ t('assets.reports.col.carrying') }}</th><th :class="th">{{ t('assets.reports.col.status') }}</th></tr></thead>
          <tbody>
            <tr v-for="r in expiring" :key="r.asset_id" class="border-b border-border" :data-testid="`expiring-row-${r.number}`">
              <td class="p-2"><RouterLink :to="`/assets/${r.asset_id}`">{{ r.number }}</RouterLink></td><td class="p-2">{{ r.name }}</td><td class="p-2">{{ r.location_name ?? '–' }}</td>
              <td class="p-2">{{ r.warranty_until ? formatDate(r.warranty_until) : '–' }}</td><td class="p-2 text-right">{{ r.months_left ?? '–' }}</td><td class="p-2 text-right">{{ formatMoney(r.carrying_value) }}</td>
              <td class="flex flex-wrap gap-1 p-2">
                <Badge v-if="r.warranty_ending" variant="warning">{{ t('assets.reports.expiring.warrantyEnding') }}</Badge><Badge v-if="r.warranty_expired" variant="outline">{{ t('assets.reports.expiring.warrantyExpired') }}</Badge>
                <Badge v-if="r.life_ending" variant="warning">{{ t('assets.reports.expiring.lifeEnding') }}</Badge><Badge v-if="r.fully_depreciated" variant="outline">{{ t('assets.reports.expiring.fully') }}</Badge>
              </td>
            </tr>
          </tbody>
        </table>
      </Card>

      <!-- the differences of the stock-takes -->
      <Card v-if="kind === 'audit-differences'" class="overflow-x-auto p-0">
        <p v-if="loaded && !differences.length" class="muted m-3" data-testid="diff-empty">{{ t('assets.reports.diffEmpty') }}</p>
        <table v-else class="w-full border-collapse text-sm tabular-nums" data-testid="diff-table">
          <thead><tr class="border-b border-border text-left"><th :class="th">{{ t('assets.audit.number') }}</th><th :class="th">{{ t('assets.reports.col.number') }}</th><th :class="th">{{ t('assets.reports.col.name') }}</th><th :class="th">{{ t('assets.reports.result') }}</th><th :class="th">{{ t('assets.audit.expectedAt') }}</th><th :class="th">{{ t('assets.audit.foundAt') }}</th><th :class="th" class="text-right">{{ t('assets.reports.col.carrying') }}</th><th :class="th">{{ t('assets.audit.proposal') }}</th></tr></thead>
          <tbody>
            <tr v-for="(r, i) in differences" :key="`${r.audit_id}-${r.asset_id}-${i}`" class="border-b border-border" :data-testid="`diff-row-${r.asset_number}`">
              <td class="p-2">{{ r.audit_number }}</td><td class="p-2"><RouterLink :to="`/assets/${r.asset_id}`">{{ r.asset_number }}</RouterLink></td><td class="p-2">{{ r.asset_name }}</td>
              <td class="p-2"><Badge :variant="r.result === 'MISSING' || r.result === 'DAMAGED' ? 'warning' : 'outline'">{{ t(`assets.audit.result.${r.result}`) }}</Badge></td>
              <td class="p-2">{{ r.expected_location_name ?? '–' }}</td><td class="p-2">{{ r.found_location_name ?? '–' }}</td><td class="p-2 text-right">{{ formatMoney(r.carrying_value) }}</td>
              <td class="p-2">{{ r.proposal_kind ? `${t(`assets.audit.proposalKind.${r.proposal_kind}`)} · ${t(`assets.audit.proposalStatus.${r.proposal_status}`)}` : '–' }}</td>
            </tr>
          </tbody>
        </table>
      </Card>
    </template>
  </section>
</template>
