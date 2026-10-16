<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { Approval, PosBill, PosBillLine, PosBillSummary, PosCatalog, PosOutlet } from '@/api/types'
import ApprovalDialog from '@/components/ApprovalDialog.vue'
import EmptyState from '@/components/app/EmptyState.vue'
import ErrorNotice from '@/components/app/ErrorNotice.vue'
import FormField from '@/components/app/FormField.vue'
import PageHeader from '@/components/app/PageHeader.vue'
import PosPayDialog from '@/components/pos/PosPayDialog.vue'
import PosPrintStatus from '@/components/pos/PosPrintStatus.vue'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { NativeSelect } from '@/components/ui/native-select'
import { t } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import { formatMoney } from '@/utils/format'
import { printReceipt } from '@/utils/posReceipt'
import { newIdempotencyKey } from '@/utils/reservations'
import { debounce, usePosEvents } from '@/composables/usePosEvents'

/**
 * The order and the bill of an outlet (docs/architecture/34-pos.md 11, mockups 11 and 16): the menu of the outlet on one side, the lines of the bill on the other, in the order they were made, with
 * what was already sent to the kitchen or the bar, what is still a draft and what was voided. Everything money is the server's: a draft carries its quote, a sent line the amounts of its ledger row.
 */
const route = useRoute()
const router = useRouter()
const auth = useAuthStore()
const property = usePropertyStore()
const pid = computed(() => property.currentId)
const billId = computed(() => Number(route.params.id))

const bill = ref<PosBill | null>(null)
const catalog = ref<PosCatalog | null>(null)
const error = ref<ApiError | null>(null)
const busy = ref(false)
const categoryId = ref<number | null>(null)
const search = ref('')
const notice = ref('')

const can = (permission: string): boolean => auth.can(permission, pid.value)
const isOpen = computed(() => bill.value?.status === 'OPEN')
const drafts = computed(() => bill.value?.lines.filter((l) => l.status === 'DRAFT') ?? [])
const sentRounds = computed(() => {
  const by = new Map<number, PosBillLine[]>()
  for (const l of bill.value?.lines ?? []) {
    if (l.status !== 'SENT') continue
    by.set(l.round, [...(by.get(l.round) ?? []), l])
  }
  return [...by.entries()].sort((a, b) => a[0] - b[0])
})
const voided = computed(() => bill.value?.lines.filter((l) => l.status === 'VOID') ?? [])
const totals = computed(() => bill.value?.totals ?? null)

async function loadBill(keepError = false): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null || !Number.isInteger(billId.value)) return
  try {
    const { data } = await api.GET('/api/v1/properties/{propertyId}/pos/bills/{id}', { params: { path: { propertyId, id: billId.value } } })
    bill.value = data ?? null
    if (!keepError) error.value = null
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  }
}

async function loadCatalog(outletId: number): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null) return
  try {
    const { data } = await api.GET('/api/v1/properties/{propertyId}/pos/outlets/{id}/catalog', { params: { path: { propertyId, id: outletId } } })
    catalog.value = data ?? null
    categoryId.value = catalog.value?.categories[0]?.id ?? null
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  }
}

watch([pid, billId], () => void loadBill(), { immediate: true })
// The bill follows the stream of its outlet: another waiter's line, the kitchen's status of a ticket, a payment at the cashier show here by themselves.
const refreshBill = debounce(() => void loadBill(true), 150)
usePosEvents({
  propertyId: () => pid.value,
  channels: () => (bill.value ? [`outlet:${bill.value.outlet_id}`] : []),
  onEvent: (e) => {
    if ((e.type === 'bill.updated' || e.type === 'bill.closed') && e.entity_id === billId.value) refreshBill()
    else if (e.type === 'ticket.updated' || e.type === 'ticket.created') refreshBill()
    else if (e.type === 'product.sold_out' && bill.value) void loadCatalog(bill.value.outlet_id)
  },
  onReload: () => void loadBill(true),
})
watch(() => bill.value?.outlet_id, (id) => { if (id && !catalog.value) void loadCatalog(id) })

// The outlet of the bill: whether it has a receipt printer decides whether Print goes to the printer or to the browser (which can always print).
const outletInfo = ref<PosOutlet | null>(null)
watch(() => bill.value?.outlet_id, async (id) => {
  const propertyId = pid.value
  if (!id || propertyId === null) return
  try {
    const { data } = await api.GET('/api/v1/properties/{propertyId}/pos/outlets/{id}', { params: { path: { propertyId, id } } })
    outletInfo.value = data ?? null
  } catch {
    outletInfo.value = null
  }
}, { immediate: true })

// ---- the paper: a pre-bill while the bill is open, a copy of the receipt (with a reason) after it is paid
const printDialog = ref(false)
const printReason = ref('')
const printError = ref<ApiError | null>(null)
async function printPaper(): Promise<void> {
  const b = bill.value
  if (!b) return
  if (!outletInfo.value?.receipt_printer_id) {
    printReceipt(b, t) // no receipt printer: the browser prints
    return
  }
  if (b.status !== 'OPEN') {
    printReason.value = ''
    printError.value = null
    printDialog.value = true
    return
  }
  await sendPrint('')
}
async function sendPrint(reasonText: string): Promise<void> {
  const propertyId = pid.value
  const b = bill.value
  if (propertyId === null || !b) return
  busy.value = true
  printError.value = null
  try {
    await api.POST('/api/v1/properties/{propertyId}/pos/bills/{id}/print', { params: { path: { propertyId, id: b.id } }, body: reasonText ? { reason: reasonText } : {} })
    printDialog.value = false
    notice.value = t(b.status === 'OPEN' ? 'posBill.prebillQueued' : 'posBill.copyQueued')
  } catch (e) {
    if (e instanceof ApiError && e.code === 'OUTLET_NO_RECEIPT_PRINTER') {
      printDialog.value = false
      printReceipt(b, t)
    } else if (printDialog.value) {
      printError.value = e instanceof ApiError ? e : null
    } else {
      error.value = e instanceof ApiError ? e : null
    }
  } finally {
    busy.value = false
  }
}

