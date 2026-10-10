<script setup lang="ts">
import { statusText } from '@/utils/status'
import { computed, nextTick, onMounted, reactive, ref } from 'vue'
import { api } from '@/api/client'
import { fetchAll } from '@/api/paging'
import { ApiError } from '@/api/problem'
import type { Arrival, CheckInResult, FreeRoom, RoomType } from '@/api/types'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Combobox } from '@/components/ui/combobox'
import { NativeSelect } from '@/components/ui/native-select'
import ErrorNotice from '@/components/app/ErrorNotice.vue'
import FormField from '@/components/app/FormField.vue'
import { vAutofocus } from '@/directives/autofocus'
import { focusProgrammatically } from '@/lib/focus'
import { t } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import { newIdempotencyKey } from '@/utils/reservations'

/** The check-in wizard for one arrival: pick a free room (with its housekeeping status), confirm the party, go. */
const props = defineProps<{ arrival: Arrival }>()
const emit = defineEmits<{ done: [result: CheckInResult]; cancel: [] }>()

const auth = useAuthStore()
const property = usePropertyStore()

const types = ref<RoomType[]>([])
const typeId = ref(props.arrival.room_type_id)
const rooms = ref<FreeRoom[]>([])
const roomId = ref<number | null>(props.arrival.room_id ?? null)
const form = reactive({ adults: props.arrival.adult_count, children: props.arrival.child_count, override: false, reason: '' })
const root = ref<HTMLFormElement | null>(null)
const busy = ref(false)
const error = ref<ApiError | null>(null)
let key = newIdempotencyKey() // kept while a request may have been lost, renewed once the server has answered

const pid = computed(() => property.currentId)
const requiresInspection = computed(() => property.current?.require_room_inspection_for_checkin ?? false)
const activeTypes = computed(() => types.value.filter((t) => t.is_active))
const selected = computed(() => rooms.value.find((r) => r.room_id === roomId.value))
/** A free room as the picker lists it: number, housekeeping status and bed (marked when it is the bed asked for), and a note when it is not ready. */
function roomLabel(r: { room_number: string; housekeeping_status: string; bed_type_id?: number | null; bed_type_name?: string }): string {
  const bed = r.bed_type_name ? ` · ${r.bed_type_name}` : ''
  const wanted = props.arrival.requested_bed_type_id
  const mark = wanted && r.bed_type_id === wanted ? ` ✓ ${t('bedTypes.matches')}` : ''
  return `${r.room_number} · ${statusText(r.housekeeping_status)}${bed}${mark}${isReady(r.housekeeping_status) ? '' : ` ${t('frontDesk.checkIn.notReadyTag')}`}`
}
const isReady = (status: string) => (requiresInspection.value ? status === 'INSPECTED' : status === 'CLEAN' || status === 'INSPECTED')
const notReady = computed(() => !!selected.value && !isReady(selected.value.housekeeping_status))
const canOverride = computed(() => auth.can('frontdesk.checkin_unready_room', pid.value))
const isUpgrade = computed(() => typeId.value !== props.arrival.room_type_id)
const fieldError = (field: string) => error.value?.fieldMessage(field)

async function loadRooms(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null) return
  try {
    const { data } = await api.GET('/api/v1/properties/{propertyId}/availability/rooms', {
      params: { path: { propertyId }, query: { room_type_id: typeId.value, arrival: props.arrival.arrival_date, departure: props.arrival.departure_date } },
    })
    const free = [...(data?.data ?? [])]
    // A room already assigned to this line is not "free" (the line holds it), so it is offered explicitly.
    if (props.arrival.room_id && typeId.value === props.arrival.room_type_id && !free.some((r) => r.room_id === props.arrival.room_id)) {
      free.unshift({ room_id: props.arrival.room_id, room_number: props.arrival.room_number ?? String(props.arrival.room_id), housekeeping_status: props.arrival.housekeeping_status ?? 'DIRTY' })
    }
    // The room already on the line first (it was chosen on purpose). Then the rooms that are ready, because a room that is not ready cannot be checked into without an override, and the one that
    // is proposed is the first of them; among those the rooms that have the bed the guest asked for come first. The rooms that are not ready follow, in the same order. The sort is stable, so the
    // rooms keep the order the server gave them (by number) otherwise.
    const wanted = props.arrival.requested_bed_type_id ?? null
    const rank = (r: { room_id: number; housekeeping_status: string; bed_type_id?: number | null }) => {
      if (r.room_id === props.arrival.room_id) return 0
      const bedFirst = wanted !== null && r.bed_type_id === wanted ? 0 : 1
      return (isReady(r.housekeeping_status) ? 1 : 3) + bedFirst
    }
    free.sort((a, b) => rank(a) - rank(b))
    rooms.value = free
    if (!free.some((r) => r.room_id === roomId.value)) roomId.value = free[0]?.room_id ?? null
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  }
}

