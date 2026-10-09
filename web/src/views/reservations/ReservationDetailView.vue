<script setup lang="ts">
import { Printer } from 'lucide-vue-next'
import { computed, reactive, ref, watch } from 'vue'
import { RouterLink } from 'vue-router'
import { api } from '@/api/client'
import { fetchAll } from '@/api/paging'
import { ApiError } from '@/api/problem'
import type { Approval, AuditLog, BedType, CancelResult, FreeRoom, GuestView, Reservation, ReservationRoom, RoomType } from '@/api/types'
import ApprovalDialog from '@/components/ApprovalDialog.vue'
import BillingInstructions from '@/components/BillingInstructions.vue'
import EditRateDialog from '@/components/EditRateDialog.vue'
import GuestEditDialog from '@/components/GuestEditDialog.vue'
import ReservationEditDialog from '@/components/ReservationEditDialog.vue'
import FormField from '@/components/app/FormField.vue'
import PageHeader from '@/components/app/PageHeader.vue'
import StatusBadge from '@/components/app/StatusBadge.vue'
import RateOverrideSection, { type OverrideNight, type RateChange } from '@/components/RateOverrideSection.vue'
import RestrictionOverride from '@/components/RestrictionOverride.vue'
import ReservationEmails from '@/components/ReservationEmails.vue'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Combobox } from '@/components/ui/combobox'
import { NativeSelect } from '@/components/ui/native-select'
import { useReservationLookups } from '@/composables/useReservationLookups'
import { t, te } from '@/i18n'
import { documentPath, openPdf } from '@/utils/documents'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import { guestLabel, newIdempotencyKey } from '@/utils/reservations'
import { type ReservationAction, reservationActions } from '@/utils/reservationActions'
import { lineUiStatus, uiStatus } from '@/utils/reservationStatus'
import { refusalOf, type RestrictionOverrideInput, type RestrictionRefusal } from '@/utils/restrictions'

const props = defineProps<{ id: string }>()
const auth = useAuthStore()
const property = usePropertyStore()

const res = ref<Reservation | null>(null)
const error = ref<ApiError | null>(null)
const notice = ref('')
const busy = ref(false)
const types = ref<RoomType[]>([])
// The action that is waiting for a reason, and the room-assignment picker.
const asking = ref<{ kind: 'cancel' | 'cancel-room' | 'no-show'; lineId?: number } | null>(null)
const reason = ref('')
const beds = ref<BedType[]>([])
// A sale the sales restrictions refused, and how to send it again with an override.
const restriction = ref<{ refusal: RestrictionRefusal; again: (o: RestrictionOverrideInput) => Promise<void> } | null>(null)
// The bed a line is being changed to, by line (until it is saved).
const bedPick = reactive<Record<number, number>>({})
// Whether the bed is kept, by line (until it is saved).
const lockPick = reactive<Record<number, boolean>>({})
const assigning = ref<{ lineId: number; typeId: number; rooms: FreeRoom[]; roomId: number | null } | null>(null)
const header = reactive({ source: 'PHONE', remarks: '' })
const deposit = reactive({ amount: '', method: 'CASH' as 'CASH' | 'CARD' | 'BANK_TRANSFER' | 'OTHER', reference: '' })
let depositKey = newIdempotencyKey() // kept while a request may have been lost, renewed once the server has answered

const { ratePlans, companies } = useReservationLookups()
// The editors this page opens: the guest and the reservation (dialogs), and the rate of a stay that is in house.
const guestOpen = ref(false)
const editOpen = ref(false)
const rateOpen = ref(false)
const rateLineId = ref<number | null>(null)
// The guest's contact data and the history of the reservation are read apart: a person who may not read them sees why, not an empty list.
const guestView = ref<GuestView | null>(null)
const history = ref<AuditLog[]>([])
const historyState = ref<'idle' | 'loading' | 'failed'>('idle')

const pid = computed(() => property.currentId)
const can = (p: string) => auth.can(p, pid.value)
const businessDate = computed(() => property.clock?.business_date ?? '')
const status = computed(() => res.value?.status ?? '')

async function printConfirmation(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null || !res.value) return
  error.value = null
  try {
    await openPdf(documentPath.confirmation(propertyId, res.value.id))
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  }
}
const fieldError = (field: string) => error.value?.fieldMessage(field)
const estimateTotal = computed(() => (res.value?.rooms ?? []).filter((r) => r.status !== 'CANCELLED').reduce((sum, r) => sum + Number(r.estimate.total), 0))

function adopt(r: Reservation): void {
  res.value = r
  header.source = r.source
  header.remarks = r.remarks ?? ''
}

async function load(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null || !can('reservation.read')) return
  error.value = null
  try {
    const { data } = await api.GET('/api/v1/properties/{propertyId}/reservations/{id}', { params: { path: { propertyId, id: Number(props.id) } } })
    if (data) {
      adopt(data)
      void loadSide(data)
    }
    types.value = await fetchAll((cursor) => api.GET('/api/v1/properties/{propertyId}/room-types', { params: { path: { propertyId }, query: { limit: 200, cursor } } }))
    if (can('reservation.update')) beds.value = (await api.GET('/api/v1/properties/{propertyId}/bed-types', { params: { path: { propertyId }, query: { active: true } } })).data?.data ?? []
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  }
}

/** The guest's contact data and the audit entries of the reservation, each only for a person who may read them. */
async function loadSide(r: Reservation): Promise<void> {
  guestView.value = null
  if (r.guest && can('guest.read')) {
    try {
      guestView.value = (await api.GET('/api/v1/guests/{id}', { params: { path: { id: r.guest.id } } })).data ?? null
    } catch {
      guestView.value = null // the contact data is optional
    }
  }
  history.value = []
  await loadHistory(r.id)
}