const products = computed(() => {
  const q = search.value.trim().toLowerCase()
  return (catalog.value?.products ?? []).filter((p) => (q ? p.name.toLowerCase().includes(q) || p.code.toLowerCase().includes(q) : p.category_id === categoryId.value))
})
const soldOut = (id: number): boolean => catalog.value?.sold_out.includes(id) ?? false
const alreadyOrdered = (id: number): number => (bill.value?.lines ?? []).filter((l) => l.product_id === id && l.status !== 'VOID').reduce((n, l) => n + Number(l.quantity), 0)

/** Runs one request, shows its error, and reads the bill again. */
async function run(fn: () => Promise<void>): Promise<boolean> {
  busy.value = true
  error.value = null
  try {
    await fn()
    return true
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
    if (e instanceof ApiError && e.code === 'BILL_NOT_OPEN') await loadBill(true)
    return false
  } finally {
    busy.value = false
  }
}

// ---- adding
const optionsFor = ref<PosCatalog['products'][number] | null>(null)
const picked = ref<Record<number, number[]>>({})
const lineNote = ref('')
const lineQty = ref('1')

function tapProduct(p: PosCatalog['products'][number]): void {
  if (!isOpen.value || soldOut(p.id)) return
  if ((p.option_groups ?? []).length > 0) {
    picked.value = {}
    for (const g of p.option_groups ?? []) picked.value[g.id ?? 0] = (g.options ?? []).filter((o) => o.is_default && o.is_active !== false).map((o) => o.id ?? 0)
    lineNote.value = ''
    lineQty.value = '1'
    optionsFor.value = p
    return
  }
  const existing = drafts.value.find((l) => l.product_id === p.id && l.options.length === 0 && !l.note)
  if (existing) void setQuantity(existing, Number(existing.quantity) + 1)
  else void addLine(p.id, '1', [], '')
}

async function addLine(productId: number, quantity: string, options: { option_id: number; qty?: number }[], note: string): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null) return
  await run(async () => {
    const { data } = await api.POST('/api/v1/properties/{propertyId}/pos/bills/{id}/lines', {
      params: { path: { propertyId, id: billId.value } },
      body: { client_uuid: newIdempotencyKey(), product_id: productId, quantity, options, ...(note ? { note } : {}) },
    })
    if (data) bill.value = data
  })
}

async function confirmOptions(): Promise<void> {
  const p = optionsFor.value
  if (!p) return
  const options = Object.values(picked.value).flat().filter((id) => id > 0).map((option_id) => ({ option_id }))
  await addLine(p.id, lineQty.value || '1', options, lineNote.value.trim())
  if (!error.value) optionsFor.value = null
}

function togglePick(groupId: number, optionId: number, single: boolean): void {
  const cur = picked.value[groupId] ?? []
  if (single) picked.value[groupId] = [optionId]
  else picked.value[groupId] = cur.includes(optionId) ? cur.filter((x) => x !== optionId) : [...cur, optionId]
}

async function setQuantity(l: PosBillLine, q: number): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null) return
  if (q < 1) return void removeLine(l)
  await run(async () => {
    const { data } = await api.PUT('/api/v1/properties/{propertyId}/pos/bills/{id}/lines/{lineId}', {
      params: { path: { propertyId, id: billId.value, lineId: l.id } },
      body: { quantity: String(q), options: l.options.map((o) => ({ option_id: o.option_id, qty: o.qty })), note: l.note ?? '' },
    })
    if (data) bill.value = data
  })
}

async function removeLine(l: PosBillLine): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null) return
  await run(async () => {
    const { data } = await api.DELETE('/api/v1/properties/{propertyId}/pos/bills/{id}/lines/{lineId}', { params: { path: { propertyId, id: billId.value, lineId: l.id } } })
    if (data) bill.value = data
  })
}

// ---- send, request, void the bill
async function send(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null) return
  const ok = await run(async () => {
    const { data } = await api.POST('/api/v1/properties/{propertyId}/pos/bills/{id}/send', { params: { path: { propertyId, id: billId.value } }, body: { client_uuid: newIdempotencyKey() } })
    if (data) bill.value = data
  })
  if (ok) notice.value = t('posBill.sent')
}

async function requestBill(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null) return
  await run(async () => {
    const { data } = await api.POST('/api/v1/properties/{propertyId}/pos/bills/{id}/request', { params: { path: { propertyId, id: billId.value } } })
    if (data) bill.value = data
  })
}

async function voidBill(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null) return
  const ok = await run(async () => {
    await api.POST('/api/v1/properties/{propertyId}/pos/bills/{id}/void', { params: { path: { propertyId, id: billId.value } } })
  })
  if (ok) await router.push('/pos/floor')
}

// ---- dialogs that need a reason and an approval
type Sheet =
  | { kind: 'void'; line: PosBillLine }
  | { kind: 'discount' }
  | { kind: 'comp' }
  | { kind: 'reopen' }
  | { kind: 'table' }
  | { kind: 'merge' }
  | { kind: 'split' }
  | { kind: 'pay' }
  | null
