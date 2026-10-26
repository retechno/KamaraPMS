<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { RouterLink, useRouter } from 'vue-router'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { Asset, AssetCategory, AssetLocation, AssetPage } from '@/api/types'
import DataTable, { type Column } from '@/components/app/DataTable.vue'
import DepartmentSelect from '@/components/app/DepartmentSelect.vue'
import ErrorNotice from '@/components/app/ErrorNotice.vue'
import KpiCard from '@/components/app/KpiCard.vue'
import PageHeader from '@/components/app/PageHeader.vue'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { NativeSelect } from '@/components/ui/native-select'
import { useAssetAccess } from '@/composables/useAssetAccess'
import { t } from '@/i18n'
import { downloadFile } from '@/utils/documents'
import { formatMoney, formatMonth } from '@/utils/format'

/** The register of fixed assets (docs/architecture/37-fixed-assets.md 4.12, mockup 51): a list that fills the screen, filters, the sums of the filter, and the choice of assets to print labels for. */
const PAGE = 100
const router = useRouter()
const { pid, canRead, canSetup } = useAssetAccess()
const rows = ref<Asset[]>([])
const total = ref(0)
const summary = ref({ count: 0, cost: '0', accum_depr: '0', carrying_value: '0' })
const categories = ref<AssetCategory[]>([])
const locations = ref<AssetLocation[]>([])
const loaded = ref(false)
const loadingMore = ref(false)
const error = ref<ApiError | null>(null)
const filter = ref({ category_id: 0, location_id: 0, department_id: null as number | null, status: 'ACTIVE', q: '' })
const selected = ref<Set<number>>(new Set())

function query(offset: number): Record<string, string | number> {
  const f = filter.value
  const q: Record<string, string | number> = { limit: PAGE, offset }
  if (f.category_id) q.category_id = f.category_id
  if (f.location_id) q.location_id = f.location_id
  if (f.department_id) q.department_id = f.department_id
  if (f.status) q.status = f.status
  if (f.q.trim()) q.q = f.q.trim()
  return q
}

