<script setup lang="ts">
import FilterBar from '@/components/app/FilterBar.vue'
import ErrorNotice from '@/components/app/ErrorNotice.vue'
import { CalendarPlus, GanttChart } from 'lucide-vue-next'
import { computed, reactive, ref, watch } from 'vue'
import { RouterLink, useRoute, useRouter } from 'vue-router'
import { api } from '@/api/client'
import type { FreeRoom, Reservation, ReservationSummary } from '@/api/types'
import DataTable, { type Column } from '@/components/app/DataTable.vue'
import EmptyState from '@/components/app/EmptyState.vue'
import FormField from '@/components/app/FormField.vue'
import PageHeader from '@/components/app/PageHeader.vue'
import RoomLink from '@/components/app/RoomLink.vue'
import StatusBadge from '@/components/app/StatusBadge.vue'
import AssignRoomsSheet from '@/components/AssignRoomsSheet.vue'
import AutoAssignDialog from '@/components/AutoAssignDialog.vue'
import RoomPicker from '@/components/RoomPicker.vue'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { NativeSelect } from '@/components/ui/native-select'
import type { RowAction } from '@/components/app/rowActions'
import { usePagedList } from '@/composables/usePagedList'
import { useServerSort } from '@/composables/useServerSort'
import { toast } from '@/composables/useToast'
import { useReservationLookups } from '@/composables/useReservationLookups'
import { useRoomTypes } from '@/composables/useRoomTypes'
import { t } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import type { AssignFailure } from '@/utils/assignRoom'
import { type ReservationAction, reservationActions } from '@/utils/reservationActions'
import { type AssignLine, isUnassigned, lineOfSummary } from '@/utils/roomAssign'
import { formatMoney } from '@/utils/format'
import { STATUS_FILTER, apiStatusOf, uiStatus } from '@/utils/reservationStatus'

/**
 * The reservation list: a workspace to scan. Every filter is asked of the server (a page is never filtered after it is cut), and the filters are kept in the address, so a search can be
 * shared, reloaded and come back to with the back button. The row shows the rooms with the booked price (the snapshot of the booking), the company, the deposit and the status staff see.
 */
const auth = useAuthStore()
const property = usePropertyStore()
const route = useRoute()
const router = useRouter()
const { types } = useRoomTypes()
const { ratePlans, companies } = useReservationLookups()

// The order is the server's (the list is read a page at a time) and lives in the address: ?sort=&order=. Without it: the nearest arrival first.
const { sort, params: sortParams, query: sortQuery, setSort } = useServerSort(['confirmation_number', 'guest_name', 'arrival_date', 'status', 'rate'])

const FIELDS = ['q', 'status', 'arrivalFrom', 'arrivalTo', 'departureFrom', 'departureTo', 'roomType', 'ratePlan', 'company'] as const
type Field = (typeof FIELDS)[number]
const fromAddress = (k: Field): string => (typeof route?.query[k] === 'string' ? (route.query[k] as string) : '')
const filter = reactive<Record<Field, string>>(Object.fromEntries(FIELDS.map((k) => [k, fromAddress(k)])) as Record<Field, string>)


const canRead = computed(() => auth.can('reservation.read', property.currentId))
const canCreate = computed(() => auth.can('reservation.create', property.currentId))
const can = (permission: string) => auth.can(permission, property.currentId)
const businessDate = computed(() => property.clock?.business_date ?? '')
const filtered = computed(() => FIELDS.some((k) => filter[k] !== ''))

// How many filters are on, for the "Filter (n)" button of a phone (the search text is in the bar and is not counted).
const activeFilters = computed(() => FIELDS.filter((k) => k !== 'q' && filter[k] !== '').length)