/** The audit entries of the reservation; read again after every action, so the history shows what was just done. */
async function loadHistory(reservationId: number): Promise<void> {
  const propertyId = pid.value
  historyState.value = 'idle'
  if (propertyId === null || !can('audit.read')) return
  historyState.value = 'loading'
  try {
    history.value = (await api.GET('/api/v1/properties/{propertyId}/audit-logs', { params: { path: { propertyId }, query: { entity_type: 'reservation', entity_id: reservationId, limit: 50 } } })).data?.data ?? []
    historyState.value = 'idle'
  } catch {
    historyState.value = 'failed'
  }
}

/** Runs one action; the reservation in the answer replaces the one on screen. A version conflict reloads it. */
async function run(action: () => Promise<{ data?: Reservation | CancelResult }>, again?: (o: RestrictionOverrideInput) => Promise<void>): Promise<void> {
  busy.value = true
  error.value = null
  notice.value = ''
  try {
    const { data } = await action()
    if (data && 'reservation' in data) {
      adopt(data.reservation)
      if (data.requires_folio_resolution) notice.value = t('reservation.foliosHold', { balance: data.folio_balance })
    } else if (data) {
      adopt(data)
    }
    asking.value = null
    assigning.value = null
    reason.value = ''
    restriction.value = null
    if (res.value) void loadHistory(res.value.id)
  } catch (e) {
    const failure = e instanceof ApiError ? e : null
    error.value = failure
    const refusal = refusalOf(failure)
    if (refusal && again) {
      restriction.value = { refusal, again }
      editOpen.value = false
    }
    if (failure?.code === 'VERSION_CONFLICT') {
      await load() // show the current state, and keep the message that explains why the action did not happen
      error.value = failure
    }
  } finally {
    busy.value = false
  }
}

const base = () => ({ path: { propertyId: pid.value as number, id: Number(props.id) } })
const lineParams = (lineId: number) => ({ path: { ...base().path, lineId } })
const version = () => res.value?.version ?? 0

// Confirming and reinstating sell the nights again, so the sales restrictions are asked: a refusal is shown with its rules and the sale can go past them with an override.
const confirm = (o?: RestrictionOverrideInput): Promise<void> => run(() => api.POST('/api/v1/properties/{propertyId}/reservations/{id}/confirm', { params: base(), body: { version: version(), restriction_override: o } }), confirm)
const reinstate = (o?: RestrictionOverrideInput): Promise<void> => run(() => api.POST('/api/v1/properties/{propertyId}/reservations/{id}/reinstate', { params: base(), body: { version: version(), restriction_override: o } }), reinstate)
const saveHeader = () => run(() => api.PATCH('/api/v1/properties/{propertyId}/reservations/{id}', {
  params: base(), body: { version: version(), source: header.source as Reservation['source'], remarks: header.remarks },
}))
const saveBed = (line: ReservationRoom) => run(() => api.PATCH('/api/v1/properties/{propertyId}/reservations/{id}/rooms/{lineId}', {
  params: lineParams(line.id), body: { version: version(), bed_type_id: bedPick[line.id] ?? line.bed_type_id ?? 0, bed_locked: lockOf(line) },
}))
// The price changes being prepared, by line, and the line whose change waits for an approver.
const rates = reactive<Record<number, RateChange>>({})
const approvingRate = ref<number | null>(null)
const rateOf = (lineId: number): RateChange => rates[lineId] ?? { overrides: [], reason: '' }
const canChangeRate = (line: ReservationRoom): boolean =>
  can('reservation.override_rate') && can('reservation.update') && (line.status === 'DRAFT' || line.status === 'CONFIRMED') && line.occupancy_kind === 'PAID'
const nightsOf = (line: ReservationRoom): OverrideNight[] =>
  line.nightly_rates.map((n) => ({ date: n.date, standard: n.grid_rate ?? n.base_rate ?? n.amount, current: n.amount }))

/** The button of a line: a change that the person cannot approve asks for an approver first. */
function askRate(line: ReservationRoom): void {
  if (!can('reservation.override_rate_approve')) {
    error.value = null
    approvingRate.value = line.id
    return
  }
  void saveRate(line)
}

async function saveRate(line: ReservationRoom, approval?: Approval): Promise<void> {
  const change = rateOf(line.id)
  await run(() => api.PATCH('/api/v1/properties/{propertyId}/reservations/{id}/rooms/{lineId}', {
    params: lineParams(line.id),
    body: { version: version(), nightly_overrides: change.overrides, rate_override_reason: change.reason, rate_override_approval: approval },
  }))
  if (!error.value) {
    delete rates[line.id]
    approvingRate.value = null
  }
}

const reasonPick = reactive<Record<number, string>>({})
const saveReason = (line: ReservationRoom) => run(() => api.PATCH('/api/v1/properties/{propertyId}/reservations/{id}/rooms/{lineId}', {
  params: lineParams(line.id), body: { version: version(), occupancy_reason: reasonPick[line.id] ?? line.occupancy_reason ?? '' },
}))
const reasonChanged = (line: ReservationRoom) => (reasonPick[line.id] ?? line.occupancy_reason ?? '') !== (line.occupancy_reason ?? '')
/** Whether the line keeps its bed as the form stands: never without a bed. */
const lockOf = (line: ReservationRoom): boolean => (bedPick[line.id] ?? line.bed_type_id ?? 0) > 0 && (lockPick[line.id] ?? line.bed_locked)
const bedChanged = (line: ReservationRoom) => (bedPick[line.id] ?? line.bed_type_id ?? 0) !== (line.bed_type_id ?? 0) || lockOf(line) !== line.bed_locked
const unassign = (lineId: number) => run(() => api.POST('/api/v1/properties/{propertyId}/reservations/{id}/rooms/{lineId}/unassign-room', { params: lineParams(lineId), body: { version: version() } }))

