<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { BanquetAvailability, BanquetEvent, BanquetRoom, BanquetSession } from '@/api/types'
import ErrorNotice from '@/components/app/ErrorNotice.vue'
import FormField from '@/components/app/FormField.vue'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { NativeSelect } from '@/components/ui/native-select'
import { t } from '@/i18n'

/**
 * Adds a session to an event, or changes / moves one (docs/architecture/36-banquet.md 6.5, 6.6). Once the room, the day and the hours are filled the room is checked against the schedule: what is
 * in the way, the place the session would take in the queue of options and, when a tentative session moves, the place it gives up and the place it gets (it goes to the back of the new queue).
 * A move to a worse place must be confirmed; guests above the capacity of the room in that set-up too.
 */
const STYLES = ['THEATER', 'CLASSROOM', 'U_SHAPE', 'ROUND_TABLE', 'COCKTAIL', 'BOARDROOM'] as const
type Style = (typeof STYLES)[number]
const props = defineProps<{
  open: boolean
  propertyId: number
  event: BanquetEvent
  rooms: BanquetRoom[]
  session?: BanquetSession | null
  prefill?: { date?: string; room_id?: number; start?: string } | null
}>()
const emit = defineEmits<{ 'update:open': [value: boolean]; saved: [event: BanquetEvent] }>()

const form = reactive({ name: '', date: '', start: '', end: '', ends_next_day: false, room_id: 0, style: '' as '' | Style, pax: 1, pax_guaranteed: null as number | null, notes: '' })
const check = ref<BanquetAvailability | null>(null)
const checkError = ref<ApiError | null>(null)
const error = ref<ApiError | null>(null)
const acceptCapacity = ref(false)
const acceptWorse = ref(false)
const busy = ref(false)

const moving = computed(() => !!props.session && props.event.status === 'TENTATIVE')
const needsRoom = computed(() => props.event.status === 'TENTATIVE' || props.event.status === 'DEFINITE')
const ready = computed(() => form.room_id > 0 && !!form.date && !!form.start && !!form.end)
const room = computed(() => props.rooms.find((r) => r.id === form.room_id) ?? null)
const styleOptions = computed(() => (room.value ? room.value.setups.map((s) => s.setup_style) : [...STYLES]))
const capacity = computed(() => room.value?.setups.find((s) => s.setup_style === form.style)?.capacity ?? null)
const blocked = computed(() => !!check.value && check.value.mode === 'REJECT' && check.value.conflicts.some((c) => !c.same_event))
const worse = computed(() => !!check.value && moving.value && check.value.rank_in_new_slot > (check.value.rank_given_up ?? 1))

watch(() => props.open, (open) => {
  if (!open) return
  error.value = null
  check.value = null
  acceptCapacity.value = false
  acceptWorse.value = false
  const s = props.session
  const p = props.prefill
  Object.assign(form, s
    ? { name: s.name, date: s.session_date, start: (s.start_time ?? '').slice(0, 5), end: (s.end_time ?? '').slice(0, 5), ends_next_day: s.ends_next_day, room_id: s.room_id ?? 0, style: s.setup_style ?? '', pax: s.pax_estimated, pax_guaranteed: s.pax_guaranteed ?? null, notes: s.notes ?? '' }
    : { name: '', date: p?.date ?? '', start: p?.start ?? '', end: '', ends_next_day: false, room_id: p?.room_id ?? 0, style: '', pax: 1, pax_guaranteed: null, notes: '' })
}, { immediate: true })

let timer: ReturnType<typeof setTimeout> | undefined
watch(() => [form.room_id, form.date, form.start, form.end, form.ends_next_day, form.style, form.pax, props.open], () => {
  clearTimeout(timer)
  check.value = null
  acceptWorse.value = false
  if (!props.open || !ready.value) return
  timer = setTimeout(() => void runCheck(), 250)
})

