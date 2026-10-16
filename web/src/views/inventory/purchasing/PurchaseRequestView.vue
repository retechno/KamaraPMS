<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { RouterLink, useRoute, useRouter } from 'vue-router'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { Department, InventoryItem, InventoryRequest, InventoryRequestLineInput, InventorySupplier, InventorySupplierItem, InventoryWarehouse } from '@/api/types'
import ErrorNotice from '@/components/app/ErrorNotice.vue'
import FormField from '@/components/app/FormField.vue'
import MoneyInput from '@/components/app/MoneyInput.vue'
import PageHeader from '@/components/app/PageHeader.vue'
import QuantityInput from '@/components/app/QuantityInput.vue'
import ApprovalChain from '@/components/inventory/ApprovalChain.vue'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { NativeSelect } from '@/components/ui/native-select'
import { departmentLabel, loadDepartments } from '@/composables/useDepartments'
import { t } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import { formatDate, formatMoney } from '@/utils/format'
import { addMoney, formatQty, isDecimal, multiply } from '@/utils/inventory'
import { requestVariant, stageText } from '@/utils/purchasing'

/**
 * A purchase request (docs/architecture/35-inventory.md 5.3.1, 5.8, mockup 19). A draft or a returned request of the signed-in requester is a form; every other state is read only, with the chain of
 * approval beside it: who decided each level, what is waited for, the edits, and, for the person who can decide the active level, the box "Keputusan Anda". An approved request is turned into orders here.
 */
interface Line {
  item_id: number
  unit_id: number
  qty: string
  est_unit_price: string
  supplier_id: number
}

const route = useRoute()
const router = useRouter()
const property = usePropertyStore()
const auth = useAuthStore()
const pid = computed(() => property.currentId)
const can = (p: string) => auth.can(p, pid.value)
const requestId = computed(() => (route.params.id ? Number(route.params.id) : null))
const isNew = computed(() => requestId.value === null)
const decimals = computed(() => property.current?.currency_decimals ?? 0)
const me = computed(() => auth.me?.user.id ?? 0)

const request = ref<InventoryRequest | null>(null)
const departments = ref<Department[]>([])
const warehouses = ref<InventoryWarehouse[]>([])
const items = ref<InventoryItem[]>([])
const suppliers = ref<InventorySupplier[]>([])
const supplierItems = ref<InventorySupplierItem[]>([])
const detail = reactive<Record<number, InventoryItem>>({})
const loaded = ref(false)
const busy = ref(false)
const error = ref<ApiError | null>(null)
const formError = ref<ApiError | null>(null)
const notice = ref('')
const fieldError = (f: string) => formError.value?.fieldMessage(f)
const lineError = (i: number, f: string): string | undefined => formError.value?.fieldMessage(`lines[${i}].${f}`)

const form = reactive({ department_id: 0, warehouse_id: 0, needed_on: '', note: '' })
const lines = ref<Line[]>([])
const clientUuid = ref(crypto.randomUUID())

const editable = computed(() => (isNew.value || (!!request.value && (request.value.status === 'DRAFT' || request.value.status === 'RETURNED') && request.value.requester_id === me.value)) && can('inventory.request'))
const decision = computed(() => request.value?.decision ?? null)
const canDecide = computed(() => !!decision.value?.can_decide)
const activeStep = computed(() => request.value?.steps.find((s) => s.cycle === request.value?.cycle && s.status === 'ACTIVE') ?? null)

async function loadMasters(propertyId: number): Promise<void> {
  const path = { params: { path: { propertyId } } }
  const [w, i, s, si] = await Promise.all([
    api.GET('/api/v1/properties/{propertyId}/inventory/warehouses', path),
    api.GET('/api/v1/properties/{propertyId}/inventory/items', { params: { path: { propertyId }, query: { limit: 200, active: true } } }),
    api.GET('/api/v1/properties/{propertyId}/inventory/suppliers', { params: { path: { propertyId }, query: { active: true } } }),
    api.GET('/api/v1/properties/{propertyId}/inventory/supplier-items', path),
  ])
  warehouses.value = (w.data?.data ?? []).filter((x) => x.is_active)
  items.value = i.data?.items ?? []
  suppliers.value = s.data?.data ?? []
  supplierItems.value = si.data?.data ?? []
  departments.value = (await loadDepartments(propertyId)).filter((d) => d.is_active)
}

