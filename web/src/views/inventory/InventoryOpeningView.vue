<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { RouterLink, useRoute, useRouter } from 'vue-router'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { InventoryItem, InventoryOpening, InventoryWarehouse } from '@/api/types'
import ErrorNotice from '@/components/app/ErrorNotice.vue'
import FormField from '@/components/app/FormField.vue'
import PageHeader from '@/components/app/PageHeader.vue'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { NativeSelect } from '@/components/ui/native-select'
import { t } from '@/i18n'
import { usePropertyStore } from '@/stores/property'
import { formatDate, formatMoney } from '@/utils/format'
import { addMoney, formatQty, isDecimal, multiply } from '@/utils/inventory'

/**
 * The opening stock of a warehouse, as a document (docs/architecture/35-inventory.md 5.2): the warehouse, then a line for each item with the unit it is counted in, the quantity and the cost
 * of one such unit. Posting is final, and books each line Dr the inventory account of the item / Cr 3900 at the close of the day. A posted opening is shown here, read only.
 */
interface Line {
  item_id: number
  unit_id: number
  qty: string
  unit_cost: string
}

const route = useRoute()
const router = useRouter()
const property = usePropertyStore()
const pid = computed(() => property.currentId)
const openingId = computed(() => (route.params.id ? Number(route.params.id) : null))
const isNew = computed(() => openingId.value === null)
const decimals = computed(() => property.current?.currency_decimals ?? 0)

const warehouses = ref<InventoryWarehouse[]>([])
const items = ref<InventoryItem[]>([])
const detail = reactive<Record<number, InventoryItem>>({})
const posted = ref<InventoryOpening | null>(null)
const loaded = ref(false)
const posting = ref(false)
const error = ref<ApiError | null>(null)
const formError = ref<ApiError | null>(null)
const notice = ref('')
const fieldError = (f: string) => formError.value?.fieldMessage(f)

const warehouseId = ref(0)
const lines = ref<Line[]>([])
const clientUuid = ref(crypto.randomUUID())

async function load(): Promise<void> {
  const propertyId = pid.value
  loaded.value = false
  error.value = null
  if (propertyId === null) return
  try {
    if (isNew.value) {
      const path = { params: { path: { propertyId } } }
      const [w, i] = await Promise.all([
        api.GET('/api/v1/properties/{propertyId}/inventory/warehouses', path),
        api.GET('/api/v1/properties/{propertyId}/inventory/items', { params: { path: { propertyId }, query: { limit: 200, active: true } } }),
      ])
      warehouses.value = (w.data?.data ?? []).filter((x) => x.is_active)
      items.value = i.data?.items ?? []
      posted.value = null
      lines.value = []
      warehouseId.value = warehouses.value[0]?.id ?? 0
    } else {
      const { data } = await api.GET('/api/v1/properties/{propertyId}/inventory/openings/{id}', { params: { path: { propertyId, id: openingId.value! } } })
      posted.value = data ?? null
    }
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    loaded.value = true
  }
}

const itemById = (id: number): InventoryItem | undefined => detail[id] ?? items.value.find((i) => i.id === id)
async function itemChanged(l: Line): Promise<void> {
  const propertyId = pid.value
  const it = items.value.find((i) => i.id === l.item_id)
  if (propertyId === null || !it) return
  l.unit_id = it.stock_unit_id
  if (!detail[it.id]) {
    try {
      const { data } = await api.GET('/api/v1/properties/{propertyId}/inventory/items/{id}', { params: { path: { propertyId, id: it.id } } })
      if (data) detail[it.id] = data
    } catch (e) {
      error.value = e instanceof ApiError ? e : null
    }
  }
}
const unitsOf = (l: Line) => itemById(l.item_id)?.units ?? []
const freeItems = (own: number): InventoryItem[] => items.value.filter((i) => i.id === own || !lines.value.some((l) => l.item_id === i.id))

function addLine(): void {
  const it = freeItems(0)[0]
  if (!it) return
  const l: Line = { item_id: it.id, unit_id: it.stock_unit_id, qty: '', unit_cost: '' }
  lines.value.push(l)
  void itemChanged(l)
}