const sheet = ref<Sheet>(null)
const stage = ref<'form' | 'approval'>('form')
const reason = ref('')
const scope = ref<'BILL' | 'LINE'>('BILL')
const lineChoice = ref<number | null>(null)
const mode = ref<'percent' | 'amount'>('percent')
const figure = ref('')
const compName = ref('')
const dialogError = ref<ApiError | null>(null)
const tableChoice = ref<number | null>(null)
const mergeChoice = ref<number | null>(null)
const splitLines = ref<number[]>([])
const others = ref<PosBillSummary[]>([])
const tables = ref<{ id: number; code: string }[]>([])

function openSheet(s: Sheet): void {
  sheet.value = s
  stage.value = 'form'
  reason.value = ''
  figure.value = ''
  compName.value = ''
  dialogError.value = null
  scope.value = 'BILL'
  lineChoice.value = null
  splitLines.value = []
}

const closeSheet = (): void => { sheet.value = null }

async function approved(approval: Approval | undefined): Promise<void> {
  const propertyId = pid.value
  const s = sheet.value
  if (propertyId === null || !s) return
  dialogError.value = null
  busy.value = true
  try {
    let data: PosBill | undefined
    if (s.kind === 'void') {
      ;({ data } = await api.POST('/api/v1/properties/{propertyId}/pos/bills/{id}/lines/{lineId}/void', {
        params: { path: { propertyId, id: billId.value, lineId: s.line.id } }, body: { reason: reason.value.trim(), ...(approval ? { approval } : {}) },
      }))
    } else if (s.kind === 'discount') {
      const body = {
        scope: scope.value, reason: reason.value.trim(), ...(scope.value === 'LINE' && lineChoice.value ? { line_id: lineChoice.value } : {}),
        ...(mode.value === 'percent' ? { percent: figure.value } : { amount: figure.value }), ...(approval ? { approval } : {}),
      }
      ;({ data } = await api.POST('/api/v1/properties/{propertyId}/pos/bills/{id}/discount', { params: { path: { propertyId, id: billId.value } }, body }))
    } else if (s.kind === 'comp') {
      ;({ data } = await api.POST('/api/v1/properties/{propertyId}/pos/bills/{id}/comp', {
        params: { path: { propertyId, id: billId.value } }, body: { reason: reason.value.trim(), name: compName.value.trim(), ...(approval ? { approval } : {}) },
      }))
    } else if (s.kind === 'reopen') {
      ;({ data } = await api.POST('/api/v1/properties/{propertyId}/pos/bills/{id}/reopen', {
        params: { path: { propertyId, id: billId.value } }, body: { reason: reason.value.trim(), ...(approval ? { approval } : {}) },
      }))
    }
    if (data) bill.value = data
    sheet.value = null
  } catch (e) {
    const err = e instanceof ApiError ? e : null
    dialogError.value = err
    // a discount within what the outlet lets its cashier give needs no approver; above it the server asks, and the dialog of approval follows
    if (err?.code === 'APPROVAL_REQUIRED' && s.kind === 'discount' && !approval) {
      dialogError.value = null
      stage.value = 'approval'
    }
  } finally {
    busy.value = false
  }
}

function submitForm(): void {
  const s = sheet.value
  if (!s) return
  if (s.kind === 'discount') void approved(undefined) // first without an approver: the server says when one is needed
  else stage.value = 'approval'
}

const approvalKind = computed(() => {
  switch (sheet.value?.kind) {
    case 'void': return 'POS_VOID' as const
    case 'discount': return 'POS_DISCOUNT' as const
    case 'comp': return 'POS_COMP' as const
    default: return 'POS_REOPEN' as const
  }
})
const approvalAmount = computed(() => {
  const s = sheet.value
  if (s?.kind === 'void') return String(Math.abs(Number(s.line.total)))
  if (s?.kind === 'reopen') return totals.value?.sold ?? '0'
  if (s?.kind === 'comp') return totals.value?.payable ?? '0'
  return mode.value === 'amount' && figure.value ? figure.value : undefined
})

// ---- table, merge, split
async function openTableSheet(): Promise<void> {
  openSheet({ kind: 'table' })
  const propertyId = pid.value
  const outletId = bill.value?.outlet_id
  if (propertyId === null || !outletId) return
  const { data } = await api.GET('/api/v1/properties/{propertyId}/pos/outlets/{id}/floor', { params: { path: { propertyId, id: outletId } } })
  tables.value = (data?.areas ?? []).flatMap((a) => a.tables.map((tb) => ({ id: tb.id ?? 0, code: tb.code })))
  tableChoice.value = bill.value?.table_id ?? null
}

async function moveTable(): Promise<void> {
  const propertyId = pid.value
  const b = bill.value
  if (propertyId === null || !b) return
  busy.value = true
  try {
    const { data } = await api.PUT('/api/v1/properties/{propertyId}/pos/bills/{id}', {
      params: { path: { propertyId, id: b.id } },
      body: { table_id: tableChoice.value, order_type: tableChoice.value ? 'DINE_IN' : b.order_type, guest_count: b.guest_count, customer_name: b.customer_name ?? '', note: b.note ?? '', version: b.version },
    })
    if (data) bill.value = data
    sheet.value = null
  } catch (e) {
    dialogError.value = e instanceof ApiError ? e : null
  } finally {
    busy.value = false
  }
}

async function openMergeSheet(): Promise<void> {
  openSheet({ kind: 'merge' })
  const propertyId = pid.value
  const outletId = bill.value?.outlet_id
  if (propertyId === null || !outletId) return
  const { data } = await api.GET('/api/v1/properties/{propertyId}/pos/outlets/{id}/bills', { params: { path: { propertyId, id: outletId }, query: { status: 'OPEN' } } })
  others.value = (data?.data ?? []).filter((x) => x.id !== billId.value)
  mergeChoice.value = null
}

