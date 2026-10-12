<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { NightRate, TapeBooking, TapeRoom } from '@/api/types'
import ErrorNotice from '@/components/app/ErrorNotice.vue'
import FormField from '@/components/app/FormField.vue'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { t } from '@/i18n'
import { usePropertyStore } from '@/stores/property'
import { formatDate, formatMoney } from '@/utils/format'
import { canChangeArrival, dateChange, datesVerdict, isConfirmed, nightsOf, type TapeRights } from '@/utils/tape'

/**
 * The confirmation of new dates for a booking (a pull of the right or left end of its block, or "Change dates" in the menu): always shown before anything is sent. The nights that are added are priced
 * by the server (the availability search, for the plan and room type of the line) and shown as an estimate; the nights that are taken away are shown with the price they were sold at. The client computes
 * no price and no tax. A line whose price is not the grid's (a night with a price changed by hand, a line of a company, a complimentary or house use plan) gets no figure: "the system computes the price
 * when it is saved". The endpoints are the ones that exist: the amendment of the room line for a booking to come, the change of departure of the stay for a guest in the house.
 */
const open = defineModel<boolean>('open', { required: true })
const props = defineProps<{ booking: TapeBooking | null; room: TapeRoom | null; arrival: string; departure: string; rights: TapeRights }>()
const emit = defineEmits<{ done: [message: string]; changed: [] }>()

const property = usePropertyStore()
const pid = computed(() => property.currentId)
const businessDate = computed(() => property.clock?.business_date ?? '')

const newArrival = ref('')
const newDeparture = ref('')
const version = ref<number | null>(null)
const loading = ref(false)
const busy = ref(false)
const error = ref<ApiError | null>(null)
const stale = ref(false)

interface Line { planId: number; typeId: number; adults: number; children: number; rates: NightRate[]; byHand: boolean }
const line = ref<Line | null>(null)
const estimates = ref<{ from: string; to: string; total: string | null }[]>([])
let seq = 0

const inHouse = computed(() => !!props.booking && !isConfirmed(props.booking))
const arrivalEditable = computed(() => !!props.booking && canChangeArrival(props.booking, props.rights))
const verdict = computed(() => (props.booking ? datesVerdict(props.booking, props.room, newArrival.value, newDeparture.value, businessDate.value, props.rights) : null))
const change = computed(() => (props.booking ? dateChange(props.booking, newArrival.value, newDeparture.value) : { added: [], removed: [] }))
const changed = computed(() => !!props.booking && (newArrival.value !== props.booking.arrival_date || newDeparture.value !== props.booking.departure_date))
const nightsBefore = computed(() => (props.booking ? nightsOf(props.booking.arrival_date, props.booking.departure_date) : 0))
const nightsAfter = computed(() => nightsOf(newArrival.value, newDeparture.value))
/** The nights taken away, with the price each was sold at (as the reservation stores it). */
const removedRates = computed(() => {
  const l = line.value
  if (!l) return []
  return change.value.removed.flatMap((w) => l.rates.filter((r) => r.date >= w.from && r.date < w.to))
})
const canConfirm = computed(() => !loading.value && !busy.value && version.value !== null && changed.value && verdict.value?.ok === true)
const refusal = computed(() => (verdict.value && !verdict.value.ok && changed.value ? t(`tapeChart.dates.refusal.${verdict.value.reason}`) : ''))

