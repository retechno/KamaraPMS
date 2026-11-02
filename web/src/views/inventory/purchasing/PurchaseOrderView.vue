<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { RouterLink, useRoute, useRouter } from 'vue-router'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { InventoryItem, InventoryPurchaseOrder, InventorySettings, InventorySupplier, InventoryWarehouse } from '@/api/types'
import ErrorNotice from '@/components/app/ErrorNotice.vue'
import FormField from '@/components/app/FormField.vue'
import MoneyInput from '@/components/app/MoneyInput.vue'
import PageHeader from '@/components/app/PageHeader.vue'
import QuantityInput from '@/components/app/QuantityInput.vue'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { DateInput } from '@/components/ui/date-input'
import { guardForm } from '@/utils/forms'
import { NativeSelect } from '@/components/ui/native-select'
import { t } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import { formatDate, formatMoney } from '@/utils/format'
import { addMoney, formatQty, isDecimal, multiply } from '@/utils/inventory'
import { draftVariant, orderVariant } from '@/utils/purchasing'

/**
 * A purchase order (docs/architecture/35-inventory.md 5.3.2). It is made from an approved request (on the page of the request); a property that does not require a request (D13) writes one here directly.
 * An order changes only by being sent, cancelled or closed short; goods are received against it.
 */
interface Line {
  item_id: number
  unit_id: number
  qty: string
  unit_price: string
}

const route = useRoute()
const router = useRouter()
const property = usePropertyStore()
const auth = useAuthStore()
const pid = computed(() => property.currentId)
const can = (p: string) => auth.can(p, pid.value)
const orderId = computed(() => (route.params.id ? Number(route.params.id) : null))
const isNew = computed(() => orderId.value === null)
const decimals = computed(() => property.current?.currency_decimals ?? 0)

const order = ref<InventoryPurchaseOrder | null>(null)
const settings = ref<InventorySettings | null>(null)
const suppliers = ref<InventorySupplier[]>([])
const warehouses = ref<InventoryWarehouse[]>([])
const items = ref<InventoryItem[]>([])
const detail = reactive<Record<number, InventoryItem>>({})
const loaded = ref(false)
const busy = ref(false)
const error = ref<ApiError | null>(null)
const formError = ref<ApiError | null>(null)
const notice = ref('')
const fieldError = (f: string) => formError.value?.fieldMessage(f)
const lineError = (i: number, f: string): string | undefined => formError.value?.fieldMessage(`lines[${i}].${f}`)

const form = reactive({ supplier_id: 0, warehouse_id: 0, expected_on: '', notes: '', send: true })
const lines = ref<Line[]>([])
const clientUuid = ref(crypto.randomUUID())

async function load(): Promise<void> {
  const propertyId = pid.value
  loaded.value = false
  error.value = null
  if (propertyId === null) return
  const path = { params: { path: { propertyId } } }
  try {
    if (isNew.value) {
      const [s, w, i, c] = await Promise.all([
        api.GET('/api/v1/properties/{propertyId}/inventory/suppliers', { params: { path: { propertyId }, query: { active: true } } }),
        api.GET('/api/v1/properties/{propertyId}/inventory/warehouses', path),
        api.GET('/api/v1/properties/{propertyId}/inventory/items', { params: { path: { propertyId }, query: { limit: 200, active: true } } }),
        api.GET('/api/v1/properties/{propertyId}/inventory/settings', path),
      ])
      suppliers.value = s.data?.data ?? []
      warehouses.value = (w.data?.data ?? []).filter((x) => x.is_active)
      items.value = i.data?.items ?? []
      settings.value = c.data ?? null
      form.warehouse_id = warehouses.value.find((x) => x.kind === 'MAIN')?.id ?? warehouses.value[0]?.id ?? 0
      form.supplier_id = suppliers.value[0]?.id ?? 0
      lines.value = []
      order.value = null
    } else {
      const { data } = await api.GET('/api/v1/properties/{propertyId}/inventory/purchase-orders/{id}', { params: { path: { propertyId, id: orderId.value! } } })
      order.value = data ?? null
    }
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    loaded.value = true
  }
}
watch([pid, orderId], load, { immediate: true })