async function load(): Promise<void> {
  const propertyId = pid.value
  loaded.value = false
  error.value = null
  if (propertyId === null) return
  try {
    if (!requestId.value) {
      await loadMasters(propertyId)
      request.value = null
      lines.value = []
      form.warehouse_id = warehouses.value.find((x) => x.kind === 'MAIN')?.id ?? warehouses.value[0]?.id ?? 0
      form.department_id = 0
      form.needed_on = ''
      form.note = ''
    } else {
      const { data } = await api.GET('/api/v1/properties/{propertyId}/inventory/requests/{id}', { params: { path: { propertyId, id: requestId.value } } })
      request.value = data ?? null
      if (request.value && (request.value.status === 'DRAFT' || request.value.status === 'RETURNED' || request.value.status === 'APPROVED')) await loadMasters(propertyId)
      if (request.value) fill(request.value)
    }
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    loaded.value = true
  }
}
watch([pid, requestId], load, { immediate: true })

function fill(r: InventoryRequest): void {
  form.department_id = r.department_id ?? 0
  form.warehouse_id = r.warehouse_id ?? 0
  form.needed_on = r.needed_on ?? ''
  form.note = r.note ?? ''
  lines.value = r.lines.map((l) => ({ item_id: l.item_id, unit_id: l.unit_id, qty: trimZeros(l.qty), est_unit_price: l.est_unit_price ?? '', supplier_id: l.supplier_id ?? 0 }))
  for (const l of lines.value) void itemDetail(l.item_id)
  edit.qty = Object.fromEntries(r.lines.map((l) => [l.line_no, trimZeros(l.qty)]))
  edit.price = Object.fromEntries(r.lines.map((l) => [l.line_no, l.est_unit_price ?? '']))
}

const trimZeros = (v: string): string => (v.includes('.') ? v.replace(/\.?0+$/, '') : v)

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

/** The last price paid for the item at the supplier of the line (or at the first supplier that sells it), in the unit of the line. */
function suggestedPrice(l: Line): string {
  const sells = supplierItems.value.filter((s) => s.item_id === l.item_id && s.purchase_unit_id === l.unit_id)
  const hit = sells.find((s) => s.supplier_id === l.supplier_id) ?? (l.supplier_id ? undefined : sells[0])
  return hit ? hit.last_price : ''
}
async function itemChanged(l: Line): Promise<void> {
  const it = await itemDetail(l.item_id)
  if (!it) return
  l.unit_id = it.units.find((u) => u.is_default_purchase)?.unit_id ?? it.stock_unit_id
  l.supplier_id = it.default_supplier_id ?? 0
  l.est_unit_price = suggestedPrice(l) || l.est_unit_price
}
function unitChanged(l: Line): void {
  l.est_unit_price = suggestedPrice(l) || l.est_unit_price
}
function addLine(): void {
  const it = freeItems(0)[0]
  if (!it) return
  const l: Line = { item_id: it.id, unit_id: it.stock_unit_id, qty: '', est_unit_price: '', supplier_id: 0 }
  lines.value.push(l)
  void itemChanged(l)
}

/** The stock of the item in the warehouse the goods come to, in stock units (the list of items carries the stock of each warehouse). */
function onHand(l: Line): string {
  const it = items.value.find((i) => i.id === l.item_id)
  if (!it) return ''
  const rows = it.stock.filter((s) => !form.warehouse_id || s.warehouse_id === form.warehouse_id)
  const sum = rows.reduce((a, s) => a + Number(s.qty), 0)
  return `${formatQty(String(sum))} ${it.stock_unit_code}`
}
const lineTotal = (l: Line): string => (isDecimal(l.qty, 6) && isDecimal(l.est_unit_price, decimals.value) ? multiply(l.qty, l.est_unit_price, decimals.value) : '')
const total = computed(() => lines.value.reduce((a, l) => (lineTotal(l) ? addMoney(a, lineTotal(l), decimals.value) : a), decimals.value > 0 ? `0.${'0'.repeat(decimals.value)}` : '0'))
const complete = computed(() => lines.value.length > 0 && lines.value.every((l) => l.item_id > 0 && l.unit_id > 0 && isDecimal(l.qty, 6) && Number(l.qty) > 0 && (l.est_unit_price === '' || isDecimal(l.est_unit_price, decimals.value))))

function body(): { department_id: number | null; warehouse_id: number | null; needed_on: string | null; note: string | null; lines: InventoryRequestLineInput[] } {
  return {
    department_id: form.department_id || null,
    warehouse_id: form.warehouse_id || null,
    needed_on: form.needed_on || null,
    note: form.note.trim() || null,
    lines: lines.value.map((l) => ({ item_id: l.item_id, unit_id: l.unit_id, qty: l.qty.trim(), est_unit_price: l.est_unit_price.trim() || null, supplier_id: l.supplier_id || null })),
  }
}

