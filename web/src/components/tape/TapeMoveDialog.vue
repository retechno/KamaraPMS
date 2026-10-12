<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { TapeBooking, TapeRoom } from '@/api/types'
import ErrorNotice from '@/components/app/ErrorNotice.vue'
import FormField from '@/components/app/FormField.vue'
import { Button } from '@/components/ui/button'
import { Combobox } from '@/components/ui/combobox'
import { Dialog, DialogContent, DialogDescription, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { t } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import { assignRoom } from '@/utils/assignRoom'
import { formatDate } from '@/utils/format'
import { dropVerdict, isConfirmed, moveNights, nightsOf, type TapeRights } from '@/utils/tape'

/**
 * The confirmation of a move of a booking to another room, from a drop on the chart or from the menu of the block: always shown before anything is sent. It says what moves (from room to room, the
 * nights, whether the type is another, and that the price of the nights does not change), reads the reservation (or the stay) again for its version, and sends through the endpoint that exists:
 * assign-room for a booking to come, the move of the stay for a guest in the house. The availability is decided by the server; a refusal is shown here and the chart is read again.
 */
const open = defineModel<boolean>('open', { required: true })
const props = defineProps<{ booking: TapeBooking | null; fromRoom: TapeRoom | null; rooms: TapeRoom[]; target: number | null; rights: TapeRights }>()
const emit = defineEmits<{ done: [message: string]; changed: [] }>()

const auth = useAuthStore()
const property = usePropertyStore()
const pid = computed(() => property.currentId)
const businessDate = computed(() => property.clock?.business_date ?? '')
const canOverride = computed(() => auth.can('frontdesk.checkin_unready_room', pid.value))

const targetId = ref<number | null>(null)
const version = ref<number | null>(null)
const bookedTypeId = ref<number | null>(null)
const loading = ref(false)
const busy = ref(false)
const error = ref<ApiError | null>(null)
const stale = ref(false) // something was refused or changed on the server: the chart is read again when the dialog closes
const form = reactive({ reason: '', override: false, overrideReason: '' })
const unready = ref(false) // the server said the room is not clean: the override is offered to who may

const inHouse = computed(() => !!props.booking && !isConfirmed(props.booking))
const nights = computed(() => (props.booking ? moveNights(props.booking, businessDate.value) : { from: '', to: '' }))
const targetRoom = computed(() => props.rooms.find((r) => r.room_id === targetId.value) ?? null)
const typeCode = (id: number | null): string => props.rooms.find((r) => r.room_type_id === id)?.room_type_code ?? ''
const bookedType = computed(() => bookedTypeId.value ?? props.booking?.room_type_id ?? null)
const isUpgrade = computed(() => !!targetRoom.value && targetRoom.value.room_type_id !== bookedType.value)

/** The rooms the booking can be dropped on, as far as the chart can tell (the server decides at the confirmation). */
const options = computed(() => {
  const b = props.booking
  if (!b) return []
  return props.rooms
    .filter((r) => dropVerdict(b, r, props.fromRoom?.room_id ?? null, businessDate.value, props.rights).ok)
    .map((r) => ({ value: r.room_id, label: `${r.room_number} · ${r.room_type_code}${r.room_type_id !== b.room_type_id ? ` (${t('tapeChart.upgrade')})` : ''}` }))
})
const reasonMissing = computed(() => inHouse.value && !form.reason.trim())
const canConfirm = computed(() => !loading.value && !busy.value && version.value !== null && targetId.value !== null && !reasonMissing.value && (!form.override || !!form.overrideReason.trim()))

async function load(): Promise<void> {
  const propertyId = pid.value
  const b = props.booking
  if (propertyId === null || !b) return
  loading.value = true
  version.value = null
  error.value = null
  try {
    if (inHouse.value && b.stay_id) {
      const { data } = await api.GET('/api/v1/properties/{propertyId}/stays/{id}', { params: { path: { propertyId, id: b.stay_id } } })
      version.value = data?.stay.version ?? null
    } else {
      const { data } = await api.GET('/api/v1/properties/{propertyId}/reservations/{id}', { params: { path: { propertyId, id: b.reservation_id } } })
      const line = data?.rooms.find((r) => r.id === b.reservation_room_id)
      if (line && line.status !== 'CONFIRMED') {
        stale.value = true
        error.value = new ApiError({ type: 't', title: 'Conflict', status: 409, code: 'LINE_NOT_CONFIRMED', detail: t('tapeChart.notWaiting') })
      }
      version.value = data?.version ?? null
      bookedTypeId.value = line?.room_type_id ?? null
    }
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
    stale.value = true
  } finally {
    loading.value = false
  }
}

watch(open, (v) => {
  if (!v) return
  targetId.value = props.target ?? options.value[0]?.value ?? null
  Object.assign(form, { reason: '', override: false, overrideReason: '' })
  unready.value = false
  stale.value = false
  bookedTypeId.value = null
  void load()
}, { immediate: true })

watch(open, (v, was) => {
  if (was && !v && stale.value) emit('changed')
})

async function confirm(): Promise<void> {
  const propertyId = pid.value
  const b = props.booking
  if (propertyId === null || !b || targetId.value === null || version.value === null) return
  busy.value = true
  error.value = null
  try {
    if (inHouse.value && b.stay_id) {
      await api.POST('/api/v1/properties/{propertyId}/stays/{id}/move', {
        params: { path: { propertyId, id: b.stay_id } },
        body: {
          version: version.value, room_id: targetId.value, reason: form.reason.trim(),
          override_room_not_ready: form.override, override_reason: form.override ? form.overrideReason.trim() : undefined,
        },
      })
    } else {
      await assignRoom(propertyId, { reservationId: b.reservation_id, lineId: b.reservation_room_id, version: version.value }, targetId.value, isUpgrade.value)
    }
    emit('done', t('tapeChart.moved', { room: targetRoom.value?.room_number ?? '' }))
    stale.value = false
    open.value = false
    emit('changed')
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
    if (e instanceof ApiError) {
      if (e.status === 409 || e.status === 422) stale.value = true
      unready.value = e.code === 'ROOM_NOT_READY'
      if (e.code === 'VERSION_CONFLICT') version.value = null
    }
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <Dialog v-model:open="open">
    <DialogContent class="top-[10vh] max-h-[85vh] max-w-lg overflow-y-auto p-5" data-testid="tape-move-dialog">
      <DialogTitle>{{ t('tapeChart.move.title') }}</DialogTitle>
      <DialogDescription class="mt-1" data-testid="tape-move-who">{{ booking?.guest_name || booking?.confirmation_number }} · {{ booking?.confirmation_number }}</DialogDescription>
      <ErrorNotice :error="error" class="mt-3" data-testid="tape-error" />

      <form class="mt-3 flex flex-col gap-3" novalidate @submit.prevent="confirm">
        <FormField :label="t('tapeChart.move.to')">
          <template #default="{ id }">
            <Combobox :id="id" v-model="targetId" name="target_room" data-autofocus :options="options" />
          </template>
        </FormField>

        <dl class="m-0 grid grid-cols-[auto_1fr] gap-x-4 gap-y-1.5 rounded-lg bg-muted px-3 py-2.5 text-sm" data-testid="tape-move-summary">
          <dt class="text-muted-foreground">{{ t('tapeChart.move.fromTo') }}</dt>
          <dd class="m-0 font-semibold" data-testid="tape-move-rooms">{{ fromRoom?.room_number ?? t('tapeChart.noRoom') }} → {{ targetRoom?.room_number ?? '…' }}</dd>
          <dt class="text-muted-foreground">{{ t('tapeChart.move.nights') }}</dt>
          <dd class="m-0" data-testid="tape-move-nights">{{ formatDate(nights.from) }} → {{ formatDate(nights.to) }} · {{ t('tapeChart.nightsN', { n: nightsOf(nights.from, nights.to) }, nightsOf(nights.from, nights.to)) }}<template v-if="inHouse"> ({{ t('tapeChart.move.fromTonight') }})</template></dd>
          <template v-if="isUpgrade">
            <dt class="text-muted-foreground">{{ t('tapeChart.upgrade') }}</dt>
            <dd class="m-0 font-semibold text-warning-text" data-testid="tape-move-upgrade">{{ t('tapeChart.move.upgradeLine', { booked: typeCode(bookedType), room: targetRoom?.room_type_code ?? '' }) }}</dd>
          </template>
          <dt class="text-muted-foreground">{{ t('tapeChart.move.price') }}</dt>
          <dd class="m-0" data-testid="tape-move-price">{{ t('tapeChart.move.priceSame') }}</dd>
        </dl>

        <FormField v-if="inHouse" :label="t('tapeChart.move.reason')" :error="error?.fieldMessage('reason')">
          <template #default="{ id, invalid }"><Input :id="id" v-model="form.reason" name="reason" maxlength="500" :aria-invalid="invalid" /></template>
        </FormField>

        <div v-if="unready && canOverride" class="alert warning m-0" data-testid="tape-override">
          <label class="flex items-center gap-2 text-sm"><input v-model="form.override" type="checkbox" name="override" /><span>{{ t('tapeChart.move.overrideUnready') }}</span></label>
          <FormField v-if="form.override" class="mt-2" :label="t('frontDesk.checkIn.reason')" :error="error?.fieldMessage('override_reason')">
            <template #default="{ id, invalid }"><Input :id="id" v-model="form.overrideReason" name="override_reason" maxlength="500" :aria-invalid="invalid" /></template>
          </FormField>
        </div>

        <div class="flex justify-end gap-2">
          <Button type="button" variant="outline" data-testid="tape-cancel" @click="open = false">{{ t('common.cancel') }}</Button>
          <Button type="submit" data-testid="tape-confirm" :disabled="!canConfirm">{{ t('tapeChart.move.confirm') }}</Button>
        </div>
      </form>
    </DialogContent>
  </Dialog>
</template>