// A manual fee (audit F-08): the person gives the amount and the reason; nothing is calculated and nothing is posted by the cancellation or the no-show itself.
const feeFor = ref<{ type: 'CANCEL_FEE' | 'NO_SHOW_FEE'; lineId?: number } | null>(null)
const fee = reactive({ amount: '', reason: '' })
function askFee(type: 'CANCEL_FEE' | 'NO_SHOW_FEE', lineId?: number): void {
  feeFor.value = { type, lineId }
  fee.amount = ''
  fee.reason = ''
  asking.value = null
  error.value = null
}
async function postFee(): Promise<void> {
  const f = feeFor.value
  if (!f || pid.value === null) return
  busy.value = true
  error.value = null
  notice.value = ''
  try {
    const { data } = await api.POST('/api/v1/properties/{propertyId}/reservations/{id}/fees', {
      params: base(),
      body: { type: f.type, reservation_room_id: f.type === 'NO_SHOW_FEE' ? f.lineId : null, amount: fee.amount.trim(), reason: fee.reason.trim() },
    })
    notice.value = t('reservation.feePosted', { folio: data?.folio_number ?? '', amount: data?.item.debit ?? '' })
    feeFor.value = null
    await load()
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    busy.value = false
  }
}

function ask(kind: 'cancel' | 'cancel-room' | 'no-show', lineId?: number): void {
  asking.value = { kind, lineId }
  reason.value = ''
  error.value = null
}

function submitReason(): Promise<void> {
  const a = asking.value
  if (!a) return Promise.resolve()
  const body = { version: version(), reason: reason.value.trim() }
  if (a.kind === 'cancel') return run(() => api.POST('/api/v1/properties/{propertyId}/reservations/{id}/cancel', { params: base(), body }))
  if (a.kind === 'cancel-room') return run(() => api.POST('/api/v1/properties/{propertyId}/reservations/{id}/rooms/{lineId}/cancel', { params: lineParams(a.lineId as number), body }))
  return run(() => api.POST('/api/v1/properties/{propertyId}/reservations/{id}/rooms/{lineId}/no-show', { params: lineParams(a.lineId as number), body }))
}

/** A free room as the picker lists it: its number, its housekeeping status and its bed, with a mark when it is the bed asked for. */
function roomLabel(r: FreeRoom, line: ReservationRoom): string {
  const bed = r.bed_type_name ? ` · ${r.bed_type_name}` : ''
  const mark = line.bed_type_id && r.bed_type_id === line.bed_type_id ? ` ✓ ${t('bedTypes.matches')}` : ''
  return `${r.room_number} · ${r.housekeeping_status}${bed}${mark}`
}

async function startAssign(line: ReservationRoom): Promise<void> {
  assigning.value = { lineId: line.id, typeId: line.room_type_id, rooms: [], roomId: null }
  await loadFree()
}

async function loadFree(): Promise<void> {
  const a = assigning.value
  const r = res.value
  const propertyId = pid.value
  if (!a || !r || propertyId === null) return
  const line = r.rooms.find((l) => l.id === a.lineId)
  if (!line) return
  try {
    const { data } = await api.GET('/api/v1/properties/{propertyId}/availability/rooms', {
      params: { path: { propertyId }, query: { room_type_id: a.typeId, arrival: line.arrival_date < businessDate.value ? businessDate.value : line.arrival_date, departure: line.departure_date } },
    })
    // The rooms that have the bed the guest asked for come first, and the first of them is the one proposed.
    const rooms = data?.data ?? []
    const wanted = line.bed_type_id ?? null
    a.rooms = wanted === null ? rooms : [...rooms.filter((r) => r.bed_type_id === wanted), ...rooms.filter((r) => r.bed_type_id !== wanted)]
    a.roomId = a.rooms[0]?.room_id ?? null
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  }
}

function submitAssign(): Promise<void> {
  const a = assigning.value
  const line = res.value?.rooms.find((l) => l.id === a?.lineId)
  if (!a || !line || a.roomId === null) return Promise.resolve()
  const upgrade = a.typeId !== line.room_type_id
  return run(() => api.POST('/api/v1/properties/{propertyId}/reservations/{id}/rooms/{lineId}/assign-room', {
    params: lineParams(a.lineId), body: { version: version(), room_id: a.roomId as number, upgrade },
  }))
}

async function takeDeposit(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null) return
  busy.value = true
  error.value = null
  notice.value = ''
  try {
    const { data } = await api.POST('/api/v1/properties/{propertyId}/reservations/{id}/deposits', {
      params: { path: { propertyId, id: Number(props.id) }, header: { 'Idempotency-Key': depositKey } },
      body: { amount: deposit.amount, payment_method: deposit.method, reference_number: deposit.reference || undefined },
    })
    depositKey = newIdempotencyKey()
    deposit.amount = ''
    deposit.reference = ''
    notice.value = data ? t('reservation.depositTaken', { number: data.payment.payment_number, balance: data.folio_balance }) : ''
    await load()
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
    if (e instanceof ApiError) depositKey = newIdempotencyKey()
  } finally {
    busy.value = false
  }
}

/** The rooms of the header: a room number with its type, or the type and how many rooms still wait for a room ("STD ×2 · not assigned"). */
const roomsText = computed(() => {
  const parts: string[] = []
  const waiting = new Map<string, number>()
  for (const l of activeRooms.value) {
    if (l.room_number) parts.push(`${l.room_number} · ${l.room_type_code}`)
    else waiting.set(l.room_type_code, (waiting.get(l.room_type_code) ?? 0) + 1)
  }
  for (const [type, n] of waiting) parts.push(`${type}${n > 1 ? ` ×${n}` : ''} · ${t('reservation.noRoom')}`)
  return parts.length ? parts.join(', ') : '—'
})
const historyText = (action: string): string => (te(`reservation.historyAction.${action.replace('reservation.', '')}`) ? t(`reservation.historyAction.${action.replace('reservation.', '')}` as never) : action)
const stayLine = computed(() => res.value?.rooms.find((l) => l.status === 'CHECKED_IN' && l.stay_id != null))
const activeRooms = computed(() => (res.value?.rooms ?? []).filter((l) => l.status !== 'CANCELLED'))
const actions = computed<ReservationAction[]>(() => {
  const r = res.value
  if (!r) return []
  return reservationActions(
    {
      displayStatus: r.display_status, rooms: r.rooms.map((l) => ({ status: l.status, arrivalDate: l.arrival_date })), hasStay: r.rooms.some((l) => l.stay_id != null),
      hasFolio: r.folios.length > 0, hasGuest: !!r.guest,
    },
    { can, businessDate: businessDate.value },
  )
})
const has = (a: ReservationAction) => actions.value.includes(a)
const firstFolio = computed(() => res.value?.folios.find((f) => f.folio_type === 'GUEST') ?? res.value?.folios[0])
const rateTarget = computed(() => {
  const l = res.value?.rooms.find((x) => x.id === rateLineId.value)
  if (!l || l.stay_id == null || !res.value) return null
  return { id: l.stay_id, guest: { name: res.value.guest ? guestLabel(res.value.guest) : '—' }, room: { number: l.room_number ?? '', room_type_code: l.room_type_code }, rate: { rate_plan_code: l.rate_plan_code } }
})
function askRateEdit(line: ReservationRoom): void {
  rateLineId.value = line.id
  rateOpen.value = true
}
const guestSaved = (): Promise<void> => load()
const saveLineFields = (lineId: number, body: Record<string, unknown>) =>
  run(() => api.PATCH('/api/v1/properties/{propertyId}/reservations/{id}/rooms/{lineId}', { params: lineParams(lineId), body: { version: version(), ...body } as never }))