const lineValue = (l: Line): string => (isDecimal(l.qty, 6) && isDecimal(l.unit_cost, decimals.value) ? multiply(l.qty, l.unit_cost, decimals.value) : '')
const total = computed(() => lines.value.reduce((sum, l) => (lineValue(l) ? addMoney(sum, lineValue(l), decimals.value) : sum), decimals.value > 0 ? `0.${'0'.repeat(decimals.value)}` : '0'))
const stockQty = (l: Line): string => {
  const u = unitsOf(l).find((x) => x.unit_id === l.unit_id)
  if (!u || !isDecimal(l.qty, 6) || u.stock_num === u.stock_den) return ''
  // the quantity in stock units, to six decimals, as the server works it out
  const scaled = multiply(l.qty, String(u.stock_num), 6)
  const [w = '0', f = ''] = scaled.split('.')
  const n = BigInt(w + f.padEnd(6, '0')) * BigInt(2) + BigInt(u.stock_den)
  const q = n / (BigInt(2) * BigInt(u.stock_den))
  const digits = q.toString().padStart(7, '0')
  return `${digits.slice(0, -6)}.${digits.slice(-6)}`
}
const complete = computed(() => warehouseId.value > 0 && lines.value.length > 0 && lines.value.every((l) => l.item_id > 0 && isDecimal(l.qty, 6) && Number(l.qty) > 0 && isDecimal(l.unit_cost, decimals.value)))

async function post(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null) return
  posting.value = true
  formError.value = null
  try {
    const { data } = await api.POST('/api/v1/properties/{propertyId}/inventory/openings', {
      params: { path: { propertyId } },
      body: { client_uuid: clientUuid.value, warehouse_id: warehouseId.value, lines: lines.value.map((l) => ({ item_id: l.item_id, unit_id: l.unit_id, qty: l.qty.trim(), unit_cost: l.unit_cost.trim() })) },
    })
    if (data) {
      notice.value = t('inventory.openings.posted', { number: data.number })
      await router.replace(`/inventory/openings/${data.id}`)
    }
  } catch (e) {
    formError.value = e instanceof ApiError ? e : null
  } finally {
    posting.value = false
  }
}
watch([pid, openingId], load, { immediate: true })
const lineError = (i: number, f: string): string | undefined => formError.value?.fieldMessage(`lines[${i}].${f}`)
</script>

