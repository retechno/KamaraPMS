<script setup lang="ts">
import { MoreVertical } from 'lucide-vue-next'
import { computed, ref, watch } from 'vue'
import { RouterLink, useRoute, useRouter } from 'vue-router'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { Arrival, CheckInResult, GuestView } from '@/api/types'
import ArrivalDrawer from '@/components/ArrivalDrawer.vue'
import DataTable, { type Column } from '@/components/app/DataTable.vue'
import EmptyState from '@/components/app/EmptyState.vue'
import FormField from '@/components/app/FormField.vue'
import StatusBadge from '@/components/app/StatusBadge.vue'
import GuestEditDialog from '@/components/GuestEditDialog.vue'
import ReadinessBadge from '@/components/ReadinessBadge.vue'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { NativeSelect } from '@/components/ui/native-select'
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover'
import { Sheet, SheetContent, SheetDescription, SheetTitle } from '@/components/ui/sheet'
import { toast } from '@/composables/useToast'
import { useRoomTypes } from '@/composables/useRoomTypes'
import { t } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import { type ArrivalAction, MORE_ARRIVAL_ACTIONS, PRIMARY_ARRIVAL_ACTIONS, arrivalActions, arrivalTarget } from '@/utils/arrivals'
import { fullName } from '@/utils/inHouse'
import CheckInPanel from './CheckInPanel.vue'

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
const open = ref<number | null>(null)
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
const isDefault = computed(() => !chosenDate.value && status.value === 'CONFIRMED' && !q.value.trim() && !typeId.value)

const canRead = computed(() => auth.can('reservation.read', property.currentId))
const can = (permission: string) => auth.can(permission, property.currentId)
const current = computed(() => rows.value.find((a) => a.reservation_room_id === open.value) ?? null)
const drawerRow = computed(() => rows.value.find((a) => a.reservation_room_id === drawerId.value) ?? null)
const sheetOpen = computed({
  get: () => open.value !== null,
  set: (v: boolean) => {
    if (!v) open.value = null
  },
})
const actionsOf = (a: Arrival) => arrivalActions(a, can)
const primary = (a: Arrival) => actionsOf(a).filter((x) => PRIMARY_ARRIVAL_ACTIONS.includes(x))
const more = (a: Arrival) => actionsOf(a).filter((x) => MORE_ARRIVAL_ACTIONS.includes(x))
const statuses = ['CONFIRMED', 'CHECKED_IN', 'CANCELLED', 'NO_SHOW']

const columns = computed<Column<Arrival>[]>(() => [
  { key: 'room_number', label: t('frontDesk.page.room'), sortable: true },
  { key: 'guest_name', label: t('frontDesk.page.guest'), sortable: true },
  { key: 'company', label: t('frontDesk.inHouse.company'), sortable: true, sortValue: (a) => a.company?.name },
  { key: 'rate', label: t('frontDesk.inHouse.rate'), align: 'right', sortable: true, sortValue: (a) => (a.rate.amount ? Number(a.rate.amount) : null), class: 'tabular-nums' },
  { key: 'stay', label: t('frontDesk.page.stay'), sortable: true, sortValue: (a) => a.departure_date },
  { key: 'state', label: t('frontDesk.arrivals.readinessTitle') },
  { key: 'deposit', label: t('frontDesk.arrivals.deposit'), align: 'right', sortable: true, sortValue: (a) => (a.deposit ? Number(a.deposit.paid) : null) },
  { key: 'actions', label: t('frontDesk.inHouse.actions'), align: 'right' },
])

