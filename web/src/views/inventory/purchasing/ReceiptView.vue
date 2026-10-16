<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { RouterLink, useRoute, useRouter } from 'vue-router'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { InventoryItem, InventoryPurchaseOrder, InventoryReceipt, InventorySettings, InventorySupplier, InventoryWarehouse } from '@/api/types'
import ErrorNotice from '@/components/app/ErrorNotice.vue'
import FormField from '@/components/app/FormField.vue'
import MoneyInput from '@/components/app/MoneyInput.vue'
import PageHeader from '@/components/app/PageHeader.vue'
import QuantityInput from '@/components/app/QuantityInput.vue'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { NativeSelect } from '@/components/ui/native-select'
import { t } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import { formatDate, formatMoney } from '@/utils/format'
import { addMoney, formatQty, isDecimal, multiply } from '@/utils/inventory'
import { draftVariant } from '@/utils/purchasing'

/**
 * The goods receipt (docs/architecture/35-inventory.md 5.3.3, mockup 20): goods come in against an order at the price of the order, which is not offered for change, or without an order at a typed price up to
 * the limit of the property. "Receive and bill" also confirms the draft bill with the supplier's invoice. A posted receipt is read only: what was billed and returned of each line, its matches, and the reversal
 * on its own day.
 */
interface Line {
  po_line_no: number | null
  item_id: number
  unit_id: number
  qty: string
  unit_price: string
  vat: string
}

const route = useRoute()
const router = useRouter()
const property = usePropertyStore()
const auth = useAuthStore()
const pid = computed(() => property.currentId)
const can = (p: string) => auth.can(p, pid.value)
const receiptId = computed(() => (route.params.id ? Number(route.params.id) : null))
const poId = computed(() => (route.query.po ? Number(route.query.po) : null))
const isNew = computed(() => receiptId.value === null)
const decimals = computed(() => property.current?.currency_decimals ?? 0)

const receipt = ref<InventoryReceipt | null>(null)
const po = ref<InventoryPurchaseOrder | null>(null)
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

const form = reactive({ supplier_id: 0, warehouse_id: 0, delivery_note: '', delivery_note_date: '', note: '' })
const bill = reactive({ on: false, invoice: '', date: '', due: '' })
const lines = ref<Line[]>([])
const clientUuid = ref(crypto.randomUUID())
const reverseReason = ref('')
const canBill = computed(() => can('inventory.bill') && can('payables.post'))

async function load(): Promise<void> {
  const propertyId = pid.value
  loaded.value = false
  error.value = null
  if (propertyId === null) return
  const path = { params: { path: { propertyId } } }
  try {
    if (isNew.value) {
      const [w, c] = await Promise.all([api.GET('/api/v1/properties/{propertyId}/inventory/warehouses', path), api.GET('/api/v1/properties/{propertyId}/inventory/settings', path)])
      warehouses.value = (w.data?.data ?? []).filter((x) => x.is_active)
      settings.value = c.data ?? null
      bill.date = property.clock?.business_date ?? ''
      receipt.value = null
      lines.value = []
      if (poId.value) {
        const { data } = await api.GET('/api/v1/properties/{propertyId}/inventory/purchase-orders/{id}', { params: { path: { propertyId, id: poId.value } } })
        po.value = data ?? null
        if (po.value) {
          form.supplier_id = po.value.supplier_id
          form.warehouse_id = po.value.warehouse_id
          lines.value = po.value.lines
            .filter((l) => !l.closed && Number(l.qty_received_stock) < Number(l.qty_stock))
            .map((l) => ({ po_line_no: l.line_no, item_id: l.item_id, unit_id: l.unit_id, qty: remaining(l), unit_price: l.unit_price, vat: '' }))
        }
      } else {
        po.value = null
        const [s, i] = await Promise.all([
          api.GET('/api/v1/properties/{propertyId}/inventory/suppliers', { params: { path: { propertyId }, query: { active: true } } }),
          api.GET('/api/v1/properties/{propertyId}/inventory/items', { params: { path: { propertyId }, query: { limit: 200, active: true } } }),
        ])
        suppliers.value = s.data?.data ?? []
        items.value = i.data?.items ?? []
        form.supplier_id = suppliers.value[0]?.id ?? 0
        form.warehouse_id = warehouses.value.find((x) => x.kind === 'MAIN')?.id ?? warehouses.value[0]?.id ?? 0
      }
    } else {
      const { data } = await api.GET('/api/v1/properties/{propertyId}/inventory/receipts/{id}', { params: { path: { propertyId, id: receiptId.value! } } })
      receipt.value = data ?? null
    }
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    loaded.value = true
  }
}
watch([pid, receiptId, poId], load, { immediate: true })

