<script setup lang="ts">
import ErrorNotice from '@/components/app/ErrorNotice.vue'
import { MoreVertical } from 'lucide-vue-next'
import { computed, ref, watch } from 'vue'
import { RouterLink, useRoute, useRouter } from 'vue-router'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { Arrival, CheckInResult, GuestView } from '@/api/types'
import ArrivalDrawer from '@/components/ArrivalDrawer.vue'
import AutoAssignDialog from '@/components/AutoAssignDialog.vue'
import RoomPicker from '@/components/RoomPicker.vue'
import DataTable, { type Column } from '@/components/app/DataTable.vue'
import EmptyState from '@/components/app/EmptyState.vue'
import FormField from '@/components/app/FormField.vue'
import RoomLink from '@/components/app/RoomLink.vue'
import StatusBadge from '@/components/app/StatusBadge.vue'
import GuestEditDialog from '@/components/GuestEditDialog.vue'
import ReadinessBadge from '@/components/ReadinessBadge.vue'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { NativeSelect } from '@/components/ui/native-select'
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover'
import { toast } from '@/composables/useToast'
import { useRoomTypes } from '@/composables/useRoomTypes'
import { t } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import { type ArrivalAction, MORE_ARRIVAL_ACTIONS, PRIMARY_ARRIVAL_ACTIONS, arrivalActions, arrivalTarget, checkInVariant } from '@/utils/arrivals'
import { type AssignFailure } from '@/utils/assignRoom'
import { fullName } from '@/utils/inHouse'
import { lineOfArrival } from '@/utils/roomAssign'
import { lineUiStatus } from '@/utils/reservationStatus'
import CheckInSheet from '@/components/CheckInSheet.vue'
import { checkedInCount } from '@/composables/useCheckInSheet'

/**
 * The arrivals tab of the front desk: the rooms arriving on a date (the business date by default) with what the desk needs before a check-in (company, booked rate, deposit, readiness), a
 * detail drawer, and from "Check in" a side sheet with the check-in form, so the list stays in view. The filters are asked of the server. The page header and the tabs belong to FrontDeskView.
 */
const props = withDefaults(defineProps<{ refresh?: number }>(), { refresh: 0 })
const emit = defineEmits<{ loaded: [count: number]; changed: [] }>()

const auth = useAuthStore()
const property = usePropertyStore()
const router = useRouter()
const route = useRoute()
const { types } = useRoomTypes()

const rows = ref<Arrival[]>([])
const checkInFor = ref<Arrival | null>(null) // the arrival in the check-in sheet: a copy, so the sheet keeps it when the list is read again
const error = ref<ApiError | null>(null)
const loaded = ref(false)
const drawerId = ref<number | null>(null)
const drawerOpen = ref(false)
const guestOpen = ref(false)
const guestId = ref<number | null>(null)

const businessDate = computed(() => property.clock?.business_date ?? '')
const chosenDate = ref('')
const status = ref('CONFIRMED')
// The search can come in the address (?q=): the reservation screens link here to check a guest in.
const q = ref(typeof route?.query.q === 'string' ? route.query.q : '')
const typeId = ref('')
const date = computed(() => chosenDate.value || businessDate.value)
// The input shows the date that is asked for (the business date until another is chosen); choosing the business date again is the default again.
const shownDate = computed({
  get: () => date.value,
  set: (v: string) => {
    chosenDate.value = v === businessDate.value ? '' : v
  },
})
const isDefault = computed(() => !chosenDate.value && status.value === 'CONFIRMED' && !q.value.trim() && !typeId.value)

const canRead = computed(() => auth.can('reservation.read', property.currentId))
const can = (permission: string) => auth.can(permission, property.currentId)
const drawerRow = computed(() => rows.value.find((a) => a.reservation_room_id === drawerId.value) ?? null)
// The menu of the row that is open: it closes when an action is chosen, so it is not left open behind the sheet or dialog the action opens.
const menuFor = ref<number | null>(null)