const columns = computed<Column<ReservationSummary>[]>(() => [
  { key: 'confirmation_number', label: t('reservations.confirmation'), sortable: true, card: 'primary' as const },
  { key: 'guest_name', label: t('reservations.guest'), sortable: true, card: 'secondary' as const },
  { key: 'room', label: t('reservations.roomCol') },
  { key: 'arrival_date', label: t('reservations.stay'), sortable: true, class: 'whitespace-nowrap' },
  { key: 'rate', label: t('reservations.rate'), align: 'right', sortable: true, class: 'tabular-nums whitespace-nowrap px-2' },
  { key: 'company', label: t('reservations.company') },
  { key: 'status', label: t('reservations.status'), sortable: true, class: 'whitespace-nowrap', card: 'badge' as const },
  { key: 'actions', label: t('reservations.actions'), align: 'right', class: 'w-16' },
])

/** The company of a reservation: its own, else the company a room is billed to. */
const companyOf = (r: ReservationSummary): string => r.company_name || r.rooms.find((l) => l.billing_company)?.billing_company || ''
const billingCompanies = (r: ReservationSummary): string[] => [...new Set(r.rooms.map((l) => l.billing_company).filter((c): c is string => !!c))]
const planCodes = (r: ReservationSummary): string => [...new Set(r.rooms.map((l) => l.rate_plan_code))].join(' · ')
/** The booked price of the arrival night: one amount, or the lowest and the highest when the rooms differ; a dash when no room has a snapshot. */
const rateText = (r: ReservationSummary): string => {
  const amounts = r.rooms.map((l) => l.rate_amount).filter(Boolean).sort((x, y) => Number(x) - Number(y))
  if (!amounts.length) return '—'
  const lo = amounts[0] as string
  const hi = amounts[amounts.length - 1] as string
  return Number(lo) === Number(hi) ? formatMoney(lo) : `${formatMoney(lo)} – ${formatMoney(hi)}`
}
/** The party in words: "2 adults", "2 adults · 1 child" (in Indonesian "2 dewasa · 1 anak"), not "2+1". */
const paxOf = (r: ReservationSummary): string => {
  const adults = r.rooms.reduce((s, l) => s + l.adult_count, 0)
  const children = r.rooms.reduce((s, l) => s + l.child_count, 0)
  return [t('reservations.paxAdults', { n: adults }, adults), ...(children > 0 ? [t('reservations.paxChildren', { n: children }, children)] : [])].join(' · ')
}

const actionsOf = (r: ReservationSummary): ReservationAction[] =>
  reservationActions(
    {
      displayStatus: r.display_status, rooms: r.rooms.map((l) => ({ status: l.status, arrivalDate: l.arrival_date })), hasStay: r.rooms.some((l) => l.stay_id != null),
      hasFolio: !!r.deposit, hasGuest: r.guest_id != null,
    },
    { can, businessDate: businessDate.value },
  ).filter((a) => ['view', 'edit', 'checkIn', 'payment', 'viewStay', 'folio'].includes(a)) // the ones that are a link; the rest ask for a reason or a confirmation and are done on the reservation

const stayOf = (r: ReservationSummary): number | undefined => r.rooms.find((l) => l.stay_id != null)?.stay_id ?? undefined
function target(r: ReservationSummary, a: ReservationAction): string | undefined {
  switch (a) {
    case 'view':
    case 'edit':
      return `/reservations/${r.id}`
    case 'checkIn':
      return `/arrivals?q=${encodeURIComponent(r.confirmation_number)}`
    case 'payment':
      return r.deposit ? `/folios/${r.deposit.folio_id}?tab=payment` : `/reservations/${r.id}`
    case 'viewStay':
      return stayOf(r) ? `/stays/${stayOf(r)}` : undefined
    case 'folio':
      return r.deposit ? `/folios/${r.deposit.folio_id}` : undefined
    default:
      return undefined
  }
}
/** The row opens the reservation, so "view" is not an action of its own; the others are in the "..." menu (and the card of a phone). */
const menuOf = (r: ReservationSummary): RowAction[] => {
  const links: RowAction[] = actionsOf(r)
    .filter((a) => a !== 'view')
    .map((a) => ({ key: a, label: t(`reservations.action.${a}`), to: target(r, a) ?? `/reservations/${r.id}`, testId: `${a}-${r.confirmation_number}` }))
  // The rooms of the reservation, each with its own list of free rooms (a sheet): for who may update a reservation, when a room of it waits for its check-in.
  return canAssign(r) ? [{ key: 'assignRooms', label: t('reservations.assignRooms'), onSelect: () => (sheetFor.value = r.id), testId: `assign-rooms-${r.confirmation_number}` }, ...links] : links
}