async function load(): Promise<void> {
  const propertyId = property.currentId
  if (propertyId === null || !canRead.value || !date.value) return
  error.value = null
  try {
    const { data } = await api.GET('/api/v1/properties/{propertyId}/arrivals', {
      params: { path: { propertyId }, query: { date: chosenDate.value || undefined, status: status.value as 'CONFIRMED', q: q.value.trim() || undefined, room_type_id: typeId.value ? Number(typeId.value) : undefined } },
    })
    rows.value = data?.data ?? []
    loaded.value = true
    if (isDefault.value) emit('loaded', rows.value.length) // the count on the tab is the count of the default list
  } catch (e) {
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
  if (action === 'checkIn') {
    drawerOpen.value = false
    open.value = a.reservation_room_id
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
  open.value = null
  toast.success(t('frontDesk.arrivals.checkedIn', { stay: result.stay.stay_number, room: result.stay_room.room_number }))
  emit('changed')
  await load()
}

watch(() => [property.currentId, businessDate.value, chosenDate.value, status.value, q.value.trim(), typeId.value], () => {
  open.value = null
  drawerOpen.value = false
  restart()
}, { immediate: true })
watch(() => props.refresh, restart)
</script>

<template>
  <div v-if="error" class="alert" role="alert" data-testid="form-error">
    {{ error.message }} <code>{{ error.code }}</code>
    <Button type="button" variant="outline" size="sm" class="ml-2" data-testid="retry" @click="restart">{{ t('frontDesk.page.retry') }}</Button>
  </div>
  <p v-if="property.currentId === null" class="muted">{{ t('frontDesk.page.selectProperty') }}</p>
  <p v-else-if="!canRead" class="muted" data-testid="no-access">{{ t('frontDesk.page.noAccessArrivals') }}</p>

  <template v-else>
    <form class="mb-3 flex flex-wrap items-end gap-3" novalidate data-testid="arrival-filters" @submit.prevent>
      <FormField class="w-44" :label="t('frontDesk.page.arrival')">
        <template #default="{ id }"><Input :id="id" v-model="chosenDate" name="date" type="date" :placeholder="businessDate" /></template>
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
          <div class="font-semibold">{{ row.room_number }}</div>
          <StatusBadge v-if="row.housekeeping_status" domain="housekeeping" :status="row.housekeeping_status" />
        </template>
        <span v-else class="text-muted-foreground" :data-testid="`no-room-${row.reservation_room_id}`">{{ t('frontDesk.arrivals.noRoom') }}</span>
        <div class="text-xs text-muted-foreground" :title="row.room_type_name">{{ row.room_type_code }}</div>
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
        <StatusBadge domain="reservation" :status="row.status" />
        <div class="mt-0.5"><ReadinessBadge :readiness="row.readiness" /></div>
      </template>
      <template #cell-deposit="{ row }">
        <span v-if="row.deposit" :data-testid="`deposit-${row.reservation_room_id}`">{{ $money(row.deposit.paid) }}</span>
        <span v-else class="text-muted-foreground" :data-testid="`deposit-${row.reservation_room_id}`">—</span>
      </template>
      <template #cell-actions="{ row }">
        <div class="flex flex-wrap items-center justify-end gap-1.5">
          <Button v-for="a in primary(row)" :key="a" type="button" size="sm" :variant="a === 'checkIn' ? 'default' : 'outline'" :data-testid="a === 'checkIn' ? `open-${row.reservation_room_id}` : `${a}-${row.reservation_room_id}`" @click="act(a, row)">
            {{ t(`frontDesk.arrivals.action.${a}`) }}
          </Button>
          <Popover v-if="more(row).length">
            <PopoverTrigger as-child>
              <Button type="button" size="sm" variant="ghost" :aria-label="t('frontDesk.inHouse.moreActions')" :data-testid="`more-${row.reservation_room_id}`"><MoreVertical /></Button>
            </PopoverTrigger>
            <PopoverContent class="w-48 p-1">
              <div :data-testid="`menu-${row.reservation_room_id}`">
                <Button v-for="a in more(row)" :key="a" type="button" variant="ghost" size="sm" class="w-full justify-start" :data-testid="`${a}-${row.reservation_room_id}`" @click="act(a, row)">{{ t(`frontDesk.arrivals.action.${a}`) }}</Button>
              </div>
            </PopoverContent>
          </Popover>
        </div>
      </template>
      <template #empty>
        <EmptyState :title="isDefault ? t('frontDesk.page.noArrivals') : t('frontDesk.arrivals.noMatch')" data-testid="empty" />
      </template>
    </DataTable>
    <EmptyState v-else :title="t('frontDesk.page.couldNotLoad')" data-testid="not-loaded" />

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
          <Button v-for="x in actionsOf(a)" :key="x" type="button" size="sm" variant="outline" @click="act(x, a)">{{ t(`frontDesk.arrivals.action.${x}`) }}</Button>
        </div>
      </li>
    </ul>

    <ArrivalDrawer v-model:open="drawerOpen" :row="drawerRow" @act="act" />
    <GuestEditDialog v-model:open="guestOpen" :guest-id="guestId" @saved="guestSaved" />
    <Sheet v-model:open="sheetOpen">
      <SheetContent v-if="current" class="p-6" data-testid="checkin-sheet">
        <SheetTitle class="text-lg">
          {{ t('frontDesk.checkIn.title', { guest: current.guest_name || current.confirmation_number, type: current.room_type_code }) }}
        </SheetTitle>
        <SheetDescription class="mt-1">
          {{ t('frontDesk.checkIn.subtitle', { confirmation: current.confirmation_number, arrival: current.arrival_date, departure: current.departure_date }) }}
        </SheetDescription>
        <CheckInPanel :key="current.reservation_room_id" :arrival="current" class="mt-4" @done="checkedIn" @cancel="open = null" />
      </SheetContent>
    </Sheet>
  </template>
</template>
