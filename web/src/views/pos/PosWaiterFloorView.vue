<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { PosFloorState, PosFloorTable } from '@/api/types'
import EmptyState from '@/components/app/EmptyState.vue'
import ErrorNotice from '@/components/app/ErrorNotice.vue'
import PageHeader from '@/components/app/PageHeader.vue'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogTitle } from '@/components/ui/dialog'
import { NativeSelect } from '@/components/ui/native-select'
import { debounce, usePosEvents } from '@/composables/usePosEvents'
import { usePosOutlet } from '@/composables/usePosOutlet'
import { t } from '@/i18n'
import { usePropertyStore } from '@/stores/property'
import { formatDuration } from '@/utils/duration'
import { formatMoney } from '@/utils/format'
import { newIdempotencyKey } from '@/utils/reservations'
import { Store } from 'lucide-vue-next'

/**
 * The tables on a phone (docs/architecture/34-pos.md 11, mockup 16): the areas as chips, the tables of an area as large cards that say their state in words and their total, and
 * the open bills below. A tap on a free table asks for the number of guests and opens the bill; a tap on a table with a bill continues it. It follows the stream of the outlet, so a
 * table that another waiter took is taken here too. The waiter pays nothing: the bill goes to the cashier.
 */
const router = useRouter()
const property = usePropertyStore()
const pid = computed(() => property.currentId)
const { outlets, outletId, outlet, error: outletsError, loaded } = usePosOutlet()

const state = ref<PosFloorState | null>(null)
const error = ref<ApiError | null>(null)
const areaId = ref<number | null>(null)
const busy = ref(false)
const guestTable = ref<PosFloorTable | null>(null)
const guests = ref(2)

async function load(): Promise<void> {
  const propertyId = pid.value
  const id = outletId.value
  if (propertyId === null || id === null) return
  try {
    const { data } = await api.GET('/api/v1/properties/{propertyId}/pos/outlets/{id}/floor', { params: { path: { propertyId, id } } })
    state.value = data ?? null
    error.value = null
    if (areaId.value === null || !state.value?.areas.some((a) => a.id === areaId.value)) areaId.value = state.value?.areas[0]?.id ?? null
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  }
}
watch(outletId, () => {
  state.value = null
  areaId.value = null
  void load()
}, { immediate: true })

const refresh = debounce(() => void load(), 150)
const events = usePosEvents({
  propertyId: () => pid.value,
  channels: () => (outletId.value === null ? [] : [`outlet:${outletId.value}`]),
  onEvent: () => refresh(),
  onReload: () => void load(),
})

const area = computed(() => state.value?.areas.find((a) => a.id === areaId.value) ?? null)
const STATUS_CLASS: Record<string, string> = {
  FREE: 'border-border bg-card',
  OCCUPIED: 'border-primary bg-primary/10',
  AWAITING_PAYMENT: 'border-warning bg-warning/20',
  HELD: 'border-dashed border-primary/60 bg-muted',
}
const tableTotal = (tb: PosFloorTable): string => formatMoney(tb.bills.reduce((n, b) => n + Number(b.total), 0).toString())

function tap(tb: PosFloorTable): void {
  if (tb.bills.length > 0 && tb.bills[0]) {
    void router.push(`/pos/waiter/bills/${tb.bills[0].id}`)
    return
  }
  error.value = null
  guests.value = Math.min(Math.max(tb.capacity ?? 2, 1), 2)
  guestTable.value = tb
}

async function openBill(body: { order_type: 'DINE_IN' | 'TAKEAWAY'; table_id?: number; guest_count?: number }): Promise<void> {
  const propertyId = pid.value
  const id = outletId.value
  if (propertyId === null || id === null || busy.value) return
  busy.value = true
  error.value = null
  try {
    const { data } = await api.POST('/api/v1/properties/{propertyId}/pos/outlets/{id}/bills', { params: { path: { propertyId, id } }, body: { client_uuid: newIdempotencyKey(), ...body } })
    guestTable.value = null
    if (data) await router.push(`/pos/waiter/bills/${data.id}`)
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    busy.value = false
  }
}
function seat(): void {
  const tb = guestTable.value
  if (!tb || tb.id === undefined) return
  void openBill({ order_type: 'DINE_IN', table_id: tb.id, guest_count: guests.value })
}
</script>

