<script setup lang="ts">
import { computed, reactive, ref } from 'vue'
import { api } from '@/api/client'
import { fetchAll } from '@/api/paging'
import { ApiError } from '@/api/problem'
import type { FreeRoom, Guest, RoomType, StayDetail } from '@/api/types'
import FormField from '@/components/app/FormField.vue'
import { Button } from '@/components/ui/button'
import { Card, CardContent } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { NativeSelect } from '@/components/ui/native-select'
import { t } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import { addDays } from '@/utils/dates'
import { guestLabel } from '@/utils/reservations'

const props = defineProps<{ detail: StayDetail }>()
const emit = defineEmits<{ changed: [message: string] }>()
const auth = useAuthStore()
const property = usePropertyStore()

type Dialog = '' | 'move' | 'departure' | 'guest'
const open = ref<Dialog>('')
const busy = ref(false)
const error = ref<ApiError | null>(null)

const pid = computed(() => property.currentId)
const businessDate = computed(() => property.clock?.business_date ?? '')
const isOpen = computed(() => props.detail.stay.status === 'OPEN')
const canMove = computed(() => isOpen.value && auth.can('frontdesk.room_move', pid.value))
const canChange = computed(() => isOpen.value && auth.can('reservation.update', pid.value))
const canAddGuest = computed(() => isOpen.value && auth.can('frontdesk.checkin', pid.value))
const canOverride = computed(() => auth.can('frontdesk.checkin_unready_room', pid.value))
const requiresInspection = computed(() => property.current?.require_room_inspection_for_checkin ?? false)
const fieldError = (field: string) => error.value?.fieldMessage(field)

// The target room must be free from the business date to the departure (at least one night).
const from = computed(() => businessDate.value)
const until = computed(() => (props.detail.stay.departure_date > from.value ? props.detail.stay.departure_date : addDays(from.value, 1)))

function show(d: Dialog): void {
  error.value = null
  open.value = d
  if (d === 'move') void loadTypes()
}

function fail(e: unknown): void {
  error.value = e instanceof ApiError ? e : null
}

// Room move.
const types = ref<RoomType[]>([])
const rooms = ref<FreeRoom[]>([])
const move = reactive({ typeId: 0, roomId: null as number | null, reason: '', override: false, overrideReason: '' })
const selected = computed(() => rooms.value.find((r) => r.room_id === move.roomId))
const isReady = (s: string) => (requiresInspection.value ? s === 'INSPECTED' : s === 'CLEAN' || s === 'INSPECTED')
const notReady = computed(() => !!selected.value && !isReady(selected.value.housekeeping_status))

async function loadTypes(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null) return
  try {
    types.value = (await fetchAll((cursor) => api.GET('/api/v1/properties/{propertyId}/room-types', { params: { path: { propertyId }, query: { limit: 200, cursor } } }))).filter((x) => x.is_active)
    move.typeId ||= types.value[0]?.id ?? 0
    await loadRooms()
  } catch (e) {
    fail(e)
  }
}

async function loadRooms(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null || !move.typeId) return
  try {
    const { data } = await api.GET('/api/v1/properties/{propertyId}/availability/rooms', {
      params: { path: { propertyId }, query: { room_type_id: move.typeId, arrival: from.value, departure: until.value } },
    })
    rooms.value = data?.data ?? []
    if (!rooms.value.some((r) => r.room_id === move.roomId)) move.roomId = rooms.value[0]?.room_id ?? null
  } catch (e) {
    fail(e)
  }
}

async function submitMove(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null || move.roomId === null) return
  busy.value = true
  error.value = null
  try {
    const { data } = await api.POST('/api/v1/properties/{propertyId}/stays/{id}/move', {
      params: { path: { propertyId, id: props.detail.stay.id } },
      body: {
        version: props.detail.stay.version, room_id: move.roomId, reason: move.reason,
        override_room_not_ready: move.override, override_reason: move.override ? move.overrideReason : undefined,
      },
    })
    open.value = ''
    emit('changed', t('stayActions.moved', { room: data?.new_segment.room_number ?? '' }))
  } catch (e) {
    fail(e)
    if (e instanceof ApiError && e.code === 'VERSION_CONFLICT') emit('changed', '')
  } finally {
    busy.value = false
  }
}