async function run(action: () => Promise<InventoryRequest | undefined>, message: string): Promise<boolean> {
  busy.value = true
  formError.value = null
  error.value = null
  try {
    const data = await action()
    if (data) {
      notice.value = message
      request.value = data
      fill(data)
      if (requestId.value !== data.id) await router.replace(`/inventory/requests/${data.id}`)
    }
    return true
  } catch (e) {
    formError.value = e instanceof ApiError ? e : null
    return false
  } finally {
    busy.value = false
  }
}

async function save(submit: boolean): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null) return
  const saved = await run(async () => {
    if (isNew.value) return (await api.POST('/api/v1/properties/{propertyId}/inventory/requests', { params: { path: { propertyId } }, body: { client_uuid: clientUuid.value, ...body() } })).data
    return (await api.PUT('/api/v1/properties/{propertyId}/inventory/requests/{id}', { params: { path: { propertyId, id: requestId.value! } }, body: { ...body(), version: request.value!.version } })).data
  }, t('inventory.request.saved'))
  if (saved && submit && request.value) {
    await run(async () => (await api.POST('/api/v1/properties/{propertyId}/inventory/requests/{id}/submit', { params: { path: { propertyId, id: request.value!.id } } })).data, t('inventory.request.submitted'))
  }
}

async function act(path: 'recall' | 'cancel', message: string): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null || !request.value) return
  const id = request.value.id
  await run(async () => (await api.POST(`/api/v1/properties/{propertyId}/inventory/requests/{id}/${path}` as '/api/v1/properties/{propertyId}/inventory/requests/{id}/recall', { params: { path: { propertyId, id } } })).data, message)
}

// ------------------------------------------------------------------------------------------ the decision of the signed-in approver
const edit = reactive<{ on: boolean; note: string; qty: Record<number, string>; price: Record<number, string> }>({ on: false, note: '', qty: {}, price: {} })
const edits = computed(() => {
  const out: { line_no: number; qty?: string; est_unit_price?: string }[] = []
  for (const l of request.value?.lines ?? []) {
    const e: { line_no: number; qty?: string; est_unit_price?: string } = { line_no: l.line_no }
    if ((edit.qty[l.line_no] ?? '') !== trimZeros(l.qty) && edit.qty[l.line_no]) e.qty = edit.qty[l.line_no]
    if ((edit.price[l.line_no] ?? '') !== (l.est_unit_price ?? '') && edit.price[l.line_no]) e.est_unit_price = edit.price[l.line_no]
    if (e.qty !== undefined || e.est_unit_price !== undefined) out.push(e)
  }
  return out
})
const newTotal = computed(() => {
  if (!request.value || !edit.on) return null
  let sum = decimals.value > 0 ? `0.${'0'.repeat(decimals.value)}` : '0'
  for (const l of request.value.lines) {
    const q = edit.qty[l.line_no] ?? ''
    const p = edit.price[l.line_no] ?? ''
    if (isDecimal(q, 6) && isDecimal(p, decimals.value)) sum = addMoney(sum, multiply(q, p, decimals.value), decimals.value)
  }
  return sum
})

async function decide(approve: boolean): Promise<void> {
  const propertyId = pid.value
  const r = request.value
  if (propertyId === null || !r || !decision.value) return
  const level = decision.value.level_no
  await run(async () => {
    const note = edit.note.trim() || null
    if (approve) {
      return (await api.POST('/api/v1/properties/{propertyId}/inventory/requests/{id}/approve', { params: { path: { propertyId, id: r.id } }, body: { level_no: level, note, edits: edit.on ? edits.value : [] } })).data
    }
    return (await api.POST('/api/v1/properties/{propertyId}/inventory/requests/{id}/return', { params: { path: { propertyId, id: r.id } }, body: { level_no: level, note: edit.note.trim() } })).data
  }, approve ? t('inventory.request.approved') : t('inventory.request.returned'))
  edit.note = ''
  edit.on = false
}