// ---- the rooms, from the list (reservation.update; the rooms that wait for check-in, of the booked type: an upgrade goes through the page of the reservation)
const canAssign = (r: ReservationSummary): boolean => can('reservation.update') && r.display_status !== 'CANCELLED' && r.rooms.some((l) => l.status === 'CONFIRMED')
/** A reservation of one room that waits for it: the list offers the picker right in its Kamar column. */
const singleUnassigned = (r: ReservationSummary): ReservationSummary['rooms'][number] | null => {
  const only = r.rooms.length === 1 ? r.rooms[0] : undefined
  return can('reservation.update') && only && isUnassigned(only) ? only : null
}
/** A reservation of several rooms, some of which wait for a room: how many have one ("1 of 3 rooms assigned"), instead of the first room and "+2 more". */
const progress = (r: ReservationSummary): { done: number; total: number } | null => {
  const live = r.rooms.filter((l) => l.status !== 'CANCELLED')
  const missing = live.filter((l) => isUnassigned(l)).length
  return live.length > 1 && missing > 0 ? { done: live.filter((l) => l.room_id != null).length, total: live.length } : null
}
const sheetFor = ref<number | null>(null)
const sheetRow = computed(() => rows.value.find((r) => r.id === sheetFor.value) ?? null)
const sheetOpen = computed({
  get: () => sheetRow.value !== null,
  set: (v: boolean) => {
    if (!v) sheetFor.value = null
  },
})
const autoLines = ref<AssignLine[]>([])
const autoOpen = ref(false)

/** The answer of an assignment on its row: the room of the line and the version the next assignment of the reservation has to ask for. */
function patchRow(res: Reservation): void {
  rows.value = rows.value.map((r) => {
    if (r.id !== res.id) return r
    return { ...r, version: res.version, rooms: r.rooms.map((l) => { const done = res.rooms.find((x) => x.id === l.id); return done ? { ...l, room_id: done.room_id ?? null, room_number: done.room_number ?? '' } : l }) }
  })
}
function roomAssigned(res: Reservation, room: FreeRoom): void {
  patchRow(res)
  toast.success(t('roomPicker.assigned', { room: room.room_number }))
}
/** The row as it is now (a conflict: the reservation was changed, a room was taken): read again by its number, and replaced in place, whatever page it is on. */
async function refreshRow(id: number): Promise<void> {
  const row = rows.value.find((r) => r.id === id)
  const propertyId = property.currentId
  if (!row || propertyId === null) return
  const { data } = await api.GET('/api/v1/properties/{propertyId}/reservations', { params: { path: { propertyId }, query: { q: row.confirmation_number, limit: 5 } } })
  const fresh = data?.data?.find((r) => r.id === id)
  if (fresh) rows.value = rows.value.map((r) => (r.id === id ? fresh : r))
}
async function assignFailed(id: number, failure: AssignFailure): Promise<void> {
  if (failure.reload) await refreshRow(id)
}
function startAuto(r: ReservationSummary): void {
  autoLines.value = r.rooms.filter((l) => isUnassigned(l)).map((l) => lineOfSummary(r, l))
  autoOpen.value = true
}