// A room for an arrival that has none, or another room for one that has: from the row, without going to the reservation (reservation.update; only a room that waits for its check-in).
// The rooms are of the booked type (an upgrade goes through the reservation); the version is the one of the row, and the list is read again after an assignment or a conflict.
const canAssign = (a: Arrival): boolean => can('reservation.update') && a.status === 'CONFIRMED'
const changing = ref<number | null>(null)
const autoOpen = ref(false)
const autoLines = computed(() => rows.value.filter((a) => canAssign(a) && !a.room_id).map(lineOfArrival))
const assignTarget = (a: Arrival) => ({ reservationId: a.reservation_id, lineId: a.reservation_room_id, version: a.reservation_version })
function startChange(a: Arrival): void {
  menuFor.value = null
  // The list opens once the menu has closed and given its focus back: a list that opened in the same moment would be closed by that very focus change.
  setTimeout(() => {
    changing.value = a.reservation_room_id
  }, 0)
}
async function roomAssigned(room: { room_number: string }): Promise<void> {
  changing.value = null
  toast.success(t('roomPicker.assigned', { room: room.room_number }))
  await load()
}
async function assignFailed(failure: AssignFailure): Promise<void> {
  if (failure.reload) await load() // the row (its version, its room) as it is now
}
const actionsOf = (a: Arrival) => arrivalActions(a, can)
const primary = (a: Arrival) => actionsOf(a).filter((x) => PRIMARY_ARRIVAL_ACTIONS.includes(x))
const more = (a: Arrival) => actionsOf(a).filter((x) => MORE_ARRIVAL_ACTIONS.includes(x))
const statuses = ['CONFIRMED', 'CHECKED_IN', 'CANCELLED', 'NO_SHOW']

const columns = computed<Column<Arrival>[]>(() => [
  { key: 'room_number', label: t('frontDesk.page.room'), sortable: true, class: 'whitespace-nowrap' },
  { key: 'guest_name', label: t('frontDesk.page.guest'), sortable: true },
  { key: 'company', label: t('frontDesk.inHouse.company'), sortable: true, sortValue: (a) => a.company?.name },
  { key: 'rate', label: t('frontDesk.inHouse.rate'), align: 'right', sortable: true, sortValue: (a) => (a.rate.amount ? Number(a.rate.amount) : null), class: 'tabular-nums' },
  { key: 'stay', label: t('frontDesk.page.stay'), sortable: true, sortValue: (a) => a.departure_date, class: 'whitespace-nowrap' },
  { key: 'state', label: t('frontDesk.arrivals.readinessTitle') },
  { key: 'actions', label: t('frontDesk.inHouse.actions'), align: 'right', class: 'min-w-[13rem]' },
])

// Only the latest request counts: a slow answer to an older search or filter must not replace the answer to a newer one.
let latest = 0

async function load(): Promise<void> {
  const propertyId = property.currentId
  if (propertyId === null || !canRead.value || !date.value) return
  const mine = ++latest
  error.value = null
  try {
    const { data } = await api.GET('/api/v1/properties/{propertyId}/arrivals', {
      params: { path: { propertyId }, query: { date: chosenDate.value || undefined, status: status.value as 'CONFIRMED', q: q.value.trim() || undefined, room_type_id: typeId.value ? Number(typeId.value) : undefined } },
    })
    if (mine !== latest) return
    rows.value = data?.data ?? []
    loaded.value = true
    if (isDefault.value) emit('loaded', rows.value.length) // the count on the tab is the count of the default list
  } catch (e) {
    if (mine !== latest) return
    error.value = e instanceof ApiError ? e : new ApiError({ type: 'about:blank', title: 'Error', status: 0, code: 'NETWORK_ERROR', detail: String(e) })
  }
}

function restart(): void {
  rows.value = []
  loaded.value = false
  void load()
}

function clearFilters(): void {
  chosenDate.value = ''
  status.value = 'CONFIRMED'
  q.value = ''
  typeId.value = ''
}

function openDetail(a: Arrival): void {
  drawerId.value = a.reservation_room_id
  drawerOpen.value = true
}

/** One handler for the row, its menu and the drawer: check-in and edit guest are done in place, the others go to the page that does them. */
function act(action: ArrivalAction, a: Arrival): void {
  menuFor.value = null
  if (action === 'checkIn') {
    drawerOpen.value = false
    checkInFor.value = a
    return
  }
  if (action === 'editGuest') {
    guestId.value = a.guest_id ?? null
    guestOpen.value = true
    return
  }
  const target = arrivalTarget(a, action)
  if (target) void router.push(target)
}

/** The guest was saved: every arrival of that guest shows the new name, the filters stay. */
function guestSaved(g: GuestView): void {
  const name = fullName(g)
  rows.value = rows.value.map((a) => (a.guest_id === g.id ? { ...a, guest_name: name } : a))
  toast.success(t('frontDesk.inHouse.guestSaved', { name }))
}

/** Checked in: the list is read again (the room is no longer waiting), the other tabs are told, and the desk is told which stay it is. */
async function checkedIn(result: CheckInResult): Promise<void> {
  toast.success(t('frontDesk.arrivals.checkedIn', { stay: result.stay.stay_number, room: result.stay_room.room_number }))
  emit('changed')
  await load()
}