// Departure.
const departure = ref('')
const departureChanged = computed(() => !!departure.value && departure.value !== props.detail.stay.departure_date)

function showDeparture(): void {
  departure.value = props.detail.stay.departure_date
  show('departure')
}

async function submitDeparture(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null) return
  busy.value = true
  error.value = null
  try {
    await api.POST('/api/v1/properties/{propertyId}/stays/{id}/change-departure', {
      params: { path: { propertyId, id: props.detail.stay.id } }, body: { version: props.detail.stay.version, departure_date: departure.value },
    })
    open.value = ''
    emit('changed', t('stayActions.departureNow', { date: departure.value }))
  } catch (e) {
    fail(e)
    if (e instanceof ApiError && e.code === 'VERSION_CONFLICT') emit('changed', '')
  } finally {
    busy.value = false
  }
}

// Accompanying guest.
const guestQuery = ref('')
const guestResults = ref<Guest[]>([])

async function findGuests(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null || !guestQuery.value.trim()) return
  try {
    const { data } = await api.GET('/api/v1/guests', { params: { query: { q: guestQuery.value.trim(), property_id: propertyId, limit: 10 } } })
    guestResults.value = data?.data ?? []
  } catch (e) {
    fail(e)
  }
}

async function addGuest(g: Guest): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null) return
  busy.value = true
  error.value = null
  try {
    await api.POST('/api/v1/properties/{propertyId}/stays/{id}/guests', {
      params: { path: { propertyId, id: props.detail.stay.id } }, body: { guest_id: g.id },
    })
    open.value = ''
    guestResults.value = []
    guestQuery.value = ''
    emit('changed', t('stayActions.guestAdded', { name: guestLabel(g) }))
  } catch (e) {
    fail(e)
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <Card v-if="canMove || canChange || canAddGuest" class="mb-4" data-testid="stay-actions">
    <CardContent class="pt-4">
      <div class="flex flex-wrap gap-2">
        <Button v-if="canMove" type="button" variant="outline" data-testid="open-move" @click="show('move')">{{ t('stayActions.moveRoom') }}</Button>
        <Button v-if="canChange" type="button" variant="outline" data-testid="open-departure" @click="showDeparture">{{ t('stayActions.extend') }}</Button>
        <Button v-if="canAddGuest" type="button" variant="outline" data-testid="open-guest" @click="show('guest')">{{ t('stayActions.addGuest') }}</Button>
      </div>
      <p v-if="error" class="alert mt-3" role="alert" data-testid="action-error">
        {{ error.message }} <code>{{ error.code }}</code>
        <template v-if="error.code === 'ROOM_NOT_AVAILABLE_FOR_EXTENSION'"> {{ t('stayActions.extensionHint') }}</template>
      </p>

      <form v-if="open === 'move'" novalidate class="mt-4 border-t border-border pt-4" data-testid="move-form" @submit.prevent="submitMove">
        <h2 class="mb-3 mt-0 text-base font-semibold">{{ t('stayActions.moveTitle') }}</h2>
        <div class="grid gap-4 sm:grid-cols-3">
          <FormField :label="t('stayActions.roomType')">
            <template #default="{ id }">
              <NativeSelect :id="id" v-model.number="move.typeId" name="room_type" @change="loadRooms">
                <option v-for="rt in types" :key="rt.id" :value="rt.id">{{ rt.code }} · {{ rt.name }}</option>
              </NativeSelect>
            </template>
          </FormField>
          <FormField :label="t('stayActions.room')" :error="fieldError('room_id')">
            <template #default="{ id, invalid }">
              <NativeSelect :id="id" v-model.number="move.roomId" name="room" :disabled="!rooms.length" :aria-invalid="invalid">
                <option v-for="r in rooms" :key="r.room_id" :value="r.room_id">{{ r.room_number }} · {{ r.housekeeping_status }}{{ isReady(r.housekeeping_status) ? '' : t('stayActions.notReadyTag') }}</option>
              </NativeSelect>
              <small v-if="!rooms.length" class="text-xs text-muted-foreground" data-testid="no-rooms">{{ t('stayActions.noRooms', { date: until }) }}</small>
            </template>
          </FormField>
          <FormField :label="t('stayActions.reason')" :error="fieldError('reason')">
            <template #default="{ id, invalid }"><Input :id="id" v-model="move.reason" name="reason" maxlength="500" :aria-invalid="invalid" /></template>
          </FormField>
        </div>
        <div v-if="notReady" class="alert warning mt-3" data-testid="not-ready">
          {{ t('stayActions.roomIs', { room: selected?.room_number ?? '', status: selected?.housekeeping_status ?? '' }) }}
          <template v-if="canOverride">
            <label class="mt-2 flex items-center gap-2 text-sm"><input v-model="move.override" type="checkbox" name="override" class="size-4 accent-primary" /><span>{{ t('stayActions.useAnyway') }}</span></label>
            <FormField v-if="move.override" class="mt-2 max-w-md" :label="t('stayActions.reason')">
              <template #default="{ id }"><Input :id="id" v-model="move.overrideReason" name="override_reason" maxlength="500" /></template>
            </FormField>
          </template>
        </div>
        <div class="mt-4 flex justify-end gap-2">
          <Button type="button" variant="outline" @click="open = ''">{{ t('common.cancel') }}</Button>
          <Button type="submit" :disabled="busy || move.roomId === null || !move.reason.trim()">{{ t('stayActions.move') }}</Button>
        </div>
      </form>

      <form v-if="open === 'departure'" novalidate class="mt-4 border-t border-border pt-4" data-testid="departure-form" @submit.prevent="submitDeparture">
        <h2 class="mb-3 mt-0 text-base font-semibold">{{ t('stayActions.departureTitle') }}</h2>
        <FormField class="max-w-xs" :label="t('stayActions.departure')" :error="fieldError('departure_date')">
          <template #default="{ id, invalid }"><Input :id="id" v-model="departure" name="departure" type="date" :min="addDays(businessDate, 1)" :aria-invalid="invalid" /></template>
        </FormField>
        <p class="mb-0 mt-2 text-sm text-muted-foreground">{{ t('stayActions.departureHint') }}</p>
        <div class="mt-4 flex justify-end gap-2">
          <Button type="button" variant="outline" @click="open = ''">{{ t('common.cancel') }}</Button>
          <Button type="submit" :disabled="busy || !departureChanged">{{ t('common.save') }}</Button>
        </div>
      </form>

      <div v-if="open === 'guest'" class="mt-4 border-t border-border pt-4" data-testid="guest-form">
        <h2 class="mb-3 mt-0 text-base font-semibold">{{ t('stayActions.guestTitle') }}</h2>
        <div class="flex flex-wrap items-end gap-3">
          <FormField class="min-w-64 flex-1" :label="t('stayActions.findGuest')">
            <template #default="{ id }"><Input :id="id" v-model="guestQuery" name="guest_q" type="search" :placeholder="t('stayActions.findPlaceholder')" @keydown.enter.prevent="findGuests" /></template>
          </FormField>
          <Button type="button" variant="outline" data-testid="find-guest" @click="findGuests">{{ t('stayActions.find') }}</Button>
          <Button type="button" variant="outline" @click="open = ''">{{ t('common.cancel') }}</Button>
        </div>
        <ul v-if="guestResults.length" class="m-0 mt-2 flex list-none flex-wrap gap-2 p-0">
          <li v-for="g in guestResults" :key="g.id"><Button type="button" variant="outline" size="sm" :disabled="busy" :data-testid="`guest-${g.code}`" @click="addGuest(g)">{{ g.code }} · {{ guestLabel(g) }}</Button></li>
        </ul>
      </div>
    </CardContent>
  </Card>
</template>
