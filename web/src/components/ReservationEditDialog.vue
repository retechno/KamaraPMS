<script setup lang="ts">
import { reactive, ref, watch } from 'vue'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { Guest, Reservation, RoomType } from '@/api/types'
import ErrorNotice from '@/components/app/ErrorNotice.vue'
import FormField from '@/components/app/FormField.vue'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogTitle } from '@/components/ui/dialog'
import { DateInput } from '@/components/ui/date-input'
import { Input } from '@/components/ui/input'
import { NativeSelect } from '@/components/ui/native-select'
import type { Choice } from '@/composables/useReservationLookups'
import { t } from '@/i18n'
import { usePropertyStore } from '@/stores/property'
import { guardForm } from '@/utils/forms'
import { guestLabel } from '@/utils/reservations'

/**
 * Edit Reservation: the stay of each room that is not checked in (dates, room type, rate plan, adults and children) and the booker, the company, the special request and the remarks of the reservation.
 * The booker is searched among the guests here: a draft made without one cannot be confirmed until it is set.
 * It only asks; the page sends the change with the version it loaded, so the server decides: availability, the sales restrictions and the price of the nights are checked again there. The
 * guest and the rate of a night are not edited here (they have their own editors), and the rate plan, the booked prices and the folios are never touched from this form.
 */
const open = defineModel<boolean>('open', { required: true })
const props = defineProps<{ reservation: Reservation; types: RoomType[]; ratePlans: Choice[]; companies: Choice[]; busy: boolean; error: ApiError | null }>()
const emit = defineEmits<{
  saveLine: [lineId: number, body: Record<string, unknown>]
  saveHeader: [body: Record<string, unknown>]
}>()

interface LineForm {
  arrival_date: string
  departure_date: string
  room_type_id: number
  rate_plan_id: number
  adult_count: number
  child_count: number
}
const lines = reactive<Record<number, LineForm>>({})
const header = reactive({ guest_id: 0, company_id: 0, special_request: '', remarks: '' })
const property = usePropertyStore()
// the booker: the guest found and picked here, shown until saved (the reservation's own guest otherwise)
const booker = ref<{ first_name?: string; last_name: string; code: string } | null>(null)
const bookerQuery = ref('')
const bookerResults = ref<Guest[]>([])
const bookerError = ref<ApiError | null>(null)

const editable = (status: string) => status === 'DRAFT' || status === 'CONFIRMED'
const fieldError = (field: string) => props.error?.fieldMessage(field)

function fill(): void {
  for (const l of props.reservation.rooms) {
    lines[l.id] = { arrival_date: l.arrival_date, departure_date: l.departure_date, room_type_id: l.room_type_id, rate_plan_id: l.rate_plan_id, adult_count: l.adult_count, child_count: l.child_count }
  }
  header.guest_id = props.reservation.guest_id ?? 0
  booker.value = props.reservation.guest ?? null
  bookerQuery.value = ''
  bookerResults.value = []
  bookerError.value = null
  header.company_id = props.reservation.company_id ?? 0
  header.special_request = props.reservation.special_request ?? ''
  header.remarks = props.reservation.remarks ?? ''
}

/** Only what changed is sent. */
function changes(lineId: number): Record<string, unknown> {
  const line = props.reservation.rooms.find((l) => l.id === lineId)
  const form = lines[lineId]
  if (!line || !form) return {}
  const out: Record<string, unknown> = {}
  for (const k of Object.keys(form) as (keyof LineForm)[]) if (form[k] !== line[k]) out[k] = form[k]
  return out
}

function headerChanges(): Record<string, unknown> {
  const r = props.reservation
  const out: Record<string, unknown> = {}
  if (header.guest_id && header.guest_id !== (r.guest_id ?? 0)) out.guest_id = header.guest_id
  if (header.company_id !== (r.company_id ?? 0)) out.company_id = header.company_id
  if (header.special_request !== (r.special_request ?? '')) out.special_request = header.special_request
  if (header.remarks !== (r.remarks ?? '')) out.remarks = header.remarks
  return out
}