async function load(): Promise<void> {
  const propertyId = pid.value
  const b = props.booking
  if (propertyId === null || !b) return
  loading.value = true
  version.value = null
  line.value = null
  error.value = null
  try {
    const { data } = await api.GET('/api/v1/properties/{propertyId}/reservations/{id}', { params: { path: { propertyId, id: b.reservation_id } } })
    const l = data?.rooms.find((r) => r.id === b.reservation_room_id)
    if (l) {
      // No figure when the price of the line is not the grid's: a night changed by hand, a company's reservation, a plan that is not paid.
      const byHand = l.nightly_rates.some((n) => n.is_override) || l.occupancy_kind !== 'PAID' || !!data?.company_id
      line.value = { planId: l.rate_plan_id, typeId: l.room_type_id, adults: Math.max(1, l.adult_count), children: l.child_count, rates: l.nightly_rates, byHand }
    }
    if (inHouse.value && b.stay_id) {
      const stay = await api.GET('/api/v1/properties/{propertyId}/stays/{id}', { params: { path: { propertyId, id: b.stay_id } } })
      version.value = stay.data?.stay.version ?? null
    } else {
      version.value = data?.version ?? null
    }
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
    stale.value = true
  } finally {
    loading.value = false
  }
  await estimate()
}

/** The nights that are added, priced by the server for the plan and room type of the line. */
async function estimate(): Promise<void> {
  const propertyId = pid.value
  const l = line.value
  const mine = ++seq
  estimates.value = []
  if (propertyId === null || !l || l.byHand || verdict.value?.ok !== true) return
  const out: { from: string; to: string; total: string | null }[] = []
  for (const w of change.value.added) {
    let total: string | null = null
    try {
      const { data } = await api.GET('/api/v1/properties/{propertyId}/availability', { params: { path: { propertyId }, query: { arrival: w.from, departure: w.to, adults: l.adults, children: l.children } } })
      const offer = data?.room_types.find((ty) => ty.room_type_id === l.typeId)?.rate_plans.find((p) => p.id === l.planId)
      if (offer && offer.missing_nights === 0 && offer.bookable && offer.estimate) total = offer.estimate.total
    } catch {
      total = null // no figure: the system prices the nights when it is saved
    }
    out.push({ ...w, total })
  }
  if (mine === seq) estimates.value = out
}

watch(open, (v) => {
  if (!v || !props.booking) return
  newArrival.value = props.arrival
  newDeparture.value = props.departure
  stale.value = false
  void load()
}, { immediate: true })
watch([newArrival, newDeparture], () => void estimate())
watch(open, (v, was) => {
  if (was && !v && stale.value) emit('changed')
})