async function merge(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null || mergeChoice.value === null) return
  busy.value = true
  try {
    const { data } = await api.POST('/api/v1/properties/{propertyId}/pos/bills/{id}/merge', { params: { path: { propertyId, id: billId.value } }, body: { into_bill_id: mergeChoice.value } })
    sheet.value = null
    if (data) await router.push(`/pos/bills/${data.id}`)
  } catch (e) {
    dialogError.value = e instanceof ApiError ? e : null
  } finally {
    busy.value = false
  }
}

async function split(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null || splitLines.value.length === 0) return
  busy.value = true
  try {
    const { data } = await api.POST('/api/v1/properties/{propertyId}/pos/bills/{id}/split', {
      params: { path: { propertyId, id: billId.value } }, body: { client_uuid: newIdempotencyKey(), line_ids: splitLines.value },
    })
    sheet.value = null
    if (data) await router.push(`/pos/bills/${data.id}`)
  } catch (e) {
    dialogError.value = e instanceof ApiError ? e : null
  } finally {
    busy.value = false
  }
}

function paid(next: PosBill, message: string): void {
  bill.value = next
  sheet.value = null
  notice.value = message
}

const statusKey = (l: PosBillLine): string => (l.ticket_status ? `posBill.ticket_${l.ticket_status}` : '')
const optionsText = (l: PosBillLine): string => l.options.map((o) => (o.qty > 1 ? `${o.name} ×${o.qty}` : o.name)).join(', ')
const roomChargeLine = computed(() => bill.value?.room_charges.filter((c) => !c.returned_at) ?? [])
</script>