watch(() => [property.currentId, businessDate.value, chosenDate.value, status.value, q.value.trim(), typeId.value], () => {
  checkInFor.value = null
  drawerOpen.value = false
  restart()
}, { immediate: true })
watch(() => props.refresh, restart)
watch(checkedInCount, () => void load()) // a check-in made from the room panel
</script>

<template>
  <ErrorNotice v-if="error" :error="error" inline data-testid="form-error">
<Button type="button" variant="outline" size="sm" class="ml-2" data-testid="retry" @click="restart">{{ t('frontDesk.page.retry') }}</Button>
</ErrorNotice>
  <p v-if="property.currentId === null" class="muted">{{ t('frontDesk.page.selectProperty') }}</p>
  <p v-else-if="!canRead" class="muted" data-testid="no-access">{{ t('frontDesk.page.noAccessArrivals') }}</p>

  <template v-else>
    <form class="mb-3 flex flex-wrap items-end gap-x-3 gap-y-6 pb-5" novalidate data-testid="arrival-filters" @submit.prevent>
      <FormField float-hint :hint="$weekday(shownDate)" class="w-44" :label="t('frontDesk.page.arrival')">
        <template #default="{ id }"><Input :id="id" v-model="shownDate" name="date" type="date" /></template>
      </FormField>
      <FormField class="w-44" :label="t('frontDesk.arrivals.status')">
        <template #default="{ id }">
          <NativeSelect :id="id" v-model="status" name="status">
            <option v-for="s in statuses" :key="s" :value="s">{{ t(`status.${s}`) }}</option>
          </NativeSelect>
        </template>
      </FormField>
      <FormField class="w-64" :label="t('frontDesk.page.search')">
        <template #default="{ id }"><Input :id="id" v-model="q" name="q" type="search" :placeholder="t('frontDesk.page.searchPlaceholder')" /></template>
      </FormField>
      <FormField v-if="types.length" class="w-44" :label="t('frontDesk.page.roomType')">
        <template #default="{ id }">
          <NativeSelect :id="id" v-model="typeId" name="room_type_id">
            <option value="">{{ t('dataTable.all') }}</option>
            <option v-for="rt in types" :key="rt.id" :value="String(rt.id)">{{ rt.code }}</option>
          </NativeSelect>
        </template>
      </FormField>
      <Button v-if="!isDefault" type="button" variant="outline" size="sm" class="mb-1" data-testid="clear-filters" @click="clearFilters">{{ t('dataTable.clearFilters') }}</Button>
    </form>

    <!-- The rooms of the arrivals that have none, put in one go: a proposal is shown first and saved when the person confirms. -->
    <div v-if="autoLines.length" class="mb-3 flex flex-wrap items-center justify-between gap-2 rounded-md border border-border bg-muted/40 px-3 py-2 text-sm" data-testid="auto-bar">
      <span data-testid="without-room">{{ t('frontDesk.arrivals.withoutRoom', { n: autoLines.length }, autoLines.length) }}</span>
      <Button type="button" size="sm" variant="outline" data-testid="auto-assign-open" @click="autoOpen = true">{{ t('autoAssign.button') }}</Button>
    </div>

    <DataTable
      v-if="!error || loaded"
      class="hidden md:block"
      :columns="columns"
      :rows="rows"
      row-key="reservation_room_id"
      :loading="!loaded"
      :row-test-id="(a) => `arrival-${a.reservation_room_id}`"
      :caption="t('frontDesk.page.arrivals')"
    >
      <template #cell-room_number="{ row }">
        <template v-if="row.room_number">
          <div class="font-semibold"><RoomLink :room-id="row.room_id" :number="row.room_number" /></div>
          <StatusBadge v-if="row.housekeeping_status" domain="housekeeping" :status="row.housekeeping_status" />
        </template>
        <span v-else class="text-muted-foreground" :data-testid="`no-room-${row.reservation_room_id}`">{{ t('frontDesk.arrivals.noRoom') }}</span>
        <div class="text-xs text-muted-foreground" :title="row.room_type_name">{{ row.room_type_code }}</div>
        <div v-if="canAssign(row) && !row.room_id" class="mt-1" @click.stop>
          <RoomPicker
            :target="assignTarget(row)" :type-id="row.room_type_id" :type-code="row.room_type_code" :arrival="row.arrival_date" :departure="row.departure_date"
            :requested-bed-type-id="row.requested_bed_type_id ?? null" :test-id="`pick-room-${row.reservation_room_id}`" @assigned="(_r, room) => roomAssigned(room)" @failed="assignFailed"
          />
        </div>
        <div v-if="changing === row.reservation_room_id" class="relative" @click.stop>
          <RoomPicker
            :open="true" bare :target="assignTarget(row)" :type-id="row.room_type_id" :type-code="row.room_type_code" :arrival="row.arrival_date" :departure="row.departure_date"
            :requested-bed-type-id="row.requested_bed_type_id ?? null" :current-room-id="row.room_id ?? null" @update:open="(v: boolean) => { if (!v) changing = null }"
            @assigned="(_r, room) => roomAssigned(room)" @failed="assignFailed"
          />
        </div>
        <Badge
          v-if="row.requested_bed_type_code && row.room_bed_type_code"
          :variant="row.requested_bed_type_code === row.room_bed_type_code ? 'success' : 'warning'"
          :title="row.requested_bed_type_code === row.room_bed_type_code ? t('bedTypes.matches') : t('bedTypes.differs')"
          :data-testid="`bed-match-${row.reservation_room_id}`"
        >{{ row.room_bed_type_code }}</Badge>
      </template>
      <template #cell-guest_name="{ row }">
        <button type="button" class="cursor-pointer border-0 bg-transparent p-0 text-left font-medium text-primary hover:underline" :data-testid="`detail-${row.reservation_room_id}`" @click="openDetail(row)">{{ row.guest_name || '—' }}</button>
        <Badge v-if="row.requested_bed_type_code" variant="outline" class="ml-1.5" :data-testid="`bed-${row.reservation_room_id}`">{{ t('bedTypes.bed') }}: {{ row.requested_bed_type_code }}<template v-if="row.bed_locked"> · {{ t('bedTypes.kept') }}</template></Badge>
        <div class="text-xs text-muted-foreground"><RouterLink :to="`/reservations/${row.reservation_id}`">{{ row.confirmation_number }}</RouterLink></div>
      </template>
      <template #cell-company="{ row }">
        <span v-if="row.company" :data-testid="`company-${row.reservation_room_id}`">{{ row.company.name }}</span>
        <span v-else class="text-muted-foreground" :data-testid="`company-${row.reservation_room_id}`">{{ t('frontDesk.inHouse.noCompany') }}</span>
      </template>
      <template #cell-rate="{ row }">
        <div :data-testid="`rate-${row.reservation_room_id}`">{{ row.rate.amount ? $money(row.rate.amount) : '—' }}</div>
        <div class="text-xs text-muted-foreground" :title="row.rate.rate_plan_name">{{ row.rate.rate_plan_code || '—' }}</div>
      </template>
      <template #cell-stay="{ row }">
        <div class="whitespace-nowrap">{{ $date(row.arrival_date) }} → {{ $date(row.departure_date) }}</div>
        <div class="text-xs text-muted-foreground">{{ row.adult_count }}+{{ row.child_count }}</div>
      </template>
      <template #cell-state="{ row }">
        <StatusBadge domain="reservation" :status="lineUiStatus(row.status)" />
        <div class="mt-0.5"><ReadinessBadge :readiness="row.readiness" /></div>
        <div class="mt-0.5 text-xs text-muted-foreground tabular-nums">
          {{ t('frontDesk.arrivals.deposit') }}
          <span v-if="row.deposit" :data-testid="`deposit-${row.reservation_room_id}`">{{ $money(row.deposit.paid) }}</span>
          <span v-else :data-testid="`deposit-${row.reservation_room_id}`">—</span>
        </div>
      </template>
      <template #cell-actions="{ row }">
        <div class="flex flex-wrap items-center justify-end gap-1.5">
          <Button v-for="a in primary(row)" :key="a" type="button" size="sm" :variant="a === 'checkIn' ? checkInVariant(row) : 'outline'" :data-testid="a === 'checkIn' ? `open-${row.reservation_room_id}` : `${a}-${row.reservation_room_id}`" @click="act(a, row)">
            {{ t(`frontDesk.arrivals.action.${a}`) }}
          </Button>
          <Popover v-if="more(row).length || (canAssign(row) && row.room_id)" :open="menuFor === row.reservation_room_id" @update:open="(v: boolean) => (menuFor = v ? row.reservation_room_id : null)">
            <PopoverTrigger as-child>
              <Button type="button" size="sm" variant="ghost" :aria-label="t('frontDesk.inHouse.moreActions')" :data-testid="`more-${row.reservation_room_id}`"><MoreVertical /></Button>
            </PopoverTrigger>
            <PopoverContent class="w-48 p-1">
              <div :data-testid="`menu-${row.reservation_room_id}`">
                <Button v-if="canAssign(row) && row.room_id" type="button" variant="ghost" size="sm" class="w-full justify-start" :data-testid="`change-room-${row.reservation_room_id}`" @click="startChange(row)">{{ t('roomPicker.change') }}</Button>
                <Button v-for="a in more(row)" :key="a" type="button" variant="ghost" size="sm" class="w-full justify-start" :data-testid="`${a}-${row.reservation_room_id}`" @click="act(a, row)">{{ t(`frontDesk.arrivals.action.${a}`) }}</Button>
              </div>
            </PopoverContent>
          </Popover>
        </div>
      </template>
      <template #empty>
        <EmptyState :description="isDefault ? t('emptyState.arrivals') : t('emptyState.noMatch')" :action-label="isDefault ? '' : t('dataTable.clearFilters')" action-variant="outline" @action="clearFilters" :title="isDefault ? t('frontDesk.page.noArrivals') : t('frontDesk.arrivals.noMatch')" data-testid="empty" />
      </template>
    </DataTable>
    <EmptyState v-else :description="t('emptyState.loadFailed')" :title="t('frontDesk.page.couldNotLoad')" data-testid="not-loaded" />

    <!-- A narrow screen gets a card for each arrival instead of the table. -->
    <ul v-if="!error || loaded" class="m-0 grid list-none gap-3 p-0 md:hidden" data-testid="arrival-cards">
      <li v-for="a in rows" :key="a.reservation_room_id" class="rounded-lg border border-border p-3" :data-testid="`card-${a.reservation_room_id}`">
        <div class="flex items-start justify-between gap-2">
          <div>
            <button type="button" class="cursor-pointer border-0 bg-transparent p-0 text-left font-medium text-primary" @click="openDetail(a)">{{ a.guest_name || '—' }}</button>
            <div class="text-sm text-muted-foreground">{{ a.confirmation_number }} · {{ a.room_number || t('frontDesk.arrivals.noRoom') }} · {{ a.room_type_code }}</div>
          </div>
          <ReadinessBadge :readiness="a.readiness" />
        </div>
        <div class="mt-1 text-sm">{{ $date(a.arrival_date) }} → {{ $date(a.departure_date) }}</div>
        <div v-if="a.company" class="text-sm text-muted-foreground">{{ a.company.name }}</div>
        <div class="mt-2 flex flex-wrap gap-1.5">
          <RoomPicker
            v-if="canAssign(a) && !a.room_id" :target="assignTarget(a)" :type-id="a.room_type_id" :type-code="a.room_type_code" :arrival="a.arrival_date" :departure="a.departure_date"
            :requested-bed-type-id="a.requested_bed_type_id ?? null" :test-id="`card-pick-room-${a.reservation_room_id}`" @assigned="(_r, room) => roomAssigned(room)" @failed="assignFailed"
          />
          <Button v-for="x in primary(a)" :key="x" type="button" size="sm" :variant="x === 'checkIn' ? checkInVariant(a) : 'outline'" @click="act(x, a)">{{ t(`frontDesk.arrivals.action.${x}`) }}</Button>
        </div>
        <details v-if="more(a).length || (canAssign(a) && a.room_id)" class="mt-2">
          <summary class="cursor-pointer text-sm text-muted-foreground">{{ t('frontDesk.inHouse.moreActions') }}</summary>
          <div class="mt-2 flex flex-wrap gap-1.5">
            <Button v-if="canAssign(a) && a.room_id" type="button" size="sm" variant="outline" :data-testid="`card-change-room-${a.reservation_room_id}`" @click="startChange(a)">{{ t('roomPicker.change') }}</Button>
            <Button v-for="x in more(a)" :key="x" type="button" size="sm" variant="outline" @click="act(x, a)">{{ t(`frontDesk.arrivals.action.${x}`) }}</Button>
          </div>
        </details>
      </li>
    </ul>

    <AutoAssignDialog v-model:open="autoOpen" :lines="autoLines" @changed="load" />
    <ArrivalDrawer v-model:open="drawerOpen" :row="drawerRow" @act="act" />
    <GuestEditDialog v-model:open="guestOpen" :guest-id="guestId" @saved="guestSaved" />
    <CheckInSheet :arrival="checkInFor" :queue="rows" @close="checkInFor = null" @done="checkedIn" />
  </template>
</template>