async function load(more = false): Promise<void> {
  const propertyId = pid.value
  error.value = null
  if (propertyId === null || !canRead.value) {
    loaded.value = true
    return
  }
  if (more) loadingMore.value = true
  try {
    const res = await api.GET('/api/v1/properties/{propertyId}/assets', { params: { path: { propertyId }, query: query(more ? rows.value.length : 0) as never } })
    const data = res.data as unknown as AssetPage | undefined // the same route answers a CSV when asked, and the type says so
    rows.value = more ? [...rows.value, ...(data?.data ?? [])] : (data?.data ?? [])
    total.value = data?.total ?? 0
    if (data?.summary) summary.value = data.summary
    if (!more) selected.value = new Set()
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    loaded.value = true
    loadingMore.value = false
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

watch(pid, () => { void loadMasters(); void load() }, { immediate: true })

function setCategory(id: number): void {
  filter.value.category_id = id
  void load()
}

const hasMore = computed(() => rows.value.length < total.value)
const allSelected = computed(() => rows.value.length > 0 && rows.value.every((r) => selected.value.has(r.id)))
function toggle(id: number): void {
  const s = new Set(selected.value)
  if (s.has(id)) s.delete(id)
  else s.add(id)
  selected.value = s
}
function toggleAll(): void {
  selected.value = allSelected.value ? new Set() : new Set(rows.value.map((r) => r.id))
}
function printLabels(): void {
  void router.push({ path: '/assets/labels', query: selected.value.size ? { ids: [...selected.value].join(',') } : {} })
}

async function exportCsv(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null) return
  const q = new URLSearchParams()
  for (const [k, v] of Object.entries(query(0))) if (k !== 'limit' && k !== 'offset') q.set(k, String(v))
  q.set('format', 'csv')
  try {
    await downloadFile(`/api/v1/properties/${propertyId}/assets?${q.toString()}`)
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  }
}

function life(a: Asset): string {
  if (a.category_kind === 'LAND') return t('assets.life.notDepreciated')
  if (a.category_kind === 'CIP') return t('assets.life.notInUse')
  if (a.fully_depreciated) return t('assets.life.fullyDepreciated')
  return a.remaining_months === null || a.remaining_months === undefined ? '–' : t('assets.life.months', { n: a.remaining_months })
}
const noDepreciation = (a: Asset): boolean => a.category_kind === 'LAND' || a.category_kind === 'CIP'
const statusVariant = (s: Asset['status']) => (s === 'ACTIVE' ? 'success' : 'outline')

const columns = computed<Column<Asset>[]>(() => [
  { key: 'select', label: '', class: 'w-8' },
  { key: 'name', label: t('assets.col.asset'), card: 'primary' },
  { key: 'category_name', label: t('assets.col.category') },
  { key: 'location_name', label: t('assets.col.place') },
  { key: 'in_service_date', label: t('assets.col.inService') },
  { key: 'cost', label: t('assets.col.cost'), align: 'right', card: 'money' },
  { key: 'accum_depr', label: t('assets.col.accum'), align: 'right' },
  { key: 'carrying_value', label: t('assets.col.carrying'), align: 'right' },
  { key: 'remaining_months', label: t('assets.col.life'), align: 'right' },
  { key: 'status', label: t('assets.col.status'), card: 'badge' },
])
</script>

<template>
  <PageHeader :title="t('assets.register.title')" :description="t('assets.register.intro')">
    <template #actions>
      <Button variant="outline" :disabled="!canRead" data-testid="export-csv" @click="exportCsv">{{ t('assets.register.csv') }}</Button>
      <Button variant="outline" data-testid="print-labels" @click="printLabels">{{ selected.size ? t('assets.register.printSelected', { n: selected.size }) : t('assets.register.print') }}</Button>
      <Button v-if="canSetup" as-child variant="outline"><RouterLink to="/assets/opening" data-testid="go-opening">{{ t('assets.register.opening') }}</RouterLink></Button>
    </template>
  </PageHeader>
  <ErrorNotice v-if="error" :error="error" inline />
  <p v-if="pid === null" class="muted">{{ t('setup.selectProperty') }}</p>
  <p v-else-if="!canRead" class="muted" data-testid="no-access">{{ t('assets.noAccess') }}</p>
  <template v-else>
    <div class="grid gap-3 sm:grid-cols-3" data-testid="asset-tiles">
      <KpiCard :label="t('assets.tiles.cost')" :value="formatMoney(summary.cost)" :hint="t('assets.tiles.count', { n: summary.count })" />
      <KpiCard :label="t('assets.tiles.accum')" :value="formatMoney(summary.accum_depr)" />
      <KpiCard :label="t('assets.tiles.carrying')" :value="formatMoney(summary.carrying_value)" />
    </div>
    <form class="flex flex-wrap items-center gap-2" data-testid="asset-filters" @submit.prevent="load()">
      <div class="flex flex-wrap gap-2" role="group" :aria-label="t('assets.col.category')">
        <button type="button" class="min-h-9 rounded-full border px-3 text-sm" :class="filter.category_id === 0 ? 'border-primary bg-primary text-primary-foreground' : 'border-border bg-card'" data-testid="chip-all" @click="setCategory(0)">{{ t('assets.filter.allCategories') }}</button>
        <button v-for="c in categories" :key="c.id" type="button" class="min-h-9 rounded-full border px-3 text-sm" :class="filter.category_id === c.id ? 'border-primary bg-primary text-primary-foreground' : 'border-border bg-card'" :data-testid="`chip-${c.code}`" @click="setCategory(c.id)">{{ c.name }}</button>
      </div>
      <span class="flex-1" />
      <NativeSelect v-model.number="filter.location_id" name="location_id" :aria-label="t('assets.col.place')" data-testid="filter-location" @change="load()">
        <option :value="0">{{ t('assets.filter.allLocations') }}</option>
        <option v-for="l in locations" :key="l.id" :value="l.id">{{ l.name }}</option>
      </NativeSelect>
      <DepartmentSelect v-model="filter.department_id" :none-label="t('assets.filter.allDepartments')" :aria-label="t('assets.col.department')" @update:model-value="load()" />
      <NativeSelect v-model="filter.status" name="status" :aria-label="t('assets.col.status')" data-testid="filter-status" @change="load()">
        <option value="">{{ t('assets.filter.anyStatus') }}</option>
        <option v-for="s in ['ACTIVE', 'DISPOSED', 'CANCELLED']" :key="s" :value="s">{{ t(`assets.status.${s}`) }}</option>
      </NativeSelect>
      <Input v-model="filter.q" name="q" class="w-56" :placeholder="t('assets.filter.search')" :aria-label="t('assets.filter.search')" data-testid="filter-q" />
    </form>
    <DataTable
      :columns="columns" :rows="rows" row-key="id" :loading="!loaded" :empty-title="t('assets.register.empty')" :empty-description="t('assets.register.emptyHint')"
      :row-test-id="(r) => `asset-${r.number}`" :has-more="hasMore" :loading-more="loadingMore" :total="total" cards data-testid="asset-table" @load-more="load(true)"
    >
      <template #header-select><input type="checkbox" class="size-4 accent-primary" :checked="allSelected" :aria-label="t('assets.register.selectAll')" data-testid="select-all" @change="toggleAll" /></template>
      <template #cell-select="{ row }"><input type="checkbox" class="size-4 accent-primary" :checked="selected.has(row.id)" :aria-label="t('assets.register.select', { number: row.number })" :data-testid="`select-${row.number}`" @change="toggle(row.id)" /></template>
      <template #cell-name="{ row }">
        <RouterLink :to="`/assets/${row.id}`" class="font-semibold text-primary hover:underline">{{ row.name }}</RouterLink>
        <div class="text-xs text-muted-foreground">{{ row.number }}<template v-if="row.serial_no"> · {{ row.serial_no }}</template><template v-if="row.source === 'OPENING'"> · {{ t('assets.register.fromOpening') }}</template><template v-if="row.unit_count > 1"> · {{ t('assets.register.units', { n: row.unit_count }) }}</template></div>
      </template>
      <template #cell-location_name="{ row }">{{ row.location_name ?? '–' }}<div class="text-xs text-muted-foreground">{{ row.department_name ?? '' }}</div></template>
      <template #cell-in_service_date="{ row }">{{ row.category_kind === 'CIP' ? '–' : formatMonth(row.in_service_date) }}</template>
      <template #cell-cost="{ row }"><span class="tabular-nums">{{ formatMoney(row.cost) }}</span></template>
      <template #cell-accum_depr="{ row }"><span class="tabular-nums">{{ noDepreciation(row) ? '–' : formatMoney(row.accum_depr) }}</span></template>
      <template #cell-carrying_value="{ row }"><span class="tabular-nums">{{ formatMoney(row.carrying_value) }}</span></template>
      <template #cell-remaining_months="{ row }"><span :class="row.fully_depreciated || noDepreciation(row) ? 'text-xs text-muted-foreground' : 'tabular-nums'">{{ life(row) }}</span></template>
      <template #cell-status="{ row }"><Badge :variant="statusVariant(row.status)">{{ t(`assets.status.${row.status}`) }}</Badge></template>
    </DataTable>
  </template>
</template>
