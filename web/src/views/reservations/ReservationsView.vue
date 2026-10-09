<script setup lang="ts">
import { CalendarPlus, GanttChart, MoreVertical } from 'lucide-vue-next'
import { computed, reactive, ref, watch } from 'vue'
import { RouterLink, useRoute, useRouter } from 'vue-router'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { ReservationSummary } from '@/api/types'
import DataTable, { type Column } from '@/components/app/DataTable.vue'
import EmptyState from '@/components/app/EmptyState.vue'
import FormField from '@/components/app/FormField.vue'
import PageHeader from '@/components/app/PageHeader.vue'
import StatusBadge from '@/components/app/StatusBadge.vue'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { NativeSelect } from '@/components/ui/native-select'
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover'
import { useReservationLookups } from '@/composables/useReservationLookups'
import { useRoomTypes } from '@/composables/useRoomTypes'
import { t } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import { type ReservationAction, reservationActions } from '@/utils/reservationActions'
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

const FIELDS = ['q', 'status', 'arrivalFrom', 'arrivalTo', 'departureFrom', 'departureTo', 'roomType', 'ratePlan', 'company'] as const
type Field = (typeof FIELDS)[number]
const fromAddress = (k: Field): string => (typeof route?.query[k] === 'string' ? (route.query[k] as string) : '')
const filter = reactive<Record<Field, string>>(Object.fromEntries(FIELDS.map((k) => [k, fromAddress(k)])) as Record<Field, string>)

const rows = ref<ReservationSummary[]>([])
const nextCursor = ref<string | undefined>()
const loading = ref(false)
const searched = ref(false)
const error = ref<ApiError | null>(null)

const canRead = computed(() => auth.can('reservation.read', property.currentId))
const canCreate = computed(() => auth.can('reservation.create', property.currentId))
const can = (permission: string) => auth.can(permission, property.currentId)
const businessDate = computed(() => property.clock?.business_date ?? '')
const filtered = computed(() => FIELDS.some((k) => filter[k] !== ''))

const columns = computed<Column<ReservationSummary>[]>(() => [
  { key: 'confirmation_number', label: t('reservations.confirmation'), sortable: true },
  { key: 'guest_name', label: t('reservations.guest'), sortable: true },
  { key: 'room', label: t('reservations.roomCol'), sortable: true, sortValue: (r) => r.rooms[0]?.room_number || r.rooms[0]?.room_type_code },
  { key: 'arrival_date', label: t('reservations.stay'), sortable: true, class: 'whitespace-nowrap' },
  { key: 'rate', label: t('reservations.rate'), align: 'right', sortable: true, sortValue: (r) => (r.rooms.find((l) => l.rate_amount) ? Number(r.rooms.find((l) => l.rate_amount)?.rate_amount) : null), class: 'tabular-nums whitespace-nowrap px-2' },
  { key: 'company', label: t('reservations.company'), sortable: true, sortValue: (r) => companyOf(r) },
  { key: 'status', label: t('reservations.status'), sortable: true, sortValue: (r) => String(uiStatus(r.display_status)), class: 'whitespace-nowrap' },
  { key: 'actions', label: t('reservations.actions'), align: 'right', class: 'min-w-[8rem]' },
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
const paxOf = (r: ReservationSummary) => `${r.rooms.reduce((s, l) => s + l.adult_count, 0)}+${r.rooms.reduce((s, l) => s + l.child_count, 0)}`

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
const primary = (r: ReservationSummary) => actionsOf(r).slice(0, 2)
const more = (r: ReservationSummary) => actionsOf(r).slice(2)

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
  }
}

// Only the latest request counts: a slow answer to an older search must not replace the answer to a newer one.
let latest = 0

async function load(more = false): Promise<void> {
  const propertyId = property.currentId
  if (propertyId === null || !canRead.value) return
  const mine = ++latest
  loading.value = true
  error.value = null
  try {
    const { data } = await api.GET('/api/v1/properties/{propertyId}/reservations', { params: { path: { propertyId }, query: { ...query(), cursor: more ? nextCursor.value : undefined } } })
    if (mine !== latest) return
    rows.value = more ? [...rows.value, ...(data?.data ?? [])] : (data?.data ?? [])
    nextCursor.value = data?.next_cursor
    searched.value = true
  } catch (e) {
    if (mine !== latest) return
    error.value = e instanceof ApiError ? e : new ApiError({ type: 'about:blank', title: 'Error', status: 0, code: 'NETWORK_ERROR', detail: String(e) })
  } finally {
    if (mine === latest) loading.value = false
  }
}

/** Searches again from the first page; the filters go to the address. */
function search(): void {
  const next = Object.fromEntries(FIELDS.filter((k) => filter[k] !== '').map((k) => [k, filter[k]]))
  void router.replace({ query: next })
  rows.value = []
  searched.value = false
  void load()
}

function clear(): void {
  for (const k of FIELDS) filter[k] = ''
  search()
}