onMounted(async () => {
  const propertyId = pid.value
  if (propertyId !== null) {
    try {
      types.value = await fetchAll((cursor) => api.GET('/api/v1/properties/{propertyId}/room-types', { params: { path: { propertyId }, query: { limit: 200, cursor } } }))
    } catch (e) {
      error.value = e instanceof ApiError ? e : null
    }
  }
  await loadRooms()
  await nextTick()
  // The cursor starts on the room, which is the choice of a check-in, but only once the rooms are there (a picker with nothing to pick is disabled). If the person has already gone to another field
  // while the rooms loaded, it stays where they put it.
  const active = document.activeElement
  const untouched = !active || active === document.body || (root.value?.contains(active) && active.matches('select[name=room_type]')) || active.matches('[role=dialog]')
  const target = root.value?.querySelector<HTMLElement>('[data-autofocus]:not([disabled])')
  if (rooms.value.length && untouched && target) focusProgrammatically(target)
})

async function submit(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null || roomId.value === null || props.arrival.guest_id === null) return
  busy.value = true
  error.value = null
  try {
    const { data } = await api.POST('/api/v1/properties/{propertyId}/reservations/{id}/rooms/{lineId}/check-in', {
      params: { path: { propertyId, id: props.arrival.reservation_id, lineId: props.arrival.reservation_room_id }, header: { 'Idempotency-Key': key } },
      body: {
        version: props.arrival.reservation_version, room_id: roomId.value, guest_id: props.arrival.guest_id, adult_count: form.adults, child_count: form.children,
        override_room_not_ready: form.override, override_reason: form.override ? form.reason : undefined,
      },
    })
    if (data) emit('done', data)
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
    if (e instanceof ApiError) key = newIdempotencyKey()
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <form ref="root" v-autofocus class="flex flex-col gap-4" novalidate :data-testid="`checkin-${arrival.reservation_room_id}`" @submit.prevent="submit">
    <ErrorNotice :error="error" data-testid="checkin-error" />
    <p v-if="arrival.guest_id === null" class="alert" data-testid="no-guest">{{ t('frontDesk.checkIn.noGuest') }}</p>

    <div class="grid gap-4 sm:grid-cols-2">
      <FormField :label="t('frontDesk.checkIn.roomType')">
        <template #default="{ id }">
          <NativeSelect :id="id" v-model.number="typeId" name="room_type" @change="loadRooms">
            <option v-for="ty in activeTypes" :key="ty.id" :value="ty.id">{{ ty.code }} {{ ty.id === arrival.room_type_id ? t('frontDesk.checkIn.booked') : t('frontDesk.checkIn.upgrade') }}</option>
          </NativeSelect>
        </template>
      </FormField>
      <FormField :label="t('frontDesk.checkIn.room')" :error="fieldError('room_id')">
        <template #default="{ id, invalid }">
          <Combobox :id="id" v-model="roomId" data-autofocus :open-on-focus="false" name="room" :disabled="!rooms.length" :aria-invalid="invalid" :options="rooms.map((r) => ({ value: r.room_id, label: roomLabel(r) }))" />
          <small v-if="!rooms.length" class="text-xs text-muted-foreground" data-testid="no-rooms">{{ t('frontDesk.checkIn.noFreeRoom') }}</small>
        </template>
      </FormField>
      <FormField :label="t('frontDesk.checkIn.adults')" :error="fieldError('adult_count')">
        <template #default="{ id, invalid }">
          <Input :id="id" v-model.number="form.adults" name="adults" type="number" min="1" :aria-invalid="invalid" />
        </template>
      </FormField>
      <FormField :label="t('frontDesk.checkIn.children')">
        <template #default="{ id }">
          <Input :id="id" v-model.number="form.children" name="children" type="number" min="0" />
        </template>
      </FormField>
    </div>
    <p v-if="isUpgrade" class="m-0 text-sm text-muted-foreground" data-testid="upgrade-note">{{ t('frontDesk.checkIn.upgradeNote') }}</p>

    <div v-if="notReady" class="alert warning m-0" data-testid="not-ready">
      {{ t('frontDesk.checkIn.notReady', { room: selected?.room_number ?? '', status: statusText(selected?.housekeeping_status), need: requiresInspection ? t('frontDesk.checkIn.needInspected') : t('frontDesk.checkIn.needClean') }) }}
      <template v-if="canOverride">
        <label class="mt-2 flex items-center gap-2 text-sm">
          <input v-model="form.override" type="checkbox" name="override" />
          <span>{{ t('frontDesk.checkIn.anyway') }}</span>
        </label>
        <FormField v-if="form.override" class="mt-2" :label="t('frontDesk.checkIn.reason')" :error="fieldError('override_reason')">
          <template #default="{ id, invalid }">
            <Input :id="id" v-model="form.reason" name="override_reason" maxlength="500" :aria-invalid="invalid" />
          </template>
        </FormField>
      </template>
    </div>

    <div class="flex justify-end gap-2">
      <Button type="button" variant="outline" @click="emit('cancel')">{{ t('common.cancel') }}</Button>
      <Button type="submit" :disabled="busy || roomId === null || arrival.guest_id === null || (notReady && !form.override)" data-testid="checkin-submit">{{ t('frontDesk.checkIn.submit') }}</Button>
    </div>
  </form>
</template>