const saveHeaderFields = (body: Record<string, unknown>) =>
  run(() => api.PATCH('/api/v1/properties/{propertyId}/reservations/{id}', { params: base(), body: { version: version(), ...body } as never }))
/** Money and ledger totals come from the folios as the server states them; a reservation without a folio says so. */
const hasFolios = computed(() => (res.value?.folios.length ?? 0) > 0)
const typeCode = (id: number) => types.value.find((t) => t.id === id)?.code ?? String(id)
const activeTypes = computed(() => types.value.filter((t) => t.is_active))
const canNoShow = (l: ReservationRoom) => can('nightaudit.no_show') && l.status === 'CONFIRMED' && l.arrival_date <= businessDate.value

watch(() => [pid.value, props.id], () => void load(), { immediate: true })
</script>

<template>
  <PageHeader :title="`${t('reservation.title')}${res ? ` ${res.confirmation_number}` : ''}`">
    <template v-if="res" #marks>
      <StatusBadge domain="reservation" :status="String(uiStatus(res.display_status))" data-testid="status" />
    </template>
    <template #actions>
      <template v-if="res">
        <Button v-if="has('edit')" variant="outline" size="sm" :disabled="busy" data-testid="edit-reservation" @click="editOpen = true">{{ t('reservation.editReservation') }}</Button>
        <Button v-if="has('editGuest')" variant="outline" size="sm" data-testid="edit-guest" @click="guestOpen = true">{{ t('reservation.editGuest') }}</Button>
        <Button v-if="has('editRate') && stayLine" variant="outline" size="sm" data-testid="edit-rate" @click="askRateEdit(stayLine)">{{ t('reservation.editRate') }}</Button>
        <Button v-if="has('confirm')" size="sm" :disabled="busy" data-testid="confirm" @click="confirm()">{{ t('reservation.confirm') }}</Button>
        <Button v-if="has('checkIn')" as-child size="sm" data-testid="check-in"><RouterLink :to="`/arrivals?q=${encodeURIComponent(res.confirmation_number)}`">{{ t('reservation.checkIn') }}</RouterLink></Button>
        <Button v-if="has('viewStay') && stayLine" as-child variant="outline" size="sm" data-testid="view-stay"><RouterLink :to="`/stays/${stayLine.stay_id}`">{{ t('reservation.viewStay') }}</RouterLink></Button>
        <Button v-if="has('checkOut') && stayLine" as-child size="sm" data-testid="check-out"><RouterLink :to="`/stays/${stayLine.stay_id}?action=checkout`">{{ t('reservation.checkOut') }}</RouterLink></Button>
        <Button v-if="has('folio') && firstFolio" as-child variant="outline" size="sm" data-testid="open-folio"><RouterLink :to="`/folios/${firstFolio.id}`">{{ t('reservation.openFolio') }}</RouterLink></Button>
        <Button v-if="has('payment') && firstFolio" as-child variant="outline" size="sm" data-testid="open-payment"><RouterLink :to="`/folios/${firstFolio.id}?tab=payment`">{{ t('reservation.payment') }}</RouterLink></Button>
        <Button v-if="status === 'CANCELLED' && can('reservation.reinstate')" size="sm" :disabled="busy" data-testid="reinstate" @click="reinstate()">{{ t('reservation.reinstate') }}</Button>
        <Button v-if="status === 'CANCELLED' && can('folio.post_charge')" variant="outline" size="sm" :disabled="busy" data-testid="post-cancel-fee" @click="askFee('CANCEL_FEE')">{{ t('reservation.postCancelFee') }}</Button>
        <Button v-if="status !== 'DRAFT' && status !== 'CANCELLED'" variant="outline" size="sm" data-testid="print-confirmation" @click="printConfirmation"><Printer />{{ t('reservation.confirmationPdf') }}</Button>
        <Button v-if="has('cancel')" variant="outline" size="sm" class="text-destructive" :disabled="busy" data-testid="cancel" @click="ask('cancel')">{{ t('reservation.cancelReservation') }}</Button>
      </template>
      <Button as-child variant="ghost" size="sm"><RouterLink to="/reservations">{{ t('reservation.list') }}</RouterLink></Button>
    </template>
  </PageHeader>

  <p v-if="error" class="alert" role="alert" data-testid="form-error">{{ error.message }} <code>{{ error.code }}</code></p>
  <p v-if="notice" class="alert warning" role="status" data-testid="notice">{{ notice }}</p>
  <p v-if="pid === null" class="muted">{{ t('reservations.selectProperty') }}</p>
  <p v-else-if="!can('reservation.read')" class="muted" data-testid="no-access">{{ t('reservations.noAccess') }}</p>

  <div v-else-if="res" class="grid items-start gap-4 lg:grid-cols-[minmax(0,1fr)_22rem]">
    <div class="min-w-0">
      <Card class="mb-4" data-testid="header-card">
        <CardContent class="grid gap-x-6 gap-y-2 p-4 text-sm sm:grid-cols-2 lg:grid-cols-4">
          <div><div class="text-xs uppercase text-muted-foreground">{{ t('reservation.guest') }}</div><div class="font-medium" data-testid="header-guest">{{ res.guest ? guestLabel(res.guest) : '—' }}</div></div>
          <div><div class="text-xs uppercase text-muted-foreground">{{ t('reservations.confirmation') }}</div><div class="font-medium">{{ res.confirmation_number }}</div></div>
          <div><div class="text-xs uppercase text-muted-foreground">{{ t('reservation.stayDates') }}</div><div>{{ $date(res.arrival_date) }} &rarr; {{ $date(res.departure_date) }}</div></div>
          <div>
            <div class="text-xs uppercase text-muted-foreground">{{ t('reservations.roomCol') }}</div>
            <div data-testid="header-rooms">{{ roomsText }}</div>
          </div>
          <p v-if="res.display_status === 'DRAFT'" class="m-0 text-xs text-muted-foreground sm:col-span-2 lg:col-span-4" data-testid="draft-note">{{ t('reservation.draftNote') }}</p>
        </CardContent>
      </Card>

      <Card v-if="res.guest" class="mb-4" data-testid="guest-card">
        <CardHeader class="flex-row items-center justify-between gap-2 pb-2">
          <CardTitle>{{ t('reservation.guest') }}</CardTitle>
          <Button v-if="can('guest.write')" variant="outline" size="sm" data-testid="edit-guest-card" @click="guestOpen = true">{{ t('reservation.editGuest') }}</Button>
        </CardHeader>
        <CardContent class="grid gap-x-6 gap-y-1 text-sm sm:grid-cols-[minmax(0,1.3fr)_minmax(0,1.3fr)_minmax(0,1fr)]">
          <div class="min-w-0 break-words"><RouterLink :to="`/guests/${res.guest.id}`" data-testid="guest-link">{{ guestLabel(res.guest) }}</RouterLink> <span class="text-muted-foreground">{{ res.guest.code }}</span></div>
          <div class="min-w-0 break-all" data-testid="guest-email">{{ can('guest.read') ? (guestView?.email || '—') : t('reservation.contactHidden') }}</div>
          <div class="min-w-0 break-words" data-testid="guest-phone">{{ can('guest.read') ? (guestView?.phone || '—') : '' }}</div>
        </CardContent>
      </Card>

      <RestrictionOverride v-if="restriction" :violations="restriction.refusal.violations" :overridable="restriction.refusal.overridable" :busy="busy" :error="error"
        @override="(o) => restriction?.again(o)" @cancel="restriction = null" />

      <Card v-if="feeFor" class="mb-4 border-primary/50">
        <form class="flex flex-wrap items-end gap-3 p-4" novalidate data-testid="fee-form" @submit.prevent="postFee">
          <p class="m-0 w-full text-sm font-medium">{{ feeFor.type === 'CANCEL_FEE' ? t('reservation.postCancelFee') : t('reservation.postNoShowFee') }}</p>
          <FormField class="w-44" :label="t('reservation.feeAmount')" :error="fieldError('amount')">
            <template #default="{ id, invalid }"><Input :id="id" v-model="fee.amount" name="fee_amount" inputmode="decimal" :aria-invalid="invalid" /></template>
          </FormField>
          <FormField class="min-w-56 flex-1" :label="t('reservation.reason')" :error="fieldError('reason')">
            <template #default="{ id, invalid }"><Input :id="id" v-model="fee.reason" name="fee_reason" maxlength="500" :aria-invalid="invalid" /></template>
          </FormField>
          <Button type="submit" :disabled="busy || !fee.amount.trim() || !fee.reason.trim()">{{ t('reservation.postFee') }}</Button>
          <Button type="button" variant="outline" @click="feeFor = null">{{ t('common.cancel') }}</Button>
          <p class="m-0 w-full text-xs text-muted-foreground">{{ t('reservation.feeHint') }}</p>
        </form>
      </Card>

      <Card v-if="asking" class="mb-4 border-primary/50">
        <form class="flex flex-wrap items-end gap-3 p-4" novalidate data-testid="reason-form" @submit.prevent="submitReason">
          <FormField class="min-w-56 flex-1" :label="asking.kind === 'no-show' ? t('reservation.reasonOptional') : t('reservation.reason')" :error="fieldError('reason')">
            <template #default="{ id, invalid }"><Input :id="id" v-model="reason" name="reason" maxlength="500" :aria-invalid="invalid" /></template>
          </FormField>
          <Button type="submit" :variant="asking.kind === 'no-show' ? 'default' : 'destructive'" :disabled="busy">{{ asking.kind === 'no-show' ? t('reservation.markNoShow') : t('common.cancel') }}</Button>
          <Button type="button" variant="outline" @click="asking = null">{{ t('reservation.keep') }}</Button>
        </form>
      </Card>

      <Card v-for="line in res.rooms" :key="line.id" class="mb-4" :data-testid="`room-${line.id}`">
        <CardHeader class="flex-row flex-wrap items-center gap-x-3 gap-y-1 pb-2">
          <CardTitle>{{ line.room_type_code }}</CardTitle>
          <span v-if="line.room_number" class="text-sm font-medium" :data-testid="`room-number-${line.id}`">{{ t('reservation.roomNumber', { number: line.room_number }) }}</span>
          <span v-else class="text-sm text-muted-foreground">{{ t('reservation.noRoom') }}</span>
          <StatusBadge domain="reservation" :status="lineUiStatus(line.status)" :data-testid="`line-status-${line.id}`" />
          <span class="text-sm">{{ $date(line.arrival_date) }} &rarr; {{ $date(line.departure_date) }} ({{ t('reservation.nights', { n: line.nights }, line.nights) }})</span>
          <Badge v-if="line.bed_type_code" variant="outline" :data-testid="`bed-${line.id}`">{{ t('bedTypes.bed') }}: {{ line.bed_type_name || line.bed_type_code }}<template v-if="line.bed_locked"> · {{ t('bedTypes.kept') }}</template></Badge>
          <Badge v-if="line.occupancy_kind !== 'PAID'" variant="warning" :data-testid="`kind-${line.id}`">{{ t(`occupancy.kind_${line.occupancy_kind}`) }}</Badge>
          <span class="text-sm text-muted-foreground">{{ t('reservation.party', { adults: line.adult_count, children: line.child_count, plan: line.rate_plan_code }) }}</span>
          <span v-if="line.stay_id" class="text-sm text-muted-foreground">{{ t('reservation.stay', { id: line.stay_id }) }}</span>
        </CardHeader>
        <CardContent>
          <table class="w-full border-collapse text-[13px]" :data-testid="`nights-${line.id}`">
            <thead>
              <tr class="text-xs uppercase tracking-wide text-muted-foreground">
                <th class="py-1 text-left font-semibold">{{ t('reservation.night') }}</th>
                <th class="py-1 text-right font-semibold">{{ t('reservation.amount') }}</th>
                <th class="py-1 text-right font-semibold">{{ t('reservation.grid') }}</th>
                <th />
              </tr>
            </thead>
            <tbody>
              <tr v-for="n in line.nightly_rates" :key="n.date" class="border-t border-border">
                <td class="py-1">{{ $date(n.date) }}</td>
                <td class="py-1 text-right tabular-nums">{{ $money(n.amount) }}</td>
                <td class="py-1 text-right tabular-nums text-muted-foreground">{{ $money(n.grid_rate ?? n.base_rate) || '—' }}</td>
                <td class="py-1 pl-3">
                  <small v-if="n.is_override" class="text-muted-foreground">{{ t('reservation.override') }}</small>
                  <small v-if="n.yield_rules?.length" class="text-muted-foreground" :data-testid="`yield-${n.date}`" :title="t('reservation.yieldTitle')">{{ t('reservation.yieldLine', { codes: n.yield_rules.join(', ') }) }}</small>
                </td>
              </tr>
            </tbody>
            <tfoot>
              <tr class="border-t border-border font-medium">
                <td class="py-1.5">{{ t('reservation.estimateLine', { plan: line.rate_plan_code }) }}</td>
                <td class="py-1.5 text-right tabular-nums" :data-testid="`line-estimate-${line.id}`">{{ $money(line.estimate.total) }}</td>
                <td colspan="2" class="py-1.5 pl-3 text-xs font-normal text-muted-foreground">{{ t('reservation.estimateBreak', { net: $money(line.estimate.net), service: $money(line.estimate.service), tax: $money(line.estimate.tax) }) }}</td>
              </tr>
            </tfoot>
          </table>

          <div class="mt-3 flex flex-wrap gap-2">
            <Button v-if="line.status === 'CONFIRMED' && can('reservation.update') && !line.room_number" variant="outline" size="sm" :disabled="busy" :data-testid="`assign-${line.id}`" @click="startAssign(line)">{{ t('reservation.assignRoom') }}</Button>
            <Button v-if="line.status === 'CONFIRMED' && can('reservation.update') && line.room_number" variant="outline" size="sm" :disabled="busy" :data-testid="`unassign-${line.id}`" @click="unassign(line.id)">{{ t('reservation.unassignRoom') }}</Button>
            <Button v-if="line.status === 'CHECKED_IN' && line.stay_id != null && can('frontdesk.rate_change')" variant="outline" size="sm" :disabled="busy" :data-testid="`edit-rate-${line.id}`" @click="askRateEdit(line)">{{ t('reservation.editRate') }}</Button>
            <Button v-if="canNoShow(line)" variant="outline" size="sm" :disabled="busy" :data-testid="`no-show-${line.id}`" @click="ask('no-show', line.id)">{{ t('reservation.noShow') }}</Button>
            <Button v-if="line.status === 'NO_SHOW' && can('folio.post_charge')" variant="outline" size="sm" :disabled="busy" :data-testid="`no-show-fee-${line.id}`" @click="askFee('NO_SHOW_FEE', line.id)">{{ t('reservation.postNoShowFee') }}</Button>
            <Button v-if="(line.status === 'DRAFT' || line.status === 'CONFIRMED') && can('reservation.cancel') && status !== 'CANCELLED'" variant="outline" size="sm" class="text-destructive" :disabled="busy" :data-testid="`cancel-room-${line.id}`" @click="ask('cancel-room', line.id)">{{ t('reservation.cancelRoom') }}</Button>
          </div>

          <form v-if="line.occupancy_kind !== 'PAID' && (line.status === 'DRAFT' || line.status === 'CONFIRMED') && can('reservation.update')" class="mt-3 flex flex-wrap items-end gap-3" novalidate :data-testid="`reason-form-${line.id}`" @submit.prevent="saveReason(line)">
            <FormField class="w-80" :label="t('occupancy.reason')">
              <template #default="{ id }">
                <Input :id="id" :model-value="reasonPick[line.id] ?? line.occupancy_reason ?? ''" :name="`occupancy_reason_${line.id}`" @update:model-value="(v) => (reasonPick[line.id] = String(v))" />
              </template>
            </FormField>
            <Button type="submit" variant="outline" size="sm" :disabled="busy || !reasonChanged(line)" :data-testid="`save-reason-${line.id}`">{{ t('common.save') }}</Button>
          </form>
          <p v-else-if="line.occupancy_reason" class="mb-0 mt-2 text-sm text-muted-foreground" :data-testid="`reason-${line.id}`">{{ t('occupancy.reason') }}: {{ line.occupancy_reason }}</p>

          <form v-if="beds.length && (line.status === 'DRAFT' || line.status === 'CONFIRMED') && can('reservation.update')" class="mt-3 flex flex-wrap items-end gap-3" novalidate :data-testid="`bed-form-${line.id}`" @submit.prevent="saveBed(line)">
            <FormField class="w-56" :label="t('bedTypes.requested')">
              <template #default="{ id }">
                <NativeSelect :id="id" :model-value="bedPick[line.id] ?? line.bed_type_id ?? 0" :name="`bed_${line.id}`" @update:model-value="(v) => (bedPick[line.id] = Number(v))">
                  <option :value="0">{{ t('bedTypes.noPreference') }}</option>
                  <option v-if="line.bed_type_id && !beds.some((b) => b.id === line.bed_type_id)" :value="line.bed_type_id">{{ line.bed_type_name }}</option>
                  <option v-for="b in beds" :key="b.id" :value="b.id">{{ b.name }}</option>
                </NativeSelect>
              </template>
            </FormField>
            <label v-if="(bedPick[line.id] ?? line.bed_type_id ?? 0) > 0" class="flex items-center gap-2 pb-2 text-sm">
              <input type="checkbox" class="size-4 accent-primary" :name="`bed_locked_${line.id}`" :checked="lockOf(line)" @change="(e) => (lockPick[line.id] = (e.target as HTMLInputElement).checked)" />
              <span>{{ t('bedTypes.keepBed') }}</span>
            </label>
            <Button type="submit" variant="outline" size="sm" :disabled="busy || !bedChanged(line)" :data-testid="`save-bed-${line.id}`">{{ t('common.save') }}</Button>
          </form>

          <div v-if="canChangeRate(line) && nightsOf(line).length" class="mt-3" :data-testid="`rate-${line.id}`">
            <RateOverrideSection :model-value="rateOf(line.id)" :nights="nightsOf(line)" @update:model-value="(v) => (rates[line.id] = v)" />
            <div v-if="rateOf(line.id).overrides.length" class="mt-2 flex justify-end">
              <Button type="button" size="sm" :disabled="busy || !rateOf(line.id).reason.trim()" :data-testid="`save-rate-${line.id}`" @click="askRate(line)">{{ t('rateOverride.save') }}</Button>
            </div>
          </div>

          <BillingInstructions v-if="['DRAFT', 'CONFIRMED', 'CHECKED_IN'].includes(line.status)" :reservation-id="res.id" :line-id="line.id" :editable="can('reservation.update')" />

          <form v-if="assigning && assigning.lineId === line.id" class="mt-3 flex flex-wrap items-end gap-3 rounded-lg border border-border bg-muted/40 p-3" novalidate :data-testid="`assign-form-${line.id}`" @submit.prevent="submitAssign">
            <FormField class="w-44" :label="t('reservation.roomType')">
              <template #default="{ id }">
                <NativeSelect :id="id" v-model.number="assigning.typeId" name="assign_type" @change="loadFree">
                  <option v-for="ty in activeTypes" :key="ty.id" :value="ty.id">{{ ty.code }} {{ ty.id === line.room_type_id ? t('reservation.booked') : t('reservation.upgrade') }}</option>
                </NativeSelect>
              </template>
            </FormField>
            <FormField class="w-52" :label="t('reservation.freeRoom')">
              <template #default="{ id }">
                <Combobox :id="id" v-model="assigning.roomId" name="assign_room" :disabled="!assigning.rooms.length" :options="[...assigning.rooms.map((r) => ({ value: r.room_id, label: roomLabel(r, line) }))]" />
                <small v-if="!assigning.rooms.length" class="text-xs text-muted-foreground" data-testid="no-free-rooms">{{ t('reservation.noFreeRooms', { type: typeCode(assigning.typeId) }) }}</small>
              </template>
            </FormField>
            <Button type="submit" size="sm" :disabled="busy || assigning.roomId === null">{{ t('reservation.assign') }}</Button>
            <Button type="button" variant="outline" size="sm" @click="assigning = null">{{ t('common.close') }}</Button>
          </form>
        </CardContent>
      </Card>
    </div>

    <aside class="flex min-w-0 flex-col gap-4">
      <Card data-testid="summary">
        <CardContent class="flex flex-col gap-2 p-5 text-sm">
          <p class="m-0 font-medium">{{ $date(res.arrival_date) }} &rarr; {{ $date(res.departure_date) }} <span class="font-normal text-muted-foreground">· {{ t('reservation.version', { n: res.version }) }}</span></p>
          <p class="m-0">
            {{ t('reservation.booker') }}:
            <RouterLink v-if="res.guest" :to="`/guests/${res.guest.id}`" data-testid="booker">{{ guestLabel(res.guest) }}</RouterLink>
            <span v-else class="text-muted-foreground" data-testid="no-booker">{{ t('reservation.notSet') }}</span>
          </p>
          <p class="m-0 text-muted-foreground">{{ t('reservation.source') }} {{ $money(res.source) }} · {{ t('reservation.bookedOn') }} {{ $date(res.reservation_date) }}</p>
          <p v-if="res.company_id || res.booking_group_id" class="m-0" data-testid="billing-links">
            <template v-if="res.company_id">
              {{ t('reservation.company') }}:
              <RouterLink v-if="can('cityledger.read')" :to="`/city-ledger/${res.company_id}`" data-testid="company-link">{{ res.company_name }}</RouterLink>
              <span v-else data-testid="company-link">{{ res.company_name }}</span>
            </template>
            <template v-if="res.booking_group_id">
              <template v-if="res.company_id"> · </template>{{ t('reservation.group') }}:
              <RouterLink :to="`/groups/${res.booking_group_id}`" data-testid="group-link">{{ res.group_code }}</RouterLink>
            </template>
          </p>
          <p v-if="res.company_id" class="m-0 text-xs text-muted-foreground" data-testid="payer-note">{{ t('reservation.payerNote') }}</p>
          <p v-if="res.cancellation_reason" class="m-0 text-muted-foreground">{{ t('reservation.cancelledReason', { reason: res.cancellation_reason }) }}</p>
          <div class="mt-1 border-t border-border pt-2">
            <p class="m-0 text-xs text-muted-foreground">{{ t('reservation.estimateTotal') }}</p>
            <p class="m-0 text-xl font-semibold tabular-nums tracking-tight"><span data-testid="estimate">{{ $money(estimateTotal) }}</span></p>
          </div>
        </CardContent>
      </Card>

      <Card data-testid="history">
        <CardHeader><CardTitle>{{ t('reservation.history') }}</CardTitle></CardHeader>
        <CardContent class="text-sm">
          <p v-if="res.special_request" class="m-0 mb-2" data-testid="special-request"><span class="text-muted-foreground">{{ t('reservation.specialRequest') }}:</span> {{ res.special_request }}</p>
          <p v-if="!can('audit.read')" class="m-0 text-muted-foreground" data-testid="history-denied">{{ t('reservation.historyDenied') }}</p>
          <p v-else-if="historyState === 'loading'" class="m-0 text-muted-foreground">{{ t('reservation.historyLoading') }}</p>
          <p v-else-if="historyState === 'failed'" class="m-0 text-destructive" data-testid="history-failed">{{ t('reservation.historyFailed') }}</p>
          <p v-else-if="!history.length" class="m-0 text-muted-foreground" data-testid="history-empty">{{ t('reservation.historyEmpty') }}</p>
          <ul v-else class="m-0 list-none p-0" data-testid="history-list">
            <li v-for="h in history" :key="h.id" class="flex flex-wrap justify-between gap-2 border-t border-border py-1 first:border-t-0">
              <span>{{ historyText(h.action) }}</span>
              <span class="text-xs text-muted-foreground">{{ $dateTime(h.created_at) }} · {{ h.user?.name ?? '—' }}</span>
            </li>
          </ul>
        </CardContent>
      </Card>

      <ReservationEmails v-if="status !== 'DRAFT'" :reservation-id="res.id" :confirmed="status === 'CONFIRMED'" />

      <Card data-testid="folios">
        <CardHeader><CardTitle>{{ t('reservation.paymentFolio') }}</CardTitle></CardHeader>
        <CardContent>
          <p v-if="!hasFolios" class="m-0 text-sm text-muted-foreground" data-testid="no-folio">{{ t('reservation.noFolio') }}</p>
          <ul v-else class="m-0 list-none p-0 text-sm">
            <li v-for="f in res.folios" :key="f.id" class="py-1.5">
              <div class="flex flex-wrap items-baseline justify-between gap-x-2">
                <RouterLink :to="`/folios/${f.id}`" :data-testid="`folio-link-${f.id}`">{{ f.folio_number }}</RouterLink>
                <span class="text-muted-foreground">{{ t('reservation.folioLine', { status: f.status, balance: $money(f.balance) }) }}</span>
              </div>
              <div v-if="f.bill_to_company_name" class="break-words text-xs text-muted-foreground" :data-testid="`folio-company-${f.id}`">{{ t('billingInstructions.folioFor', { company: f.bill_to_company_name }) }}</div>
            </li>
          </ul>
        </CardContent>
      </Card>

      <Card v-if="(res.display_status === 'DRAFT' || res.display_status === 'CONFIRMED') && can('payment.post')" data-testid="deposit-card">
        <form novalidate data-testid="deposit-form" @submit.prevent="takeDeposit">
          <CardHeader><CardTitle>{{ t('reservation.takeDeposit') }}</CardTitle></CardHeader>
          <CardContent class="flex flex-col gap-3">
            <FormField :label="t('reservation.amount')" :error="fieldError('amount')">
              <template #default="{ id, invalid }"><Input :id="id" v-model="deposit.amount" name="deposit_amount" inputmode="decimal" :aria-invalid="invalid" /></template>
            </FormField>
            <FormField :label="t('reservation.method')">
              <template #default="{ id }">
                <NativeSelect :id="id" v-model="deposit.method" name="deposit_method">
                  <option v-for="m in ['CASH', 'CARD', 'BANK_TRANSFER', 'OTHER']" :key="m" :value="m">{{ t(`cashier.${m}` as never) }}</option>
                </NativeSelect>
              </template>
            </FormField>
            <FormField :label="t('reservation.reference')">
              <template #default="{ id }"><Input :id="id" v-model="deposit.reference" name="deposit_reference" /></template>
            </FormField>
            <Button type="submit" :disabled="busy || !deposit.amount">{{ t('reservation.takeDepositButton') }}</Button>
          </CardContent>
        </form>
      </Card>

      <Card v-if="status !== 'CANCELLED' && can('reservation.update')">
        <form novalidate data-testid="header-form" @submit.prevent="saveHeader">
          <CardHeader><CardTitle>{{ t('reservation.details') }}</CardTitle></CardHeader>
          <CardContent class="flex flex-col gap-3">
            <FormField :label="t('reservation.source')">
              <template #default="{ id }">
                <NativeSelect :id="id" v-model="header.source" name="source">
                  <option v-for="s in ['WALK_IN', 'PHONE', 'EMAIL', 'WEBSITE', 'OTA', 'AGENT', 'OTHER']" :key="s" :value="s">{{ s }}</option>
                </NativeSelect>
              </template>
            </FormField>
            <FormField :label="t('reservation.remarks')">
              <template #default="{ id }"><Input :id="id" v-model="header.remarks" name="remarks" /></template>
            </FormField>
            <Button type="submit" variant="outline" :disabled="busy">{{ t('common.save') }}</Button>
          </CardContent>
        </form>
      </Card>
    </aside>
  </div>
  <GuestEditDialog v-if="res?.guest" v-model:open="guestOpen" :guest-id="res.guest.id" @saved="guestSaved" />
  <ReservationEditDialog v-if="res" v-model:open="editOpen" :reservation="res" :types="types" :rate-plans="ratePlans" :companies="companies" :busy="busy" :error="error"
    @save-line="saveLineFields" @save-header="saveHeaderFields" />
  <EditRateDialog v-model:open="rateOpen" :row="rateTarget" @saved="load" />
  <ApprovalDialog
    v-for="line in (res?.rooms ?? []).filter((l) => l.id === approvingRate)"
    :key="line.id"
    :title="t('rateOverride.approvalTitle')"
    :message="t('rateOverride.approvalMessage')"
    :busy="busy"
    :error="error"
    @approve="(a) => saveRate(line, a)"
    @cancel="approvingRate = null"
  />
</template>