/** What is left of an order line to receive, in the unit it was ordered in. */
function remaining(l: { qty: string; qty_received: string }): string {
  const left = Number(l.qty) - Number(l.qty_received)
  return left > 0 ? String(Number(left.toFixed(6))) : ''
}
const poLine = (n: number | null) => po.value?.lines.find((l) => l.line_no === n)

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
  const l: Line = { po_line_no: null, item_id: it.id, unit_id: it.stock_unit_id, qty: '', unit_price: '', vat: '' }
  lines.value.push(l)
  void itemChanged(l)
}
const lineValue = (l: Line): string => (isDecimal(l.qty, 6) && isDecimal(l.unit_price, decimals.value) ? multiply(l.qty, l.unit_price, decimals.value) : '')
const total = computed(() => lines.value.reduce((a, l) => (lineValue(l) ? addMoney(a, lineValue(l), decimals.value) : a), decimals.value > 0 ? `0.${'0'.repeat(decimals.value)}` : '0'))
/** The quantity that goes into stock, in stock units (an approximation for display: the server works it out). */
function inStock(l: Line): string {
  const pl = poLine(l.po_line_no)
  if (pl && isDecimal(l.qty, 6) && Number(pl.qty) > 0) return `${formatQty(String(Number(((Number(l.qty) * Number(pl.qty_stock)) / Number(pl.qty)).toFixed(6))))} ${pl.stock_unit_code}`
  const it = itemOf(l.item_id)
  const u = it?.units.find((x) => x.unit_id === l.unit_id)
  if (it && u && isDecimal(l.qty, 6)) return `${formatQty(String(Number(((Number(l.qty) * u.stock_num) / u.stock_den).toFixed(6))))} ${it.stock_unit_code}`
  return ''
}
const complete = computed(
  () =>
    lines.value.length > 0 &&
    (!!po.value || (form.supplier_id > 0 && form.warehouse_id > 0)) &&
    lines.value.every((l) => isDecimal(l.qty, 6) && Number(l.qty) > 0 && (!!l.po_line_no || (l.item_id > 0 && isDecimal(l.unit_price, decimals.value)))) &&
    (!bill.on || (bill.invoice.trim() !== '' && bill.date !== '')),
)
/** Quantities of lines that were left empty are not received: they leave the document. */
const received = computed(() => lines.value.filter((l) => isDecimal(l.qty, 6) && Number(l.qty) > 0))

async function post(withBill: boolean): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null) return
  busy.value = true
  formError.value = null
  try {
    const rows = received.value
    const { data } = await api.POST('/api/v1/properties/{propertyId}/inventory/receipts', {
      params: { path: { propertyId } },
      body: {
        client_uuid: clientUuid.value,
        ...(po.value ? { po_id: po.value.id } : { supplier_id: form.supplier_id, warehouse_id: form.warehouse_id }),
        delivery_note: form.delivery_note.trim() || null,
        delivery_note_date: form.delivery_note_date || null,
        note: form.note.trim() || null,
        lines: rows.map((l) => (po.value ? { po_line_no: l.po_line_no, qty: l.qty.trim() } : { item_id: l.item_id, unit_id: l.unit_id, qty: l.qty.trim(), unit_price: l.unit_price.trim() })),
        ...(withBill ? { bill: { supplier_invoice_number: bill.invoice.trim(), bill_date: bill.date, due_date: bill.due || null, vat_amounts: rows.map((l) => l.vat || '0') } } : {}),
      },
    })
    if (data) await router.replace(`/inventory/receipts/${data.id}`)
  } catch (e) {
    formError.value = e instanceof ApiError ? e : null
  } finally {
    busy.value = false
  }
}

