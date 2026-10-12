<script setup lang="ts">
import { findRoomByNumber, openRoomPanel } from '@/composables/useRoomPanel'
import { t } from '@/i18n'
import { cn } from '@/lib/utils'
import { usePropertyStore } from '@/stores/property'

/**
 * A room number that opens the room panel: `<RoomLink :room-id="row.room_id" :number="row.room_number" />`. Without the id the number is looked up among the rooms of the property.
 * It is a span that is a button, not a <button>: in a table row it must not be taken for one of the row's own buttons (the menu of the row). The click does not reach the row it is in (a row that opens a drawer stays shut). Enter and Space open it too. The text in the slot (default: the number) is what the table showed before.
 */
const props = defineProps<{ roomId?: number | null; number: string; class?: string }>()
const property = usePropertyStore()

async function open(): Promise<void> {
  const id = props.roomId ?? (await findRoomByNumber(property.currentId, props.number))?.room_id
  if (id) openRoomPanel(id, props.number)
}
</script>

<template>
  <span
    role="button"
    tabindex="0"
    data-slot="room-link"
    :aria-label="t('roomPanel.openFor', { number })"
    :class="cn('inline cursor-pointer font-semibold text-primary underline-offset-2 hover:underline focus-visible:rounded-sm focus-visible:outline-2 focus-visible:outline-ring', props.class)"
    @click.stop="open"
    @keydown.enter.prevent.stop="open"
    @keydown.space.prevent.stop="open"
  >
    <slot>{{ number }}</slot>
  </span>
</template>
