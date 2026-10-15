<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { PosBillSummary, PosFloorState, PosFloorTable } from '@/api/types'
import EmptyState from '@/components/app/EmptyState.vue'
import ErrorNotice from '@/components/app/ErrorNotice.vue'
import FormField from '@/components/app/FormField.vue'
import PageHeader from '@/components/app/PageHeader.vue'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { NativeSelect } from '@/components/ui/native-select'
import { debounce, usePosEvents } from '@/composables/usePosEvents'
import { usePosOutlet } from '@/composables/usePosOutlet'
import { t } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import { formatDate, formatMoney } from '@/utils/format'
import { newIdempotencyKey } from '@/utils/reservations'
import { Store } from 'lucide-vue-next'

/**
 * The cashier floor plan (docs/architecture/34-pos.md 11, mockup 10): the areas of an outlet as tabs, each table drawn where it stands with its state, the number of guests, the minutes and the total of
 * its bill, and beside it the open bills with a search. A table is opened by tapping it; "takeaway" opens a bill without one. It reads again by itself.
 */
const router = useRouter()
const auth = useAuthStore()
const property = usePropertyStore()
const pid = computed(() => property.currentId)
const { outlets, outletId, outlet, error: outletsError, loaded } = usePosOutlet()

const state = ref<PosFloorState | null>(null)
const error = ref<ApiError | null>(null)
const loading = ref(false)
const areaId = ref<number | null>(null)
const search = ref('')
const busy = ref(false)

const canCashier = computed(() => auth.can('pos.cashier', pid.value))
const canReport = computed(() => auth.can('pos.report', pid.value))

async function load(): Promise<void> {
  const propertyId = pid.value
  const id = outletId.value
  if (propertyId === null || id === null || loading.value) return
  loading.value = true
  try {
    const { data } = await api.GET('/api/v1/properties/{propertyId}/pos/outlets/{id}/floor', { params: { path: { propertyId, id } } })
    state.value = data ?? null
    error.value = null
    if (areaId.value === null || !state.value?.areas.some((a) => a.id === areaId.value)) areaId.value = state.value?.areas[0]?.id ?? null
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    loading.value = false
  }
}

watch(outletId, () => {
  state.value = null
  areaId.value = null
  void load()
}, { immediate: true })
// The floor follows the stream of its outlet (a table, a bill or a hold changed anywhere); it reads again when the stream says so and after a gap.
const refresh = debounce(() => void load(), 150)
usePosEvents({
  propertyId: () => pid.value,
  channels: () => (outletId.value === null ? [] : [`outlet:${outletId.value}`]),
  onEvent: () => refresh(),
  onReload: () => void load(),
})

const area = computed(() => state.value?.areas.find((a) => a.id === areaId.value) ?? null)
const matches = (b: PosBillSummary): boolean => {
  const q = search.value.trim().toLowerCase()
  if (!q) return true
  return [b.bill_number, b.table_code, b.target_room, b.customer_name].some((s) => (s ?? '').toLowerCase().includes(q))
}
const bills = computed(() => (state.value?.open_bills ?? []).filter(matches))

// The state of a table in words and colours; never by colour alone (the word is on the table).
const STATUS_CLASS: Record<string, string> = {
  FREE: 'border-border bg-card',
  OCCUPIED: 'border-primary bg-primary/10',
  AWAITING_PAYMENT: 'border-warning bg-warning/20',
  HELD: 'border-dashed border-primary/60 bg-muted',
}
const tableTotal = (tb: PosFloorTable): string => formatMoney(tb.bills.reduce((n, b) => n + Number(b.total), 0).toString())
const tableGuests = (tb: PosFloorTable): number => tb.bills.reduce((n, b) => n + (b.guest_count ?? 0), 0)
const tableMinutes = (tb: PosFloorTable): number => Math.max(0, ...tb.bills.map((b) => b.minutes))

// ---- what a tap does
const sheet = ref<{ kind: 'table'; table: PosFloorTable } | { kind: 'takeaway' } | { kind: 'drawer' } | { kind: 'hold'; table: PosFloorTable } | null>(null)
const sheetTable = computed(() => (sheet.value && 'table' in sheet.value ? sheet.value.table : null))
const guestCount = ref('')
const orderType = ref<'TAKEAWAY' | 'DIRECT' | 'ROOM_SERVICE'>('TAKEAWAY')
const customer = ref('')
const reason = ref('')
const holdName = ref('')
const holdTime = ref('19:00')
const holdParty = ref('')
const dialogError = ref<ApiError | null>(null)