async function itemDetail(id: number): Promise<InventoryItem | undefined> {
  const propertyId = pid.value
  if (detail[id] || propertyId === null) return detail[id]
  try {
    const { data } = await api.GET('/api/v1/properties/{propertyId}/inventory/items/{id}', { params: { path: { propertyId, id } } })
    if (data) detail[id] = data
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  }
  return detail[id]
}
const itemOf = (id: number): InventoryItem | undefined => detail[id] ?? items.value.find((i) => i.id === id)
const unitsOf = (l: Line) => (itemOf(l.item_id)?.units ?? []).filter((u) => u.is_purchase || u.unit_id === itemOf(l.item_id)?.stock_unit_id)
const freeItems = (own: number): InventoryItem[] => items.value.filter((i) => i.id === own || !lines.value.some((l) => l.item_id === i.id))
async function itemChanged(l: Line): Promise<void> {
  const it = await itemDetail(l.item_id)
  if (it) l.unit_id = it.units.find((u) => u.is_default_purchase)?.unit_id ?? it.stock_unit_id
}
function addLine(): void {
  const it = freeItems(0)[0]
  if (!it) return
  const l: Line = { item_id: it.id, unit_id: it.stock_unit_id, qty: '', unit_price: '' }
  lines.value.push(l)
  void itemChanged(l)
}
const lineTotal = (l: Line): string => (isDecimal(l.qty, 6) && isDecimal(l.unit_price, decimals.value) ? multiply(l.qty, l.unit_price, decimals.value) : '')
const total = computed(() => lines.value.reduce((a, l) => (lineTotal(l) ? addMoney(a, lineTotal(l), decimals.value) : a), decimals.value > 0 ? `0.${'0'.repeat(decimals.value)}` : '0'))
const complete = computed(() => form.supplier_id > 0 && form.warehouse_id > 0 && lines.value.length > 0 && lines.value.every((l) => l.item_id > 0 && isDecimal(l.qty, 6) && Number(l.qty) > 0 && isDecimal(l.unit_price, decimals.value) && Number(l.unit_price) > 0))

async function create(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null) return
  busy.value = true
  formError.value = null
  try {
    const { data } = await api.POST('/api/v1/properties/{propertyId}/inventory/purchase-orders', {
      params: { path: { propertyId } },
      body: {
        client_uuid: clientUuid.value, supplier_id: form.supplier_id, warehouse_id: form.warehouse_id, expected_on: form.expected_on || null, notes: form.notes.trim() || null, send: form.send,
        lines: lines.value.map((l) => ({ item_id: l.item_id, unit_id: l.unit_id, qty: l.qty.trim(), unit_price: l.unit_price.trim() })),
      },
    })
    if (data) await router.replace(`/inventory/purchase-orders/${data.id}`)
  } catch (e) {
    formError.value = e instanceof ApiError ? e : null
  } finally {
    busy.value = false
  }
}

async function act(path: 'send' | 'cancel' | 'close', message: string): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null || !order.value) return
  busy.value = true
  formError.value = null
  try {
    const { data } = await api.POST(`/api/v1/properties/{propertyId}/inventory/purchase-orders/{id}/${path}` as '/api/v1/properties/{propertyId}/inventory/purchase-orders/{id}/send', { params: { path: { propertyId, id: order.value.id } } })
    if (data) {
      order.value = data
      notice.value = message
    }
  } catch (e) {
    formError.value = e instanceof ApiError ? e : null
  } finally {
    busy.value = false
  }
}
const canReceive = computed(() => !!order.value && (order.value.status === 'ORDERED' || order.value.status === 'PARTIAL') && can('inventory.receive'))
</script>

