<script setup lang="ts">
import { Search } from 'lucide-vue-next'
import { computed, nextTick, ref, watch } from 'vue'
import type { FreeRoom, Reservation } from '@/api/types'
import StatusBadge from '@/components/app/StatusBadge.vue'
import { Button, type ButtonVariants } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover'
import { useFreeRooms } from '@/composables/useFreeRooms'
import { t } from '@/i18n'
import { usePropertyStore } from '@/stores/property'
import { type AssignFailure, type AssignTarget, assignFailure, assignRoom } from '@/utils/assignRoom'
import { hasRequestedBed, isAfterBusinessDate, isRoomReady } from '@/utils/freeRooms'

/**
 * Chooses a room for one room of a reservation, from where the person is (a list of arrivals, of reservations, the page of the day), without going to the reservation. A button opens the free
 * rooms of the booked type for the nights of the line (never another type: an upgrade is made on the page of the reservation), in the one order of `sortFreeRooms`; the person finds a
 * number in the search field (arrows and Enter choose), and the choice is the assignment. A room that is already on the line is replaced. The answer, or the failure, goes to the parent, which
 * puts the new version of the reservation on its row. A conflict (the room was taken, the reservation changed) is told here, in words, and the rooms are read again.
 */
const props = withDefaults(
  defineProps<{
    target: AssignTarget
    typeId: number
    typeCode?: string
    arrival: string
    departure: string
    requestedBedTypeId?: number | null
    currentRoomId?: number | null
    /** A room of another type than the booked one: the page of the reservation sends it, a list never does. */
    upgrade?: boolean
    variant?: ButtonVariants['variant']
    testId?: string
    disabled?: boolean
    label?: string
    /** No button: the list opens (`v-model:open`) where this component is, for an action chosen elsewhere ("Change room" in the menu of a row). */
    bare?: boolean
  }>(),
  { typeCode: '', requestedBedTypeId: null, currentRoomId: null, upgrade: false, variant: 'outline', testId: 'pick-room', disabled: false, label: '', bare: false },
)
const emit = defineEmits<{ assigned: [reservation: Reservation, room: FreeRoom]; failed: [failure: AssignFailure]; opened: [] }>()

const property = usePropertyStore()
const open = defineModel<boolean>('open', { default: false })
const busy = ref(false)
const needle = ref('')
const active = ref(0)
const message = ref('')
const search = ref<InstanceType<typeof Input> | null>(null)

/** The guest comes after the business date: today's housekeeping status does not order the rooms and is shown dimmed. */
const later = computed(() => isAfterBusinessDate(props.arrival, property.clock?.business_date ?? ''))

const { rooms, loading, loaded, error, reload, requiresInspection } = useFreeRooms(
  () => (open.value ? { typeId: props.typeId, arrival: props.arrival, departure: props.departure } : null),
  () => ({ requestedBedTypeId: props.requestedBedTypeId, currentRoomId: props.currentRoomId, ignoreReadiness: later.value }),
)

const shown = computed(() => {
  const n = needle.value.trim().toLowerCase()
  return n ? rooms.value.filter((r) => r.room_number.toLowerCase().includes(n)) : rooms.value
})
const buttonLabel = computed(() => props.label || (props.currentRoomId ? t('roomPicker.change') : t('roomPicker.choose')))

watch(open, async (isOpen) => {
  if (!isOpen) return
  needle.value = ''
  active.value = 0
  message.value = ''
  emit('opened')
  await nextTick()
  focusSearch()
})
watch(shown, (list) => {
  if (active.value >= list.length) active.value = Math.max(0, list.length - 1)
})

function focusSearch(): void {
  const el = (search.value as unknown as { $el?: HTMLElement } | null)?.$el ?? (search.value as unknown as HTMLElement | null)
  el?.focus?.()
}

function move(by: number): void {
  if (!shown.value.length) return
  active.value = (active.value + by + shown.value.length) % shown.value.length
  void nextTick(() => document.getElementById(optionId(shown.value[active.value]!))?.scrollIntoView?.({ block: 'nearest' }))
}
const optionId = (r: FreeRoom): string => `room-option-${props.target.lineId}-${r.room_id}`

function onKey(e: KeyboardEvent): void {
  if (e.key === 'ArrowDown') {
    e.preventDefault()
    move(1)
  } else if (e.key === 'ArrowUp') {
    e.preventDefault()
    move(-1)
  } else if (e.key === 'Enter') {
    e.preventDefault() // not the submit of a form the picker may sit in
    const room = shown.value[active.value]
    if (room) void pick(room)
  }
}

