<script setup lang="ts">
import { X } from 'lucide-vue-next'
import { computed, ref, watch } from 'vue'
import { RouterLink } from 'vue-router'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { InventoryCategory, InventoryItem, InventoryItemPage, InventoryStockCard } from '@/api/types'
import DataTable, { type Column } from '@/components/app/DataTable.vue'
import ErrorNotice from '@/components/app/ErrorNotice.vue'
import PageHeader from '@/components/app/PageHeader.vue'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { t } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import { formatMoney } from '@/utils/format'
import { formatQty } from '@/utils/inventory'

/**
 * Items and stock (docs/architecture/35-inventory.md, mockup 17): a list that fills the screen with a column for each warehouse, the chips of the categories, and a side panel of the item
 * that was clicked: its units, its stock per warehouse and a short stock card. The costing method of the property is shown, with its lock.
 */
const property = usePropertyStore()
const auth = useAuthStore()
const pid = computed(() => property.currentId)
const canManage = computed(() => auth.can('inventory.manage', pid.value))
const canOpen = computed(() => auth.can('inventory.opening', pid.value))

const page = ref<InventoryItemPage | null>(null)
const categories = ref<InventoryCategory[]>([])
const loaded = ref(false)
const error = ref<ApiError | null>(null)
const q = ref('')
const category = ref<number | null>(null)
const belowMin = ref(false)
const limit = ref(50)

const selected = ref<InventoryItem | null>(null)
const card = ref<InventoryStockCard | null>(null)
const cardWarehouse = ref<number | null>(null)

async function load(): Promise<void> {
  const propertyId = pid.value
  error.value = null
  if (propertyId === null) {
    loaded.value = true
    return
  }
  try {
    const query: { q?: string; category?: number; below_min?: boolean; limit: number } = { limit: limit.value }
    if (q.value.trim()) query.q = q.value.trim()
    if (category.value !== null) query.category = category.value
    if (belowMin.value) query.below_min = true
    const [items, cats] = await Promise.all([
      api.GET('/api/v1/properties/{propertyId}/inventory/items', { params: { path: { propertyId }, query } }),
      api.GET('/api/v1/properties/{propertyId}/inventory/categories', { params: { path: { propertyId } } }),
    ])
    page.value = items.data ?? null
    categories.value = cats.data?.data ?? []
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    loaded.value = true
  }
}

let timer: ReturnType<typeof setTimeout> | undefined
watch(q, () => {
  clearTimeout(timer)
  timer = setTimeout(() => {
    limit.value = 50
    void load()
  }, 250)
})
watch([category, belowMin], () => {
  limit.value = 50
  void load()
})
watch(pid, load, { immediate: true })

const warehouses = computed(() => page.value?.warehouses ?? [])
const items = computed(() => page.value?.items ?? [])
const belowMinCount = ref(0)
watch(items, () => {
  if (!belowMin.value && !q.value && category.value === null) belowMinCount.value = items.value.filter((i) => i.below_min).length
})

const columns = computed<Column<InventoryItem>[]>(() => [
  { key: 'code', label: t('inventory.items.code'), class: 'whitespace-nowrap font-medium' },
  { key: 'name', label: t('inventory.items.item') },
  { key: 'stock_unit_code', label: t('inventory.items.unit') },
  ...warehouses.value.map((w): Column<InventoryItem> => ({ key: `w_${w.id}`, label: w.name, align: 'right', hideOnMobile: true })),
  { key: 'value_on_hand', label: t('inventory.items.value'), align: 'right', format: 'money' },
  { key: 'status', label: t('inventory.items.status') },
])

const stockOf = (item: InventoryItem, warehouseId: number): string => {
  const s = item.stock.find((x) => x.warehouse_id === warehouseId)
  return s && s.qty !== '0' ? formatQty(s.qty) : '—'
}
const negative = (item: InventoryItem, warehouseId: number): boolean => item.stock.some((x) => x.warehouse_id === warehouseId && x.qty.startsWith('-'))

async function select(item: InventoryItem): Promise<void> {
  selected.value = item
  card.value = null
  cardWarehouse.value = null
  const propertyId = pid.value
  if (propertyId === null) return
  try {
    // the list carries no units: the item is read whole for the panel
    const detail = await api.GET('/api/v1/properties/{propertyId}/inventory/items/{id}', { params: { path: { propertyId, id: item.id } } })
    if (detail.data && selected.value?.id === item.id) selected.value = detail.data
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
    return
  }
  const whole = selected.value
  const withStock = whole?.stock.find((s) => s.qty !== '0') ?? whole?.stock[0]
  if (!withStock) return
  cardWarehouse.value = withStock.warehouse_id
  try {
    const { data } = await api.GET('/api/v1/properties/{propertyId}/inventory/stock-card', { params: { path: { propertyId }, query: { warehouse: withStock.warehouse_id, item: item.id } } })
    if (selected.value?.id === item.id) card.value = data ?? null
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  }
}
const lastLines = computed(() => (card.value?.lines ?? []).slice(-4).reverse())
const warehouseName = (id: number | null): string => warehouses.value.find((w) => w.id === id)?.name ?? ''
const cardTo = computed(() => (selected.value && cardWarehouse.value ? `/inventory/costing?warehouse=${cardWarehouse.value}&item=${selected.value.id}` : '/inventory/costing'))
</script>