<template>
  <PageHeader :title="isNew ? t('inventory.order.newTitle') : t('inventory.order.viewTitle', { number: order?.po_number ?? '' })" :description="isNew ? t('inventory.order.hint') : ''">
    <template v-if="order" #marks><Badge :variant="orderVariant(order.status)" data-testid="order-status">{{ t(`inventory.orderStatus.${order.status}`) }}</Badge></template>
    <template #actions>
      <Button as-child variant="outline"><RouterLink :to="{ path: '/inventory/purchasing', query: { tab: 'orders' } }" data-testid="back">{{ t('inventory.order.back') }}</RouterLink></Button>
      <Button v-if="canReceive && order" as-child><RouterLink :to="`/inventory/receipts/new?po=${order.id}`" data-testid="order-receive">{{ t('inventory.order.receive') }}</RouterLink></Button>
    </template>
  </PageHeader>
  <ErrorNotice v-if="error" :error="error" inline />
  <p v-if="notice" class="notice" role="status" data-testid="notice">{{ notice }}</p>
  <p v-if="pid === null" class="muted">{{ t('setup.selectProperty') }}</p>

  <!-- an order written directly: only for a property that does not order from requests -->
  <template v-else-if="loaded && isNew">
    <p v-if="settings?.po_requires_pr" class="alert warning" data-testid="requires-request">{{ t('inventory.order.requiresRequest') }}</p>
    <form v-else novalidate class="flex flex-col gap-4" data-testid="order-form" @submit.prevent="guardForm($event) && create()">
      <ErrorNotice v-if="formError" :error="formError" inline data-testid="order-error" />
      <div class="grid gap-3 sm:grid-cols-3">
        <FormField :label="t('inventory.order.supplier')" :error="fieldError('supplier_id')" required>
          <template #default="{ id, invalid }">
            <NativeSelect :id="id" v-model.number="form.supplier_id" name="supplier_id" :aria-invalid="invalid"><option v-for="s in suppliers" :key="s.id" :value="s.id">{{ s.name }}</option></NativeSelect>
          </template>
        </FormField>
        <FormField :label="t('inventory.order.warehouse')" :error="fieldError('warehouse_id')" required>
          <template #default="{ id, invalid }">
            <NativeSelect :id="id" v-model.number="form.warehouse_id" name="warehouse_id" :aria-invalid="invalid"><option v-for="w in warehouses" :key="w.id" :value="w.id">{{ w.code }} · {{ w.name }}</option></NativeSelect>
          </template>
        </FormField>
        <FormField :label="t('inventory.order.expectedOn')" :error="fieldError('expected_on')"><template #default="{ id }"><DateInput :id="id" v-model="form.expected_on" name="expected_on" /></template></FormField>
      </div>
      <FormField :label="t('inventory.order.notes')" :error="fieldError('notes')"><template #default="{ id }"><Input :id="id" v-model="form.notes" name="notes" maxlength="300" /></template></FormField>
      <div class="overflow-x-auto">
        <table class="w-full min-w-[44rem] text-sm" data-testid="order-lines">
          <thead>
            <tr class="text-left text-xs text-muted-foreground">
              <th class="py-1 pr-2 font-medium">{{ t('inventory.order.item') }}</th><th class="py-1 pr-2 font-medium">{{ t('inventory.order.qty') }}</th><th class="py-1 pr-2 font-medium">{{ t('inventory.order.unit') }}</th>
              <th class="py-1 pr-2 font-medium">{{ t('inventory.order.price') }}</th><th class="py-1 pr-2 text-right font-medium">{{ t('inventory.order.amount') }}</th><th />
            </tr>
          </thead>
          <tbody>
            <tr v-for="(l, i) in lines" :key="i" class="border-t border-border align-top" :data-testid="`line-${i}`">
              <td class="py-2 pr-2">
                <NativeSelect v-model.number="l.item_id" :name="`item_${i}`" class="max-w-56" :aria-label="t('inventory.order.item')" @change="itemChanged(l)">
                  <option v-for="it in freeItems(l.item_id)" :key="it.id" :value="it.id">{{ it.code }} · {{ it.name }}</option>
                </NativeSelect>
                <p v-if="lineError(i, 'item_id')" class="m-0 mt-1 text-xs text-destructive">{{ lineError(i, 'item_id') }}</p>
              </td>
              <td class="py-2 pr-2"><QuantityInput v-model="l.qty" :name="`qty_${i}`" class="w-24" :aria-label="t('inventory.order.qty')" /><p v-if="lineError(i, 'qty')" class="m-0 mt-1 text-xs text-destructive">{{ lineError(i, 'qty') }}</p></td>
              <td class="py-2 pr-2">
                <NativeSelect v-model.number="l.unit_id" :name="`unit_${i}`" class="w-28" :aria-label="t('inventory.order.unit')"><option v-for="u in unitsOf(l)" :key="u.unit_id" :value="u.unit_id">{{ u.unit_code }}</option></NativeSelect>
              </td>
              <td class="py-2 pr-2"><MoneyInput v-model="l.unit_price" :name="`price_${i}`" :decimals="decimals" class="w-32" :aria-label="t('inventory.order.price')" /><p v-if="lineError(i, 'unit_price')" class="m-0 mt-1 text-xs text-destructive">{{ lineError(i, 'unit_price') }}</p></td>
              <td class="py-2 pr-2 text-right">{{ lineTotal(l) ? formatMoney(lineTotal(l)) : '' }}</td>
              <td class="py-2 text-right"><Button type="button" variant="ghost" size="sm" :data-testid="`line-remove-${i}`" @click="lines.splice(i, 1)">{{ t('common.remove') }}</Button></td>
            </tr>
          </tbody>
          <tfoot>
            <tr class="border-t border-border"><td colspan="4" class="py-2 pr-2 text-right font-medium">{{ t('inventory.order.total') }}</td><td class="py-2 pr-2 text-right font-semibold" data-testid="order-total">{{ formatMoney(total) }}</td><td /></tr>
          </tfoot>
        </table>
      </div>
      <div class="flex flex-wrap items-center justify-between gap-2">
        <Button type="button" variant="outline" size="sm" data-testid="line-add" :disabled="freeItems(0).length === 0" @click="addLine">{{ t('inventory.order.addLine') }}</Button>
        <div class="flex flex-wrap items-center gap-3">
          <label class="flex items-center gap-2 text-sm"><input v-model="form.send" type="checkbox" name="send" /> {{ t('inventory.order.sendNow') }}</label>
          <Button type="submit" :disabled="busy || !complete" data-testid="order-save">{{ t('inventory.order.save') }}</Button>
        </div>
      </div>
    </form>
  </template>

  <!-- an order that exists: read only, with what was received of it -->
  <section v-else-if="loaded && order" class="flex flex-col gap-4" data-testid="order-view">
    <ErrorNotice v-if="formError" :error="formError" inline data-testid="order-error" />
    <dl class="m-0 grid max-w-3xl grid-cols-[auto_1fr] gap-x-4 gap-y-1 text-sm">
      <dt class="text-muted-foreground">{{ t('inventory.order.supplier') }}</dt><dd class="m-0">{{ order.supplier_name }}</dd>
      <dt class="text-muted-foreground">{{ t('inventory.order.warehouse') }}</dt><dd class="m-0">{{ order.warehouse_name }}</dd>
      <dt class="text-muted-foreground">{{ t('inventory.order.orderDate') }}</dt><dd class="m-0">{{ formatDate(order.order_date) }}</dd>
      <dt v-if="order.expected_on" class="text-muted-foreground">{{ t('inventory.order.expectedOn') }}</dt><dd v-if="order.expected_on" class="m-0">{{ formatDate(order.expected_on) }}</dd>
      <dt v-if="order.request_number" class="text-muted-foreground">{{ t('inventory.order.fromRequest') }}</dt>
      <dd v-if="order.request_number" class="m-0"><RouterLink :to="`/inventory/requests/${order.request_id}`" class="text-primary hover:underline">{{ order.request_number }}</RouterLink></dd>
      <dt v-if="order.notes" class="text-muted-foreground">{{ t('inventory.order.notes') }}</dt><dd v-if="order.notes" class="m-0">{{ order.notes }}</dd>
    </dl>
    <div class="overflow-x-auto">
      <table class="w-full min-w-[40rem] text-sm" data-testid="order-lines-read">
        <thead>
          <tr class="text-left text-xs text-muted-foreground">
            <th class="py-1 pr-2 font-medium">{{ t('inventory.order.item') }}</th><th class="py-1 pr-2 text-right font-medium">{{ t('inventory.order.ordered') }}</th><th class="py-1 pr-2 text-right font-medium">{{ t('inventory.order.received') }}</th>
            <th class="py-1 pr-2 font-medium">{{ t('inventory.order.unit') }}</th><th class="py-1 pr-2 text-right font-medium">{{ t('inventory.order.price') }}</th><th class="py-1 text-right font-medium">{{ t('inventory.order.amount') }}</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="l in order.lines" :key="l.line_no" class="border-t border-border" :data-testid="`order-line-${l.line_no}`">
            <td class="py-2 pr-2">{{ l.item_name }} <span class="text-xs text-muted-foreground">{{ l.item_code }}</span> <Badge v-if="l.closed" variant="outline">{{ t('inventory.order.closedShort') }}</Badge></td>
            <td class="py-2 pr-2 text-right">{{ formatQty(l.qty) }}</td><td class="py-2 pr-2 text-right">{{ formatQty(l.qty_received) }}</td><td class="py-2 pr-2">{{ l.unit_code }}</td>
            <td class="py-2 pr-2 text-right">{{ formatMoney(l.unit_price) }}</td><td class="py-2 text-right">{{ formatMoney(l.amount) }}</td>
          </tr>
        </tbody>
        <tfoot><tr class="border-t border-border"><td colspan="5" class="py-2 pr-2 text-right font-medium">{{ t('inventory.order.total') }}</td><td class="py-2 text-right font-semibold" data-testid="order-total">{{ formatMoney(order.total) }}</td></tr></tfoot>
      </table>
    </div>
    <div v-if="can('inventory.purchase')" class="flex flex-wrap justify-end gap-2">
      <Button v-if="order.status === 'DRAFT'" type="button" :disabled="busy" data-testid="order-send" @click="act('send', t('inventory.order.sent'))">{{ t('inventory.order.send') }}</Button>
      <Button v-if="order.status === 'DRAFT' || order.status === 'ORDERED'" type="button" variant="outline" :disabled="busy" data-testid="order-cancel" @click="act('cancel', t('inventory.order.cancelled'))">{{ t('inventory.order.cancel') }}</Button>
      <Button v-if="order.status === 'PARTIAL' || order.status === 'ORDERED'" type="button" variant="outline" :disabled="busy" data-testid="order-close" @click="act('close', t('inventory.order.closed'))">{{ t('inventory.order.closeShort') }}</Button>
    </div>
    <section v-if="order.receipts.length" class="rounded-xl border border-border bg-card p-4" data-testid="order-receipts">
      <h2 class="m-0 text-base font-semibold">{{ t('inventory.order.receipts') }}</h2>
      <ul class="m-0 mt-2 list-none p-0 text-sm">
        <li v-for="r in order.receipts" :key="r.id" class="flex flex-wrap items-center justify-between gap-2 border-t border-border py-1.5">
          <RouterLink :to="`/inventory/receipts/${r.id}`" class="text-primary hover:underline">{{ r.receipt_number }}</RouterLink>
          <span class="text-muted-foreground">{{ formatDate(r.received_on) }}</span><span>{{ formatMoney(r.total_value) }}</span>
          <Badge v-if="r.draft_status" :variant="draftVariant(r.draft_status)">{{ t(`inventory.draftStatus.${r.draft_status}`) }}</Badge>
        </li>
      </ul>
    </section>
  </section>
</template>