function tap(tb: PosFloorTable): void {
  if (tb.bills.length === 1 && tb.bills[0]) {
    void router.push(`/pos/bills/${tb.bills[0].id}`)
    return
  }
  dialogError.value = null
  guestCount.value = ''
  sheet.value = { kind: 'table', table: tb }
}

async function openBill(input: { order_type: 'DINE_IN' | 'TAKEAWAY' | 'DIRECT' | 'ROOM_SERVICE'; table_id?: number; guest_count?: number; customer_name?: string }): Promise<void> {
  const propertyId = pid.value
  const id = outletId.value
  if (propertyId === null || id === null) return
  busy.value = true
  dialogError.value = null
  try {
    const { data } = await api.POST('/api/v1/properties/{propertyId}/pos/outlets/{id}/bills', {
      params: { path: { propertyId, id } },
      body: { client_uuid: newIdempotencyKey(), ...input },
    })
    if (data) await router.push(`/pos/bills/${data.id}`)
  } catch (e) {
    dialogError.value = e instanceof ApiError ? e : null
  } finally {
    busy.value = false
  }
}

function openTable(tb: PosFloorTable): void {
  if (tb.id === undefined) return
  const n = Number(guestCount.value)
  void openBill({ order_type: 'DINE_IN', table_id: tb.id, ...(Number.isInteger(n) && n > 0 ? { guest_count: n } : {}) })
}

function openTakeaway(): void {
  void openBill({ order_type: orderType.value, ...(customer.value.trim() ? { customer_name: customer.value.trim() } : {}) })
}

async function openDrawer(): Promise<void> {
  const propertyId = pid.value
  const id = outletId.value
  if (propertyId === null || id === null) return
  busy.value = true
  dialogError.value = null
  try {
    await api.POST('/api/v1/properties/{propertyId}/pos/outlets/{id}/drawer/open', { params: { path: { propertyId, id } }, body: { reason: reason.value.trim() } })
    sheet.value = null
    reason.value = ''
  } catch (e) {
    dialogError.value = e instanceof ApiError ? e : null
  } finally {
    busy.value = false
  }
}

async function hold(tb: PosFloorTable): Promise<void> {
  const propertyId = pid.value
  const id = outletId.value
  if (propertyId === null || id === null) return
  busy.value = true
  dialogError.value = null
  try {
    const party = Number(holdParty.value)
    await api.POST('/api/v1/properties/{propertyId}/pos/outlets/{id}/holds', {
      params: { path: { propertyId, id } },
      body: { table_id: tb.id ?? 0, from_time: holdTime.value, name: holdName.value.trim(), ...(Number.isInteger(party) && party > 0 ? { party_size: party } : {}) },
    })
    sheet.value = null
    holdName.value = ''
    holdParty.value = ''
    await load()
  } catch (e) {
    dialogError.value = e instanceof ApiError ? e : null
  } finally {
    busy.value = false
  }
}

async function releaseHold(holdId: number, seated: boolean): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null) return
  try {
    await api.POST(seated ? '/api/v1/properties/{propertyId}/pos/holds/{id}/seat' : '/api/v1/properties/{propertyId}/pos/holds/{id}/cancel', { params: { path: { propertyId, id: holdId } } })
    await load()
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  }
}

const minutesText = (m: number): string => t('posFloor.minutes', { n: m })
</script>

