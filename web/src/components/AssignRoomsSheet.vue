<script setup lang="ts">
import { computed } from 'vue'
import type { FreeRoom, Reservation, ReservationSummary } from '@/api/types'
import RoomPicker from '@/components/RoomPicker.vue'
import { Button } from '@/components/ui/button'
import { Sheet, SheetContent, SheetDescription, SheetTitle } from '@/components/ui/sheet'
import { t } from '@/i18n'
import type { AssignFailure } from '@/utils/assignRoom'
import { formatDate } from '@/utils/format'
import { isUnassigned } from '@/utils/roomAssign'

/**
 * The rooms of one reservation of the list, each with its own picker ("Choose room" / "Change room"), for a reservation that has several rooms or whose rooms are to be changed: the person
 * does not have to open the reservation. It shows what the row of the list says (`row`): the parent puts the answer of every assignment on the row (the room and the new version), so the next
 * assignment of the same reservation asks for the version the last answer gave. Only the rooms that wait for check-in can be assigned; one that is checked in, cancelled or a no-show is not here.
 */
const props = defineProps<{ row: ReservationSummary | null }>()
const open = defineModel<boolean>('open', { required: true })
const emit = defineEmits<{ assigned: [reservation: Reservation, room: FreeRoom]; failed: [failure: AssignFailure]; auto: [] }>()

const lines = computed(() => (props.row?.rooms ?? []).filter((l) => l.status === 'CONFIRMED'))
const missing = computed(() => lines.value.filter((l) => isUnassigned(l)).length)
</script>

<template>
  <Sheet v-model:open="open">
    <SheetContent v-if="row" class="p-6" data-testid="assign-rooms-sheet">
      <SheetTitle class="text-lg">{{ t('reservations.assignTitle', { number: row.confirmation_number }) }}</SheetTitle>
      <SheetDescription class="mt-1">{{ row.guest_name || '—' }} · {{ t('reservations.assignHint') }}</SheetDescription>

      <div v-if="missing > 0" class="mt-4">
        <Button type="button" size="sm" variant="outline" data-testid="sheet-auto" @click="emit('auto')">{{ t('autoAssign.button') }}</Button>
      </div>

      <ul class="m-0 mt-4 grid list-none gap-3 p-0">
        <li v-for="l in lines" :key="l.id" class="rounded-lg border border-border p-3" :data-testid="`sheet-line-${l.id}`">
          <div class="flex flex-wrap items-start justify-between gap-2">
            <div>
              <div class="font-medium">{{ l.room_type_code }}</div>
              <div class="text-xs text-muted-foreground">{{ t('reservations.assignNights', { from: formatDate(l.arrival_date), to: formatDate(l.departure_date) }) }}</div>
              <div class="mt-1 text-sm" :data-testid="`sheet-room-${l.id}`">{{ l.room_number || t('reservations.assignNoRoom') }}</div>
            </div>
            <RoomPicker
              :target="{ reservationId: row.id, lineId: l.id, version: row.version }" :type-id="l.room_type_id" :type-code="l.room_type_code" :arrival="l.arrival_date" :departure="l.departure_date"
              :requested-bed-type-id="l.requested_bed_type_id ?? null" :current-room-id="l.room_id ?? null" :test-id="`sheet-pick-${l.id}`"
              @assigned="(res, room) => emit('assigned', res, room)" @failed="(f) => emit('failed', f)"
            />
          </div>
        </li>
      </ul>
    </SheetContent>
  </Sheet>
</template>