async function pick(room: FreeRoom): Promise<void> {
  const propertyId = property.currentId
  if (propertyId === null || busy.value) return
  busy.value = true
  message.value = ''
  try {
    const reservation = await assignRoom(propertyId, props.target, room.room_id, props.upgrade)
    open.value = false
    emit('assigned', reservation, room)
  } catch (e) {
    const failure = assignFailure(e)
    message.value = failure.message
    if (failure.reload) void reload() // the rooms that are free now
    emit('failed', failure)
  } finally {
    busy.value = false
  }
}

const ready = (r: FreeRoom): boolean => isRoomReady(r.housekeeping_status, requiresInspection.value)
</script>

<template>
  <Popover v-model:open="open">
    <PopoverTrigger as-child>
      <span v-if="bare" class="pointer-events-none absolute left-0 top-0 size-0" aria-hidden="true" />
      <Button v-else type="button" size="sm" :variant="variant" :disabled="disabled" :data-testid="testId" @click.stop>{{ buttonLabel }}</Button>
    </PopoverTrigger>
    <PopoverContent class="w-80 p-2" align="start">
      <div data-testid="room-picker" @click.stop>
      <p class="m-0 px-1 pb-1.5 text-xs font-medium text-muted-foreground">{{ t('roomPicker.title', { type: typeCode }) }}</p>
      <div class="relative">
        <Search class="pointer-events-none absolute left-2.5 top-2.5 size-4 text-muted-foreground" aria-hidden="true" />
        <Input
          ref="search"
          v-model="needle"
          class="pl-8"
          name="room_search"
          type="search"
          autocomplete="off"
          role="combobox"
          aria-expanded="true"
          :aria-controls="`room-list-${target.lineId}`"
          :aria-activedescendant="shown[active] ? optionId(shown[active]!) : undefined"
          :placeholder="t('roomPicker.search')"
          :aria-label="t('roomPicker.search')"
          data-testid="room-search"
          @keydown="onKey"
        />
      </div>
      <p v-if="message" class="m-0 mt-2 rounded-md border border-border bg-muted px-2 py-1.5 text-xs text-warning-text" role="alert" data-testid="room-picker-error">{{ message }}</p>
      <p v-if="loading && !loaded" class="m-0 px-1 py-3 text-sm text-muted-foreground" data-testid="room-picker-loading">{{ t('roomPicker.loading') }}</p>
      <p v-else-if="error && !loaded" class="m-0 px-1 py-3 text-sm text-destructive" role="alert" data-testid="room-picker-failed">{{ t('roomPicker.loadFailed') }}</p>
      <p v-else-if="loaded && !rooms.length" class="m-0 px-1 py-3 text-sm text-muted-foreground" data-testid="room-picker-none">{{ t('roomPicker.none', { type: typeCode }) }}</p>
      <p v-else-if="loaded && !shown.length" class="m-0 px-1 py-3 text-sm text-muted-foreground" data-testid="room-picker-nomatch">{{ t('roomPicker.noMatch') }}</p>
      <ul v-else :id="`room-list-${target.lineId}`" class="m-0 mt-1 max-h-64 list-none overflow-y-auto p-0" role="listbox" :aria-label="t('roomPicker.list')">
        <li v-for="(r, i) in shown" :id="optionId(r)" :key="r.room_id" role="option" :aria-selected="i === active">
          <button
            type="button"
            :class="['flex w-full cursor-pointer items-center gap-2 rounded-md border-0 bg-transparent px-2 py-1.5 text-left text-sm text-foreground hover:bg-accent', i === active && 'bg-accent']"
            :disabled="busy"
            :data-testid="`room-option-${r.room_id}`"
            :data-ready="ready(r)"
            :data-bed-match="hasRequestedBed(r, requestedBedTypeId)"
            @mouseenter="active = i"
            @click="pick(r)"
          >
            <b class="w-12 shrink-0">{{ r.room_number }}</b>
            <StatusBadge domain="housekeeping" :status="r.housekeeping_status" :class="later && 'opacity-50'" :data-later="later" />
            <span v-if="r.bed_type_name" class="min-w-0 flex-1 truncate text-xs text-muted-foreground">
              {{ r.bed_type_name }}<template v-if="hasRequestedBed(r, requestedBedTypeId)"> <span class="ml-1 text-success-text" :title="t('bedTypes.matches')" data-slot="bed-match">✓</span></template>
            </span>
          </button>
        </li>
      </ul>
      </div>
    </PopoverContent>
  </Popover>
</template>