<template>
  <PageHeader :title="t('inventory.items.title')" :description="t('inventory.items.intro')">
    <template #marks>
      <Badge v-if="page" variant="outline" data-testid="method-badge">
        {{ t(`inventory.method.${page.costing_method}`) }}<template v-if="page.costing_locked"> · {{ t('inventory.method.locked') }}</template>
      </Badge>
    </template>
    <template #actions>
      <Button v-if="canOpen" as-child variant="outline"><RouterLink to="/inventory/openings/new" data-testid="opening-new">{{ t('inventory.items.opening') }}</RouterLink></Button>
      <Button v-if="canManage" as-child><RouterLink to="/inventory/items/new" data-testid="item-new">{{ t('inventory.items.new') }}</RouterLink></Button>
    </template>
  </PageHeader>
  <ErrorNotice v-if="error" :error="error" inline />
  <p v-if="pid === null" class="muted">{{ t('setup.selectProperty') }}</p>

  <div v-else class="grid gap-4" :class="selected ? 'xl:grid-cols-[minmax(0,1fr)_22rem]' : ''">
    <section class="min-w-0">
      <div class="mb-3 flex flex-wrap items-center gap-2">
        <Input v-model="q" name="q" type="search" class="max-w-64" :placeholder="t('inventory.items.search')" :aria-label="t('inventory.items.search')" data-testid="item-search" />
        <Button type="button" size="sm" class="rounded-full" :variant="category === null && !belowMin ? 'default' : 'outline'" data-testid="chip-all" @click="category = null; belowMin = false">
          {{ t('inventory.items.all', { n: Object.values(page?.by_category ?? {}).reduce((a, b) => a + b, 0) }) }}
        </Button>
        <Button v-for="c in categories" :key="c.id" type="button" size="sm" class="rounded-full" :variant="category === c.id ? 'default' : 'outline'" :data-testid="`chip-${c.code}`" @click="category = category === c.id ? null : c.id">
          {{ c.name }} · {{ page?.by_category?.[String(c.id)] ?? 0 }}
        </Button>
        <Button type="button" size="sm" class="rounded-full" :variant="belowMin ? 'default' : 'outline'" data-testid="chip-below-min" @click="belowMin = !belowMin">
          {{ t('inventory.items.belowMin', { n: belowMinCount }) }}
        </Button>
      </div>

      <DataTable
        :columns="columns" :rows="items" row-key="id" :loading="!loaded" clickable :empty-title="q || category !== null || belowMin ? t('inventory.items.emptyFiltered') : t('inventory.items.empty')"
        :empty-description="t('emptyState.inventoryItems')" :row-test-id="(r) => `item-${r.code}`" :row-class="(r) => (selected?.id === r.id ? 'bg-muted/60' : undefined)" data-testid="items-table" @row-click="select"
      >
        <template v-for="w in warehouses" :key="w.id" #[`cell-w_${w.id}`]="{ row }">
          <span :class="negative(row, w.id) ? 'font-medium text-destructive' : ''">{{ stockOf(row, w.id) }}</span>
        </template>
        <template #cell-name="{ row }">{{ row.name }}</template>
        <template #cell-status="{ row }">
          <Badge v-if="row.below_zero" variant="destructive">{{ t('inventory.items.badgeBelowZero') }}</Badge>
          <Badge v-else-if="row.below_min" variant="warning">{{ t('inventory.items.badgeBelowMin') }}</Badge>
          <Badge v-if="!row.is_active" variant="outline">{{ t('inventory.items.badgeOff') }}</Badge>
        </template>
      </DataTable>

      <div v-if="page" class="mt-3 flex flex-wrap items-center justify-between gap-2 text-sm text-muted-foreground">
        <span data-testid="showing">{{ t('inventory.items.showing', { n: items.length, total: page.total }) }}</span>
        <Button v-if="items.length < page.total" type="button" variant="outline" size="sm" data-testid="load-more" @click="limit += 50; load()">{{ t('inventory.items.loadMore') }}</Button>
        <span>{{ t('inventory.items.totalValue') }}: <strong class="text-foreground" data-testid="total-value">{{ formatMoney(page.total_value) }}</strong></span>
      </div>
    </section>

    <aside v-if="selected" class="min-w-0 rounded-xl border border-border bg-card p-4 xl:sticky xl:top-16 xl:max-h-[calc(100vh-5rem)] xl:self-start xl:overflow-y-auto" data-testid="item-panel">
      <div class="flex items-start justify-between gap-2">
        <div class="min-w-0">
          <strong class="block">{{ selected.name }}</strong>
          <span class="text-xs text-muted-foreground">{{ selected.code }} · {{ selected.category_name }}</span>
        </div>
        <Button type="button" variant="ghost" size="icon" :aria-label="t('inventory.items.panelClose')" data-testid="panel-close" @click="selected = null"><X class="size-4" aria-hidden="true" /></Button>
      </div>

      <h3 class="mb-1 mt-4 text-xs font-semibold uppercase tracking-wide text-muted-foreground">{{ t('inventory.itemForm.unitsTitle') }}</h3>
      <dl class="m-0 grid grid-cols-[auto_1fr] gap-x-3 gap-y-1 text-sm" data-testid="panel-units">
        <template v-for="u in selected.units" :key="u.unit_id">
          <dt class="text-muted-foreground">{{ u.unit_id === selected.stock_unit_id ? t('inventory.items.stockUnit') : u.is_purchase ? t('inventory.items.buy') : t('inventory.items.recipe') }}</dt>
          <dd class="m-0">
            <template v-if="u.unit_id === selected.stock_unit_id">{{ u.unit_code }}</template>
            <template v-else>{{ u.unit_code }} = {{ u.stock_num }}<template v-if="u.stock_den !== 1"> / {{ u.stock_den }}</template> {{ selected.stock_unit_code }}</template>
          </dd>
        </template>
      </dl>

      <h3 class="mb-1 mt-4 text-xs font-semibold uppercase tracking-wide text-muted-foreground">{{ t('inventory.items.perWarehouse') }}</h3>
      <ul class="m-0 list-none p-0 text-sm" data-testid="panel-stock">
        <li v-for="s in selected.stock.filter((x) => x.qty !== '0' || x.min_qty !== '0')" :key="s.warehouse_id" class="border-b border-border py-1.5 last:border-0">
          <div class="flex justify-between gap-2"><span>{{ s.warehouse_name }}</span><strong :class="s.qty.startsWith('-') ? 'text-destructive' : ''">{{ formatQty(s.qty) }} {{ selected.stock_unit_code }}</strong></div>
          <div class="flex justify-between gap-2 text-xs text-muted-foreground">
            <span v-if="s.average_cost">{{ t('inventory.items.average', { cost: formatMoney(s.average_cost) }) }}</span><span v-else>{{ formatMoney(s.value) }}</span>
            <span v-if="s.min_qty !== '0'">{{ t('inventory.items.minimumOf', { warehouse: s.warehouse_name }) }} {{ formatQty(s.min_qty) }}</span>
          </div>
        </li>
        <li v-if="!selected.stock.some((x) => x.qty !== '0' || x.min_qty !== '0')" class="py-1.5 text-muted-foreground">{{ t('inventory.items.noStock') }}</li>
      </ul>
      <p v-if="selected.reference_cost" class="m-0 mt-2 text-xs text-muted-foreground">{{ t('inventory.items.referenceCost', { cost: formatMoney(selected.reference_cost) }) }}</p>

      <div class="mb-1 mt-4 flex items-center justify-between gap-2">
        <h3 class="m-0 text-xs font-semibold uppercase tracking-wide text-muted-foreground">{{ t('inventory.items.cardTitle', { warehouse: warehouseName(cardWarehouse) }) }}</h3>
        <RouterLink :to="cardTo" class="text-xs underline" data-testid="panel-card-link">{{ t('inventory.items.seeAll') }}</RouterLink>
      </div>
      <ul class="m-0 list-none p-0 text-sm" data-testid="panel-card">
        <li v-for="l in lastLines" :key="l.movement_id" class="flex justify-between gap-2 border-b border-border py-1 last:border-0">
          <span class="min-w-0 truncate"><span class="text-muted-foreground">{{ l.business_date.slice(5) }}</span> {{ t(`inventory.kind.${l.kind}`) + (l.document_number ? ' ' + l.document_number : '') }}</span>
          <strong :class="l.qty.startsWith('-') ? 'text-destructive' : ''">{{ l.qty.startsWith('-') ? '' : '+' }}{{ formatQty(l.qty) }}</strong>
        </li>
        <li v-if="card && lastLines.length === 0" class="py-1 text-muted-foreground">{{ t('inventory.items.cardEmpty') }}</li>
      </ul>

      <div class="mt-4 flex flex-wrap gap-2">
        <Button v-if="canManage" as-child variant="outline" size="sm"><RouterLink :to="`/inventory/items/${selected.id}`" data-testid="panel-edit">{{ t('inventory.items.edit') }}</RouterLink></Button>
        <Button as-child variant="outline" size="sm"><RouterLink :to="cardTo">{{ t('inventory.items.stockCard') }}</RouterLink></Button>
      </div>
    </aside>
  </div>
</template>