// ------------------------------------------------------------------------------------------ the orders of an approved request
const split = reactive<{ warehouse_id: number; send: boolean; supplier: Record<number, number>; price: Record<number, string>; qty: Record<number, string> }>({ warehouse_id: 0, send: true, supplier: {}, price: {}, qty: {} })
const uuids = reactive<Record<number, string>>({})
const canOrder = computed(() => !!request.value && request.value.status === 'APPROVED' && can('inventory.purchase'))
watch(
  () => [request.value?.id, request.value?.status],
  () => {
    const r = request.value
    if (!r || r.status !== 'APPROVED') return
    split.warehouse_id = r.warehouse_id ?? warehouses.value[0]?.id ?? 0
    for (const l of r.lines) {
      split.supplier[l.line_no] = l.supplier_id ?? itemOf(l.item_id)?.default_supplier_id ?? 0
      split.price[l.line_no] = l.est_unit_price ?? ''
      split.qty[l.line_no] = trimZeros(l.qty)
    }
  },
  { immediate: true },
)
const groups = computed(() => {
  const by = new Map<number, { supplier: number; total: string; lines: number[] }>()
  for (const l of request.value?.lines ?? []) {
    const s = split.supplier[l.line_no] ?? 0
    const g = by.get(s) ?? { supplier: s, total: decimals.value > 0 ? `0.${'0'.repeat(decimals.value)}` : '0', lines: [] }
    const q = split.qty[l.line_no] ?? ''
    const p = split.price[l.line_no] ?? ''
    if (isDecimal(q, 6) && isDecimal(p, decimals.value)) g.total = addMoney(g.total, multiply(q, p, decimals.value), decimals.value)
    g.lines.push(l.line_no)
    by.set(s, g)
  }
  return [...by.values()]
})
const supplierName = (id: number): string => suppliers.value.find((s) => s.id === id)?.name ?? ''
const splitReady = computed(() => split.warehouse_id > 0 && groups.value.length > 0 && groups.value.every((g) => g.supplier > 0) && (request.value?.lines ?? []).every((l) => isDecimal(split.price[l.line_no] ?? '', decimals.value) && Number(split.price[l.line_no]) > 0 && isDecimal(split.qty[l.line_no] ?? '', 6) && Number(split.qty[l.line_no]) > 0))

