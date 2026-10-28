<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { RouterLink, useRoute } from 'vue-router'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { AssetCategory, AssetDetail, AssetHistoryEntry, AssetLocation, AssetRepair, AssetSchedule } from '@/api/types'
import DepartmentSelect from '@/components/app/DepartmentSelect.vue'
import ErrorNotice from '@/components/app/ErrorNotice.vue'
import FormField from '@/components/app/FormField.vue'
import PageHeader from '@/components/app/PageHeader.vue'
import StatusBadge from '@/components/app/StatusBadge.vue'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card } from '@/components/ui/card'
import { Dialog, DialogContent, DialogDescription, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { NativeSelect } from '@/components/ui/native-select'
import { useAssetAccess } from '@/composables/useAssetAccess'
import { toast } from '@/composables/useToast'
import { t, te } from '@/i18n'
import { usePropertyStore } from '@/stores/property'
import { formatDate, formatMoney, formatMonth } from '@/utils/format'
import AssetDisposeDialog from './AssetDisposeDialog.vue'
import AssetMoveDialog from './AssetMoveDialog.vue'
import AssetReviseDialog from './AssetReviseDialog.vue'
import { fromMinor, toMinor } from './split'

/** One asset (docs/architecture/37-fixed-assets.md 9, mockup 52, the part of PR 1): its data, its values, its fiscal group and the history of the register. The schedule comes with the depreciation. */
const route = useRoute()
const { pid, canRead, canManage, canDispose } = useAssetAccess()
const property = usePropertyStore()
const decimals = computed(() => property.current?.currency_decimals ?? 0)
const assetId = computed(() => Number(route.params.id))
const asset = ref<AssetDetail | null>(null)
const locations = ref<AssetLocation[]>([])
const loaded = ref(false)
const error = ref<ApiError | null>(null)

async function load(): Promise<void> {
  const propertyId = pid.value
  error.value = null
  if (propertyId === null || !canRead.value) {
    loaded.value = true
    return
  }
  try {
    const { data } = await api.GET('/api/v1/properties/{propertyId}/assets/{id}', { params: { path: { propertyId, id: assetId.value } } })
    asset.value = data ?? null
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    loaded.value = true
  }
  await loadSchedule()
  await loadHistory()
  await loadRepairs()
}
watch([pid, assetId], load, { immediate: true })

// the commercial depreciation schedule: the months posted and the months projected, summed by year
const schedule = ref<AssetSchedule | null>(null)
const tab = ref<'schedule' | 'history' | 'repairs'>('schedule')
const monthly = ref(false)
async function loadSchedule(): Promise<void> {
  const propertyId = pid.value
  schedule.value = null
  if (propertyId === null || !canRead.value || !asset.value) return
  try {
    const { data } = await api.GET('/api/v1/properties/{propertyId}/assets/{id}/schedule', { params: { path: { propertyId, id: assetId.value } } })
    schedule.value = data ?? null
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  }
}
interface YearRow { year: string; months: number; amount: string; accum: string; carrying: string; label: 'POSTED' | 'CURRENT' | 'PROJECTED' | 'OPENING'; fiscal: string | null; fiscalAccum: string | null }
/** The schedule has a fiscal column when the asset has a fiscal group (docs/architecture/37-fixed-assets.md 4.9). */
const hasFiscal = computed(() => (schedule.value?.rows ?? []).some((r) => r.fiscal_amount !== null && r.fiscal_amount !== undefined))
const years = computed<YearRow[]>(() => {
  const out: YearRow[] = []
  const by = new Map<string, { rows: AssetSchedule['rows']; amount: bigint; fiscal: bigint; months: number }>()
  for (const r of schedule.value?.rows ?? []) {
    const y = r.month.slice(0, 4)
    const e = by.get(y) ?? { rows: [], amount: 0n, fiscal: 0n, months: 0 }
    e.rows.push(r)
    if (r.kind !== 'OPENING') e.amount += toMinor(r.amount, decimals.value)
    if (r.fiscal_amount) e.fiscal += toMinor(r.fiscal_amount, decimals.value)
    e.months += r.kind === 'OPENING' ? 0 : r.months
    by.set(y, e)
  }
  for (const [year, e] of by) {
    const last = e.rows[e.rows.length - 1]
    if (!last) continue
    const kinds = new Set(e.rows.map((r) => r.kind))
    const label = kinds.size === 1 && kinds.has('OPENING') ? 'OPENING' : kinds.has('PROJECTED') ? (kinds.has('POSTED') ? 'CURRENT' : 'PROJECTED') : 'POSTED'
    out.push({ year, months: e.months, amount: fromMinor(e.amount, decimals.value), accum: last.accum_after, carrying: last.carrying_after, label, fiscal: hasFiscal.value ? fromMinor(e.fiscal, decimals.value) : null, fiscalAccum: last.fiscal_accum_after ?? null })
  }
  return out
})
const yearLabel = (r: YearRow): string => t(`assets.schedule.label.${r.label}`)

const noDepreciation = computed(() => asset.value?.category_kind === 'LAND' || asset.value?.category_kind === 'CIP')
const statusVariant = computed(() => (asset.value?.status === 'ACTIVE' ? 'success' : 'outline'))
const methodText = computed(() => {
  const a = asset.value
  if (!a || noDepreciation.value || !a.method) return t('assets.life.notDepreciated')
  return `${t(`assets.method.${a.method}`)} · ${t('assets.life.months', { n: a.life_months ?? 0 })}`
})
// what happened to the asset: moves, changes, months of depreciation and disposals, in the order they happened
const history = ref<AssetHistoryEntry[]>([])
async function loadHistory(): Promise<void> {
  const propertyId = pid.value
  history.value = []
  if (propertyId === null || !canRead.value || !asset.value) return
  try {
    const { data } = await api.GET('/api/v1/properties/{propertyId}/assets/{id}/history', { params: { path: { propertyId, id: assetId.value } } })
    history.value = data ?? []
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  }
}
// the repair requests that name the asset, without any cost (docs/architecture/37-fixed-assets.md 4.11)
const repairs = ref<AssetRepair[]>([])
async function loadRepairs(): Promise<void> {
  const propertyId = pid.value
  repairs.value = []
  if (propertyId === null || !canRead.value || !asset.value) return
  try {
    const { data } = await api.GET('/api/v1/properties/{propertyId}/assets/{id}/maintenance', { params: { path: { propertyId, id: assetId.value } } })
    repairs.value = data?.data ?? []
  } catch {
    // the repairs are a side view: a failure here never hides the asset
  }
}
const detail = (e: AssetHistoryEntry, k: string): string => {
  const v = (e.details as Record<string, unknown> | null | undefined)?.[k]
  return v === null || v === undefined || v === '' ? '–' : String(v)
}
function historyText(e: AssetHistoryEntry): string {
  switch (e.kind) {
    case 'MOVE': {
      const parts: string[] = []
      if (detail(e, 'from_location') !== detail(e, 'to_location')) parts.push(t('assets.history.place', { from: detail(e, 'from_location'), to: detail(e, 'to_location') }))
      if (detail(e, 'from_department') !== detail(e, 'to_department')) parts.push(t('assets.history.department', { from: detail(e, 'from_department'), to: detail(e, 'to_department') }))
      return `${t('assets.history.kind.MOVE')}: ${parts.join('; ')}`
    }
    case 'LIFE': case 'RESIDUAL': case 'METHOD': case 'CATEGORY':
      return `${t(`assets.history.kind.${e.kind}`)}: ${detail(e, 'old')} → ${detail(e, 'new')}`
    case 'CAPITALIZE':
      return `${t('assets.history.kind.CAPITALIZE')}${detail(e, 'amount') !== '–' ? `: ${formatMoney(detail(e, 'amount'))}` : ''}`
    case 'DEPRECIATION':
      return `${t('assets.history.kind.DEPRECIATION')} ${detail(e, 'month') !== '–' ? formatMonth(detail(e, 'month').slice(0, 7)) : ''}: ${formatMoney(detail(e, 'amount'))}${Number(detail(e, 'months')) > 1 ? ` (${t('assets.schedule.months', { n: detail(e, 'months') })})` : ''}`
    case 'DISPOSAL': case 'DISPOSAL_REQUEST': {
      const k = detail(e, 'kind')
      return `${t(`assets.history.kind.${e.kind}`)}${te(`assets.dispose.kindShort.${k}`) ? `: ${t(`assets.dispose.kindShort.${k}`)}` : ''}`
    }
    default:
      return te(`assets.history.kind.${e.kind}`) ? t(`assets.history.kind.${e.kind}`) : e.kind
  }
}
/** The reason of an entry: a disposal keeps a code (SOLD, DAMAGED...), shown only when it has a text. */
function historyReason(e: AssetHistoryEntry): string {
  if (e.kind === 'DISPOSAL' || e.kind === 'DISPOSAL_REQUEST') return e.reason && te(`assets.dispose.reasonCode.${e.reason}`) ? t(`assets.dispose.reasonCode.${e.reason}`) : ''
  return e.reason ?? ''
}

const kindText = (kind: string): string => (te(`assets.ledger.kind.${kind}`) ? t(`assets.ledger.kind.${kind}`) : kind)

// the details
const editing = ref(false)
const busy = ref(false)
const formError = ref<ApiError | null>(null)
const form = ref({ name: '', location_id: 0, department_id: null as number | null, serial_no: '', warranty_until: '', notes: '', in_service_date: '', start_rule: 'IN_SERVICE_MONTH' })
/** The start of the depreciation can change until the first month has been posted. */
const startEditable = computed(() => !!asset.value && !noDepreciation.value && asset.value.dep_months_done === 0)
async function openEdit(): Promise<void> {
  const a = asset.value
  const propertyId = pid.value
  if (!a || propertyId === null) return
  form.value = { name: a.name, location_id: a.location_id ?? 0, department_id: a.department_id ?? null, serial_no: a.serial_no ?? '', warranty_until: a.warranty_until ?? '', notes: a.notes ?? '', in_service_date: a.in_service_date, start_rule: a.start_rule }
  formError.value = null
  editing.value = true
  try {
    const { data } = await api.GET('/api/v1/properties/{propertyId}/assets/locations', { params: { path: { propertyId } } })
    locations.value = (data?.data ?? []).filter((l) => l.is_active || l.id === a.location_id)
  } catch {
    locations.value = []
  }
}
async function save(): Promise<void> {
  const a = asset.value
  const propertyId = pid.value
  if (!a || propertyId === null) return
  busy.value = true
  formError.value = null
  try {
    const f = form.value
    const { data } = await api.PATCH('/api/v1/properties/{propertyId}/assets/{id}', {
      params: { path: { propertyId, id: a.id } },
      body: { name: f.name.trim(), location_id: f.location_id || null, department_id: f.department_id, serial_no: f.serial_no.trim() || null, warranty_until: f.warranty_until || null, notes: f.notes.trim() || null, version: a.version,
        ...(startEditable.value && f.in_service_date !== a.in_service_date ? { in_service_date: f.in_service_date } : {}),
        ...(startEditable.value && f.start_rule !== a.start_rule ? { start_rule: f.start_rule } : {}),
      },
    })
    if (data) asset.value = data
    editing.value = false
    toast.success(t('assets.asset.saved'))
  } catch (e) {
    formError.value = e instanceof ApiError ? e : null
  } finally {
    busy.value = false
  }
}

// cancel
const cancelling = ref(false)
const reason = ref('')
async function cancelAsset(): Promise<void> {
  const a = asset.value
  const propertyId = pid.value
  if (!a || propertyId === null) return
  busy.value = true
  formError.value = null
  try {
    const { data } = await api.POST('/api/v1/properties/{propertyId}/assets/{id}/cancel', { params: { path: { propertyId, id: a.id } }, body: { reason: reason.value.trim() } })
    if (data) asset.value = data
    cancelling.value = false
    toast.success(t('assets.asset.cancelled'))
  } catch (e) {
    formError.value = e instanceof ApiError ? e : null
  } finally {
    busy.value = false
  }
}

// the changes of PR 3: move, change the terms or the category, add value, dispose
const moving = ref(false)
const revising = ref<'TERMS' | 'CATEGORY' | null>(null)
const disposing = ref(false)
const categories = ref<AssetCategory[]>([])
const actionable = computed(() => !!asset.value && asset.value.status === 'ACTIVE' && !asset.value.disposal_pending)
const isCip = computed(() => asset.value?.category_kind === 'CIP')
async function openMove(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null) return
  try {
    const { data } = await api.GET('/api/v1/properties/{propertyId}/assets/locations', { params: { path: { propertyId } } })
    locations.value = data?.data ?? []
  } catch {
    locations.value = []
  }
  moving.value = true
}
async function openRevise(kind: 'TERMS' | 'CATEGORY'): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null) return
  try {
    const { data } = await api.GET('/api/v1/properties/{propertyId}/assets/categories', { params: { path: { propertyId } } })
    categories.value = data?.data ?? []
  } catch {
    categories.value = []
  }
  revising.value = kind
}
async function changed(next: AssetDetail): Promise<void> {
  moving.value = false
  revising.value = null
  asset.value = next
  await loadSchedule()
  await loadHistory()
  await loadRepairs()
}
async function disposed(): Promise<void> {
  disposing.value = false
  await load()
}
</script>

