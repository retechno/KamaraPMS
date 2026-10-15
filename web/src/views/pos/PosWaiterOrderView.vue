<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { PosBill, PosBillLine, PosCatalog } from '@/api/types'
import ErrorNotice from '@/components/app/ErrorNotice.vue'
import PageHeader from '@/components/app/PageHeader.vue'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { debounce, usePosEvents } from '@/composables/usePosEvents'
import { t } from '@/i18n'
import { usePropertyStore } from '@/stores/property'
import { formatMoney } from '@/utils/format'
import { newIdempotencyKey } from '@/utils/reservations'

/**
 * The order on a phone (docs/architecture/34-pos.md 11, mockup 16): the menu as rows with the price, "Tambah" that becomes "− n +", what the bill already holds ("sudah dipesan 2"),
 * a product that is sold out today that cannot be chosen, the options sheet (variant, extras, a note for the kitchen), and at the foot the draft items, the bill and "Send to kitchen
 * and bar". It is for the permission to order only: it has no payment, no void and no discount. Two waiters on one bill see each other's lines through the stream of the outlet.
 */
type Product = PosCatalog['products'][number]
const route = useRoute()
const router = useRouter()
const property = usePropertyStore()
const pid = computed(() => property.currentId)
const billId = computed(() => Number(route.params.id))

const bill = ref<PosBill | null>(null)
const catalog = ref<PosCatalog | null>(null)
const error = ref<ApiError | null>(null)
const notice = ref('')
const busy = ref(false)
const categoryId = ref<number | null>(null)
const search = ref('')
const showBill = ref(false)

const isOpen = computed(() => bill.value?.status === 'OPEN')
const drafts = computed(() => bill.value?.lines.filter((l) => l.status === 'DRAFT') ?? [])
const draftCount = computed(() => drafts.value.reduce((n, l) => n + Number(l.quantity), 0))

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
    if (categoryId.value === null || !catalog.value?.categories.some((c) => c.id === categoryId.value)) categoryId.value = catalog.value?.categories[0]?.id ?? null
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  }
}
watch([pid, billId], () => void loadBill(), { immediate: true })
watch(() => bill.value?.outlet_id, (id) => { if (id && !catalog.value) void loadCatalog(id) })

const refreshBill = debounce(() => void loadBill(true), 150)
const events = usePosEvents({
  propertyId: () => pid.value,
  channels: () => (bill.value ? [`outlet:${bill.value.outlet_id}`] : []),
  onEvent: (e) => {
    if (e.type === 'product.sold_out' && bill.value) void loadCatalog(bill.value.outlet_id)
    else if (e.entity_id === billId.value || e.type === 'ticket.updated' || e.type === 'ticket.created') refreshBill()
  },
  onReload: () => {
    void loadBill(true)
    if (bill.value) void loadCatalog(bill.value.outlet_id)
  },
})

const products = computed(() => {
  const q = search.value.trim().toLowerCase()
  return (catalog.value?.products ?? []).filter((p) => (q ? p.name.toLowerCase().includes(q) || p.code.toLowerCase().includes(q) : p.category_id === categoryId.value))
})
const soldOut = (id: number): boolean => catalog.value?.sold_out.includes(id) ?? false
const ordered = (id: number): number => (bill.value?.lines ?? []).filter((l) => l.product_id === id && l.status === 'SENT').reduce((n, l) => n + Number(l.quantity), 0)
const draftsOf = (id: number): PosBillLine[] => drafts.value.filter((l) => l.product_id === id)
const draftQty = (id: number): number => draftsOf(id).reduce((n, l) => n + Number(l.quantity), 0)
const hasOptions = (p: Product): boolean => (p.option_groups ?? []).length > 0

async function run(fn: () => Promise<void>): Promise<boolean> {
  busy.value = true
  error.value = null
  notice.value = ''
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

async function addLine(productId: number, quantity: string, options: { option_id: number }[], note: string): Promise<boolean> {
  const propertyId = pid.value
  if (propertyId === null) return false
  return run(async () => {
    const { data } = await api.POST('/api/v1/properties/{propertyId}/pos/bills/{id}/lines', {
      params: { path: { propertyId, id: billId.value } },
      body: { client_uuid: newIdempotencyKey(), product_id: productId, quantity, options, ...(note ? { note } : {}) },
    })
    if (data) bill.value = data
  })
}

async function setQuantity(l: PosBillLine, q: number): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null) return
  await run(async () => {
    if (q < 1) {
      const { data } = await api.DELETE('/api/v1/properties/{propertyId}/pos/bills/{id}/lines/{lineId}', { params: { path: { propertyId, id: billId.value, lineId: l.id } } })
      if (data) bill.value = data
      return
    }
    const { data } = await api.PUT('/api/v1/properties/{propertyId}/pos/bills/{id}/lines/{lineId}', {
      params: { path: { propertyId, id: billId.value, lineId: l.id } },
      body: { quantity: String(q), options: l.options.map((o) => ({ option_id: o.option_id, qty: o.qty })), note: l.note ?? '' },
    })
    if (data) bill.value = data
  })
}