watch(() => property.currentId, () => {
  rows.value = []
  searched.value = false
  void load()
}, { immediate: true })
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

  <div v-if="error" class="alert" role="alert" data-testid="form-error">
    {{ error.message }} <code>{{ error.code }}</code>
    <Button type="button" variant="outline" size="sm" class="ml-2" data-testid="retry" @click="search">{{ t('frontDesk.page.retry') }}</Button>
  </div>
  <p v-if="property.currentId === null" class="muted">{{ t('reservations.selectProperty') }}</p>
  <p v-else-if="!canRead" class="muted" data-testid="no-access">{{ t('reservations.noAccess') }}</p>

  <template v-else>
    <form class="mb-4 flex flex-wrap items-end gap-3" role="search" novalidate data-testid="filters" @submit.prevent="search">
      <FormField class="min-w-52 flex-1" :label="t('reservations.search')">
        <template #default="{ id }"><Input :id="id" v-model="filter.q" name="q" type="search" :placeholder="t('reservations.searchPlaceholder')" /></template>
      </FormField>
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
      <Button type="submit" :disabled="loading">{{ t('reservations.search') }}</Button>
      <Button v-if="filtered" type="button" variant="outline" data-testid="clear-filters" @click="clear">{{ t('dataTable.clearFilters') }}</Button>
    </form>

    <DataTable v-if="!error || searched" class="hidden md:block" :columns="columns" :rows="rows" row-key="id" :loading="!searched" :row-test-id="(r) => `res-${r.confirmation_number}`" :caption="t('reservations.title')">
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
          <div :data-testid="`room-${row.confirmation_number}`">
            {{ row.rooms[0]?.room_number || t('reservations.noRoom') }}<span v-if="row.rooms.length > 1" class="text-muted-foreground"> {{ t('reservations.moreRooms', { n: row.rooms.length - 1 }) }}</span>
          </div>
          <div class="text-xs text-muted-foreground">{{ [...new Set(row.rooms.map((l) => l.room_type_code))].join(' · ') }}</div>
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
      <template #cell-status="{ row }">
        <StatusBadge domain="reservation" :status="String(uiStatus(row.display_status))" :title="row.status === 'DRAFT' ? t('reservations.noInventory') : undefined" />
        <div class="mt-0.5 text-xs text-muted-foreground tabular-nums">
          {{ t('reservations.deposit') }}
          <span v-if="row.deposit" :data-testid="`deposit-${row.confirmation_number}`">{{ $money(row.deposit.paid) }}</span>
          <span v-else :data-testid="`deposit-${row.confirmation_number}`">—</span>
        </div>
      </template>
      <template #cell-actions="{ row }">
        <div class="flex items-center justify-end gap-1.5">
          <Button v-for="a in primary(row)" :key="a" as-child size="sm" variant="outline" :data-testid="`${a}-${row.confirmation_number}`">
            <RouterLink :to="target(row, a) ?? `/reservations/${row.id}`">{{ t(`reservations.action.${a}`) }}</RouterLink>
          </Button>
          <Popover v-if="more(row).length">
            <PopoverTrigger as-child>
              <Button type="button" size="sm" variant="ghost" :aria-label="t('frontDesk.inHouse.moreActions')" :data-testid="`more-${row.confirmation_number}`"><MoreVertical /></Button>
            </PopoverTrigger>
            <PopoverContent class="w-44 p-1">
              <div :data-testid="`menu-${row.confirmation_number}`">
                <Button v-for="a in more(row)" :key="a" as-child variant="ghost" size="sm" class="w-full justify-start" :data-testid="`${a}-${row.confirmation_number}`">
                  <RouterLink :to="target(row, a) ?? `/reservations/${row.id}`">{{ t(`reservations.action.${a}`) }}</RouterLink>
                </Button>
              </div>
            </PopoverContent>
          </Popover>
        </div>
      </template>
      <template #empty><EmptyState :title="filtered ? t('reservations.noMatch') : t('reservations.empty')" data-testid="empty" /></template>
      <template #footer>
        <div v-if="nextCursor" class="flex justify-center p-3">
          <Button variant="outline" size="sm" :disabled="loading" data-testid="more" @click="load(true)">{{ t('reservations.loadMore') }}</Button>
        </div>
      </template>
    </DataTable>
    <EmptyState v-else :title="t('frontDesk.page.couldNotLoad')" data-testid="not-loaded" />

    <!-- A narrow screen gets a card for each reservation instead of the table. -->
    <ul v-if="!error || searched" class="m-0 grid list-none gap-3 p-0 md:hidden" data-testid="reservation-cards">
      <li v-for="r in rows" :key="r.id" class="rounded-lg border border-border p-3" :data-testid="`card-${r.confirmation_number}`">
        <div class="flex items-start justify-between gap-2">
          <div>
            <RouterLink :to="`/reservations/${r.id}`" class="font-medium">{{ r.confirmation_number }}</RouterLink>
            <div class="text-sm">{{ r.guest_name || '—' }} <span class="text-muted-foreground">{{ paxOf(r) }}</span></div>
          </div>
          <StatusBadge domain="reservation" :status="String(uiStatus(r.display_status))" />
        </div>
        <div class="mt-1 text-sm">{{ $date(r.arrival_date) }} → {{ $date(r.departure_date) }} <span class="text-muted-foreground">· {{ t('reservations.nightsCount', { n: r.nights }) }}</span></div>
        <div class="text-sm text-muted-foreground">
          {{ r.rooms.length ? [...new Set(r.rooms.map((l) => l.room_type_code))].join(' · ') : '—' }} · {{ rateText(r) }}<template v-if="companyOf(r)"> · {{ companyOf(r) }}</template>
          <template v-if="r.deposit"> · {{ t('reservations.deposit') }} {{ $money(r.deposit.paid) }}</template>
        </div>
        <div class="mt-2 flex flex-wrap gap-1.5">
          <Button v-for="a in actionsOf(r)" :key="a" as-child size="sm" variant="outline">
            <RouterLink :to="target(r, a) ?? `/reservations/${r.id}`">{{ t(`reservations.action.${a}`) }}</RouterLink>
          </Button>
        </div>
      </li>
    </ul>
    <div v-if="nextCursor" class="flex justify-center p-3 md:hidden">
      <Button variant="outline" size="sm" :disabled="loading" data-testid="more-mobile" @click="load(true)">{{ t('reservations.loadMore') }}</Button>
    </div>
  </template>
</template>
