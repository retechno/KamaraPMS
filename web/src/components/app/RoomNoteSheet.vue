<script setup lang="ts">
/**
 * "Add note" on a room: the flags of the room (priority, do not disturb, make-up requested) and the note, in a sheet. Shared by the board of housekeeping and by My tasks. It is open while `room` is
 * given. The focus goes to the note when it opens and, when it closes, to the element `restoreFocus` returns (the "..." of the room that opened it). A refusal of the server stays in the sheet.
 */
import { nextTick, reactive, ref, watch } from 'vue'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import ErrorNotice from '@/components/app/ErrorNotice.vue'
import FormField from '@/components/app/FormField.vue'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import { Input } from '@/components/ui/input'
import { NativeSelect } from '@/components/ui/native-select'
import { Sheet, SheetContent, SheetDescription, SheetTitle } from '@/components/ui/sheet'
import { t } from '@/i18n'
import { usePropertyStore } from '@/stores/property'

export interface NoteRoom {
  room_id: number
  room_number: string
  priority: 'NORMAL' | 'HIGH'
  dnd: boolean
  make_up_requested: boolean
  flag_note?: string | null
}

const props = defineProps<{ room: NoteRoom | null; restoreFocus?: () => HTMLElement | null | undefined }>()
const emit = defineEmits<{ close: []; saved: [] }>()

const property = usePropertyStore()
const form = reactive({ priority: 'NORMAL' as 'NORMAL' | 'HIGH', dnd: false, make_up_requested: false, note: '' })
const saving = ref(false)
const error = ref<ApiError | null>(null)
const body = ref<HTMLElement | null>(null)

watch(
  () => props.room,
  (room) => {
    if (!room) return
    Object.assign(form, { priority: room.priority, dnd: room.dnd, make_up_requested: room.make_up_requested, note: room.flag_note ?? '' })
    error.value = null
  },
  { immediate: true },
)

function focusNote(e: Event): void {
  e.preventDefault()
  body.value?.querySelector<HTMLInputElement>('input[name=note]')?.focus()
}
function focusBack(e: Event): void {
  e.preventDefault()
  void nextTick(() => props.restoreFocus?.()?.focus())
}

async function save(): Promise<void> {
  const room = props.room
  const propertyId = property.currentId
  if (propertyId === null || !room) return
  saving.value = true
  error.value = null
  try {
    await api.PUT('/api/v1/properties/{propertyId}/rooms/{id}/housekeeping/flags', {
      params: { path: { propertyId, id: room.room_id } },
      body: { ...form },
    })
    emit('saved')
    emit('close')
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    saving.value = false
  }
}
</script>

<template>
  <Sheet :open="!!room" @update:open="(v: boolean) => { if (!v) emit('close') }">
    <SheetContent v-if="room" class="p-6" data-testid="flag-sheet" @open-auto-focus="focusNote" @close-auto-focus="focusBack">
      <div ref="body">
        <SheetTitle class="text-lg">{{ t('housekeeping.flagsTitle', { number: room.room_number }) }}</SheetTitle>
        <SheetDescription class="mt-1">{{ t('housekeeping.flagsHelp') }}</SheetDescription>
        <form class="mt-4" novalidate data-testid="flag-form" @submit.prevent="save">
          <div class="grid gap-4">
            <FormField :label="t('housekeeping.priority')">
              <template #default="{ id }">
                <NativeSelect :id="id" v-model="form.priority" name="priority">
                  <option value="NORMAL">{{ t('housekeeping.normal') }}</option>
                  <option value="HIGH">{{ t('housekeeping.high') }}</option>
                </NativeSelect>
              </template>
            </FormField>
            <FormField :label="t('housekeeping.note')">
              <template #default="{ id }"><Input :id="id" v-model="form.note" name="note" maxlength="500" /></template>
            </FormField>
            <Checkbox v-model="form.dnd" name="dnd" label-class="text-sm">{{ t('housekeeping.dnd') }}</Checkbox>
            <Checkbox v-model="form.make_up_requested" name="make_up_requested" label-class="text-sm">{{ t('housekeeping.makeUp') }}</Checkbox>
          </div>
          <ErrorNotice :error="error" inline class="mt-3" />
          <div class="mt-4 flex justify-end gap-3">
            <Button type="button" variant="outline" @click="emit('close')">{{ t('common.cancel') }}</Button>
            <Button type="submit" :disabled="saving">{{ t('common.save') }}</Button>
          </div>
        </form>
      </div>
    </SheetContent>
  </Sheet>
</template>
