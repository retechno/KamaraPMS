<script setup lang="ts">
import { Printer } from 'lucide-vue-next'
import { computed, reactive, ref, watch } from 'vue'
import { RouterLink } from 'vue-router'
import { api } from '@/api/client'
import { fetchAll } from '@/api/paging'
import { ApiError } from '@/api/problem'
import type { BedType, CancelResult, FreeRoom, Reservation, ReservationRoom, RoomType } from '@/api/types'
import FormField from '@/components/app/FormField.vue'
import PageHeader from '@/components/app/PageHeader.vue'
import StatusBadge from '@/components/app/StatusBadge.vue'
import ReservationEmails from '@/components/ReservationEmails.vue'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Combobox } from '@/components/ui/combobox'
import { NativeSelect } from '@/components/ui/native-select'
import { t } from '@/i18n'
import { documentPath, openPdf } from '@/utils/documents'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import { guestLabel, newIdempotencyKey } from '@/utils/reservations'

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
// The bed a line is being changed to, by line (until it is saved).
const bedPick = reactive<Record<number, number>>({})
const assigning = ref<{ lineId: number; typeId: number; rooms: FreeRoom[]; roomId: number | null } | null>(null)
const header = reactive({ source: 'PHONE', remarks: '' })
const deposit = reactive({ amount: '', method: 'CASH' as 'CASH' | 'CARD' | 'BANK_TRANSFER' | 'OTHER', reference: '' })
let depositKey = newIdempotencyKey() // kept while a request may have been lost, renewed once the server has answered

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
    if (data) adopt(data)
    types.value = await fetchAll((cursor) => api.GET('/api/v1/properties/{propertyId}/room-types', { params: { path: { propertyId }, query: { limit: 200, cursor } } }))
    if (can('reservation.update')) beds.value = (await api.GET('/api/v1/properties/{propertyId}/bed-types', { params: { path: { propertyId }, query: { active: true } } })).data?.data ?? []
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  }
}