async function confirm(): Promise<void> {
  const propertyId = pid.value
  const b = props.booking
  if (propertyId === null || !b || version.value === null || !canConfirm.value) return
  busy.value = true
  error.value = null
  try {
    if (inHouse.value && b.stay_id) {
      await api.POST('/api/v1/properties/{propertyId}/stays/{id}/change-departure', {
        params: { path: { propertyId, id: b.stay_id } },
        body: { version: version.value, departure_date: newDeparture.value },
      })
    } else {
      await api.PATCH('/api/v1/properties/{propertyId}/reservations/{id}/rooms/{lineId}', {
        params: { path: { propertyId, id: b.reservation_id, lineId: b.reservation_room_id } },
        body: {
          version: version.value,
          arrival_date: newArrival.value !== b.arrival_date ? newArrival.value : undefined,
          departure_date: newDeparture.value !== b.departure_date ? newDeparture.value : undefined,
        },
      })
    }
    stale.value = false
    emit('done', t('tapeChart.datesChanged', { n: nightsAfter.value }, nightsAfter.value))
    open.value = false
    emit('changed')
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
    if (e instanceof ApiError) {
      if (e.status === 409 || e.status === 422) stale.value = true
      if (e.code === 'VERSION_CONFLICT') version.value = null
    }
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <Dialog v-model:open="open">
    <DialogContent class="top-[10vh] max-h-[85vh] max-w-lg overflow-y-auto p-5" data-testid="tape-dates-dialog">
      <DialogTitle>{{ t('tapeChart.dates.title') }}</DialogTitle>
      <DialogDescription class="mt-1" data-testid="tape-dates-who">{{ booking?.guest_name || booking?.confirmation_number }} · {{ booking?.confirmation_number }}</DialogDescription>
      <ErrorNotice :error="error" class="mt-3" data-testid="tape-error" />

      <form class="mt-3 flex flex-col gap-3" novalidate @submit.prevent="confirm">
        <div class="grid gap-3 sm:grid-cols-2">
          <FormField :label="t('tapeChart.dates.arrival')" :hint="inHouse ? t('tapeChart.dates.arrivalLocked') : undefined">
            <template #default="{ id }"><Input :id="id" v-model="newArrival" type="date" name="arrival_date" :disabled="!arrivalEditable" /></template>
          </FormField>
          <FormField :label="t('tapeChart.dates.departure')">
            <template #default="{ id }"><Input :id="id" v-model="newDeparture" type="date" name="departure_date" data-autofocus /></template>
          </FormField>
        </div>

        <dl class="m-0 grid grid-cols-[auto_1fr] gap-x-4 gap-y-1.5 rounded-lg bg-muted px-3 py-2.5 text-sm" data-testid="tape-dates-summary">
          <dt class="text-muted-foreground">{{ t('tapeChart.dates.was') }}</dt>
          <dd class="m-0">{{ booking ? formatDate(booking.arrival_date) : '' }} → {{ booking ? formatDate(booking.departure_date) : '' }} · {{ t('tapeChart.nightsN', { n: nightsBefore }, nightsBefore) }}</dd>
          <dt class="text-muted-foreground">{{ t('tapeChart.dates.becomes') }}</dt>
          <dd class="m-0 font-semibold" data-testid="tape-dates-new">{{ formatDate(newArrival) }} → {{ formatDate(newDeparture) }} · {{ t('tapeChart.nightsN', { n: Math.max(0, nightsAfter) }, Math.max(0, nightsAfter)) }}</dd>
          <template v-if="room">
            <dt class="text-muted-foreground">{{ t('tapeChart.dates.room') }}</dt>
            <dd class="m-0">{{ room.room_number }} · {{ room.room_type_code }}</dd>
          </template>
        </dl>

        <p v-if="refusal" class="alert warning m-0" role="alert" data-testid="tape-dates-refusal">{{ refusal }}</p>

        <section v-if="changed && !refusal" class="flex flex-col gap-1.5 text-sm" data-testid="tape-dates-price">
          <h3 class="m-0 text-sm font-bold">{{ t('tapeChart.dates.priceTitle') }}</h3>
          <p v-if="line?.byHand" class="m-0 text-muted-foreground" data-testid="tape-price-system">{{ t('tapeChart.dates.priceBySystem') }}</p>
          <template v-else>
            <template v-if="change.added.length">
              <p v-for="e in estimates" :key="e.from" class="m-0" data-testid="tape-price-added">
                <template v-if="e.total !== null">{{ t('tapeChart.dates.addedEstimate', { n: nightsOf(e.from, e.to), total: formatMoney(e.total) }, nightsOf(e.from, e.to)) }}</template>
                <template v-else><span class="text-muted-foreground" data-testid="tape-price-system">{{ t('tapeChart.dates.priceBySystem') }}</span></template>
              </p>
              <p v-if="loading || (!estimates.length && line)" class="m-0 text-muted-foreground">…</p>
            </template>
            <template v-if="change.removed.length">
              <p class="m-0 text-muted-foreground">{{ t('tapeChart.dates.removed') }}</p>
              <ul class="m-0 list-none p-0" data-testid="tape-price-removed">
                <li v-for="r in removedRates" :key="r.date" class="flex justify-between gap-3 tabular-nums"><span>{{ formatDate(r.date) }}</span><span>{{ formatMoney(r.amount) }}</span></li>
              </ul>
            </template>
          </template>
          <p class="m-0 text-xs text-muted-foreground">{{ t('tapeChart.dates.priceNote') }}</p>
        </section>

        <div class="flex justify-end gap-2">
          <Button type="button" variant="outline" data-testid="tape-cancel" @click="open = false">{{ t('common.cancel') }}</Button>
          <Button type="submit" data-testid="tape-confirm" :disabled="!canConfirm">{{ t('tapeChart.dates.confirm') }}</Button>
        </div>
      </form>
    </DialogContent>
  </Dialog>
</template>
