<script setup lang="ts">
import { computed } from 'vue'
import RoomPanel from '@/components/RoomPanel.vue'
import { closeRoomPanel, useRoomPanel } from '@/composables/useRoomPanel'

/**
 * The one room panel of the application, opened by `openRoomPanel(roomId)` from anywhere (a RoomLink, the quick finder). It is in the shell, so it does not belong to any page and survives going from one
 * page to another; the pages that open it close it themselves when they navigate.
 */
const { target } = useRoomPanel()
const open = computed({
  get: () => target.value !== null,
  set: (v: boolean) => {
    if (!v) closeRoomPanel()
  },
})
</script>

<template>
  <RoomPanel v-if="target" :key="target.roomId" v-model:open="open" :room-id="target.roomId" :number="target.number" :seed="target.seed" @close="closeRoomPanel" />
</template>