async function makeOrders(): Promise<void> {
  const propertyId = pid.value
  const r = request.value
  if (propertyId === null || !r) return
  busy.value = true
  formError.value = null
  try {
    const orders = groups.value.map((g) => {
      uuids[g.supplier] ??= crypto.randomUUID()
      return { client_uuid: uuids[g.supplier]!, supplier_id: g.supplier, lines: g.lines.map((no) => ({ line_no: no, qty: split.qty[no]!, unit_price: split.price[no]! })) }
    })
    const { data } = await api.POST('/api/v1/properties/{propertyId}/inventory/requests/{id}/purchase-orders', { params: { path: { propertyId, id: r.id } }, body: { warehouse_id: split.warehouse_id, send: split.send, orders } })
    notice.value = t('inventory.request.ordersMade', { n: data?.data.length ?? 0 })
    await load()
  } catch (e) {
    formError.value = e instanceof ApiError ? e : null
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <PageHeader :title="isNew ? t('inventory.request.newTitle') : t('inventory.request.viewTitle', { number: request?.request_number ?? '' })" :description="isNew ? t('inventory.request.hint') : ''">
    <template v-if="request" #marks>
      <Badge :variant="requestVariant(request.status)" data-testid="request-status">{{ t(`inventory.requestStatus.${request.status}`) }}</Badge>
    </template>
    <template #actions>
      <Button as-child variant="outline"><RouterLink to="/inventory/purchasing" data-testid="back">{{ t('inventory.request.back') }}</RouterLink></Button>
    </template>
  </PageHeader>
  <ErrorNotice v-if="error" :error="error" inline />
  <p v-if="notice" class="notice" role="status" data-testid="notice">{{ notice }}</p>
  <p v-if="pid === null" class="muted">{{ t('setup.selectProperty') }}</p>

  <div v-else-if="loaded" class="grid gap-4" :class="request ? 'xl:grid-cols-[minmax(0,1fr)_24rem]' : ''">
    <section class="flex min-w-0 flex-col gap-4">
      <!-- the document: a form for the requester of a draft, read only for everyone else -->
      <form v-if="editable" novalidate class="flex flex-col gap-4" data-testid="request-form" @submit.prevent="save(false)">
        <ErrorNotice v-if="formError" :error="formError" inline data-testid="request-error" />
        <p v-if="request?.status === 'RETURNED' && request.returned_note" class="alert warning" data-testid="returned-note">{{ t('inventory.request.returnedNote', { note: request.returned_note }) }}</p>
        <div class="grid gap-3 sm:grid-cols-3">
          <FormField :label="t('inventory.request.department')" :error="fieldError('department_id')" :hint="t('inventory.request.departmentHint')">
            <template #default="{ id, invalid }">
              <NativeSelect :id="id" v-model.number="form.department_id" name="department_id" :aria-invalid="invalid">
                <option :value="0">{{ t('inventory.request.chooseDepartment') }}</option>
                <option v-for="d in departments" :key="d.id" :value="d.id">{{ departmentLabel(d) }}</option>
              </NativeSelect>
            </template>
          </FormField>
          <FormField :label="t('inventory.request.deliverTo')" :error="fieldError('warehouse_id')">
            <template #default="{ id, invalid }">
              <NativeSelect :id="id" v-model.number="form.warehouse_id" name="warehouse_id" :aria-invalid="invalid">
                <option :value="0">—</option>
                <option v-for="w in warehouses" :key="w.id" :value="w.id">{{ w.code }} · {{ w.name }}</option>
              </NativeSelect>
            </template>
          </FormField>
          <FormField :label="t('inventory.request.neededOn')" :error="fieldError('needed_on')">
            <template #default="{ id }"><Input :id="id" v-model="form.needed_on" type="date" name="needed_on" /></template>
          </FormField>
        </div>
        <FormField :label="t('inventory.request.note')" :error="fieldError('note')">
          <template #default="{ id }"><Input :id="id" v-model="form.note" name="note" maxlength="300" /></template>
        </FormField>

        <p v-if="items.length === 0" class="muted" data-testid="no-items">{{ t('inventory.request.noItems') }}</p>
        <div v-else class="overflow-x-auto">
          <table class="w-full min-w-[56rem] text-sm" data-testid="request-lines">
            <thead>
              <tr class="text-left text-xs text-muted-foreground">
                <th class="py-1 pr-2 font-medium">{{ t('inventory.request.item') }}</th><th class="py-1 pr-2 font-medium">{{ t('inventory.request.qty') }}</th><th class="py-1 pr-2 font-medium">{{ t('inventory.request.unit') }}</th>
                <th class="py-1 pr-2 font-medium">{{ t('inventory.request.onHand') }}</th><th class="py-1 pr-2 font-medium">{{ t('inventory.request.estPrice') }}</th><th class="py-1 pr-2 text-right font-medium">{{ t('inventory.request.estTotal') }}</th>
                <th class="py-1 pr-2 font-medium">{{ t('inventory.request.supplier') }}</th><th />
              </tr>
            </thead>
            <tbody>
              <tr v-for="(l, i) in lines" :key="i" class="border-t border-border align-top" :data-testid="`line-${i}`">
                <td class="py-2 pr-2">
                  <NativeSelect v-model.number="l.item_id" :name="`item_${i}`" class="max-w-56" :aria-label="t('inventory.request.item')" :aria-invalid="!!lineError(i, 'item_id')" @change="itemChanged(l)">
                    <option v-for="it in freeItems(l.item_id)" :key="it.id" :value="it.id">{{ it.code }} · {{ it.name }}</option>
                  </NativeSelect>
                  <p v-if="lineError(i, 'item_id')" class="m-0 mt-1 text-xs text-destructive">{{ lineError(i, 'item_id') }}</p>
                </td>
                <td class="py-2 pr-2">
                  <QuantityInput v-model="l.qty" :name="`qty_${i}`" class="w-24" :aria-label="t('inventory.request.qty')" :aria-invalid="!!lineError(i, 'qty')" />
                  <p v-if="lineError(i, 'qty')" class="m-0 mt-1 text-xs text-destructive">{{ lineError(i, 'qty') }}</p>
                </td>
                <td class="py-2 pr-2">
                  <NativeSelect v-model.number="l.unit_id" :name="`unit_${i}`" class="w-28" :aria-label="t('inventory.request.unit')" @change="unitChanged(l)">
                    <option v-for="u in unitsOf(l)" :key="u.unit_id" :value="u.unit_id">{{ u.unit_code }}<template v-if="u.stock_num !== u.stock_den || u.unit_id !== itemOf(l.item_id)?.stock_unit_id"> ({{ u.stock_num }}<template v-if="u.stock_den !== 1">/{{ u.stock_den }}</template> {{ itemOf(l.item_id)?.stock_unit_code }})</template></option>
                  </NativeSelect>
                </td>
                <td class="py-2 pr-2 text-muted-foreground" :data-testid="`on-hand-${i}`">{{ onHand(l) }}</td>
                <td class="py-2 pr-2">
                  <MoneyInput v-model="l.est_unit_price" :name="`price_${i}`" :decimals="decimals" class="w-32" :aria-label="t('inventory.request.estPrice')" :aria-invalid="!!lineError(i, 'est_unit_price')" />
                  <p v-if="lineError(i, 'est_unit_price')" class="m-0 mt-1 text-xs text-destructive">{{ lineError(i, 'est_unit_price') }}</p>
                </td>
                <td class="py-2 pr-2 text-right" :data-testid="`line-total-${i}`">{{ lineTotal(l) ? formatMoney(lineTotal(l)) : '' }}</td>
                <td class="py-2 pr-2">
                  <NativeSelect v-model.number="l.supplier_id" :name="`supplier_${i}`" class="max-w-44" :aria-label="t('inventory.request.supplier')">
                    <option :value="0">—</option>
                    <option v-for="s in suppliers" :key="s.id" :value="s.id">{{ s.name }}</option>
                  </NativeSelect>
                </td>
                <td class="py-2 text-right"><Button type="button" variant="ghost" size="sm" :data-testid="`line-remove-${i}`" @click="lines.splice(i, 1)">{{ t('common.remove') }}</Button></td>
              </tr>
            </tbody>
            <tfoot>
              <tr class="border-t border-border">
                <td colspan="5" class="py-2 pr-2 text-right font-medium">{{ t('inventory.request.estTotal') }}</td>
                <td class="py-2 pr-2 text-right font-semibold" data-testid="request-total">{{ formatMoney(total) }}</td><td colspan="2" />
              </tr>
            </tfoot>
          </table>
        </div>
        <div class="flex flex-wrap items-center justify-between gap-2">
          <Button type="button" variant="outline" size="sm" data-testid="line-add" :disabled="freeItems(0).length === 0" @click="addLine">{{ t('inventory.request.addLine') }}</Button>
          <div class="flex flex-wrap gap-2">
            <Button v-if="request" type="button" variant="ghost" data-testid="request-cancel" :disabled="busy" @click="act('cancel', t('inventory.request.cancelled'))">{{ t('inventory.request.cancel') }}</Button>
            <Button type="submit" variant="outline" :disabled="busy || !complete" data-testid="request-save">{{ t('inventory.request.saveDraft') }}</Button>
            <Button type="button" :disabled="busy || !complete || form.department_id === 0" data-testid="request-submit" @click="save(true)">{{ t('inventory.request.submit') }}</Button>
          </div>
        </div>
      </form>

      <template v-else-if="request">
        <ErrorNotice v-if="formError" :error="formError" inline data-testid="request-error" />
        <p v-if="request.status === 'RETURNED' && request.returned_note" class="alert warning" data-testid="returned-note">{{ t('inventory.request.returnedNote', { note: request.returned_note }) }}</p>
        <dl class="m-0 grid max-w-3xl grid-cols-[auto_1fr] gap-x-4 gap-y-1 text-sm" data-testid="request-facts">
          <dt class="text-muted-foreground">{{ t('inventory.request.requester') }}</dt><dd class="m-0">{{ request.requester_name }}<template v-if="request.submitted_at"> · {{ t('inventory.request.submittedAt', { date: formatDate(request.submitted_at.slice(0, 10)) }) }}</template></dd>
          <dt class="text-muted-foreground">{{ t('inventory.request.department') }}</dt><dd class="m-0">{{ request.department_code ? `${request.department_code} · ${request.department_name}` : '—' }}</dd>
          <dt class="text-muted-foreground">{{ t('inventory.request.deliverTo') }}</dt><dd class="m-0">{{ request.warehouse_name ?? '—' }}</dd>
          <dt class="text-muted-foreground">{{ t('inventory.request.neededOn') }}</dt><dd class="m-0">{{ request.needed_on ? formatDate(request.needed_on) : '—' }}</dd>
          <dt v-if="request.note" class="text-muted-foreground">{{ t('inventory.request.note') }}</dt><dd v-if="request.note" class="m-0">{{ request.note }}</dd>
          <dt class="text-muted-foreground">{{ t('inventory.request.stageLabel') }}</dt><dd class="m-0" data-testid="request-stage">{{ stageText(request.stage, request.status) }}</dd>
        </dl>
        <div class="overflow-x-auto">
          <table class="w-full min-w-[46rem] text-sm" data-testid="request-lines-read">
            <thead>
              <tr class="text-left text-xs text-muted-foreground">
                <th class="py-1 pr-2 font-medium">{{ t('inventory.request.item') }}</th><th class="py-1 pr-2 text-right font-medium">{{ t('inventory.request.qty') }}</th><th class="py-1 pr-2 font-medium">{{ t('inventory.request.unit') }}</th>
                <th class="py-1 pr-2 text-right font-medium">{{ t('inventory.request.onHand') }}</th><th class="py-1 pr-2 text-right font-medium">{{ t('inventory.request.estPrice') }}</th><th class="py-1 pr-2 text-right font-medium">{{ t('inventory.request.estTotal') }}</th>
                <th class="py-1 pr-2 font-medium">{{ t('inventory.request.supplier') }}</th><th v-if="request.status === 'CONVERTED'" class="py-1 font-medium">{{ t('inventory.request.order') }}</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="l in request.lines" :key="l.line_no" class="border-t border-border" :data-testid="`read-line-${l.line_no}`">
                <td class="py-2 pr-2">{{ l.item_name }} <span class="text-xs text-muted-foreground">{{ l.item_code }}</span></td>
                <td class="py-2 pr-2 text-right">
                  <QuantityInput v-if="canDecide && edit.on && activeStep?.edit_allowed" v-model="edit.qty[l.line_no]!" :name="`edit_qty_${l.line_no}`" class="w-24 text-right" :aria-label="t('inventory.request.qty')" />
                  <template v-else>{{ formatQty(l.qty) }}</template>
                </td>
                <td class="py-2 pr-2">{{ l.unit_code }}</td>
                <td class="py-2 pr-2 text-right text-muted-foreground">{{ formatQty(l.on_hand) }} {{ l.stock_unit_code }}</td>
                <td class="py-2 pr-2 text-right">
                  <MoneyInput v-if="canDecide && edit.on && activeStep?.edit_allowed" v-model="edit.price[l.line_no]!" :name="`edit_price_${l.line_no}`" :decimals="decimals" class="w-32 text-right" :aria-label="t('inventory.request.estPrice')" />
                  <template v-else>{{ l.est_unit_price ? formatMoney(l.est_unit_price) : '—' }}</template>
                </td>
                <td class="py-2 pr-2 text-right">{{ formatMoney(l.amount) }}</td>
                <td class="py-2 pr-2">{{ l.supplier_name ?? '—' }}</td>
                <td v-if="request.status === 'CONVERTED'" class="py-2">{{ l.po_number ?? '—' }}</td>
              </tr>
            </tbody>
            <tfoot>
              <tr class="border-t border-border">
                <td colspan="5" class="py-2 pr-2 text-right font-medium">{{ t('inventory.request.estTotal') }}</td>
                <td class="py-2 pr-2 text-right font-semibold" data-testid="request-total">{{ formatMoney(newTotal ?? request.est_total) }}<span v-if="newTotal !== null && newTotal !== request.est_total" class="block text-xs font-normal text-muted-foreground">{{ t('inventory.request.wasTotal', { total: formatMoney(request.est_total) }) }}</span></td><td colspan="2" />
              </tr>
            </tfoot>
          </table>
        </div>

        <div v-if="request.status === 'SUBMITTED' && request.requester_id === me" class="flex justify-end">
          <Button type="button" variant="outline" :disabled="busy" data-testid="request-recall" @click="act('recall', t('inventory.request.recalled'))">{{ t('inventory.request.recall') }}</Button>
        </div>

        <!-- an approved request is turned into orders, one for each supplier -->
        <section v-if="canOrder" class="rounded-xl border border-border bg-card p-4" data-testid="orders-box">
          <h2 class="m-0 text-base font-semibold">{{ t('inventory.request.ordersTitle') }}</h2>
          <p class="muted mt-1 text-sm">{{ t('inventory.request.ordersHint') }}</p>
          <div class="mt-3 overflow-x-auto">
            <table class="w-full min-w-[40rem] text-sm">
              <thead>
                <tr class="text-left text-xs text-muted-foreground">
                  <th class="py-1 pr-2 font-medium">{{ t('inventory.request.item') }}</th><th class="py-1 pr-2 font-medium">{{ t('inventory.request.qty') }}</th><th class="py-1 pr-2 font-medium">{{ t('inventory.request.supplier') }}</th>
                  <th class="py-1 font-medium">{{ t('inventory.request.orderPrice') }}</th>
                </tr>
              </thead>
              <tbody>
                <tr v-for="l in request.lines" :key="l.line_no" class="border-t border-border align-top" :data-testid="`order-line-${l.line_no}`">
                  <td class="py-2 pr-2">{{ l.item_name }}</td>
                  <td class="py-2 pr-2"><QuantityInput v-model="split.qty[l.line_no]!" :name="`order_qty_${l.line_no}`" class="w-24" :aria-label="t('inventory.request.qty')" /> <span class="text-xs text-muted-foreground">{{ l.unit_code }}</span></td>
                  <td class="py-2 pr-2">
                    <NativeSelect v-model.number="split.supplier[l.line_no]" :name="`order_supplier_${l.line_no}`" class="max-w-52" :aria-label="t('inventory.request.supplier')">
                      <option :value="0">{{ t('inventory.request.chooseSupplier') }}</option>
                      <option v-for="s in suppliers" :key="s.id" :value="s.id">{{ s.name }}</option>
                    </NativeSelect>
                  </td>
                  <td class="py-2"><MoneyInput v-model="split.price[l.line_no]!" :name="`order_price_${l.line_no}`" :decimals="decimals" class="w-32" :aria-label="t('inventory.request.orderPrice')" /></td>
                </tr>
              </tbody>
            </table>
          </div>
          <ul class="m-0 mt-3 list-none p-0 text-sm" data-testid="order-groups">
            <li v-for="g in groups" :key="g.supplier" class="flex justify-between gap-2 border-t border-border py-1.5"><span>{{ g.supplier ? supplierName(g.supplier) : t('inventory.request.noSupplier') }}</span><strong>{{ formatMoney(g.total) }}</strong></li>
          </ul>
          <div class="mt-3 flex flex-wrap items-center justify-between gap-2">
            <div class="flex flex-wrap items-center gap-4">
              <FormField class="max-w-56" :label="t('inventory.request.deliverTo')">
                <template #default="{ id }">
                  <NativeSelect :id="id" v-model.number="split.warehouse_id" name="order_warehouse"><option v-for="w in warehouses" :key="w.id" :value="w.id">{{ w.code }} · {{ w.name }}</option></NativeSelect>
                </template>
              </FormField>
              <label class="flex items-center gap-2 text-sm"><input v-model="split.send" type="checkbox" name="send" /> {{ t('inventory.request.sendNow') }}</label>
            </div>
            <Button type="button" :disabled="busy || !splitReady" data-testid="orders-make" @click="makeOrders">{{ t('inventory.request.makeOrders', { n: groups.length }) }}</Button>
          </div>
        </section>
      </template>
    </section>

    <!-- the chain beside the document: who decided each level, what is waited for, and the decision of the signed-in approver -->
    <aside v-if="request && request.status !== 'DRAFT' && request.status !== 'CANCELLED' || (request && request.steps.length > 0)" class="flex min-w-0 flex-col gap-4" data-testid="chain-aside">
      <ApprovalChain :request="request" />
      <section v-if="decision && request.status === 'SUBMITTED'" class="rounded-xl border border-border bg-card p-4" data-testid="decision-box">
        <h3 class="m-0 text-sm font-semibold">{{ t('inventory.request.yourDecision', { level: decision.level_no }) }}</h3>
        <template v-if="canDecide">
          <p class="muted mt-1 text-sm">{{ t('inventory.request.decisionHint') }}</p>
          <label v-if="activeStep?.edit_allowed" class="mt-2 flex items-center gap-2 text-sm"><input v-model="edit.on" type="checkbox" name="edit_lines" data-testid="decision-edit" /> {{ t('inventory.request.editLines') }}</label>
          <FormField class="mt-2" :label="t('inventory.request.decisionNote')" :hint="t('inventory.request.decisionNoteHint')" :error="formError?.fieldMessage('note')">
            <template #default="{ id }"><textarea :id="id" v-model="edit.note" name="note" rows="3" maxlength="500" class="w-full rounded-md border border-border bg-card px-3 py-2 text-sm" data-testid="decision-note" /></template>
          </FormField>
          <div class="mt-3 flex justify-end gap-2">
            <Button type="button" variant="outline" :disabled="busy || !edit.note.trim()" data-testid="decision-return" @click="decide(false)">{{ t('inventory.request.returnIt') }}</Button>
            <Button type="button" :disabled="busy" data-testid="decision-approve" @click="decide(true)">{{ t('inventory.request.approve') }}</Button>
          </div>
        </template>
        <p v-else class="muted mt-1 text-sm" data-testid="decision-refused">{{ t(`inventory.request.cannotDecide.${decision.reason ?? 'NOT_ELIGIBLE'}`) }}</p>
      </section>
    </aside>
  </div>
</template>