async function findBooker(): Promise<void> {
  const propertyId = property.currentId
  if (propertyId === null || !bookerQuery.value.trim()) return
  bookerError.value = null
  try {
    const { data } = await api.GET('/api/v1/guests', { params: { query: { q: bookerQuery.value.trim(), property_id: propertyId, limit: 10 } } })
    bookerResults.value = data?.data ?? []
  } catch (e) {
    bookerError.value = e instanceof ApiError ? e : null
  }
}

function pickBooker(g: Guest): void {
  header.guest_id = g.id
  booker.value = g
  bookerResults.value = []
  bookerQuery.value = ''
}

watch(() => [open.value, props.reservation.version], () => { if (open.value) fill() }, { immediate: true })
</script>

<template>
  <Dialog v-model:open="open">
    <DialogContent class="top-[5vh] max-h-[90vh] max-w-3xl overflow-y-auto p-5" data-testid="reservation-edit-dialog">
      <DialogTitle>{{ t('reservation.editReservation') }}</DialogTitle>
      <DialogDescription class="mt-1">{{ t('reservation.editHint') }}</DialogDescription>
      <ErrorNotice :error="error" class="mt-3" data-testid="edit-error" />

      <section v-for="(l, index) in reservation.rooms.filter((x) => editable(x.status))" :key="l.id" class="mt-4 rounded-lg border border-border p-3" :data-testid="`edit-line-${l.id}`">
        <h3 class="m-0 mb-2 text-sm font-semibold">{{ l.room_type_code }} · {{ l.room_number || t('reservation.noRoom') }}</h3>
        <form v-if="lines[l.id]" class="grid gap-x-3 gap-y-7 sm:grid-cols-3" novalidate @submit.prevent="guardForm($event) && emit('saveLine', l.id, changes(l.id))">
          <FormField :label="t('reservation.arrival')" :error="fieldError('arrival_date')">
            <template #default="{ id, invalid }"><DateInput :id="id" v-model="lines[l.id]!.arrival_date" :data-autofocus="index === 0 ? '' : undefined" :name="`arrival_${l.id}`" :aria-invalid="invalid" /></template>
          </FormField>
          <FormField :label="t('reservation.departure')" :error="fieldError('departure_date')">
            <template #default="{ id, invalid }"><DateInput :id="id" v-model="lines[l.id]!.departure_date" :name="`departure_${l.id}`" :aria-invalid="invalid" /></template>
          </FormField>
          <FormField :label="t('reservation.roomType')" :error="fieldError('room_type_id')">
            <template #default="{ id }">
              <NativeSelect :id="id" v-model.number="lines[l.id]!.room_type_id" :name="`type_${l.id}`" :disabled="!!l.room_number">
                <option v-for="ty in types.filter((x) => x.is_active || x.id === l.room_type_id)" :key="ty.id" :value="ty.id">{{ ty.code }}</option>
              </NativeSelect>
              <small v-if="l.room_number" class="text-xs text-muted-foreground">{{ t('reservation.typeLocked') }}</small>
            </template>
          </FormField>
          <FormField :label="t('reservation.ratePlan')" :error="fieldError('rate_plan_id')">
            <template #default="{ id }">
              <NativeSelect :id="id" v-model.number="lines[l.id]!.rate_plan_id" :name="`plan_${l.id}`">
                <option v-for="p in ratePlans" :key="p.id" :value="p.id">{{ p.code }}</option>
                <option v-if="!ratePlans.some((p) => p.id === l.rate_plan_id)" :value="l.rate_plan_id">{{ l.rate_plan_code }}</option>
              </NativeSelect>
            </template>
          </FormField>
          <FormField :label="t('reservation.adults')" :error="fieldError('adult_count')">
            <template #default="{ id, invalid }"><Input :id="id" v-model.number="lines[l.id]!.adult_count" :name="`adults_${l.id}`" type="number" min="1" :aria-invalid="invalid" /></template>
          </FormField>
          <FormField :label="t('reservation.children')" :error="fieldError('child_count')">
            <template #default="{ id, invalid }"><Input :id="id" v-model.number="lines[l.id]!.child_count" :name="`children_${l.id}`" type="number" min="0" :aria-invalid="invalid" /></template>
          </FormField>
          <div class="flex items-end justify-end sm:col-span-3">
            <Button type="submit" size="sm" :disabled="busy || !Object.keys(changes(l.id)).length" :data-testid="`save-line-${l.id}`">{{ t('common.save') }}</Button>
          </div>
        </form>
      </section>

      <form class="mt-4 grid gap-3 rounded-lg border border-border p-3 sm:grid-cols-2" novalidate data-testid="edit-header" @submit.prevent="emit('saveHeader', headerChanges())">
        <div class="sm:col-span-2">
          <p class="m-0 text-sm" data-testid="edit-booker">
            {{ t('reservation.booker') }}:
            <strong v-if="booker">{{ guestLabel(booker) }} ({{ booker.code }})</strong>
            <span v-else class="text-muted-foreground">{{ t('reservation.notSet') }}</span>
          </p>
          <div class="mt-2 flex items-end gap-3">
            <FormField class="flex-1" :label="t('reservation.bookerFind')" :hint="reservation.status === 'DRAFT' && !reservation.guest_id ? t('reservation.bookerDraftHint') : undefined" :error="fieldError('guest_id')">
              <template #default="{ id }">
                <Input :id="id" v-model="bookerQuery" name="booker_q" type="search" :placeholder="t('newReservation.bookerPlaceholder')" @keydown.enter.prevent="findBooker" />
              </template>
            </FormField>
            <Button type="button" variant="outline" data-testid="find-booker" @click="findBooker">{{ t('newReservation.find') }}</Button>
          </div>
          <ErrorNotice :error="bookerError" class="mt-2" />
          <ul v-if="bookerResults.length" class="m-0 mt-2 flex list-none flex-wrap gap-2 p-0">
            <li v-for="g in bookerResults" :key="g.id">
              <Button type="button" variant="outline" size="sm" :data-testid="`booker-${g.code}`" @click="pickBooker(g)">{{ g.code }} · {{ guestLabel(g) }}</Button>
            </li>
          </ul>
        </div>
        <FormField :label="t('reservation.company')" :error="fieldError('company_id')">
          <template #default="{ id }">
            <NativeSelect :id="id" v-model.number="header.company_id" name="company_id">
              <option :value="0">{{ t('reservation.noCompany') }}</option>
              <option v-for="c in companies" :key="c.id" :value="c.id">{{ c.name }}</option>
              <option v-if="reservation.company_id && !companies.some((c) => c.id === reservation.company_id)" :value="reservation.company_id">{{ reservation.company_name }}</option>
            </NativeSelect>
          </template>
        </FormField>
        <FormField :label="t('reservation.specialRequest')" :error="fieldError('special_request')">
          <template #default="{ id }"><Input :id="id" v-model="header.special_request" name="special_request" maxlength="2000" /></template>
        </FormField>
        <FormField class="sm:col-span-2" :label="t('reservation.remarks')" :error="fieldError('remarks')">
          <template #default="{ id }"><Input :id="id" v-model="header.remarks" name="edit_remarks" maxlength="2000" /></template>
        </FormField>
        <div class="flex justify-end sm:col-span-2">
          <Button type="submit" size="sm" :disabled="busy || !Object.keys(headerChanges()).length" data-testid="save-header-fields">{{ t('common.save') }}</Button>
        </div>
      </form>

      <div class="sticky -bottom-5 -mx-5 mt-4 flex justify-end border-t border-border bg-card px-5 py-3">
        <Button type="button" variant="outline" data-testid="edit-close" @click="open = false">{{ t('common.close') }}</Button>
      </div>
    </DialogContent>
  </Dialog>
</template>