<template>
  <PageHeader :title="bill ? `${t('posBill.title')} ${bill.bill_number}` : t('posBill.title')" :description="bill ? `${bill.outlet_name}${bill.table_code ? ' · ' + t('posFloor.table', { code: bill.table_code }) : ''}${bill.target_room ? ' · ' + t('posFloor.room', { room: bill.target_room }) : ''}` : ''">
    <template #actions>
      <div v-if="bill" class="flex flex-wrap items-center gap-2">
        <Badge variant="secondary" data-testid="bill-status">{{ t(`posBill.status_${bill.status}`) }}</Badge>
        <Button as-child variant="outline" size="sm"><RouterLink to="/pos/floor" data-testid="back-to-floor">{{ t('posBill.backToFloor') }}</RouterLink></Button>
      </div>
    </template>
  </PageHeader>
  <ErrorNotice v-if="error" :error="error" inline data-testid="bill-error" />
  <p v-if="notice" class="notice" role="status" data-testid="bill-notice">{{ notice }}</p>
  <PosPrintStatus
    v-if="bill && pid !== null && outletInfo" class="mb-3" :property-id="pid" :outlet-id="bill.outlet_id" :receipt-printer-id="outletInfo.receipt_printer_id ?? null"
    :order-mode="outletInfo.order_mode" :can-reprint="can('pos.cashier')"
  />

  <div v-if="bill" class="grid gap-4 lg:grid-cols-[minmax(0,1fr)_26rem]">
    <!-- the menu -->
    <section v-if="isOpen" class="min-w-0" data-testid="menu">
      <div class="mb-3 flex flex-wrap items-center gap-2">
        <Input v-model="search" type="search" name="search" class="w-48" :placeholder="t('posBill.searchMenu')" :aria-label="t('posBill.searchMenu')" data-testid="menu-search" />
        <div class="flex flex-wrap gap-1" role="tablist" :aria-label="t('posBill.categories')">
          <Button
            v-for="c in catalog?.categories ?? []" :key="c.id" size="sm" role="tab" :aria-selected="c.id === categoryId && !search" :variant="c.id === categoryId && !search ? 'default' : 'outline'"
            :data-testid="`category-${c.id}`" @click="categoryId = c.id; search = ''"
          >{{ c.name }}</Button>
        </div>
      </div>
      <EmptyState v-if="products.length === 0" :title="t('posBill.noProducts')" :description="t('posBill.noProductsHint')" data-testid="no-products" />
      <div v-else class="grid grid-cols-2 gap-2 sm:grid-cols-3 xl:grid-cols-4" data-testid="products">
        <button
          v-for="p in products" :key="p.id" type="button" :disabled="soldOut(p.id) || busy"
          class="relative flex min-h-20 flex-col items-start justify-between gap-1 rounded-lg border border-border bg-card p-2 text-left text-sm hover:bg-accent disabled:cursor-not-allowed disabled:opacity-50"
          :data-testid="`product-${p.id}`" @click="tapProduct(p)"
        >
          <strong class="line-clamp-2 pr-7 leading-tight">{{ p.name }}</strong>
          <span class="tabular-nums text-muted-foreground">{{ formatMoney(p.price ?? '') }}</span>
          <span v-if="soldOut(p.id)" class="text-xs font-medium text-destructive">{{ t('posBill.soldOut') }}</span>
          <span v-else-if="alreadyOrdered(p.id) > 0" class="absolute right-1 top-1 rounded-full bg-primary px-1.5 text-xs text-primary-foreground" :data-testid="`ordered-${p.id}`">{{ alreadyOrdered(p.id) }}</span>
          <span v-if="(p.option_groups ?? []).length > 0" class="text-[11px] text-muted-foreground">{{ t('posBill.hasOptions') }}</span>
        </button>
      </div>
    </section>
    <section v-else class="min-w-0">
      <p class="alert" data-testid="bill-closed">{{ t(`posBill.closedNote_${bill.status}`) }}</p>
    </section>

    <!-- the bill -->
    <aside class="min-w-0 rounded-xl border border-border bg-card p-3" data-testid="bill-panel">
      <div class="flex flex-wrap items-baseline justify-between gap-2">
        <strong>{{ bill.bill_number }}</strong>
        <span class="text-xs text-muted-foreground">{{ t(`posFloor.type_${bill.order_type}`) }}<template v-if="bill.guest_count"> · {{ t('posFloor.guests', { n: bill.guest_count }) }}</template><template v-if="bill.waiter_name"> · {{ bill.waiter_name }}</template></span>
      </div>

      <!-- drafts -->
      <h3 v-if="drafts.length > 0" class="m-0 mb-1 mt-3 text-xs font-semibold uppercase tracking-wide text-muted-foreground">{{ t('posBill.drafts') }}</h3>
      <ul v-if="drafts.length > 0" class="m-0 list-none p-0" data-testid="draft-lines">
        <li v-for="l in drafts" :key="l.id" class="border-t border-border py-2 text-sm" :data-testid="`line-${l.id}`">
          <div class="flex items-baseline gap-2">
            <span class="min-w-0 flex-1"><strong>{{ l.product_name }}</strong><span v-if="l.options.length > 0" class="block text-xs text-muted-foreground">{{ optionsText(l) }}</span><span v-if="l.note" class="block text-xs italic text-muted-foreground">{{ l.note }}</span></span>
            <span class="tabular-nums" :data-testid="`amount-${l.id}`">{{ formatMoney(l.net_amount) }}</span>
          </div>
          <div class="mt-1 flex items-center gap-1">
            <Button type="button" size="sm" variant="outline" class="size-9 p-0" :aria-label="t('posBill.less')" :data-testid="`less-${l.id}`" :disabled="busy" @click="setQuantity(l, Number(l.quantity) - 1)">−</Button>
            <span class="w-8 text-center tabular-nums" :data-testid="`qty-${l.id}`">{{ Number(l.quantity) }}</span>
            <Button type="button" size="sm" variant="outline" class="size-9 p-0" :aria-label="t('posBill.more')" :data-testid="`more-${l.id}`" :disabled="busy" @click="setQuantity(l, Number(l.quantity) + 1)">+</Button>
            <span v-if="Number(l.discount_amount) > 0" class="ml-2 text-xs text-primary">{{ t('posBill.discounted', { amount: formatMoney(l.discount_amount) }) }}</span>
            <Button type="button" size="sm" variant="ghost" class="ml-auto" :aria-label="t('posBill.remove')" :data-testid="`remove-${l.id}`" :disabled="busy" @click="removeLine(l)">{{ t('posBill.remove') }}</Button>
          </div>
        </li>
      </ul>

      <!-- sent, by round -->
      <template v-for="[round, ls] in sentRounds" :key="round">
        <h3 class="m-0 mb-1 mt-3 text-xs font-semibold uppercase tracking-wide text-muted-foreground">{{ round > 1 ? t('posBill.addition', { n: round }) : t('posBill.round', { n: round }) }}</h3>
        <ul class="m-0 list-none p-0" :data-testid="`round-${round}`">
          <li v-for="l in ls" :key="l.id" class="flex items-baseline gap-2 border-t border-border py-2 text-sm" :data-testid="`line-${l.id}`">
            <span class="min-w-0 flex-1">
              <strong>{{ Number(l.quantity) }} × {{ l.product_name }}</strong>
              <span v-if="l.options.length > 0" class="block text-xs text-muted-foreground">{{ optionsText(l) }}</span>
              <span v-if="statusKey(l)" class="block text-xs text-muted-foreground" :data-testid="`ticket-${l.id}`">{{ t(statusKey(l)) }}</span>
            </span>
            <span class="tabular-nums" :data-testid="`amount-${l.id}`">{{ formatMoney(l.net_amount) }}</span>
            <Button v-if="isOpen && can('pos.void')" type="button" size="sm" variant="ghost" :aria-label="t('posBill.void')" :data-testid="`void-${l.id}`" @click="openSheet({ kind: 'void', line: l })">{{ t('posBill.void') }}</Button>
          </li>
        </ul>
      </template>

      <!-- voided -->
      <ul v-if="voided.length > 0" class="m-0 mt-3 list-none p-0" data-testid="void-lines">
        <li v-for="l in voided" :key="l.id" class="border-t border-border py-1.5 text-xs text-muted-foreground" :data-testid="`line-${l.id}`">
          <span class="line-through">{{ Number(l.quantity) }} × {{ l.product_name }}</span>
          · {{ t('posBill.voidedBy', { reason: l.void_reason ?? '', who: l.approved_by_name || l.voided_by_name || '' }) }}
        </li>
      </ul>

      <EmptyState v-if="bill.lines.length === 0" :title="t('posBill.empty')" :description="t('posBill.emptyHint')" data-testid="bill-empty" />

      <!-- the totals -->
      <dl v-if="totals && bill.lines.length > 0" class="m-0 mt-3 grid grid-cols-[1fr_auto] gap-x-3 gap-y-0.5 border-t border-border pt-3 text-sm" data-testid="totals">
        <dt>{{ t('posBill.net') }}</dt><dd class="m-0 text-right tabular-nums">{{ formatMoney(totals.net) }}</dd>
        <dt>{{ t('posBill.service') }}</dt><dd class="m-0 text-right tabular-nums">{{ formatMoney(totals.service) }}</dd>
        <dt>{{ t('posBill.tax') }}</dt><dd class="m-0 text-right tabular-nums">{{ formatMoney(totals.tax) }}</dd>
        <template v-if="Number(totals.discount) !== 0"><dt>{{ t('posBill.discount') }}</dt><dd class="m-0 text-right tabular-nums">−{{ formatMoney(totals.discount) }}</dd></template>
        <template v-if="Number(totals.rounding) !== 0"><dt>{{ t('posBill.rounding') }}</dt><dd class="m-0 text-right tabular-nums">{{ formatMoney(totals.rounding) }}</dd></template>
        <template v-if="Number(totals.paid) > 0"><dt>{{ t('posBill.paid') }}</dt><dd class="m-0 text-right tabular-nums" data-testid="paid">−{{ formatMoney(totals.paid) }}</dd></template>
        <template v-if="Number(totals.transferred) > 0"><dt>{{ t('posBill.toRoom') }}</dt><dd class="m-0 text-right tabular-nums">−{{ formatMoney(totals.transferred) }}</dd></template>
        <dt class="text-base font-semibold">{{ isOpen ? t('posBill.toPay') : t('posBill.total') }}</dt>
        <dd class="m-0 text-right text-base font-semibold tabular-nums" data-testid="payable">{{ formatMoney(isOpen ? totals.payable : totals.sold) }}</dd>
      </dl>
      <ul v-if="roomChargeLine.length > 0" class="m-0 mt-2 list-none p-0 text-xs text-muted-foreground" data-testid="room-charges">
        <li v-for="c in roomChargeLine" :key="c.id">{{ t('posBill.chargedTo', { room: c.room || '', amount: formatMoney(c.amount) }) }}</li>
      </ul>

      <!-- actions -->
      <div v-if="isOpen" class="mt-3 flex flex-wrap gap-2" data-testid="actions">
        <Button type="button" :disabled="busy || drafts.length === 0" data-testid="send" @click="send">{{ t('posBill.send') }}</Button>
        <Button v-if="can('pos.cashier')" type="button" variant="default" :disabled="busy || bill.lines.length === 0" data-testid="pay" @click="openSheet({ kind: 'pay' })">{{ t('posBill.pay') }}</Button>
        <Button type="button" variant="outline" :disabled="busy || bill.lines.length === 0" data-testid="request" @click="requestBill">{{ t('posBill.request') }}</Button>
        <Button type="button" variant="outline" :disabled="busy || bill.lines.length === 0" data-testid="print" @click="printPaper">{{ t('posBill.print') }}</Button>
        <Button v-if="outletInfo?.receipt_printer_id" type="button" variant="ghost" :disabled="bill.lines.length === 0" data-testid="print-browser" @click="printReceipt(bill, t)">{{ t('posBill.printBrowser') }}</Button>
        <Button v-if="can('pos.discount')" type="button" variant="outline" :disabled="busy || bill.lines.length === 0" data-testid="discount" @click="openSheet({ kind: 'discount' })">{{ t('posBill.discountAction') }}</Button>
        <Button v-if="can('pos.comp') && can('pos.cashier')" type="button" variant="outline" :disabled="busy || bill.lines.length === 0" data-testid="comp" @click="openSheet({ kind: 'comp' })">{{ t('posBill.comp') }}</Button>
        <Button type="button" variant="outline" :disabled="busy" data-testid="move-table" @click="openTableSheet">{{ t('posBill.moveTable') }}</Button>
        <Button v-if="can('pos.cashier')" type="button" variant="outline" :disabled="busy" data-testid="merge" @click="openMergeSheet">{{ t('posBill.merge') }}</Button>
        <Button v-if="can('pos.cashier') && bill.lines.filter((l) => l.status !== 'VOID').length > 1" type="button" variant="outline" :disabled="busy" data-testid="split" @click="openSheet({ kind: 'split' })">{{ t('posBill.split') }}</Button>
        <Button v-if="bill.lines.every((l) => l.status === 'DRAFT')" type="button" variant="ghost" :disabled="busy" data-testid="void-bill" @click="voidBill">{{ t('posBill.voidBill') }}</Button>
      </div>
      <div v-else class="mt-3 flex flex-wrap gap-2" data-testid="closed-actions">
        <Button type="button" variant="outline" :disabled="busy" data-testid="print" @click="printPaper">{{ outletInfo?.receipt_printer_id && can('pos.cashier') ? t('posBill.reprintReceipt') : t('posBill.print') }}</Button>
        <Button v-if="outletInfo?.receipt_printer_id" type="button" variant="ghost" data-testid="print-browser" @click="printReceipt(bill, t)">{{ t('posBill.printBrowser') }}</Button>
        <Button v-if="bill.status === 'CLOSED' && can('pos.cashier') && can('folio.reopen')" type="button" variant="outline" data-testid="reopen" @click="openSheet({ kind: 'reopen' })">{{ t('posBill.reopen') }}</Button>
      </div>
    </aside>
  </div>

  <!-- the reason of a copy of the receipt -->
  <Dialog :open="printDialog" @update:open="(v) => (printDialog = v)">
    <DialogContent class="max-w-md p-5" data-testid="print-dialog">
      <DialogTitle>{{ t('posBill.reprintReceipt') }}</DialogTitle>
      <DialogDescription class="mb-3 mt-1">{{ t('posBill.reprintHint') }}</DialogDescription>
      <ErrorNotice v-if="printError" :error="printError" inline />
      <form class="flex flex-col gap-3" novalidate @submit.prevent="sendPrint(printReason.trim())">
        <FormField :label="t('posBill.reprintReason')" :error="printError?.fieldMessage('reason')">
          <template #default="{ id }"><Input :id="id" v-model="printReason" name="reason" maxlength="200" autocomplete="off" data-testid="print-reason" /></template>
        </FormField>
        <div class="flex justify-end gap-2">
          <Button type="button" variant="outline" @click="printDialog = false">{{ t('common.cancel') }}</Button>
          <Button type="submit" :disabled="busy || !printReason.trim()" data-testid="print-submit">{{ t('posBill.reprintReceipt') }}</Button>
        </div>
      </form>
    </DialogContent>
  </Dialog>

  <!-- options of a product -->
  <Dialog v-if="optionsFor" :open="true" @update:open="(v) => { if (!v) optionsFor = null }">
    <DialogContent class="max-w-lg p-5" data-testid="options-dialog">
      <DialogTitle>{{ optionsFor.name }}</DialogTitle>
      <DialogDescription class="mb-3 mt-1">{{ formatMoney(optionsFor.price ?? '') }}</DialogDescription>
      <ErrorNotice v-if="error" :error="error" inline />
      <form class="flex flex-col gap-4" novalidate @submit.prevent="confirmOptions">
        <fieldset v-for="g in optionsFor.option_groups ?? []" :key="g.id" class="m-0 border-0 p-0" :data-testid="`group-${g.id}`">
          <legend class="mb-1 text-sm font-medium">{{ g.name }} <span class="text-xs font-normal text-muted-foreground">{{ g.kind === 'VARIANT' ? t('posBill.pickOne') : t('posBill.pickUpTo', { n: g.max_select }) }}</span></legend>
          <div class="flex flex-wrap gap-2">
            <label
              v-for="o in (g.options ?? []).filter((x) => x.is_active !== false)" :key="o.id" class="flex min-h-11 cursor-pointer items-center gap-2 rounded-md border px-3 text-sm"
              :class="(picked[g.id ?? 0] ?? []).includes(o.id ?? 0) ? 'border-primary bg-primary/10' : 'border-border'"
            >
              <input
                :type="g.kind === 'VARIANT' ? 'radio' : 'checkbox'" :name="`g${g.id}`" :checked="(picked[g.id ?? 0] ?? []).includes(o.id ?? 0)" :data-testid="`option-${o.id}`"
                @change="togglePick(g.id ?? 0, o.id ?? 0, g.kind === 'VARIANT')"
              >
              {{ o.name }}<span v-if="Number(o.price_delta) !== 0" class="text-xs text-muted-foreground">{{ Number(o.price_delta) > 0 ? '+' : '' }}{{ formatMoney(o.price_delta) }}</span>
            </label>
          </div>
        </fieldset>
        <div class="grid grid-cols-[6rem_1fr] gap-3">
          <FormField :label="t('posBill.quantity')"><template #default="{ id }"><Input :id="id" v-model="lineQty" name="quantity" inputmode="decimal" data-testid="options-qty" /></template></FormField>
          <FormField :label="t('posBill.kitchenNote')"><template #default="{ id }"><Input :id="id" v-model="lineNote" name="note" autocomplete="off" maxlength="200" data-testid="options-note" /></template></FormField>
        </div>
        <div class="flex justify-end gap-2">
          <Button type="button" variant="outline" @click="optionsFor = null">{{ t('common.cancel') }}</Button>
          <Button type="submit" :disabled="busy" data-testid="options-add">{{ t('posBill.add') }}</Button>
        </div>
      </form>
    </DialogContent>
  </Dialog>

  <!-- reason (and the approval) of a void, a discount, a complimentary and a reopening -->
  <Dialog v-if="sheet && ['void', 'discount', 'comp', 'reopen'].includes(sheet.kind) && stage === 'form'" :open="true" @update:open="(v) => { if (!v) closeSheet() }">
    <DialogContent class="max-w-md p-5" data-testid="reason-dialog" @interact-outside.prevent>
      <form novalidate @submit.prevent="submitForm">
        <DialogTitle>{{ t(`posBill.sheet_${sheet.kind}`) }}</DialogTitle>
        <DialogDescription class="mb-3 mt-1">{{ t(`posBill.sheetHint_${sheet.kind}`) }}</DialogDescription>
        <ErrorNotice v-if="dialogError" :error="dialogError" inline data-testid="reason-error" />
        <div class="flex flex-col gap-3">
          <template v-if="sheet.kind === 'discount'">
            <FormField :label="t('posBill.scope')">
              <template #default="{ id }">
                <NativeSelect :id="id" v-model="scope" name="scope" data-testid="discount-scope">
                  <option value="BILL">{{ t('posBill.scopeBill') }}</option>
                  <option value="LINE">{{ t('posBill.scopeLine') }}</option>
                </NativeSelect>
              </template>
            </FormField>
            <FormField v-if="scope === 'LINE'" :label="t('posBill.line')">
              <template #default="{ id }">
                <NativeSelect :id="id" v-model="lineChoice" name="line_id" data-testid="discount-line">
                  <option :value="null" disabled>{{ t('posBill.chooseLine') }}</option>
                  <option v-for="l in bill?.lines.filter((x) => x.status !== 'VOID')" :key="l.id" :value="l.id">{{ l.product_name }}</option>
                </NativeSelect>
              </template>
            </FormField>
            <div class="grid grid-cols-[8rem_1fr] gap-3">
              <FormField :label="t('posBill.mode')">
                <template #default="{ id }">
                  <NativeSelect :id="id" v-model="mode" name="mode" data-testid="discount-mode">
                    <option value="percent">%</option>
                    <option value="amount">{{ t('posBill.amountMode') }}</option>
                  </NativeSelect>
                </template>
              </FormField>
              <FormField :label="mode === 'percent' ? t('posBill.percent') : t('posBill.amount')">
                <template #default="{ id }"><Input :id="id" v-model="figure" name="figure" inputmode="decimal" autocomplete="off" data-testid="discount-figure" /></template>
              </FormField>
            </div>
          </template>
          <FormField v-if="sheet.kind === 'comp'" :label="t('posBill.compName')">
            <template #default="{ id }"><Input :id="id" v-model="compName" name="name" autocomplete="off" data-testid="comp-name" /></template>
          </FormField>
          <FormField :label="t('posBill.reason')">
            <template #default="{ id }"><Input :id="id" v-model="reason" name="reason" autocomplete="off" maxlength="200" data-testid="reason" /></template>
          </FormField>
        </div>
        <div class="mt-5 flex justify-end gap-2">
          <Button type="button" variant="outline" @click="closeSheet">{{ t('common.cancel') }}</Button>
          <Button
            type="submit" data-testid="reason-submit"
            :disabled="busy || !reason.trim() || (sheet.kind === 'comp' && !compName.trim()) || (sheet.kind === 'discount' && (!figure || (scope === 'LINE' && !lineChoice)))"
          >{{ sheet.kind === 'discount' ? t('posBill.give') : t('posBill.continue') }}</Button>
        </div>
      </form>
    </DialogContent>
  </Dialog>
  <ApprovalDialog
    v-if="sheet && ['void', 'discount', 'comp', 'reopen'].includes(sheet.kind) && stage === 'approval'" :title="t(`posBill.sheet_${sheet.kind}`)" :kind="approvalKind" :amount="approvalAmount"
    :busy="busy" :error="dialogError" @approve="approved" @cancel="closeSheet"
  />

  <!-- move the table -->
  <Dialog v-if="sheet?.kind === 'table'" :open="true" @update:open="(v) => { if (!v) closeSheet() }">
    <DialogContent class="max-w-sm p-5" data-testid="table-dialog">
      <DialogTitle>{{ t('posBill.moveTable') }}</DialogTitle>
      <DialogDescription class="mb-3 mt-1">{{ t('posBill.moveTableHint') }}</DialogDescription>
      <ErrorNotice v-if="dialogError" :error="dialogError" inline />
      <NativeSelect v-model="tableChoice" name="table" :aria-label="t('posBill.moveTable')" data-testid="table-choice">
        <option :value="null">{{ t('posBill.noTable') }}</option>
        <option v-for="tb in tables" :key="tb.id" :value="tb.id">{{ tb.code }}</option>
      </NativeSelect>
      <div class="mt-4 flex justify-end gap-2">
        <Button type="button" variant="outline" @click="closeSheet">{{ t('common.cancel') }}</Button>
        <Button type="button" :disabled="busy" data-testid="table-submit" @click="moveTable">{{ t('posBill.move') }}</Button>
      </div>
    </DialogContent>
  </Dialog>

  <!-- merge -->
  <Dialog v-if="sheet?.kind === 'merge'" :open="true" @update:open="(v) => { if (!v) closeSheet() }">
    <DialogContent class="max-w-md p-5" data-testid="merge-dialog">
      <DialogTitle>{{ t('posBill.merge') }}</DialogTitle>
      <DialogDescription class="mb-3 mt-1">{{ t('posBill.mergeHint', { bill: bill?.bill_number ?? '' }) }}</DialogDescription>
      <ErrorNotice v-if="dialogError" :error="dialogError" inline />
      <EmptyState v-if="others.length === 0" :title="t('posBill.noOtherBills')" :description="t('posBill.noOtherBillsHint')" />
      <ul v-else class="m-0 list-none p-0">
        <li v-for="o in others" :key="o.id">
          <label class="flex min-h-11 cursor-pointer items-center gap-3 border-t border-border px-1 text-sm">
            <input v-model="mergeChoice" type="radio" name="merge" :value="o.id" :data-testid="`merge-${o.id}`">
            <strong>{{ o.bill_number }}</strong><span class="text-muted-foreground">{{ o.table_code }}</span><span class="ml-auto tabular-nums">{{ formatMoney(o.total) }}</span>
          </label>
        </li>
      </ul>
      <div class="mt-4 flex justify-end gap-2">
        <Button type="button" variant="outline" @click="closeSheet">{{ t('common.cancel') }}</Button>
        <Button type="button" :disabled="busy || mergeChoice === null" data-testid="merge-submit" @click="merge">{{ t('posBill.merge') }}</Button>
      </div>
    </DialogContent>
  </Dialog>

  <!-- split by item -->
  <Dialog v-if="sheet?.kind === 'split'" :open="true" @update:open="(v) => { if (!v) closeSheet() }">
    <DialogContent class="max-w-md p-5" data-testid="split-dialog">
      <DialogTitle>{{ t('posBill.split') }}</DialogTitle>
      <DialogDescription class="mb-3 mt-1">{{ t('posBill.splitHint') }}</DialogDescription>
      <ErrorNotice v-if="dialogError" :error="dialogError" inline />
      <ul class="m-0 list-none p-0">
        <li v-for="l in bill?.lines.filter((x) => x.status !== 'VOID')" :key="l.id">
          <label class="flex min-h-11 cursor-pointer items-center gap-3 border-t border-border px-1 text-sm">
            <input v-model="splitLines" type="checkbox" :value="l.id" :data-testid="`split-line-${l.id}`">
            <span>{{ Number(l.quantity) }} × {{ l.product_name }}</span><span class="ml-auto tabular-nums">{{ formatMoney(l.net_amount) }}</span>
          </label>
        </li>
      </ul>
      <div class="mt-4 flex justify-end gap-2">
        <Button type="button" variant="outline" @click="closeSheet">{{ t('common.cancel') }}</Button>
        <Button type="button" :disabled="busy || splitLines.length === 0 || splitLines.length >= (bill?.lines.filter((x) => x.status !== 'VOID').length ?? 0)" data-testid="split-submit" @click="split">{{ t('posBill.split') }}</Button>
      </div>
    </DialogContent>
  </Dialog>

  <PosPayDialog v-if="sheet?.kind === 'pay' && bill" :bill="bill" @paid="(b, m) => paid(b, m)" @close="closeSheet" />
</template>