<template>
  <PageHeader :title="t('posFloor.title')" :description="outlet ? `${outlet.name} · ${formatDate(state?.business_date)}` : ''">
    <template #actions>
    <div class="flex flex-wrap items-center gap-2">
      <NativeSelect v-if="outlets.length > 1" v-model="outletId" :aria-label="t('posFloor.outlet')" data-testid="outlet-select" class="w-auto">
        <option v-for="o in outlets" :key="o.id" :value="o.id">{{ o.name }}</option>
      </NativeSelect>
      <Button v-if="canReport" as-child variant="outline" size="sm"><RouterLink to="/pos/report" data-testid="to-report">{{ t('posFloor.report') }}</RouterLink></Button>
    </div>
    </template>
  </PageHeader>
  <ErrorNotice v-if="error ?? outletsError" :error="(error ?? outletsError)!" inline />
  <p v-if="pid === null" class="muted">{{ t('setup.selectProperty') }}</p>
  <EmptyState v-else-if="loaded && outlets.length === 0" :icon="Store" :title="t('posFloor.noOutlet')" :description="t('posFloor.noOutletHint')" data-testid="no-outlet" />

  <div v-else-if="state" class="grid gap-4 lg:grid-cols-[minmax(0,1fr)_22rem]">
    <section class="min-w-0">
      <div class="mb-3 flex flex-wrap items-center gap-2">
        <div v-if="state.areas.length > 1" class="flex flex-wrap gap-1" role="tablist" :aria-label="t('posFloor.areas')" data-testid="areas">
          <Button v-for="a in state.areas" :key="a.id" size="sm" role="tab" :aria-selected="a.id === areaId" :variant="a.id === areaId ? 'default' : 'outline'" :data-testid="`area-${a.id}`" @click="areaId = a.id">{{ a.name }}</Button>
        </div>
        <span class="ml-auto flex flex-wrap items-center gap-3 text-xs text-muted-foreground" data-testid="legend">
          <span v-for="s in (['FREE', 'OCCUPIED', 'AWAITING_PAYMENT', 'HELD'] as const)" :key="s" class="inline-flex items-center gap-1">
            <i class="inline-block size-3 rounded border" :class="STATUS_CLASS[s]" />{{ t(`posFloor.status_${s}`) }}
          </span>
        </span>
      </div>

      <EmptyState v-if="!area" :title="t('posFloor.noTables')" :description="t('posFloor.noTablesHint')" data-testid="no-tables" />
      <div v-else class="relative w-full overflow-hidden rounded-xl border border-border bg-muted/40" :style="{ aspectRatio: `${area.grid_w} / ${area.grid_h}` }" data-testid="plan">
        <div
          v-for="m in area.marks" :key="`m${m.id}`" class="absolute flex items-center justify-center rounded border border-dashed border-border bg-card/60 text-[11px] text-muted-foreground"
          :style="{ left: `${(m.x / area.grid_w) * 100}%`, top: `${(m.y / area.grid_h) * 100}%`, width: `${(m.w / area.grid_w) * 100}%`, height: `${(m.h / area.grid_h) * 100}%` }"
        >{{ m.label }}</div>
        <button
          v-for="tb in area.tables" :key="tb.id" type="button"
          class="absolute flex min-h-11 min-w-11 flex-col items-center justify-center gap-0.5 overflow-hidden border-2 p-1 text-center text-xs leading-tight shadow-sm"
          :class="[STATUS_CLASS[tb.status], tb.shape === 'ROUND' ? 'rounded-full' : 'rounded-lg']"
          :style="{ left: `${(tb.x / area.grid_w) * 100}%`, top: `${(tb.y / area.grid_h) * 100}%`, width: `${(tb.w / area.grid_w) * 100}%`, height: `${(tb.h / area.grid_h) * 100}%` }"
          :data-testid="`table-${tb.code}`" :data-status="tb.status" @click="tap(tb)"
        >
          <strong class="text-sm">{{ tb.code }}</strong>
          <template v-if="tb.bills.length > 0">
            <span class="tabular-nums">{{ tableTotal(tb) }}</span>
            <span class="text-[11px] text-muted-foreground">{{ tableGuests(tb) > 0 ? t('posFloor.guests', { n: tableGuests(tb) }) + ' · ' : '' }}{{ minutesText(tableMinutes(tb)) }}</span>
            <span v-if="tb.status === 'AWAITING_PAYMENT'" class="font-medium">{{ t('posFloor.status_AWAITING_PAYMENT') }}</span>
            <span v-if="tb.bills.length > 1" class="text-[11px]">{{ t('posFloor.bills', { n: tb.bills.length }) }}</span>
          </template>
          <template v-else-if="tb.holds.length > 0"><span class="text-[11px]">{{ tb.holds[0]!.from_time }} · {{ tb.holds[0]!.name }}</span></template>
          <span v-else class="text-[11px] text-muted-foreground">{{ t('posFloor.seats', { n: tb.capacity }) }}</span>
        </button>
      </div>

      <section v-if="state.holds.length > 0" class="mt-4 rounded-xl border border-border bg-card p-3" data-testid="holds">
        <h2 class="m-0 mb-2 text-sm font-semibold">{{ t('posFloor.holds') }}</h2>
        <ul class="m-0 list-none p-0 text-sm">
          <li v-for="h in state.holds" :key="h.id" class="flex flex-wrap items-center gap-2 border-t border-border py-1.5 first:border-t-0">
            <strong>{{ h.from_time }}</strong><span>{{ h.table_code }} · {{ h.name }}<template v-if="h.party_size"> · {{ t('posFloor.guests', { n: h.party_size }) }}</template></span>
            <span class="ml-auto flex gap-1">
              <Button size="sm" variant="outline" @click="releaseHold(h.id, true)">{{ t('posFloor.seated') }}</Button>
              <Button size="sm" variant="outline" @click="releaseHold(h.id, false)">{{ t('posFloor.noShow') }}</Button>
            </span>
          </li>
        </ul>
      </section>
    </section>

    <aside class="min-w-0 rounded-xl border border-border bg-card p-3" data-testid="open-bills">
      <div class="mb-2 flex flex-wrap gap-2">
        <Button type="button" data-testid="new-takeaway" @click="dialogError = null; customer = ''; sheet = { kind: 'takeaway' }">{{ t('posFloor.takeaway') }}</Button>
        <Button v-if="canCashier && outlet?.drawer" type="button" variant="outline" data-testid="open-drawer" @click="dialogError = null; reason = ''; sheet = { kind: 'drawer' }">{{ t('posFloor.drawer') }}</Button>
      </div>
      <Input v-model="search" type="search" name="search" :placeholder="t('posFloor.search')" :aria-label="t('posFloor.search')" data-testid="bill-search" />
      <h2 class="m-0 mb-1 mt-3 text-sm font-semibold">{{ t('posFloor.openBills', { n: bills.length }) }}</h2>
      <EmptyState v-if="bills.length === 0" :title="t('posFloor.noBills')" :description="t('posFloor.noBillsHint')" data-testid="no-bills" />
      <ul v-else class="m-0 list-none p-0">
        <li v-for="b in bills" :key="b.id">
          <RouterLink :to="`/pos/bills/${b.id}`" class="flex flex-wrap items-baseline gap-x-2 border-t border-border py-2 text-sm no-underline hover:bg-accent" :data-testid="`bill-${b.id}`">
            <strong>{{ b.bill_number }}</strong>
            <span class="text-muted-foreground">{{ b.table_code ? t('posFloor.table', { code: b.table_code }) : b.target_room ? t('posFloor.room', { room: b.target_room }) : t(`posFloor.type_${b.order_type}`) }}<template v-if="b.customer_name"> · {{ b.customer_name }}</template></span>
            <strong class="ml-auto tabular-nums">{{ formatMoney(b.total) }}</strong>
            <span class="basis-full text-xs text-muted-foreground">{{ minutesText(b.minutes) }}<template v-if="b.bill_requested_at"> · {{ t('posFloor.billPrinted') }}</template><template v-if="b.draft_lines > 0"> · {{ t('posFloor.drafts', { n: b.draft_lines }) }}</template></span>
          </RouterLink>
        </li>
      </ul>
    </aside>
  </div>

  <!-- a table: a new bill (and the number of guests), or choose among the bills there -->
  <Dialog v-if="sheet?.kind === 'table' && sheetTable" :open="true" @update:open="(v) => { if (!v) sheet = null }">
    <DialogContent class="max-w-md p-5" data-testid="table-dialog">
      <DialogTitle>{{ t('posFloor.table', { code: sheetTable.code }) }}</DialogTitle>
      <DialogDescription class="mb-3 mt-1">{{ t('posFloor.tableHint', { n: sheetTable.capacity }) }}</DialogDescription>
      <ErrorNotice v-if="dialogError" :error="dialogError" inline />
      <ul v-if="sheetTable.bills.length > 0" class="m-0 mb-3 list-none p-0 text-sm" data-testid="table-bills">
        <li v-for="b in sheetTable.bills" :key="b.id"><RouterLink :to="`/pos/bills/${b.id}`" class="flex justify-between border-t border-border py-2 no-underline hover:bg-accent"><strong>{{ b.bill_number }}</strong><span class="tabular-nums">{{ formatMoney(b.total) }}</span></RouterLink></li>
      </ul>
      <form class="flex flex-col gap-3" novalidate @submit.prevent="openTable(sheetTable)">
        <FormField :label="t('posFloor.guestCount')">
          <template #default="{ id }"><Input :id="id" v-model="guestCount" name="guest_count" inputmode="numeric" autocomplete="off" data-testid="guest-count" /></template>
        </FormField>
        <div class="flex flex-wrap justify-end gap-2">
          <Button type="button" variant="outline" @click="holdName = ''; dialogError = null; sheet = { kind: 'hold', table: sheetTable }">{{ t('posFloor.holdTable') }}</Button>
          <Button type="submit" :disabled="busy" data-testid="open-table">{{ t('posFloor.newBill') }}</Button>
        </div>
      </form>
    </DialogContent>
  </Dialog>

  <Dialog v-if="sheet?.kind === 'takeaway'" :open="true" @update:open="(v) => { if (!v) sheet = null }">
    <DialogContent class="max-w-md p-5" data-testid="takeaway-dialog">
      <DialogTitle>{{ t('posFloor.takeaway') }}</DialogTitle>
      <DialogDescription class="mb-3 mt-1">{{ t('posFloor.takeawayHint') }}</DialogDescription>
      <ErrorNotice v-if="dialogError" :error="dialogError" inline />
      <form class="flex flex-col gap-3" novalidate @submit.prevent="openTakeaway">
        <FormField :label="t('posFloor.orderType')">
          <template #default="{ id }">
            <NativeSelect :id="id" v-model="orderType" name="order_type">
              <option value="TAKEAWAY">{{ t('posFloor.type_TAKEAWAY') }}</option>
              <option value="DIRECT">{{ t('posFloor.type_DIRECT') }}</option>
              <option value="ROOM_SERVICE">{{ t('posFloor.type_ROOM_SERVICE') }}</option>
            </NativeSelect>
          </template>
        </FormField>
        <FormField :label="t('posFloor.customer')">
          <template #default="{ id }"><Input :id="id" v-model="customer" name="customer_name" autocomplete="off" data-testid="customer" /></template>
        </FormField>
        <div class="flex justify-end gap-2">
          <Button type="button" variant="outline" @click="sheet = null">{{ t('common.cancel') }}</Button>
          <Button type="submit" :disabled="busy" data-testid="open-takeaway">{{ t('posFloor.newBill') }}</Button>
        </div>
      </form>
    </DialogContent>
  </Dialog>

  <Dialog v-if="sheet?.kind === 'drawer'" :open="true" @update:open="(v) => { if (!v) sheet = null }">
    <DialogContent class="max-w-md p-5" data-testid="drawer-dialog">
      <DialogTitle>{{ t('posFloor.drawer') }}</DialogTitle>
      <DialogDescription class="mb-3 mt-1">{{ t('posFloor.drawerHint') }}</DialogDescription>
      <ErrorNotice v-if="dialogError" :error="dialogError" inline />
      <form class="flex flex-col gap-3" novalidate @submit.prevent="openDrawer">
        <FormField :label="t('posFloor.reason')">
          <template #default="{ id }"><Input :id="id" v-model="reason" name="reason" autocomplete="off" data-testid="drawer-reason" /></template>
        </FormField>
        <div class="flex justify-end gap-2">
          <Button type="button" variant="outline" @click="sheet = null">{{ t('common.cancel') }}</Button>
          <Button type="submit" :disabled="busy || !reason.trim()" data-testid="drawer-submit">{{ t('posFloor.drawerOpen') }}</Button>
        </div>
      </form>
    </DialogContent>
  </Dialog>

  <Dialog v-if="sheet?.kind === 'hold' && sheetTable" :open="true" @update:open="(v) => { if (!v) sheet = null }">
    <DialogContent class="max-w-md p-5" data-testid="hold-dialog">
      <DialogTitle>{{ t('posFloor.holdTable') }} · {{ sheetTable.code }}</DialogTitle>
      <DialogDescription class="mb-3 mt-1">{{ t('posFloor.holdHint') }}</DialogDescription>
      <ErrorNotice v-if="dialogError" :error="dialogError" inline />
      <form class="flex flex-col gap-3" novalidate @submit.prevent="hold(sheetTable)">
        <FormField :label="t('posFloor.holdName')">
          <template #default="{ id }"><Input :id="id" v-model="holdName" name="name" autocomplete="off" data-testid="hold-name" /></template>
        </FormField>
        <div class="grid grid-cols-2 gap-3">
          <FormField :label="t('posFloor.holdTime')">
            <template #default="{ id }"><Input :id="id" v-model="holdTime" name="from_time" type="time" data-testid="hold-time" /></template>
          </FormField>
          <FormField :label="t('posFloor.holdParty')">
            <template #default="{ id }"><Input :id="id" v-model="holdParty" name="party_size" inputmode="numeric" autocomplete="off" /></template>
          </FormField>
        </div>
        <div class="flex justify-end gap-2">
          <Button type="button" variant="outline" @click="sheet = null">{{ t('common.cancel') }}</Button>
          <Button type="submit" :disabled="busy || !holdName.trim()" data-testid="hold-submit">{{ t('posFloor.holdSave') }}</Button>
        </div>
      </form>
    </DialogContent>
  </Dialog>
</template>