<template>
  <ErrorNotice v-if="error" :error="error" inline />
  <p v-if="pid === null" class="muted">{{ t('setup.selectProperty') }}</p>
  <p v-else-if="!canRead" class="muted" data-testid="no-access">{{ t('assets.noAccess') }}</p>
  <template v-else-if="asset">
    <RouterLink to="/assets" class="text-sm text-primary hover:underline" data-testid="back">← {{ t('assets.register.title') }}</RouterLink>
    <PageHeader :title="asset.name" :description="`${asset.number} · ${asset.category_name}`">
      <template #actions>
        <Badge :variant="statusVariant" data-testid="asset-status">{{ t(`assets.status.${asset.status}`) }}</Badge>
        <Badge v-if="asset.disposal_pending" variant="warning" data-testid="asset-disposal-pending">{{ t('assets.dispose.pendingBadge') }}</Badge>
        <Button v-if="canManage && asset.status === 'ACTIVE'" variant="outline" data-testid="asset-edit" @click="openEdit">{{ t('assets.asset.edit') }}</Button>
        <Button v-if="canManage && asset.status === 'ACTIVE'" variant="outline" data-testid="asset-cancel" @click="cancelling = true; formError = null; reason = ''">{{ t('assets.asset.cancel') }}</Button>
        <Button as-child variant="outline"><RouterLink :to="{ path: '/assets/labels', query: { ids: String(asset.id) } }" data-testid="asset-label">{{ t('assets.asset.label') }}</RouterLink></Button>
      </template>
    </PageHeader>
    <p v-if="asset.disposal_pending" class="alert warning m-0" role="status" data-testid="disposal-pending-note">{{ t('assets.dispose.pendingNote') }} <RouterLink to="/assets/disposals" class="font-medium text-primary hover:underline">{{ t('assets.dispose.pendingLink') }}</RouterLink></p>
    <div v-if="actionable && (canManage || canDispose)" class="flex flex-wrap gap-2" data-testid="asset-actions">
      <Button v-if="canManage" variant="outline" size="sm" data-testid="act-move" @click="openMove">{{ t('assets.actions.move') }}</Button>
      <Button v-if="canManage && !noDepreciation" variant="outline" size="sm" data-testid="act-terms" @click="openRevise('TERMS')">{{ t('assets.actions.terms') }}</Button>
      <Button v-if="canManage && !noDepreciation" as-child variant="outline" size="sm"><RouterLink :to="{ path: '/assets/waiting', query: { addTo: String(asset.id) } }" data-testid="act-addvalue">{{ t('assets.actions.addValue') }}</RouterLink></Button>
      <Button v-if="canManage" variant="outline" size="sm" data-testid="act-category" @click="openRevise('CATEGORY')">{{ isCip ? t('assets.actions.finish') : t('assets.actions.category') }}</Button>
      <Button v-if="canDispose" variant="outline" size="sm" data-testid="act-dispose" @click="disposing = true">{{ t('assets.actions.dispose') }}</Button>
    </div>
    <p class="text-sm text-muted-foreground" data-testid="asset-source">
      <template v-if="asset.source === 'OPENING'">{{ t('assets.asset.fromOpening', { number: asset.opening_import_number ?? '' }) }}</template>
      <template v-else-if="asset.source_link?.bill_number">{{ t('assets.asset.fromBill', { bill: asset.source_link.bill_number, line: asset.source_link.bill_line_no ?? '', journal: asset.source_link.journal_number }) }}</template>
      <template v-else-if="asset.source_link">{{ t('assets.asset.fromJournalNumber', { journal: asset.source_link.journal_number }) }}</template>
      <template v-else>{{ t('assets.asset.fromJournal') }}</template>
    </p>
    <div class="grid gap-4 lg:grid-cols-2">
      <Card class="flex flex-col gap-1 p-4" data-testid="asset-data">
        <h2 class="m-0 mb-1 text-base font-semibold">{{ t('assets.asset.data') }}</h2>
        <dl class="m-0 grid grid-cols-[10rem_1fr] gap-x-3 gap-y-1 text-sm">
          <dt class="text-muted-foreground">{{ t('assets.col.place') }}</dt><dd class="m-0">{{ asset.location_name ?? '–' }}</dd>
          <dt class="text-muted-foreground">{{ t('assets.col.department') }}</dt><dd class="m-0">{{ asset.department_name ?? '–' }}</dd>
          <dt class="text-muted-foreground">{{ t('assets.asset.supplier') }}</dt><dd class="m-0">{{ asset.supplier_name ?? '–' }}</dd>
          <dt class="text-muted-foreground">{{ t('assets.asset.serial') }}</dt><dd class="m-0">{{ asset.serial_no ?? '–' }}</dd>
          <dt class="text-muted-foreground">{{ t('assets.asset.acquired') }}</dt><dd class="m-0">{{ formatDate(asset.acquisition_date) }}</dd>
          <dt class="text-muted-foreground">{{ t('assets.col.inService') }}</dt><dd class="m-0">{{ formatDate(asset.in_service_date) }}</dd>
          <dt class="text-muted-foreground">{{ t('assets.asset.depStart') }}</dt><dd class="m-0">{{ noDepreciation ? '–' : asset.first_dep_month ? formatMonth(asset.first_dep_month.slice(0, 7)) : '–' }}</dd>
          <dt class="text-muted-foreground">{{ t('assets.asset.warranty') }}</dt><dd class="m-0">{{ asset.warranty_until ? formatDate(asset.warranty_until) : '–' }}</dd>
          <template v-if="asset.notes"><dt class="text-muted-foreground">{{ t('assets.asset.notes') }}</dt><dd class="m-0">{{ asset.notes }}</dd></template>
        </dl>
      </Card>
      <Card class="flex flex-col gap-1 p-4" data-testid="asset-values">
        <h2 class="m-0 mb-1 text-base font-semibold">{{ t('assets.asset.values') }}</h2>
        <dl class="m-0 grid grid-cols-[10rem_1fr] gap-x-3 gap-y-1 text-sm tabular-nums">
          <dt class="text-muted-foreground">{{ t('assets.col.cost') }}</dt><dd class="m-0 font-semibold" data-testid="value-cost">{{ formatMoney(asset.cost) }}</dd>
          <dt class="text-muted-foreground">{{ t('assets.asset.residual') }}</dt><dd class="m-0">{{ formatMoney(asset.residual_value) }}</dd>
          <dt class="text-muted-foreground">{{ t('assets.asset.methodLife') }}</dt><dd class="m-0">{{ methodText }}</dd>
          <dt class="text-muted-foreground">{{ t('assets.asset.accumMonths', { n: asset.dep_months_done }) }}</dt><dd class="m-0" data-testid="value-accum">{{ formatMoney(asset.accum_depr) }}</dd>
          <dt class="font-semibold">{{ t('assets.col.carrying') }}</dt><dd class="m-0 font-semibold" data-testid="value-carrying">{{ formatMoney(asset.carrying_value) }}</dd>
          <template v-if="asset.fully_depreciated"><dt /><dd class="m-0"><Badge variant="outline" data-testid="fully-depreciated">{{ t('assets.life.fullyDepreciated') }}</Badge></dd></template>
          <template v-if="asset.unit_count > 1"><dt class="text-muted-foreground">{{ t('assets.asset.unitCount') }}</dt><dd class="m-0">{{ asset.unit_count }}</dd></template>
        </dl>
        <h3 class="m-0 mt-3 text-sm font-semibold">{{ t('assets.asset.fiscal') }}</h3>
        <dl class="m-0 grid grid-cols-[10rem_1fr] gap-x-3 gap-y-1 text-sm tabular-nums" data-testid="asset-fiscal">
          <dt class="text-muted-foreground">{{ t('assets.asset.fiscalGroup') }}</dt><dd class="m-0">{{ asset.fiscal_group === 'NONE' ? '–' : `${t(`assets.fiscalGroup.${asset.fiscal_group}`)}${asset.fiscal_method ? ` · ${t(`assets.method.${asset.fiscal_method}`)}` : ''}` }}</dd>
          <template v-if="asset.fiscal_accum_opening !== null && asset.fiscal_accum_opening !== undefined">
            <dt class="text-muted-foreground">{{ t('assets.asset.fiscalOpening', { date: formatDate(asset.fiscal_opening_as_of) }) }}</dt>
            <dd class="m-0">{{ formatMoney(asset.fiscal_accum_opening) }}<Badge v-if="asset.fiscal_opening_estimated" variant="warning" class="ml-2" data-testid="fiscal-estimated">{{ t('assets.asset.fiscalEstimated') }}</Badge></dd>
          </template>
        </dl>
      </Card>
    </div>
    <div class="flex gap-2" role="tablist">
      <button type="button" role="tab" :aria-selected="tab === 'schedule'" class="min-h-9 rounded-full border px-3 text-sm" :class="tab === 'schedule' ? 'border-primary bg-primary text-primary-foreground' : 'border-border bg-card'" data-testid="tab-schedule" @click="tab = 'schedule'">{{ t('assets.schedule.tab') }}</button>
      <button type="button" role="tab" :aria-selected="tab === 'history'" class="min-h-9 rounded-full border px-3 text-sm" :class="tab === 'history' ? 'border-primary bg-primary text-primary-foreground' : 'border-border bg-card'" data-testid="tab-history" @click="tab = 'history'">{{ t('assets.asset.history') }}</button>
      <button type="button" role="tab" :aria-selected="tab === 'repairs'" class="min-h-9 rounded-full border px-3 text-sm" :class="tab === 'repairs' ? 'border-primary bg-primary text-primary-foreground' : 'border-border bg-card'" data-testid="tab-repairs" @click="tab = 'repairs'">{{ t('assets.repairs.tab') }}</button>
    </div>
    <Card v-if="tab === 'schedule'" class="flex flex-col gap-2 p-4" data-testid="asset-schedule">
      <div class="flex flex-wrap items-center justify-between gap-2">
        <h2 class="m-0 text-base font-semibold">{{ t('assets.schedule.title') }}</h2>
        <Button v-if="schedule?.depreciable" size="sm" variant="outline" :aria-pressed="monthly" data-testid="schedule-toggle" @click="monthly = !monthly">{{ monthly ? t('assets.schedule.byYear') : t('assets.schedule.byMonth') }}</Button>
      </div>
      <p v-if="!schedule || !schedule.depreciable" class="muted m-0" data-testid="schedule-none">{{ t('assets.schedule.none') }}</p>
      <div v-else class="overflow-x-auto">
        <table v-if="!monthly" class="w-full border-collapse text-sm tabular-nums" data-testid="schedule-years">
          <thead><tr class="text-left text-xs uppercase text-muted-foreground"><th class="p-2">{{ t('assets.schedule.year') }}</th><th class="p-2 text-right">{{ t('assets.schedule.depreciation') }}</th><th class="p-2 text-right">{{ t('assets.col.accum') }}</th><th class="p-2 text-right">{{ t('assets.col.carrying') }}</th><th v-if="hasFiscal" class="p-2 text-right" data-testid="fiscal-col">{{ t('assets.schedule.fiscalAmount') }}</th><th v-if="hasFiscal" class="p-2 text-right">{{ t('assets.schedule.fiscalAccum') }}</th></tr></thead>
          <tbody>
            <tr v-for="r in years" :key="r.year" class="border-t border-border" :data-testid="`schedule-year-${r.year}`">
              <td class="p-2">{{ r.year }}<span class="text-xs text-muted-foreground"> · {{ r.label === 'OPENING' ? '' : t('assets.schedule.months', { n: r.months }) }} {{ yearLabel(r) }}</span></td>
              <td class="p-2 text-right">{{ formatMoney(r.amount) }}</td><td class="p-2 text-right">{{ formatMoney(r.accum) }}</td><td class="p-2 text-right">{{ formatMoney(r.carrying) }}</td>
              <td v-if="hasFiscal" class="p-2 text-right">{{ r.fiscal === null ? '–' : formatMoney(r.fiscal) }}</td><td v-if="hasFiscal" class="p-2 text-right">{{ r.fiscalAccum === null ? '–' : formatMoney(r.fiscalAccum) }}</td>
            </tr>
          </tbody>
        </table>
        <table v-else class="w-full border-collapse text-sm tabular-nums" data-testid="schedule-months">
          <thead><tr class="text-left text-xs uppercase text-muted-foreground"><th class="p-2">{{ t('assets.schedule.month') }}</th><th class="p-2">{{ t('assets.col.status') }}</th><th class="p-2 text-right">{{ t('assets.schedule.depreciation') }}</th><th class="p-2 text-right">{{ t('assets.col.accum') }}</th><th class="p-2 text-right">{{ t('assets.col.carrying') }}</th><th v-if="hasFiscal" class="p-2 text-right">{{ t('assets.schedule.fiscalAmount') }}</th><th v-if="hasFiscal" class="p-2 text-right">{{ t('assets.schedule.fiscalAccum') }}</th></tr></thead>
          <tbody>
            <tr v-for="r in schedule.rows" :key="`${r.kind}-${r.month}`" class="border-t border-border" :data-testid="`schedule-month-${r.month.slice(0, 7)}`">
              <td class="p-2">{{ formatMonth(r.month.slice(0, 7)) }}<span v-if="r.months > 1" class="text-xs text-muted-foreground"> · {{ t('assets.schedule.months', { n: r.months }) }}</span></td>
              <td class="p-2"><Badge :variant="r.kind === 'PROJECTED' ? 'outline' : 'success'">{{ t(`assets.schedule.kind.${r.kind}`) }}</Badge><span v-if="r.journal_number" class="ml-2 text-xs text-muted-foreground">{{ r.journal_number }}</span></td>
              <td class="p-2 text-right">{{ formatMoney(r.amount) }}</td><td class="p-2 text-right">{{ formatMoney(r.accum_after) }}</td><td class="p-2 text-right">{{ formatMoney(r.carrying_after) }}</td>
              <td v-if="hasFiscal" class="p-2 text-right">{{ r.fiscal_amount ? formatMoney(r.fiscal_amount) : '–' }}</td><td v-if="hasFiscal" class="p-2 text-right">{{ r.fiscal_accum_after ? formatMoney(r.fiscal_accum_after) : '–' }}</td>
            </tr>
          </tbody>
        </table>
      </div>
      <p v-if="schedule?.depreciable" class="m-0 text-xs text-muted-foreground">{{ t('assets.schedule.note', { residual: formatMoney(schedule.residual_value) }) }}</p>
      <p v-if="hasFiscal" class="m-0 text-xs text-muted-foreground" data-testid="schedule-fiscal-note">{{ t('assets.schedule.fiscalNote') }} <RouterLink to="/assets/reports/fiscal">{{ t('assets.schedule.fiscalLink') }}</RouterLink></p>
    </Card>
    <Card v-else-if="tab === 'history'" class="flex flex-col gap-2 p-4" data-testid="asset-history">
      <h2 class="m-0 text-base font-semibold">{{ t('assets.asset.history') }}</h2>
      <ul v-if="history.length" class="m-0 flex list-none flex-col gap-1 p-0 text-sm" data-testid="history-list">
        <li v-for="(e, i) in history" :key="`${e.kind}-${e.at}-${i}`" class="flex flex-wrap items-baseline gap-x-3 border-b border-border py-1" :data-testid="`history-${i}`">
          <b class="w-28 font-medium">{{ formatDate(e.date) }}</b>
          <span class="flex-1">{{ historyText(e) }}<span v-if="historyReason(e)" class="text-muted-foreground"> · {{ historyReason(e) }}</span></span>
          <span v-if="e.journal_number" class="text-xs text-muted-foreground">{{ e.journal_number }}</span>
          <span v-if="e.by" class="text-xs text-muted-foreground">{{ e.by }}</span>
        </li>
      </ul>
      <h3 v-if="asset.ledger.length" class="m-0 mt-2 text-sm font-semibold">{{ t('assets.asset.ledgerTitle') }}</h3>
      <p v-if="!asset.ledger.length && !history.length" class="muted m-0">{{ t('assets.asset.noHistory') }}</p>
      <ul v-else class="m-0 flex list-none flex-col gap-1 p-0 text-sm">
        <li v-for="e in asset.ledger" :key="e.id" class="flex flex-wrap items-baseline gap-x-3 border-b border-border py-1" :data-testid="`ledger-${e.id}`">
          <b class="w-28 font-medium">{{ formatDate(e.event_date) }}</b>
          <span class="flex-1">{{ kindText(e.kind) }}</span>
          <span class="tabular-nums">{{ t('assets.asset.costDelta') }} {{ formatMoney(e.cost_delta) }}</span>
          <span class="tabular-nums">{{ t('assets.asset.accumDelta') }} {{ formatMoney(e.accum_delta) }}</span>
        </li>
      </ul>
    </Card>
    <Card v-else class="flex flex-col gap-2 p-4" data-testid="asset-repairs">
      <h2 class="m-0 text-base font-semibold">{{ t('assets.repairs.title') }}</h2>
      <p v-if="!repairs.length" class="muted m-0" data-testid="repairs-none">{{ t('assets.repairs.none') }}</p>
      <ul v-else class="m-0 flex list-none flex-col gap-1 p-0 text-sm" data-testid="repairs-list">
        <li v-for="r in repairs" :key="r.id" class="flex flex-wrap items-baseline gap-x-3 border-b border-border py-1" :data-testid="`repair-${r.request_number}`">
          <b class="w-28 font-medium">{{ formatDate(r.business_date) }}</b>
          <span class="flex-1">{{ r.request_number }} · {{ r.description }}<span v-if="r.resolution_note" class="block text-xs text-muted-foreground">{{ r.resolution_note }}</span></span>
          <StatusBadge domain="work" :status="r.status" />
          <span v-if="r.location || r.room_number" class="text-xs text-muted-foreground">{{ r.location ?? r.room_number }}</span>
        </li>
      </ul>
      <p class="m-0 text-xs text-muted-foreground">{{ t('assets.repairs.noCost') }}</p>
    </Card>
  </template>
  <p v-else-if="loaded && !error" class="muted">{{ t('assets.asset.notFound') }}</p>

  <AssetMoveDialog v-if="pid !== null" :asset="moving ? asset : null" :locations="locations" :property-id="pid" @close="moving = false" @done="changed" />
  <AssetReviseDialog v-if="pid !== null" :asset="revising ? asset : null" :categories="categories" :property-id="pid" :initial-kind="revising ?? 'TERMS'" @close="revising = null" @done="changed" />
  <AssetDisposeDialog v-if="pid !== null" :asset="disposing ? asset : null" :property-id="pid" @close="disposing = false" @done="disposed" />

  <Dialog :open="editing" @update:open="(v) => (editing = v)">
    <DialogContent class="max-h-[90dvh] max-w-lg overflow-y-auto p-5" data-testid="edit-dialog" @interact-outside.prevent>
      <form novalidate class="flex flex-col gap-3" @submit.prevent="save">
        <DialogTitle>{{ t('assets.asset.edit') }}</DialogTitle>
        <DialogDescription>{{ t('assets.asset.editHint') }}</DialogDescription>
        <ErrorNotice v-if="formError" :error="formError" inline />
        <FormField :label="t('assets.asset.name')" required :error="formError?.fieldMessage('name')"><template #default="{ id }"><Input :id="id" v-model="form.name" name="name" maxlength="150" /></template></FormField>
        <FormField :label="t('assets.col.place')">
          <template #default="{ id }">
            <NativeSelect :id="id" v-model.number="form.location_id" name="location_id">
              <option :value="0">–</option>
              <option v-for="l in locations" :key="l.id" :value="l.id">{{ l.name }}</option>
            </NativeSelect>
          </template>
        </FormField>
        <FormField :label="t('assets.col.department')"><template #default="{ id }"><DepartmentSelect :id="id" v-model="form.department_id" /></template></FormField>
        <div class="grid gap-3 sm:grid-cols-2">
          <FormField :label="t('assets.asset.serial')"><template #default="{ id }"><Input :id="id" v-model="form.serial_no" name="serial_no" maxlength="100" /></template></FormField>
          <FormField :label="t('assets.asset.warranty')" :error="formError?.fieldMessage('warranty_until')"><template #default="{ id }"><Input :id="id" v-model="form.warranty_until" name="warranty_until" type="date" /></template></FormField>
        </div>
        <FormField :label="t('assets.asset.notes')"><template #default="{ id }"><Input :id="id" v-model="form.notes" name="notes" maxlength="500" /></template></FormField>
        <div v-if="startEditable" class="grid gap-3 sm:grid-cols-2" data-testid="edit-start">
          <FormField :label="t('assets.col.inService')" :error="formError?.fieldMessage('in_service_date')"><template #default="{ id }"><Input :id="id" v-model="form.in_service_date" name="in_service_date" type="date" /></template></FormField>
          <FormField :label="t('assets.make.startRule')" :hint="t('assets.make.startHint')" :error="formError?.fieldMessage('start_rule')">
            <template #default="{ id }"><NativeSelect :id="id" v-model="form.start_rule" name="start_rule"><option v-for="r in ['IN_SERVICE_MONTH', 'NEXT_MONTH']" :key="r" :value="r">{{ t(`assets.startRule.${r}`) }}</option></NativeSelect></template>
          </FormField>
        </div>
        <div class="flex justify-end gap-2"><Button type="button" variant="outline" @click="editing = false">{{ t('common.cancel') }}</Button><Button type="submit" :disabled="busy || !form.name.trim()" data-testid="edit-save">{{ t('common.save') }}</Button></div>
      </form>
    </DialogContent>
  </Dialog>

  <Dialog :open="cancelling" @update:open="(v) => (cancelling = v)">
    <DialogContent class="max-w-md p-5" data-testid="cancel-dialog" @interact-outside.prevent>
      <form novalidate class="flex flex-col gap-3" @submit.prevent="cancelAsset">
        <DialogTitle>{{ t('assets.asset.cancelTitle') }}</DialogTitle>
        <DialogDescription>{{ t('assets.asset.cancelHint') }}</DialogDescription>
        <ErrorNotice v-if="formError" :error="formError" inline />
        <FormField :label="t('assets.asset.reason')" required :error="formError?.fieldMessage('reason')"><template #default="{ id }"><Input :id="id" v-model="reason" name="reason" maxlength="300" /></template></FormField>
        <div class="flex justify-end gap-2"><Button type="button" variant="outline" @click="cancelling = false">{{ t('common.cancel') }}</Button><Button type="submit" variant="destructive" :disabled="busy || !reason.trim()" data-testid="cancel-confirm">{{ t('assets.asset.cancel') }}</Button></div>
      </form>
    </DialogContent>
  </Dialog>
</template>