function query() {
  return {
    limit: 50,
    q: filter.q.trim() || undefined,
    display_status: (apiStatusOf(filter.status) || undefined) as 'DRAFT' | undefined,
    arrival_from: filter.arrivalFrom || undefined,
    arrival_to: filter.arrivalTo || undefined,
    departure_from: filter.departureFrom || undefined,
    departure_to: filter.departureTo || undefined,
    room_type_id: filter.roomType ? Number(filter.roomType) : undefined,
    rate_plan_id: filter.ratePlan ? Number(filter.ratePlan) : undefined,
    company_id: filter.company ? Number(filter.company) : undefined,
    ...sortParams.value,
  }
}

// One page of 50 at a time. Only the latest request counts: a slow answer to an older search must not replace the answer to a newer one (usePagedList keeps to that).
const list = usePagedList<ReservationSummary>(async (cursor) => {
  const { data } = await api.GET('/api/v1/properties/{propertyId}/reservations', { params: { path: { propertyId: property.currentId! }, query: { ...query(), cursor } } })
  return { data: data?.data ?? [], next_cursor: data?.next_cursor }
})
const { rows, error, loading, loadingMore, loaded: searched, hasMore } = list

async function load(): Promise<void> {
  if (property.currentId === null || !canRead.value) return
  await list.reload()
}

/** Searches again from the first page; the filters go to the address. */
function search(): void {
  const next = Object.fromEntries(FIELDS.filter((k) => filter[k] !== '').map((k) => [k, filter[k]]))
  void router.replace({ query: { ...next, ...sortQuery.value } })
  list.reset() // a new search shows nothing of the old one, and a failed one shows no list
  void load()
}

function clear(): void {
  for (const k of FIELDS) filter[k] = ''
  search()
}

watch(() => property.currentId, () => {
  list.reset()
  void load()
}, { immediate: true })
// Another order reads the list again from its first page (a cursor belongs to an order); the address already holds it.
watch(sort, () => {
  list.reset()
  void load()
})
// Choosing a filter searches at once; the text waits for the search button (or Enter).
watch(() => [filter.status, filter.arrivalFrom, filter.arrivalTo, filter.departureFrom, filter.departureTo, filter.roomType, filter.ratePlan, filter.company], search)
</script>

<template>
  <PageHeader :title="t('reservations.title')">
    <template #actions>
      <Button as-child variant="outline" size="sm">
        <RouterLink to="/reservations/tape"><GanttChart />{{ t('reservations.tapeChart') }}</RouterLink>
      </Button>
      <Button v-if="canCreate" as-child size="sm" data-testid="new">
        <RouterLink to="/reservations/new"><CalendarPlus />{{ t('reservations.newReservation') }}</RouterLink>
      </Button>
    </template>
  </PageHeader>

  <ErrorNotice v-if="error" :error="error" inline data-testid="form-error">