<template>
  <PageHeader :title="isNew ? t('inventory.openings.newTitle') : t('inventory.openings.viewTitle', { number: posted?.number ?? '' })" :description="isNew ? t('inventory.openings.docHint') : t('inventory.openings.journal')">
    <template #actions>
      <Button as-child variant="outline"><RouterLink to="/inventory/openings" data-testid="back">{{ t('inventory.openings.back') }}</RouterLink></Button>
    </template>
  </PageHeader>
  <ErrorNotice v-if="error" :error="error" inline />
  <p v-if="notice" class="notice" role="status" data-testid="notice">{{ notice }}</p>
  <p v-if="pid === null" class="muted">{{ t('setup.selectProperty') }}</p>

  <!-- a posted opening: read only -->
  <section v-else-if="loaded && posted" class="flex flex-col gap-4" data-testid="opening-posted">
    <dl class="m-0 grid max-w-xl grid-cols-[auto_1fr] gap-x-4 gap-y-1 text-sm">
      <dt class="text-muted-foreground">{{ t('inventory.openings.warehouse') }}</dt><dd class="m-0">{{ posted.warehouse_name }}</dd>
      <dt class="text-muted-foreground">{{ t('inventory.openings.date') }}</dt><dd class="m-0">{{ formatDate(posted.business_date) }}</dd>
      <dt class="text-muted-foreground">{{ t('inventory.openings.total') }}</dt><dd class="m-0 font-semibold" data-testid="posted-total">{{ formatMoney(posted.total_value) }}</dd>
    </dl>
    <div class="overflow-x-auto">
      <table class="w-full min-w-[34rem] text-sm" data-testid="posted-lines">
        <thead>
          <tr class="text-left text-xs text-muted-foreground">
            <th class="py-1 pr-2 font-medium">{{ t('inventory.openings.item') }}</th><th class="py-1 pr-2 text-right font-medium">{{ t('inventory.openings.qty') }}</th>
            <th class="py-1 pr-2 text-right font-medium">{{ t('inventory.openings.unitCost') }}</th><th class="py-1 text-right font-medium">{{ t('inventory.openings.value') }}</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="l in posted.lines ?? []" :key="l.line_no" class="border-t border-border">
            <td class="py-2 pr-2">{{ l.item_name }} <span class="text-xs text-muted-foreground">{{ l.item_code }}</span></td>
            <td class="py-2 pr-2 text-right">{{ formatQty(l.qty) }} {{ l.unit_code }}</td>
            <td class="py-2 pr-2 text-right">{{ formatMoney(l.unit_cost) }}</td>
            <td class="py-2 text-right">{{ formatMoney(l.value) }}</td>
          </tr>
        </tbody>
      </table>
    </div>
  </section>

  <!-- a new opening: the document -->
  <form v-else-if="loaded && isNew" novalidate class="flex flex-col gap-4" data-testid="opening-form" @submit.prevent="post">
    <ErrorNotice v-if="formError" :error="formError" inline data-testid="opening-error" />
    <FormField class="max-w-sm" :label="t('inventory.openings.warehouse')" :error="fieldError('warehouse_id')" required>
      <template #default="{ id, invalid }">
        <NativeSelect :id="id" v-model.number="warehouseId" name="warehouse_id" :aria-invalid="invalid">
          <option :value="0" disabled>{{ t('inventory.openings.chooseWarehouse') }}</option>
          <option v-for="w in warehouses" :key="w.id" :value="w.id">{{ w.code }} · {{ w.name }}</option>
        </NativeSelect>
      </template>
    </FormField>
    <p v-if="items.length === 0" class="muted" data-testid="no-items">{{ t('inventory.openings.noItems') }}</p>
    <div v-else class="overflow-x-auto">
      <table class="w-full min-w-[44rem] text-sm" data-testid="opening-lines">
        <thead>
          <tr class="text-left text-xs text-muted-foreground">
            <th class="py-1 pr-2 font-medium">{{ t('inventory.openings.item') }}</th><th class="py-1 pr-2 font-medium">{{ t('inventory.openings.unit') }}</th>
            <th class="py-1 pr-2 font-medium">{{ t('inventory.openings.qty') }}</th><th class="py-1 pr-2 font-medium">{{ t('inventory.openings.unitCost') }}</th>
            <th class="py-1 pr-2 text-right font-medium">{{ t('inventory.openings.value') }}</th><th />
          </tr>
        </thead>
        <tbody>
          <tr v-for="(l, i) in lines" :key="i" class="border-t border-border align-top" :data-testid="`line-${i}`">
            <td class="py-2 pr-2">
              <NativeSelect v-model.number="l.item_id" :name="`item_${i}`" class="max-w-64" :aria-label="t('inventory.openings.item')" :aria-invalid="!!lineError(i, 'item_id')" @change="itemChanged(l)">
                <option v-for="it in freeItems(l.item_id)" :key="it.id" :value="it.id">{{ it.code }} · {{ it.name }}</option>
              </NativeSelect>
              <p v-if="lineError(i, 'item_id')" class="m-0 mt-1 text-xs text-destructive">{{ lineError(i, 'item_id') }}</p>
            </td>
            <td class="py-2 pr-2">
              <NativeSelect v-model.number="l.unit_id" :name="`unit_${i}`" class="w-28" :aria-label="t('inventory.openings.unit')">
                <option v-for="u in unitsOf(l)" :key="u.unit_id" :value="u.unit_id">{{ u.unit_code }}</option>
              </NativeSelect>
            </td>
            <td class="py-2 pr-2">
              <Input v-model="l.qty" :name="`qty_${i}`" inputmode="decimal" class="w-28" :aria-label="t('inventory.openings.qty')" :aria-invalid="!!lineError(i, 'qty')" />
              <p v-if="stockQty(l)" class="m-0 mt-1 text-xs text-muted-foreground">{{ t('inventory.openings.inStock', { qty: formatQty(stockQty(l)), unit: itemById(l.item_id)?.stock_unit_code ?? '' }) }}</p>
              <p v-if="lineError(i, 'qty')" class="m-0 mt-1 text-xs text-destructive">{{ lineError(i, 'qty') }}</p>
            </td>
            <td class="py-2 pr-2">
              <Input v-model="l.unit_cost" :name="`cost_${i}`" inputmode="decimal" class="w-32" :aria-label="t('inventory.openings.unitCost')" :aria-invalid="!!lineError(i, 'unit_cost')" />
              <p v-if="lineError(i, 'unit_cost')" class="m-0 mt-1 text-xs text-destructive">{{ lineError(i, 'unit_cost') }}</p>
            </td>
            <td class="py-2 pr-2 text-right" :data-testid="`line-value-${i}`">{{ lineValue(l) ? formatMoney(lineValue(l)) : '' }}</td>
            <td class="py-2 text-right"><Button type="button" variant="ghost" size="sm" :data-testid="`line-remove-${i}`" @click="lines.splice(i, 1)">{{ t('common.remove') }}</Button></td>
          </tr>
        </tbody>
        <tfoot>
          <tr class="border-t border-border">
            <td colspan="4" class="py-2 pr-2 text-right font-medium">{{ t('inventory.openings.total') }}</td>
            <td class="py-2 pr-2 text-right font-semibold" data-testid="opening-total">{{ formatMoney(total) }}</td><td />
          </tr>
        </tfoot>
      </table>
    </div>
    <div class="flex flex-wrap items-center justify-between gap-2">
      <Button type="button" variant="outline" size="sm" data-testid="line-add" :disabled="freeItems(0).length === 0" @click="addLine">{{ t('inventory.openings.addLine') }}</Button>
      <div class="flex gap-2">
        <Button as-child variant="outline"><RouterLink to="/inventory/openings">{{ t('common.cancel') }}</RouterLink></Button>
        <Button type="submit" :disabled="posting || !complete" data-testid="opening-post">{{ t('inventory.openings.post') }}</Button>
      </div>
    </div>
  </form>
</template>