async function runCheck(): Promise<void> {
  checkError.value = null
  const propertyId = props.propertyId
  try {
    const { data } = await api.POST('/api/v1/properties/{propertyId}/banquet/events/{id}/availability', {
      params: { path: { propertyId, id: props.event.id } },
      body: { session_id: props.session?.id ?? null, room_id: form.room_id, session_date: form.date, start_time: form.start, end_time: form.end, ends_next_day: form.ends_next_day, setup_style: form.style || null, pax: form.pax },
    })
    check.value = data ?? null
  } catch (e) {
    checkError.value = e instanceof ApiError ? e : null
  }
}

async function save(): Promise<void> {
  busy.value = true
  error.value = null
  const propertyId = props.propertyId
  const body = {
    name: form.name.trim(),
    session_date: form.date,
    start_time: form.start || null,
    end_time: form.end || null,
    ends_next_day: form.ends_next_day,
    room_id: form.room_id || null,
    setup_style: form.style || null,
    pax_estimated: form.pax,
    pax_guaranteed: form.pax_guaranteed,
    notes: form.notes.trim() || null,
    accept_capacity: acceptCapacity.value,
  }
  try {
    const res = props.session
      ? await api.PATCH('/api/v1/properties/{propertyId}/banquet/events/{id}/sessions/{sid}', { params: { path: { propertyId, id: props.event.id, sid: props.session.id } }, body })
      : await api.POST('/api/v1/properties/{propertyId}/banquet/events/{id}/sessions', { params: { path: { propertyId, id: props.event.id } }, body })
    if (res.data) emit('saved', res.data)
    emit('update:open', false)
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
    if (error.value?.code === 'CAPACITY_EXCEEDED') acceptCapacity.value = false
  } finally {
    busy.value = false
  }
}

const canSave = computed(() => !busy.value && !blocked.value && (!worse.value || acceptWorse.value) && (!check.value?.capacity_exceeds || acceptCapacity.value))
</script>