<Button type="button" variant="outline" size="sm" class="ml-2" data-testid="retry" @click="search">{{ t('frontDesk.page.retry') }}</Button>
</ErrorNotice>
  <p v-if="property.currentId === null" class="muted">{{ t('reservations.selectProperty') }}</p>
  <p v-else-if="!canRead" class="muted" data-testid="no-access">{{ t('reservations.noAccess') }}</p>

  <template v-else>
    <FilterBar class="mb-4 flex flex-wrap items-end gap-3" role="search" data-testid="filters" :active="activeFilters" @submit="search">
      <template #search>
        <FormField class="min-w-52 flex-1" :label="t('reservations.search')">
        <template #default="{ id }"><Input :id="id" v-model="filter.q" name="q" type="search" :placeholder="t('reservations.searchPlaceholder')" /></template>
      </FormField>
      </template>

      
      <FormField class="w-40" :label="t('reservations.status')">
        <template #default="{ id }">
          <NativeSelect :id="id" v-model="filter.status" name="status">
            <option value="">{{ t('reservations.any') }}</option>
            <option v-for="s in STATUS_FILTER" :key="s.ui" :value="s.ui">{{ t(`status.${s.ui}`) }}</option>
          </NativeSelect>
        </template>
      </FormField>
      <FormField class="w-40" :label="t('reservations.arrivalFrom')">
        <template #default="{ id }"><Input :id="id" v-model="filter.arrivalFrom" name="arrival_from" type="date" /></template>
      </FormField>
      <FormField class="w-40" :label="t('reservations.arrivalTo')">
        <template #default="{ id }"><Input :id="id" v-model="filter.arrivalTo" name="arrival_to" type="date" /></template>
      </FormField>
      <FormField class="w-40" :label="t('reservations.departureFrom')">
        <template #default="{ id }"><Input :id="id" v-model="filter.departureFrom" name="departure_from" type="date" /></template>
      </FormField>
      <FormField class="w-40" :label="t('reservations.departureTo')">
        <template #default="{ id }"><Input :id="id" v-model="filter.departureTo" name="departure_to" type="date" /></template>
      </FormField>
      <FormField v-if="types.length" class="w-36" :label="t('reservations.roomType')">
        <template #default="{ id }">
          <NativeSelect :id="id" v-model="filter.roomType" name="room_type_id">
            <option value="">{{ t('reservations.any') }}</option>
            <option v-for="rt in types" :key="rt.id" :value="String(rt.id)">{{ rt.code }}</option>
          </NativeSelect>
        </template>
      </FormField>
      <FormField v-if="ratePlans.length" class="w-36" :label="t('reservations.ratePlan')">
        <template #default="{ id }">
          <NativeSelect :id="id" v-model="filter.ratePlan" name="rate_plan_id">
            <option value="">{{ t('reservations.any') }}</option>
            <option v-for="p in ratePlans" :key="p.id" :value="String(p.id)">{{ p.code }}</option>
          </NativeSelect>
        </template>
      </FormField>
      <FormField v-if="companies.length" class="w-44" :label="t('reservations.company')">
        <template #default="{ id }">
          <NativeSelect :id="id" v-model="filter.company" name="company_id">
            <option value="">{{ t('reservations.any') }}</option>
            <option v-for="c in companies" :key="c.id" :value="String(c.id)">{{ c.name }}</option>
          </NativeSelect>
        </template>
      </FormField>
      <template #actions>
        <Button type="submit" :disabled="loading">{{ t('reservations.search') }}</Button>
      <Button v-if="filtered" type="button" variant="outline" data-testid="clear-filters" @click="clear">{{ t('dataTable.clearFilters') }}</Button>
      </template>
    </FilterBar>

    <DataTable
      v-if="!error || searched"
      :columns="columns"
      :rows="rows"
      row-key="id"
      :loading="!searched"
      :has-more="hasMore"
      :loading-more="loadingMore"
      manual
      :sort="sort"
      cards
      :row-to="(r) => `/reservations/${r.id}`"
      :row-actions="menuOf"
      :row-test-id="(r) => `res-${r.confirmation_number}`"
      :caption="t('reservations.title')"
      @load-more="list.loadMore()"
      @sort-change="setSort"
    >
      <template #cell-confirmation_number="{ row }">
        <RouterLink :to="`/reservations/${row.id}`">{{ row.confirmation_number }}</RouterLink>
        <div v-if="row.group_code" class="text-xs text-muted-foreground">{{ row.group_code }}</div>
      </template>
      <template #cell-guest_name="{ row }">
        <div>{{ row.guest_name || '—' }}</div>
        <div class="text-xs text-muted-foreground">{{ paxOf(row) }}</div>
      </template>
      <template #cell-room="{ row }">
        <template v-if="row.rooms.length">
          <div v-if="progress(row)" class="font-medium text-warning-text" :data-testid="`rooms-progress-${row.confirmation_number}`">{{ t('reservations.roomsAssigned', { done: progress(row)!.done, total: progress(row)!.total }) }}</div>
          <div v-else :data-testid="`room-${row.confirmation_number}`">
            <RoomLink v-if="row.rooms[0]?.room_number" :room-id="row.rooms[0].room_id" :number="row.rooms[0].room_number" /><template v-else>{{ t('reservations.noRoom') }}</template><span v-if="row.rooms.length > 1" class="text-muted-foreground"> {{ t('reservations.moreRooms', { n: row.rooms.length - 1 }) }}</span>
          </div>
          <div class="text-xs text-muted-foreground">{{ [...new Set(row.rooms.map((l) => l.room_type_code))].join(' · ') }}</div>
          <div v-if="singleUnassigned(row)" class="mt-1" @click.stop>
            <RoomPicker
              :target="{ reservationId: row.id, lineId: singleUnassigned(row)!.id, version: row.version }" :type-id="singleUnassigned(row)!.room_type_id" :type-code="singleUnassigned(row)!.room_type_code"
              :arrival="singleUnassigned(row)!.arrival_date" :departure="singleUnassigned(row)!.departure_date" :requested-bed-type-id="singleUnassigned(row)!.requested_bed_type_id ?? null"
              :test-id="`pick-room-${row.confirmation_number}`" @assigned="roomAssigned" @failed="(f) => assignFailed(row.id, f)"
            />
          </div>
        </template>
        <span v-else>—</span>
      </template>
      <template #cell-arrival_date="{ row }">
        {{ $date(row.arrival_date) }} → {{ $date(row.departure_date) }}
        <div class="text-xs text-muted-foreground">{{ t('reservations.nightsCount', { n: row.nights }) }}</div>
      </template>
      <template #cell-rate="{ row }">
        <div :data-testid="`rate-${row.confirmation_number}`">{{ rateText(row) }}</div>
        <div class="text-xs text-muted-foreground">{{ planCodes(row) || '—' }}</div>
      </template>
      <template #cell-company="{ row }">
        <div :data-testid="`company-${row.confirmation_number}`">{{ companyOf(row) || '—' }}</div>
        <div v-if="billingCompanies(row).length" class="text-xs text-muted-foreground" :data-testid="`billing-${row.confirmation_number}`">{{ t('reservations.billedTo', { names: billingCompanies(row).join(', ') }) }}</div>
      </template>
      <template #cell-status="{ row, card }">
        <StatusBadge domain="reservation" :status="String(uiStatus(row.display_status))" :title="row.status === 'DRAFT' ? t('reservations.noInventory') : undefined" />
        <div v-if="row.deposit || !card" class="mt-0.5 text-xs text-muted-foreground tabular-nums">
          {{ t('reservations.deposit') }}
          <span v-if="row.deposit" :data-testid="`deposit-${row.confirmation_number}`">{{ $money(row.deposit.paid) }}</span>
          <span v-else :data-testid="`deposit-${row.confirmation_number}`">—</span>
        </div>
      </template>
      <template #empty><EmptyState :title="filtered ? t('reservations.noMatch') : t('reservations.empty')" :description="filtered ? t('emptyState.noMatch') : t('emptyState.reservations')" :action-label="filtered ? t('dataTable.clearFilters') : canCreate ? t('reservations.newReservation') : ''" :action-to="filtered ? '' : '/reservations/new'" :action-variant="filtered ? 'outline' : 'default'" data-testid="empty" @action="clear" /></template>
    </DataTable>
    <EmptyState v-else :description="t('emptyState.loadFailed')" :title="t('frontDesk.page.couldNotLoad')" data-testid="not-loaded" />

    <AssignRoomsSheet v-model:open="sheetOpen" :row="sheetRow" @assigned="roomAssigned" @failed="(f) => sheetRow && assignFailed(sheetRow.id, f)" @auto="sheetRow && startAuto(sheetRow)" />
    <AutoAssignDialog v-model:open="autoOpen" :lines="autoLines" @changed="() => sheetFor !== null && refreshRow(sheetFor)" />
  </template>
</template>