<template>
  <div class="mx-auto flex w-full max-w-[430px] flex-col gap-3" data-testid="waiter-floor">
    <PageHeader :title="t('posWaiter.tablesTitle')" class="mb-0">
      <template #actions>
        <span class="inline-flex items-center gap-1 text-xs text-muted-foreground" :data-testid="`stream-${events.state.value}`">
          <span class="size-2 rounded-full" :class="events.state.value === 'live' ? 'bg-primary' : 'bg-warning'" aria-hidden="true" />
          {{ t(`posWaiter.stream_${events.state.value}`) }}
        </span>
      </template>
    </PageHeader>
    <ErrorNotice v-if="error || outletsError" :error="(error ?? outletsError)!" inline />
    <p v-if="pid === null" class="muted">{{ t('setup.selectProperty') }}</p>
    <EmptyState v-else-if="loaded && outlets.length === 0" :icon="Store" :title="t('posFloor.noOutlet')" :description="t('posFloor.noOutletHint')" />
    <template v-else>
      <NativeSelect v-if="outlets.length > 1" v-model="outletId" :aria-label="t('posFloor.outlet')" data-testid="outlet-select">
        <option v-for="o in outlets" :key="o.id" :value="o.id">{{ o.name }}</option>
      </NativeSelect>
      <p v-else-if="outlet" class="m-0 text-sm text-muted-foreground">{{ outlet.name }}</p>

      <div v-if="state" class="flex gap-2 overflow-x-auto pb-1 [scrollbar-width:none] [&::-webkit-scrollbar]:hidden" role="tablist" :aria-label="t('posFloor.areas')">
        <button
          v-for="a in state.areas" :key="a.id" type="button" role="tab" :aria-selected="a.id === areaId" :data-testid="`area-${a.id}`"
          class="h-10 shrink-0 cursor-pointer rounded-full border px-4 text-sm"
          :class="a.id === areaId ? 'border-primary bg-primary/10 font-semibold' : 'border-border bg-card'"
          @click="areaId = a.id"
        >{{ a.name }}</button>
      </div>

      <div v-if="area" class="grid grid-cols-2 gap-2" data-testid="waiter-tables">
        <button
          v-for="tb in area.tables" :key="tb.id" type="button" class="flex min-h-[88px] cursor-pointer flex-col items-start gap-1 rounded-xl border-2 p-3 text-left"
          :class="STATUS_CLASS[tb.status] ?? STATUS_CLASS.FREE" :data-testid="`table-${tb.code}`" :data-status="tb.status" @click="tap(tb)"
        >
          <span class="text-lg font-bold">{{ tb.code }}</span>
          <span class="w-full truncate text-xs text-muted-foreground" data-testid="table-state">{{ tb.bills.length > 1 ? t('posFloor.bills', { n: tb.bills.length }) : t(`posFloor.status_${tb.status}`) }}</span>
          <span v-if="tb.bills.length > 0" class="w-full truncate text-sm font-semibold tabular-nums">{{ tableTotal(tb) }}</span>
          <span v-if="tb.bills.length > 0" class="w-full truncate text-xs text-muted-foreground" data-testid="table-since">{{ formatDuration(Math.max(0, ...tb.bills.map((b) => b.minutes))) }}</span>
          <span v-else class="text-xs text-muted-foreground">{{ t('posWaiter.seats', { n: tb.capacity }) }}</span>
        </button>
      </div>

      <section v-if="state && state.open_bills.length > 0" data-testid="waiter-bills">
        <h2 class="m-0 mb-1 mt-2 text-sm font-semibold">{{ t('posFloor.openBills', { n: state.open_bills.length }) }}</h2>
        <ul class="m-0 list-none p-0">
          <li v-for="b in state.open_bills" :key="b.id">
            <button type="button" class="flex min-h-12 w-full cursor-pointer items-center gap-2 border-0 border-t border-border bg-transparent px-1 text-left text-sm" :data-testid="`bill-${b.id}`" @click="router.push(`/pos/waiter/bills/${b.id}`)">
              <strong>{{ b.bill_number }}</strong>
              <span class="text-muted-foreground">{{ b.table_code || b.customer_name || t(`posFloor.type_${b.order_type}`) }}</span>
              <span class="ml-auto tabular-nums">{{ formatMoney(b.total) }}</span>
            </button>
          </li>
        </ul>
      </section>
      <Button type="button" variant="outline" class="min-h-12" data-testid="waiter-takeaway" :disabled="busy" @click="openBill({ order_type: 'TAKEAWAY' })">{{ t('posFloor.takeaway') }}</Button>
    </template>
  </div>

  <Dialog v-if="guestTable" :open="true" @update:open="(v) => { if (!v) guestTable = null }">
    <DialogContent class="max-w-xs p-5" data-testid="guests-dialog">
      <DialogTitle>{{ t('posWaiter.openTable', { code: guestTable.code }) }}</DialogTitle>
      <DialogDescription class="mb-3 mt-1">{{ t('posWaiter.guestsHint') }}</DialogDescription>
      <div class="flex items-center justify-center gap-4">
        <Button type="button" variant="outline" class="size-12 text-xl" :aria-label="t('posBill.less')" data-testid="guests-less" @click="guests = Math.max(1, guests - 1)">−</Button>
        <span class="w-10 text-center text-2xl font-bold tabular-nums" data-testid="guests-count">{{ guests }}</span>
        <Button type="button" variant="outline" class="size-12 text-xl" :aria-label="t('posBill.more')" data-testid="guests-more" @click="guests = Math.min(99, guests + 1)">+</Button>
      </div>
      <div class="mt-4 flex justify-end gap-2">
        <Button type="button" variant="outline" @click="guestTable = null">{{ t('common.cancel') }}</Button>
        <Button type="button" :disabled="busy" data-testid="guests-open" @click="seat">{{ t('posWaiter.openBill') }}</Button>
      </div>
    </DialogContent>
  </Dialog>
</template>
