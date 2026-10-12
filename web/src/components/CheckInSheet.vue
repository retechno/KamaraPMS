<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import type { Arrival, CheckInResult } from '@/api/types'
import { Sheet, SheetContent, SheetDescription, SheetTitle } from '@/components/ui/sheet'
import { t } from '@/i18n'
import { formatDate } from '@/utils/format'
import CheckInPanel from '@/views/frontdesk/CheckInPanel.vue'

/**
 * The one sheet for a check-in, used by Arrivals, Today and the room panel. The host hands it the arrival to check in and the list it came from (`queue`); the sheet keeps its own copy of
 * the arrival, so it stays on the "done" step when the host reads its list again and the line is no longer waiting. "Next check-in" moves to the next ready arrival of the queue without closing.
 */
const props = withDefaults(defineProps<{ arrival: Arrival | null; queue?: Arrival[] }>(), { queue: () => [] })
const emit = defineEmits<{ close: []; done: [result: CheckInResult] }>()

const current = ref<Arrival | null>(null)
const finished = reactive(new Set<number>())
const panel = ref<InstanceType<typeof CheckInPanel> | null>(null)

watch(() => props.arrival, (a) => { if (a) current.value = a }, { immediate: true })

const open = computed({
  get: () => current.value !== null,
  set: (v: boolean) => { if (!v) close() },
})

function close(): void {
  current.value = null
  finished.clear()
  emit('close')
}

function onDone(result: CheckInResult): void {
  if (current.value) finished.add(current.value.reservation_room_id)
  emit('done', result)
}
</script>

<template>
  <Sheet v-model:open="open">
    <SheetContent v-if="current" class="p-6 sm:w-[600px]" data-testid="checkin-sheet" @escape-key-down="(e: Event) => panel?.escape(e)">
      <SheetTitle class="text-lg">{{ t('frontDesk.checkIn.title', { guest: current.guest_name || current.confirmation_number, type: current.room_type_code }) }}</SheetTitle>
      <SheetDescription class="mt-1">
        {{ t('frontDesk.checkIn.subtitle', { confirmation: current.confirmation_number, arrival: formatDate(current.arrival_date), departure: formatDate(current.departure_date) }) }}
      </SheetDescription>
      <CheckInPanel
        :key="current.reservation_room_id" ref="panel" :arrival="current" :queue="queue" :done="finished" class="mt-4"
        @done="onDone" @cancel="close" @next="(a: Arrival) => (current = a)"
      />
    </SheetContent>
  </Sheet>
</template>