<template>
  <Dialog :open="open" @update:open="(v) => emit('update:open', v)">
    <DialogContent class="max-h-[90dvh] max-w-lg overflow-y-auto p-5" data-testid="banquet-session-dialog" @interact-outside.prevent>
      <form novalidate class="flex flex-col gap-3" @submit.prevent="save">
        <DialogTitle>{{ session ? (moving ? t('banquet.session.move') : t('banquet.session.edit')) : t('banquet.session.new') }}</DialogTitle>
        <DialogDescription>{{ t(needsRoom ? 'banquet.session.hintHeld' : 'banquet.session.hintInquiry') }}</DialogDescription>
        <ErrorNotice v-if="error" :error="error" inline />
        <FormField :label="t('banquet.session.name')" required :error="error?.fieldMessage('name')">
          <template #default="{ id }"><Input :id="id" v-model="form.name" name="name" maxlength="100" /></template>
        </FormField>
        <FormField :label="t('banquet.session.room')" :required="needsRoom" :error="error?.fieldMessage('room_id')">
          <template #default="{ id }">
            <NativeSelect :id="id" v-model.number="form.room_id" name="room_id" data-testid="session-room">
              <option :value="0">{{ t('banquet.session.noRoom') }}</option>
              <option v-for="r in rooms.filter((x) => x.is_active || x.id === session?.room_id)" :key="r.id" :value="r.id">{{ r.name }}</option>
            </NativeSelect>
          </template>
        </FormField>
        <div class="grid gap-3 sm:grid-cols-3">
          <FormField :label="t('banquet.session.date')" required :error="error?.fieldMessage('session_date')">
            <template #default="{ id }"><Input :id="id" v-model="form.date" name="session_date" type="date" /></template>
          </FormField>
          <FormField :label="t('banquet.session.start')" :error="error?.fieldMessage('start_time')">
            <template #default="{ id }"><Input :id="id" v-model="form.start" name="start_time" type="time" /></template>
          </FormField>
          <FormField :label="t('banquet.session.end')" :error="error?.fieldMessage('end_time')">
            <template #default="{ id }"><Input :id="id" v-model="form.end" name="end_time" type="time" /></template>
          </FormField>
        </div>
        <label class="flex min-h-11 cursor-pointer items-center gap-2 text-sm"><input v-model="form.ends_next_day" type="checkbox" name="ends_next_day" class="size-4 accent-primary" />{{ t('banquet.session.endsNextDay') }}</label>
        <div class="grid gap-3 sm:grid-cols-3">
          <FormField :label="t('banquet.session.style')">
            <template #default="{ id }">
              <NativeSelect :id="id" v-model="form.style" name="setup_style">
                <option value="">{{ t('banquet.session.noStyle') }}</option>
                <option v-for="s in styleOptions" :key="s" :value="s">{{ t(`banquet.style.${s}`) }}</option>
              </NativeSelect>
            </template>
          </FormField>
          <FormField :label="t('banquet.session.pax')" required :hint="capacity !== null ? t('banquet.session.capacityHint', { n: capacity }) : undefined" :error="error?.fieldMessage('pax_estimated')">
            <template #default="{ id }"><Input :id="id" v-model.number="form.pax" name="pax_estimated" type="number" min="1" /></template>
          </FormField>
          <FormField :label="t('banquet.session.paxGuaranteed')">
            <template #default="{ id }"><Input :id="id" v-model.number="form.pax_guaranteed" name="pax_guaranteed" type="number" min="0" /></template>
          </FormField>
        </div>
        <FormField :label="t('banquet.session.notes')">
          <template #default="{ id }"><Input :id="id" v-model="form.notes" name="notes" maxlength="500" /></template>
        </FormField>

        <!-- the room check -->
        <div v-if="check || checkError" class="flex flex-col gap-1.5 rounded-md border border-border p-3 text-sm" data-testid="session-check" aria-live="polite">
          <ErrorNotice v-if="checkError" :error="checkError" inline />
          <template v-if="check">
            <p v-if="!check.conflicts.some((c) => !c.same_event)" class="m-0 text-success-text" data-testid="check-free">{{ t('banquet.check.free', { from: check.hold_from, to: check.hold_to }) }}</p>
            <template v-else>
              <p class="m-0 font-medium" :class="blocked ? 'text-destructive' : ''" data-testid="check-conflict">{{ blocked ? t('banquet.check.taken') : t('banquet.check.queued', { n: check.rank_in_new_slot }) }}</p>
              <ul class="m-0 flex list-none flex-col gap-0.5 p-0 text-muted-foreground">
                <li v-for="c in check.conflicts.filter((x) => !x.same_event)" :key="c.session_id">{{ c.event_number }} {{ c.event_name }} · {{ c.session_name }} · {{ t(`banquet.hold.${c.hold_kind}`) }} · {{ c.room_code }}</li>
              </ul>
            </template>
            <p v-if="moving" class="m-0" data-testid="check-ranks">{{ t('banquet.check.ranks', { from: check.rank_given_up ?? 1, to: check.rank_in_new_slot }) }}</p>
            <label v-if="worse" class="flex min-h-11 cursor-pointer items-center gap-2"><input v-model="acceptWorse" type="checkbox" name="accept_worse" class="size-4 accent-primary" data-testid="accept-worse" />{{ t('banquet.check.acceptWorse') }}</label>
            <p v-if="check.capacity_exceeds" class="m-0 text-destructive" data-testid="check-capacity">{{ t('banquet.check.capacity', { pax: form.pax, n: check.capacity ?? 0 }) }}</p>
            <label v-if="check.capacity_exceeds" class="flex min-h-11 cursor-pointer items-center gap-2"><input v-model="acceptCapacity" type="checkbox" name="accept_capacity" class="size-4 accent-primary" data-testid="accept-capacity" />{{ t('banquet.check.acceptCapacity') }}</label>
            <p v-if="form.style && !check.setup_offered" class="m-0 text-destructive">{{ t('banquet.check.noSetup') }}</p>
          </template>
        </div>
        <div class="flex justify-end gap-2">
          <Button type="button" variant="outline" @click="emit('update:open', false)">{{ t('common.cancel') }}</Button>
          <Button type="submit" :disabled="!canSave" data-testid="banquet-session-save">{{ moving ? t('banquet.session.moveAction') : t('common.save') }}</Button>
        </div>
      </form>
    </DialogContent>
  </Dialog>
</template>