/** Runs one action; the reservation in the answer replaces the one on screen. A version conflict reloads it. */
async function run(action: () => Promise<{ data?: Reservation | CancelResult }>): Promise<void> {
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
  } catch (e) {
    const failure = e instanceof ApiError ? e : null
    error.value = failure
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

const confirm = () => run(() => api.POST('/api/v1/properties/{propertyId}/reservations/{id}/confirm', { params: base(), body: { version: version() } }))
const reinstate = () => run(() => api.POST('/api/v1/properties/{propertyId}/reservations/{id}/reinstate', { params: base(), body: { version: version() } }))
const saveHeader = () => run(() => api.PATCH('/api/v1/properties/{propertyId}/reservations/{id}', {
  params: base(), body: { version: version(), source: header.source as Reservation['source'], remarks: header.remarks },
}))
const saveBed = (line: ReservationRoom) => run(() => api.PATCH('/api/v1/properties/{propertyId}/reservations/{id}/rooms/{lineId}', {
  params: lineParams(line.id), body: { version: version(), bed_type_id: bedPick[line.id] ?? line.bed_type_id ?? 0 },
}))
const bedChanged = (line: ReservationRoom) => (bedPick[line.id] ?? line.bed_type_id ?? 0) !== (line.bed_type_id ?? 0)
const unassign = (lineId: number) => run(() => api.POST('/api/v1/properties/{propertyId}/reservations/{id}/rooms/{lineId}/unassign-room', { params: lineParams(lineId), body: { version: version() } }))

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

const typeCode = (id: number) => types.value.find((t) => t.id === id)?.code ?? String(id)
const activeTypes = computed(() => types.value.filter((t) => t.is_active))
const canNoShow = (l: ReservationRoom) => can('nightaudit.no_show') && l.status === 'CONFIRMED' && l.arrival_date <= businessDate.value

watch(() => [pid.value, props.id], () => void load(), { immediate: true })
</script>

<template>
  <PageHeader :title="`${t('reservation.title')}${res ? ` ${res.confirmation_number}` : ''}`">
    <template v-if="res" #marks>
      <StatusBadge domain="reservation" :status="res.display_status" data-testid="status" />
    </template>
    <template #actions>
      <template v-if="res">
        <Button v-if="status === 'DRAFT' && can('reservation.create')" size="sm" :disabled="busy" data-testid="confirm" @click="confirm">{{ t('reservation.confirm') }}</Button>
        <Button v-if="status === 'CANCELLED' && can('reservation.reinstate')" size="sm" :disabled="busy" data-testid="reinstate" @click="reinstate">{{ t('reservation.reinstate') }}</Button>
        <Button v-if="status !== 'DRAFT' && status !== 'CANCELLED'" variant="outline" size="sm" data-testid="print-confirmation" @click="printConfirmation"><Printer />{{ t('reservation.confirmationPdf') }}</Button>
        <Button v-if="status !== 'CANCELLED' && can('reservation.cancel')" variant="outline" size="sm" class="text-destructive" :disabled="busy" data-testid="cancel" @click="ask('cancel')">{{ t('reservation.cancelReservation') }}</Button>
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
          <StatusBadge domain="reservation" :status="line.status" :data-testid="`line-status-${line.id}`" />
          <span class="text-sm">{{ $date(line.arrival_date) }} &rarr; {{ $date(line.departure_date) }} ({{ t('reservation.nights', { n: line.nights }, line.nights) }})</span>
          <Badge v-if="line.bed_type_code" variant="outline" :data-testid="`bed-${line.id}`">{{ t('bedTypes.bed') }}: {{ line.bed_type_name || line.bed_type_code }}</Badge>
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
            <Button v-if="canNoShow(line)" variant="outline" size="sm" :disabled="busy" :data-testid="`no-show-${line.id}`" @click="ask('no-show', line.id)">{{ t('reservation.noShow') }}</Button>
            <Button v-if="(line.status === 'DRAFT' || line.status === 'CONFIRMED') && can('reservation.cancel') && status !== 'CANCELLED'" variant="outline" size="sm" class="text-destructive" :disabled="busy" :data-testid="`cancel-room-${line.id}`" @click="ask('cancel-room', line.id)">{{ t('reservation.cancelRoom') }}</Button>
          </div>

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
            <Button type="submit" variant="outline" size="sm" :disabled="busy || !bedChanged(line)" :data-testid="`save-bed-${line.id}`">{{ t('common.save') }}</Button>
          </form>

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
          <p v-if="res.cancellation_reason" class="m-0 text-muted-foreground">{{ t('reservation.cancelledReason', { reason: res.cancellation_reason }) }}</p>
          <div class="mt-1 border-t border-border pt-2">
            <p class="m-0 text-xs text-muted-foreground">{{ t('reservation.estimateTotal') }}</p>
            <p class="m-0 text-xl font-semibold tabular-nums tracking-tight"><span data-testid="estimate">{{ $money(estimateTotal) }}</span></p>
          </div>
        </CardContent>
      </Card>

      <ReservationEmails v-if="status !== 'DRAFT'" :reservation-id="res.id" :confirmed="status === 'CONFIRMED'" />

      <Card v-if="res.folios.length" data-testid="folios">
        <CardHeader><CardTitle>{{ t('reservation.folios') }}</CardTitle></CardHeader>
        <CardContent>
          <ul class="m-0 list-none p-0 text-sm">
            <li v-for="f in res.folios" :key="f.id" class="flex items-center gap-2 py-1">
              <RouterLink :to="`/folios/${f.id}`" :data-testid="`folio-link-${f.id}`">{{ f.folio_number }}</RouterLink>
              <span class="text-muted-foreground">· {{ t('reservation.folioLine', { status: f.status, balance: $money(f.balance) }) }}</span>
            </li>
          </ul>
        </CardContent>
      </Card>

      <Card v-if="(status === 'DRAFT' || status === 'CONFIRMED') && can('payment.post')">
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
</template>