// ---- the buttons of a row
const optionsFor = ref<Product | null>(null)
const picked = ref<Record<number, number[]>>({})
const lineNote = ref('')

function more(p: Product): void {
  if (!isOpen.value || soldOut(p.id) || busy.value) return
  if (hasOptions(p)) {
    picked.value = {}
    for (const g of p.option_groups ?? []) picked.value[g.id ?? 0] = (g.options ?? []).filter((o) => o.is_default && o.is_active !== false).map((o) => o.id ?? 0)
    lineNote.value = ''
    optionsFor.value = p
    return
  }
  const own = draftsOf(p.id).find((l) => l.options.length === 0 && !l.note)
  if (own) void setQuantity(own, Number(own.quantity) + 1)
  else void addLine(p.id, '1', [], '')
}
function less(p: Product): void {
  const last = draftsOf(p.id).at(-1)
  if (last && !busy.value) void setQuantity(last, Number(last.quantity) - 1)
}
function togglePick(groupId: number, optionId: number, single: boolean): void {
  const cur = picked.value[groupId] ?? []
  if (single) picked.value[groupId] = [optionId]
  else picked.value[groupId] = cur.includes(optionId) ? cur.filter((x) => x !== optionId) : [...cur, optionId]
}
async function confirmOptions(): Promise<void> {
  const p = optionsFor.value
  if (!p) return
  const options = Object.values(picked.value).flat().filter((id) => id > 0).map((option_id) => ({ option_id }))
  if (await addLine(p.id, '1', options, lineNote.value.trim())) optionsFor.value = null
}

async function send(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null) return
  const ok = await run(async () => {
    const { data } = await api.POST('/api/v1/properties/{propertyId}/pos/bills/{id}/send', { params: { path: { propertyId, id: billId.value } }, body: { client_uuid: newIdempotencyKey() } })
    if (data) bill.value = data
  })
  if (ok) notice.value = t('posBill.sent')
}

const title = computed(() => {
  const b = bill.value
  if (!b) return ''
  const place = b.table_code ? t('posKds.table', { code: b.table_code }) : t(`posFloor.type_${b.order_type}`)
  return b.guest_count ? `${place} · ${t('posFloor.guests', { n: b.guest_count })}` : place
})
const optionsText = (l: PosBillLine): string => l.options.map((o) => (o.qty > 1 ? `${o.name} ×${o.qty}` : o.name)).join(', ')
const statusKey = (l: PosBillLine): string => (l.ticket_status ? `posBill.ticket_${l.ticket_status}` : '')
</script>

