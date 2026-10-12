<script setup lang="ts">
import { ref, watch } from 'vue'
import { api } from '@/api/client'
import type { Arrival, CheckInResult } from '@/api/types'
import CheckInSheet from '@/components/CheckInSheet.vue'
import { checkedInCount, closeCheckIn, useCheckInSheet } from '@/composables/useCheckInSheet'
import { toast } from '@/composables/useToast'
import { t } from '@/i18n'
import { usePropertyStore } from '@/stores/property'

/**
 * The check-in sheet of the application, opened by `openCheckIn(arrival)` from anywhere that has an arrival but no list of them (the room panel). The arrivals still to come are read when it opens, so
 * "next check-in" has somewhere to go; if they cannot be read the button is simply not there.
 */
const { target } = useCheckInSheet()
const property = usePropertyStore()
const queue = ref<Arrival[]>([])

async function loadQueue(): Promise<void> {
  const propertyId = property.currentId
  if (propertyId === null) return
  try {
    const { data } = await api.GET('/api/v1/properties/{propertyId}/arrivals', { params: { path: { propertyId }, query: { status: 'CONFIRMED' } } })
    queue.value = data?.data ?? []
  } catch {
    queue.value = []
  }
}

watch(target, (a, before) => { if (a && !before) void loadQueue() })

async function onDone(result: CheckInResult): Promise<void> {
  toast.success(t('frontDesk.arrivals.checkedIn', { stay: result.stay.stay_number, room: result.stay_room.room_number }))
  checkedInCount.value++
  await loadQueue()
}
</script>

<template>
  <CheckInSheet :arrival="target" :queue="queue" @close="closeCheckIn" @done="onDone" />
</template>