async function reverse(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null || !receipt.value) return
  busy.value = true
  formError.value = null
  try {
    const { data } = await api.POST('/api/v1/properties/{propertyId}/inventory/receipts/{id}/reverse', { params: { path: { propertyId, id: receipt.value.id } }, body: { reason: reverseReason.value.trim() } })
    if (data) {
      receipt.value = data
      notice.value = t('inventory.receipt.reversed')
    }
  } catch (e) {
    formError.value = e instanceof ApiError ? e : null
  } finally {
    busy.value = false
  }
}
const sourceLabel = computed(() => (po.value ? t('inventory.receipt.forOrder', { number: po.value.po_number }) : ''))
</script>

<template>
  <PageHeader :title="isNew ? (po ? t('inventory.receipt.newTitle') : t('inventory.receipt.directTitle')) : t('inventory.receipt.viewTitle', { number: receipt?.receipt_number ?? '' })" :description="isNew ? sourceLabel : ''">
    <template v-if="receipt" #marks><Badge :variant="receipt.status === 'POSTED' ? 'success' : 'outline'" data-testid="receipt-status">{{ t(`inventory.receiptStatus.${receipt.status}`) }}</Badge></template>
    <template #actions>
      <Button as-child variant="outline"><RouterLink :to="{ path: '/inventory/purchasing', query: { tab: 'receipts' } }" data-testid="back">{{ t('inventory.receipt.back') }}</RouterLink></Button>
    </template>
  </PageHeader>
  <ErrorNotice v-if="error" :error="error" inline />
  <p v-if="notice" class="notice" role="status" data-testid="notice">{{ notice }}</p>
  <p v-if="pid === null" class="muted">{{ t('setup.selectProperty') }}</p>

  <!-- a new receipt, against an order or without one -->
  <form v-else-if="loaded && isNew" novalidate class="flex flex-col gap-4" data-testid="receipt-form" @submit.prevent="post(false)">
    <ErrorNotice v-if="formError" :error="formError" inline data-testid="receipt-error" />
    <p v-if="!po && settings && settings.direct_receipt_limit === null" class="alert warning" data-testid="direct-not-allowed">{{ t('inventory.receipt.directNotAllowed') }}</p>
    <p v-else-if="!po && settings?.direct_receipt_limit && Number(settings.direct_receipt_limit) > 0" class="muted text-sm" data-testid="direct-limit">{{ t('inventory.receipt.directLimit', { limit: formatMoney(settings.direct_receipt_limit) }) }}</p>
    <div class="grid gap-3 sm:grid-cols-4">
      <FormField :label="t('inventory.receipt.businessDate')"><template #default="{ id }"><Input :id="id" :model-value="property.clock?.business_date ?? ''" readonly name="business_date" /></template></FormField>
      <FormField :label="t('inventory.receipt.supplier')" :error="fieldError('supplier_id')" required>
        <template #default="{ id, invalid }">
          <Input v-if="po" :id="id" :model-value="po.supplier_name" readonly name="supplier" />
          <NativeSelect v-else :id="id" v-model.number="form.supplier_id" name="supplier_id" :aria-invalid="invalid"><option v-for="s in suppliers" :key="s.id" :value="s.id">{{ s.name }}</option></NativeSelect>
        </template>
      </FormField>
      <FormField :label="t('inventory.receipt.warehouse')" :error="fieldError('warehouse_id')" required>
        <template #default="{ id, invalid }">
          <Input v-if="po" :id="id" :model-value="po.warehouse_name" readonly name="warehouse" />
          <NativeSelect v-else :id="id" v-model.number="form.warehouse_id" name="warehouse_id" :aria-invalid="invalid"><option v-for="w in warehouses" :key="w.id" :value="w.id">{{ w.code }} · {{ w.name }}</option></NativeSelect>
        </template>
      </FormField>
      <FormField :label="t('inventory.receipt.deliveryNote')" :error="fieldError('delivery_note')"><template #default="{ id }"><Input :id="id" v-model="form.delivery_note" name="delivery_note" maxlength="60" /></template></FormField>
    </div>
    <FormField :label="t('inventory.receipt.note')" :error="fieldError('note')"><template #default="{ id }"><Input :id="id" v-model="form.note" name="note" maxlength="300" /></template></FormField>

    <div class="overflow-x-auto">
      <table class="w-full min-w-[52rem] text-sm" data-testid="receipt-lines">
        <thead>
          <tr class="text-left text-xs text-muted-foreground">
            <th class="py-1 pr-2 font-medium">{{ t('inventory.receipt.item') }}</th><th class="py-1 pr-2 font-medium">{{ t('inventory.receipt.unit') }}</th>
            <template v-if="po"><th class="py-1 pr-2 text-right font-medium">{{ t('inventory.receipt.ordered') }}</th><th class="py-1 pr-2 text-right font-medium">{{ t('inventory.receipt.alreadyReceived') }}</th></template>
            <th class="py-1 pr-2 font-medium">{{ t('inventory.receipt.receivedNow') }}</th><th class="py-1 pr-2 font-medium">{{ po ? t('inventory.receipt.poPrice') : t('inventory.receipt.price') }}</th>
            <th class="py-1 pr-2 text-right font-medium">{{ t('inventory.receipt.value') }}</th><th class="py-1 pr-2 font-medium">{{ t('inventory.receipt.intoStock') }}</th>
            <th v-if="bill.on" class="py-1 pr-2 font-medium">{{ t('inventory.receipt.vat') }}</th><th v-if="!po" />
          </tr>
        </thead>
        <tbody>
          <tr v-for="(l, i) in lines" :key="i" class="border-t border-border align-top" :data-testid="`line-${i}`">
            <td class="py-2 pr-2">
              <template v-if="po">{{ poLine(l.po_line_no)?.item_name }} <span class="text-xs text-muted-foreground">{{ poLine(l.po_line_no)?.item_code }}</span></template>
              <template v-else>
                <NativeSelect v-model.number="l.item_id" :name="`item_${i}`" class="max-w-56" :aria-label="t('inventory.receipt.item')" @change="itemChanged(l)">
                  <option v-for="it in freeItems(l.item_id)" :key="it.id" :value="it.id">{{ it.code }} · {{ it.name }}</option>
                </NativeSelect>
                <p v-if="lineError(i, 'item_id')" class="m-0 mt-1 text-xs text-destructive">{{ lineError(i, 'item_id') }}</p>
              </template>
            </td>
            <td class="py-2 pr-2">
              <template v-if="po">{{ poLine(l.po_line_no)?.unit_code }}</template>
              <NativeSelect v-else v-model.number="l.unit_id" :name="`unit_${i}`" class="w-28" :aria-label="t('inventory.receipt.unit')"><option v-for="u in unitsOf(l)" :key="u.unit_id" :value="u.unit_id">{{ u.unit_code }}</option></NativeSelect>
            </td>
            <template v-if="po"><td class="py-2 pr-2 text-right">{{ formatQty(poLine(l.po_line_no)?.qty ?? '') }}</td><td class="py-2 pr-2 text-right">{{ formatQty(poLine(l.po_line_no)?.qty_received ?? '') }}</td></template>
            <td class="py-2 pr-2">
              <QuantityInput v-model="l.qty" :name="`qty_${i}`" class="w-24" :aria-label="t('inventory.receipt.receivedNow')" :aria-invalid="!!lineError(i, 'qty')" />
              <p v-if="lineError(i, 'qty')" class="m-0 mt-1 text-xs text-destructive">{{ lineError(i, 'qty') }}</p>
            </td>
            <td class="py-2 pr-2">
              <span v-if="po" data-testid="po-price">{{ formatMoney(l.unit_price) }}</span>
              <MoneyInput v-else v-model="l.unit_price" :name="`price_${i}`" :decimals="decimals" class="w-32" :aria-label="t('inventory.receipt.price')" :aria-invalid="!!lineError(i, 'unit_price')" />
              <p v-if="lineError(i, 'unit_price')" class="m-0 mt-1 text-xs text-destructive">{{ lineError(i, 'unit_price') }}</p>
            </td>
            <td class="py-2 pr-2 text-right" :data-testid="`line-value-${i}`">{{ lineValue(l) ? formatMoney(lineValue(l)) : '' }}</td>
            <td class="py-2 pr-2 text-muted-foreground">{{ inStock(l) }}</td>
            <td v-if="bill.on" class="py-2 pr-2"><MoneyInput v-model="l.vat" :name="`vat_${i}`" :decimals="decimals" class="w-28" :aria-label="t('inventory.receipt.vat')" /></td>
            <td v-if="!po" class="py-2 text-right"><Button type="button" variant="ghost" size="sm" :data-testid="`line-remove-${i}`" @click="lines.splice(i, 1)">{{ t('common.remove') }}</Button></td>
          </tr>
        </tbody>
        <tfoot><tr class="border-t border-border"><td :colspan="po ? 6 : 4" class="py-2 pr-2 text-right font-medium">{{ t('inventory.receipt.total') }}</td><td class="py-2 pr-2 text-right font-semibold" data-testid="receipt-total">{{ formatMoney(total) }}</td><td colspan="3" /></tr></tfoot>
      </table>
    </div>
    <p class="muted text-sm">{{ po ? t('inventory.receipt.poPriceHint') : t('inventory.receipt.directHint') }}</p>

    <section v-if="canBill" class="rounded-xl border border-border bg-card p-4" data-testid="receive-and-bill">
      <label class="flex items-center gap-2 text-sm font-medium"><input v-model="bill.on" type="checkbox" name="receive_and_bill" data-testid="bill-toggle" /> {{ t('inventory.receipt.receiveAndBill') }}</label>
      <div v-if="bill.on" class="mt-3 grid gap-3 sm:grid-cols-3">
        <FormField :label="t('inventory.receipt.invoiceNumber')" :error="fieldError('bill.supplier_invoice_number')" required><template #default="{ id }"><Input :id="id" v-model="bill.invoice" name="invoice_number" maxlength="60" /></template></FormField>
        <FormField :label="t('inventory.receipt.billDate')" :error="fieldError('bill_date')" required><template #default="{ id }"><Input :id="id" v-model="bill.date" type="date" name="bill_date" /></template></FormField>
        <FormField :label="t('inventory.receipt.dueDate')"><template #default="{ id }"><Input :id="id" v-model="bill.due" type="date" name="due_date" /></template></FormField>
      </div>
      <p v-if="bill.on" class="muted mt-2 text-sm">{{ t('inventory.receipt.billHint') }}</p>
    </section>

    <div class="flex flex-wrap items-center justify-between gap-2">
      <Button v-if="!po" type="button" variant="outline" size="sm" data-testid="line-add" :disabled="freeItems(0).length === 0" @click="addLine">{{ t('inventory.receipt.addLine') }}</Button><span v-else />
      <div class="flex flex-wrap gap-2">
        <Button as-child variant="outline"><RouterLink :to="po ? `/inventory/purchase-orders/${po.id}` : '/inventory/purchasing?tab=receipts'">{{ t('common.cancel') }}</RouterLink></Button>
        <Button v-if="bill.on" type="button" :disabled="busy || !complete" data-testid="receipt-post-bill" @click="post(true)">{{ t('inventory.receipt.postAndBill') }}</Button>
        <Button v-else type="submit" :disabled="busy || !complete" data-testid="receipt-post">{{ t('inventory.receipt.post') }}</Button>
      </div>
    </div>
  </form>

  <!-- a posted receipt: read only -->
  <section v-else-if="loaded && receipt" class="flex flex-col gap-4" data-testid="receipt-view">
    <ErrorNotice v-if="formError" :error="formError" inline data-testid="receipt-error" />
    <dl class="m-0 grid max-w-3xl grid-cols-[auto_1fr] gap-x-4 gap-y-1 text-sm">
      <dt class="text-muted-foreground">{{ t('inventory.receipt.supplier') }}</dt><dd class="m-0">{{ receipt.supplier_name }}</dd>
      <dt class="text-muted-foreground">{{ t('inventory.receipt.warehouse') }}</dt><dd class="m-0">{{ receipt.warehouse_name }}</dd>
      <dt class="text-muted-foreground">{{ t('inventory.receipt.businessDate') }}</dt><dd class="m-0">{{ formatDate(receipt.received_on) }}</dd>
      <dt v-if="receipt.po_number" class="text-muted-foreground">{{ t('inventory.receipt.order') }}</dt><dd v-if="receipt.po_number" class="m-0"><RouterLink :to="`/inventory/purchase-orders/${receipt.po_id}`" class="text-primary hover:underline">{{ receipt.po_number }}</RouterLink></dd>
      <dt v-if="receipt.delivery_note" class="text-muted-foreground">{{ t('inventory.receipt.deliveryNote') }}</dt><dd v-if="receipt.delivery_note" class="m-0">{{ receipt.delivery_note }}</dd>
      <dt v-if="receipt.note" class="text-muted-foreground">{{ t('inventory.receipt.note') }}</dt><dd v-if="receipt.note" class="m-0">{{ receipt.note }}</dd>
      <dt v-if="receipt.draft_id" class="text-muted-foreground">{{ t('inventory.receipt.bill') }}</dt>
      <dd v-if="receipt.draft_id" class="m-0"><RouterLink :to="`/inventory/bill-drafts/${receipt.draft_id}`" class="text-primary hover:underline" data-testid="draft-link">{{ t('inventory.receipt.draftBill') }}</RouterLink> <Badge v-if="receipt.draft_status" :variant="draftVariant(receipt.draft_status)">{{ t(`inventory.draftStatus.${receipt.draft_status}`) }}</Badge></dd>
      <dt v-if="receipt.status === 'REVERSED'" class="text-muted-foreground">{{ t('inventory.receipt.reverseReason') }}</dt><dd v-if="receipt.status === 'REVERSED'" class="m-0">{{ receipt.reverse_reason }}</dd>
    </dl>
    <div class="overflow-x-auto">
      <table class="w-full min-w-[46rem] text-sm" data-testid="receipt-lines-read">
        <thead>
          <tr class="text-left text-xs text-muted-foreground">
            <th class="py-1 pr-2 font-medium">{{ t('inventory.receipt.item') }}</th><th class="py-1 pr-2 text-right font-medium">{{ t('inventory.receipt.receivedQty') }}</th><th class="py-1 pr-2 font-medium">{{ t('inventory.receipt.unit') }}</th>
            <th class="py-1 pr-2 text-right font-medium">{{ t('inventory.receipt.price') }}</th><th class="py-1 pr-2 text-right font-medium">{{ t('inventory.receipt.value') }}</th><th class="py-1 pr-2 text-right font-medium">{{ t('inventory.receipt.billed') }}</th>
            <th class="py-1 text-right font-medium">{{ t('inventory.receipt.returned') }}</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="l in receipt.lines" :key="l.line_no" class="border-t border-border" :data-testid="`receipt-line-${l.line_no}`">
            <td class="py-2 pr-2">{{ l.item_name }} <span class="text-xs text-muted-foreground">{{ l.item_code }}</span></td>
            <td class="py-2 pr-2 text-right">{{ formatQty(l.qty) }}</td><td class="py-2 pr-2">{{ l.unit_code }}</td><td class="py-2 pr-2 text-right">{{ formatMoney(l.unit_price) }}</td>
            <td class="py-2 pr-2 text-right">{{ formatMoney(l.value) }}</td><td class="py-2 pr-2 text-right">{{ formatQty(l.qty_billed_stock) }} {{ l.stock_unit_code }}</td><td class="py-2 text-right">{{ formatQty(l.qty_returned_stock) }} {{ l.stock_unit_code }}</td>
          </tr>
        </tbody>
        <tfoot><tr class="border-t border-border"><td colspan="4" class="py-2 pr-2 text-right font-medium">{{ t('inventory.receipt.total') }}</td><td class="py-2 pr-2 text-right font-semibold" data-testid="receipt-total">{{ formatMoney(receipt.total_value) }}</td><td colspan="2" /></tr></tfoot>
      </table>
    </div>
    <section v-if="receipt.matches.length" class="rounded-xl border border-border bg-card p-4" data-testid="receipt-matches">
      <h2 class="m-0 text-base font-semibold">{{ t('inventory.receipt.matches') }}</h2>
      <ul class="m-0 mt-2 list-none p-0 text-sm">
        <li v-for="m in receipt.matches" :key="m.id" class="flex flex-wrap justify-between gap-2 border-t border-border py-1.5">
          <span>{{ m.bill_number }} · {{ m.supplier_invoice_number }}</span><span>{{ formatMoney(m.bill_cost) }}</span>
          <span :class="Number(m.variance) === 0 ? 'text-muted-foreground' : 'text-foreground'">{{ t('inventory.receipt.variance', { amount: formatMoney(m.variance) }) }}</span>
        </li>
      </ul>
    </section>
    <section v-if="receipt.returns.length" class="rounded-xl border border-border bg-card p-4" data-testid="receipt-returns">
      <h2 class="m-0 text-base font-semibold">{{ t('inventory.receipt.returns') }}</h2>
      <ul class="m-0 mt-2 list-none p-0 text-sm">
        <li v-for="r in receipt.returns" :key="r.id" class="flex flex-wrap justify-between gap-2 border-t border-border py-1.5">
          <RouterLink :to="`/inventory/returns/${r.id}`" class="text-primary hover:underline">{{ r.return_number }}</RouterLink><span>{{ r.reason }}</span><span>{{ formatMoney(r.total_value) }}</span>
        </li>
      </ul>
    </section>
    <div v-if="can('inventory.receive') && receipt.status === 'POSTED'" class="flex flex-wrap items-end justify-end gap-2">
      <Button as-child variant="outline"><RouterLink :to="`/inventory/returns/new?receipt=${receipt.id}`" data-testid="receipt-return">{{ t('inventory.receipt.return') }}</RouterLink></Button>
      <template v-if="receipt.reversible">
        <FormField class="max-w-72" :label="t('inventory.receipt.reverseReasonLabel')"><template #default="{ id }"><Input :id="id" v-model="reverseReason" name="reason" maxlength="300" /></template></FormField>
        <Button type="button" variant="outline" :disabled="busy || !reverseReason.trim()" data-testid="receipt-reverse" @click="reverse">{{ t('inventory.receipt.reverse') }}</Button>
      </template>
    </div>
  </section>
</template>