<template>
  <div class="mx-auto flex w-full max-w-[430px] flex-col pb-40" data-testid="waiter-order">
    <div class="mb-1 flex items-center gap-2">
      <Button type="button" variant="ghost" size="sm" class="size-10 p-0" :aria-label="t('posWaiter.backToTables')" data-testid="waiter-back" @click="router.push('/pos/waiter')">‹</Button>
      <div class="min-w-0 flex-1" data-testid="waiter-title">
        <PageHeader :title="title || t('posWaiter.orderTitle')" :description="bill ? `${bill.bill_number} · ${bill.outlet_name}` : ''" class="mb-0" />
      </div>
      <span class="size-2 rounded-full" :class="events.state.value === 'live' ? 'bg-primary' : 'bg-warning'" :title="t(`posWaiter.stream_${events.state.value}`)" :data-testid="`stream-${events.state.value}`" />
    </div>

    <ErrorNotice v-if="error" :error="error" inline data-testid="waiter-error" />
    <p v-if="notice" class="notice" role="status" data-testid="waiter-notice">{{ notice }}</p>
    <p v-if="bill && !isOpen" class="alert warning" data-testid="waiter-closed">{{ t('posWaiter.notOpen') }}</p>

    <template v-if="catalog">
      <Input v-model="search" type="search" name="search" :placeholder="t('posWaiter.search')" :aria-label="t('posWaiter.search')" class="mb-2 h-11" data-testid="waiter-search" />
      <div v-if="!search" class="mb-2 flex gap-2 overflow-x-auto pb-1" role="tablist">
        <button
          v-for="c in catalog.categories" :key="c.id" type="button" role="tab" :aria-selected="c.id === categoryId" :data-testid="`cat-${c.id}`"
          class="h-10 shrink-0 cursor-pointer rounded-full border px-4 text-sm"
          :class="c.id === categoryId ? 'border-primary bg-primary/10 font-semibold' : 'border-border bg-card'"
          @click="categoryId = c.id"
        >{{ c.name }}</button>
      </div>

      <ul class="m-0 list-none p-0" data-testid="waiter-menu">
        <li v-for="p in products" :key="p.id" class="flex items-center gap-3 border-t border-border py-3" :data-testid="`product-${p.id}`" :data-sold-out="soldOut(p.id)">
          <div class="min-w-0 flex-1">
            <div class="truncate text-[15px] font-semibold" :class="soldOut(p.id) ? 'text-muted-foreground line-through' : ''">{{ p.name }}</div>
            <div class="text-[13px] text-muted-foreground">
              {{ formatMoney(p.price) }}<span v-if="ordered(p.id) > 0" data-testid="ordered"> · {{ t('posWaiter.ordered', { n: ordered(p.id) }) }}</span>
            </div>
          </div>
          <span v-if="soldOut(p.id)" class="text-sm font-medium text-destructive" data-testid="sold-out">{{ t('posBill.soldOut') }}</span>
          <div v-else-if="draftQty(p.id) > 0" class="inline-flex h-11 min-w-24 items-center rounded-xl bg-primary text-primary-foreground" data-testid="stepper">
            <button type="button" class="flex size-10 cursor-pointer items-center justify-center border-0 bg-transparent text-lg text-primary-foreground" :aria-label="t('posBill.less')" :disabled="busy || !isOpen" :data-testid="`less-${p.id}`" @click="less(p)">−</button>
            <span class="min-w-4 text-center font-bold tabular-nums" :data-testid="`qty-${p.id}`">{{ draftQty(p.id) }}</span>
            <button type="button" class="flex size-10 cursor-pointer items-center justify-center border-0 bg-transparent text-lg text-primary-foreground" :aria-label="t('posBill.more')" :disabled="busy || !isOpen" :data-testid="`more-${p.id}`" @click="more(p)">+</button>
          </div>
          <button
            v-else type="button" class="inline-flex h-11 min-w-24 cursor-pointer items-center justify-center rounded-xl border-0 bg-primary/15 px-3 text-sm font-semibold text-primary disabled:opacity-50"
            :disabled="busy || !isOpen" :data-testid="`add-${p.id}`" @click="more(p)"
          >{{ t('posWaiter.add') }}</button>
        </li>
        <li v-if="products.length === 0" class="border-t border-border py-6 text-center text-sm text-muted-foreground">{{ t('posWaiter.noProducts') }}</li>
      </ul>
    </template>

    <footer class="fixed inset-x-0 bottom-0 z-20 border-t border-border bg-background p-3" data-testid="waiter-footer">
      <div class="mx-auto flex max-w-[430px] flex-col gap-2">
        <div class="flex items-center justify-between text-sm">
          <span data-testid="draft-summary">{{ t('posWaiter.draftSummary', { n: draftCount, total: formatMoney(bill?.totals.payable ?? '0') }) }}</span>
          <button type="button" class="min-h-11 cursor-pointer border-0 bg-transparent px-2 font-semibold text-primary" data-testid="view-bill" @click="showBill = true">{{ t('posWaiter.viewBill') }}</button>
        </div>
        <Button type="button" class="min-h-12 text-base" :disabled="busy || !isOpen || drafts.length === 0" data-testid="waiter-send" @click="send">{{ t('posBill.send') }}</Button>
      </div>
    </footer>
  </div>

  <!-- options of a product -->
  <Dialog v-if="optionsFor" :open="true" @update:open="(v) => { if (!v) optionsFor = null }">
    <DialogContent class="max-w-sm p-5" data-testid="options-dialog">
      <DialogTitle>{{ optionsFor.name }}</DialogTitle>
      <DialogDescription class="mb-3 mt-1">{{ formatMoney(optionsFor.price) }}</DialogDescription>
      <ErrorNotice v-if="error" :error="error" inline />
      <form class="flex flex-col gap-4" novalidate @submit.prevent="confirmOptions">
        <fieldset v-for="g in optionsFor.option_groups ?? []" :key="g.id" class="m-0 border-0 p-0" :data-testid="`group-${g.id}`">
          <legend class="mb-1 text-sm font-medium">{{ g.name }} <span class="text-xs font-normal text-muted-foreground">{{ g.kind === 'VARIANT' ? t('posBill.pickOne') : t('posBill.pickUpTo', { n: g.max_select }) }}</span></legend>
          <div class="flex flex-wrap gap-2">
            <label
              v-for="o in (g.options ?? []).filter((x) => x.is_active !== false)" :key="o.id" class="flex min-h-11 cursor-pointer items-center gap-2 rounded-md border px-3 text-sm"
              :class="(picked[g.id ?? 0] ?? []).includes(o.id ?? 0) ? 'border-primary bg-primary/10' : 'border-border'"
            >
              <input :type="g.kind === 'VARIANT' ? 'radio' : 'checkbox'" :name="`g${g.id}`" :checked="(picked[g.id ?? 0] ?? []).includes(o.id ?? 0)" :data-testid="`option-${o.id}`" @change="togglePick(g.id ?? 0, o.id ?? 0, g.kind === 'VARIANT')">
              {{ o.name }}<span v-if="Number(o.price_delta) !== 0" class="text-xs text-muted-foreground">{{ Number(o.price_delta) > 0 ? '+' : '' }}{{ formatMoney(o.price_delta) }}</span>
            </label>
          </div>
        </fieldset>
        <label class="flex flex-col gap-1 text-sm">
          {{ t('posBill.kitchenNote') }}
          <Input v-model="lineNote" name="note" autocomplete="off" maxlength="200" data-testid="options-note" />
        </label>
        <div class="flex justify-end gap-2">
          <Button type="button" variant="outline" @click="optionsFor = null">{{ t('common.cancel') }}</Button>
          <Button type="submit" :disabled="busy" data-testid="options-add">{{ t('posBill.add') }}</Button>
        </div>
      </form>
    </DialogContent>
  </Dialog>

  <!-- the bill, read-only -->
  <Dialog v-if="showBill && bill" :open="true" @update:open="(v) => { if (!v) showBill = false }">
    <DialogContent class="max-w-sm p-5" data-testid="bill-dialog">
      <DialogTitle>{{ bill.bill_number }}</DialogTitle>
      <DialogDescription class="mb-2 mt-1">{{ title }}</DialogDescription>
      <ul class="m-0 max-h-72 list-none overflow-y-auto p-0 text-sm">
        <li v-for="l in bill.lines.filter((x) => x.status !== 'VOID')" :key="l.id" class="flex items-baseline gap-2 border-t border-border py-2 first:border-t-0" :data-testid="`bill-line-${l.id}`">
          <span class="min-w-0 flex-1">
            {{ Number(l.quantity) }} × {{ l.product_name }}
            <span v-if="l.options.length > 0" class="block text-xs text-muted-foreground">{{ optionsText(l) }}</span>
            <span class="block text-xs text-muted-foreground">{{ l.status === 'DRAFT' ? t('posBill.drafts') : (statusKey(l) ? t(statusKey(l)) : t('posBill.round', { n: l.round })) }}</span>
          </span>
          <span class="tabular-nums">{{ formatMoney(l.net_amount) }}</span>
        </li>
      </ul>
      <dl class="m-0 mt-2 grid grid-cols-[1fr_auto] gap-x-3 gap-y-1 border-t border-border pt-2 text-sm">
        <dt>{{ t('posBill.net') }}</dt><dd class="m-0 text-right tabular-nums">{{ formatMoney(bill.totals.net) }}</dd>
        <dt>{{ t('posBill.service') }}</dt><dd class="m-0 text-right tabular-nums">{{ formatMoney(bill.totals.service) }}</dd>
        <dt>{{ t('posBill.tax') }}</dt><dd class="m-0 text-right tabular-nums">{{ formatMoney(bill.totals.tax) }}</dd>
        <dt class="font-semibold">{{ t('posBill.total') }}</dt><dd class="m-0 text-right font-semibold tabular-nums">{{ formatMoney(bill.totals.payable) }}</dd>
      </dl>
      <div class="mt-3 flex justify-end"><Button type="button" variant="outline" @click="showBill = false">{{ t('common.close') }}</Button></div>
    </DialogContent>
  </Dialog>
</template>
