<script setup lang="ts">
import { statusText } from '@/utils/status'
import { computed, nextTick, onMounted, ref, watch } from 'vue'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { Arrival, CheckInResult } from '@/api/types'
import { useCheckIn } from '@/composables/useCheckIn'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Combobox } from '@/components/ui/combobox'
import { NativeSelect } from '@/components/ui/native-select'
import ErrorNotice from '@/components/app/ErrorNotice.vue'
import FormField from '@/components/app/FormField.vue'
import { focusProgrammatically } from '@/lib/focus'
import { t } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import { CHECKIN_STEPS, DEPOSIT_METHODS, nextReadyArrival, nightsOf } from '@/utils/checkin'
import { documentPath, openPdf } from '@/utils/documents'
import { formatDate, formatMoney } from '@/utils/format'

/**
 * The check-in of one arrival in four steps (guest, room, deposit, done) inside one sheet. Enter goes to the next step, Esc (or the step in the header) goes back and nothing typed is lost:
 * all the state lives in `useCheckIn`. The deposit is taken, then the check-in is made, each its own request (see the composable for what a failure in between leaves).
 */
const props = withDefaults(defineProps<{ arrival: Arrival; queue?: Arrival[]; done?: ReadonlySet<number> }>(), { queue: () => [], done: () => new Set<number>() })
const emit = defineEmits<{ done: [result: CheckInResult]; cancel: []; next: [arrival: Arrival] }>()

const auth = useAuthStore()
const property = usePropertyStore()
const ci = useCheckIn(props.arrival)
const root = ref<HTMLFormElement | null>(null)
const pid = computed(() => property.currentId)
const fieldError = (field: string) => ci.error.value?.fieldMessage(field)
const nights = computed(() => nightsOf(props.arrival))
const upNext = computed(() => nextReadyArrival(props.queue, props.arrival.reservation_room_id, props.done))
const stepIndex = (s: (typeof CHECKIN_STEPS)[number]): number => CHECKIN_STEPS.indexOf(s)
const canResendEmail = computed(() => emailsEnabled.value && !!ci.form.email.trim() && auth.can('reservation.update', pid.value))

const guestName = computed(() => [ci.form.first_name, ci.form.last_name].filter(Boolean).join(' ') || props.arrival.guest_name || props.arrival.confirmation_number)
const roomNumber = computed(() => ci.result.value?.stay_room.room_number ?? ci.selected.value?.room_number ?? '')

/** The sheet closes on Esc at the first step and goes back at the others. A list that is open (the room picker) takes the key first. */
function escape(event: Event): void {
  const open = document.activeElement?.getAttribute('aria-expanded') === 'true'
  if (ci.canGoBack.value && !open) {
    event.preventDefault()
    ci.back()
  }
}
defineExpose({ escape })

/** Puts the cursor where the step is done: on the button when the step is complete already (so Enter goes on), else on the field that is needed. */
async function focusStep(): Promise<void> {
  await nextTick()
  const el = root.value
  if (!el) return
  const pick = (sel: string) => el.querySelector<HTMLElement>(`${sel}:not([disabled])`)
  const target = ci.step.value === 'room' ? pick('[data-autofocus]') ?? pick('[data-primary]') : ci.step.value === 'deposit' ? pick('[data-autofocus]') ?? pick('[data-primary]') : ci.step.value === 'done' ? pick('[data-primary]') : ci.guestId.value === null ? pick('[data-autofocus]') : pick('[data-primary]')
  if (target) focusProgrammatically(target)
}

onMounted(async () => {
  await ci.init()
  await focusStep()
})
watch(() => ci.step.value, () => void focusStep())

async function onSubmit(): Promise<void> {
  await ci.next()
  if (ci.result.value && ci.step.value === 'done') emit('done', ci.result.value)
}

// ---- the end: documents and e-mail
const emailsEnabled = ref(false)
const emailNotice = ref('')
const docError = ref<ApiError | null>(null)
watch(() => ci.step.value, async (s) => {
  if (s !== 'done' || pid.value === null) return
  try {
    const { data } = await api.GET('/api/v1/properties/{propertyId}/reservations/{id}/emails', { params: { path: { propertyId: pid.value, id: props.arrival.reservation_id } } })
    emailsEnabled.value = !!data?.enabled
  } catch {
    emailsEnabled.value = false // the e-mail is an extra: without the answer its button is simply not offered
  }
})

async function print(path: string): Promise<void> {
  docError.value = null
  try {
    await openPdf(path)
  } catch (e) {
    docError.value = e instanceof ApiError ? e : null
  }
}

async function sendEmail(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null) return
  docError.value = null
  emailNotice.value = ''
  try {
    await api.POST('/api/v1/properties/{propertyId}/reservations/{id}/emails', { params: { path: { propertyId, id: props.arrival.reservation_id } } })
    emailNotice.value = t('frontDesk.checkIn.emailQueued')
  } catch (e) {
    docError.value = e instanceof ApiError ? e : null
  }
}

const hintText = (h: 'identity' | 'phone' | 'passport'): string => t(`frontDesk.checkIn.hint.${h}`)
const lastVisit = computed(() => {
  const s = ci.lastStay.value
  return s ? t('frontDesk.checkIn.lastVisit', { n: ci.stays.value.length, date: formatDate(s.departure_date), property: s.property_name }, ci.stays.value.length) : ''
})
</script>

<template>
  <form ref="root" class="flex flex-col gap-4" novalidate :data-testid="`checkin-${arrival.reservation_room_id}`" @submit.prevent="onSubmit">
    <ol class="m-0 grid list-none grid-cols-4 gap-2 p-0" :aria-label="t('frontDesk.checkIn.steps')" data-testid="checkin-steps">
      <li v-for="s in CHECKIN_STEPS" :key="s" class="flex flex-col gap-1.5">
        <span class="h-1 rounded-sm" :class="stepIndex(s) <= ci.reached.value ? 'bg-primary' : 'bg-border'" />
        <button
          type="button" class="border-0 bg-transparent p-0 text-left text-sm disabled:cursor-default"
          :class="ci.step.value === s ? 'font-bold text-foreground' : 'text-muted-foreground'"
          :aria-current="ci.step.value === s ? 'step' : undefined" :data-testid="`checkin-step-${s}`"
          :disabled="stepIndex(s) > ci.reached.value || (ci.finished.value && s !== 'done')" @click="ci.go(s)"
        >{{ t(`frontDesk.checkIn.step.${s}`) }}</button>
      </li>
    </ol>

    <ErrorNotice :error="ci.error.value" data-testid="checkin-error" />

    <!-- 1. guest -->
    <section v-if="ci.step.value === 'guest'" class="flex flex-col gap-4" data-testid="step-guest">
      <h3 class="m-0 text-lg font-bold">{{ t('frontDesk.checkIn.guestTitle') }}</h3>

      <template v-if="ci.guestId.value === null">
        <p class="alert warning m-0" data-testid="no-guest">{{ t('frontDesk.checkIn.noGuest') }}</p>
        <FormField :label="t('frontDesk.checkIn.findGuest')">
          <template #default="{ id }">
            <div class="flex gap-2">
              <Input :id="id" v-model="ci.query.value" name="guest_q" type="search" data-autofocus :placeholder="t('frontDesk.checkIn.findPlaceholder')" @keydown.enter.prevent="ci.search" />
              <Button type="button" variant="outline" data-testid="guest-search" @click="ci.search">{{ t('common.search') }}</Button>
            </div>
          </template>
        </FormField>
        <ul v-if="ci.results.value.length" class="m-0 flex list-none flex-col gap-1 p-0">
          <li v-for="g in ci.results.value" :key="g.id">
            <Button type="button" variant="outline" class="h-auto w-full justify-start py-2 text-left" :data-testid="`guest-result-${g.id}`" @click="ci.pick(g)">
              {{ [g.first_name, g.last_name].filter(Boolean).join(' ') }} <span class="ml-2 text-xs text-muted-foreground">{{ g.code }}</span>
            </Button>
          </li>
        </ul>
        <p v-else-if="ci.searched.value" class="m-0 text-sm text-muted-foreground" data-testid="guest-none">{{ t('frontDesk.checkIn.noGuestFound') }}</p>
        <div v-if="ci.canWriteGuest.value" class="grid gap-4 sm:grid-cols-2">
          <FormField :label="t('guestFields.first_name')"><template #default="{ id }"><Input :id="id" v-model="ci.newGuest.first_name" name="new_first_name" /></template></FormField>
          <FormField :label="t('guestFields.last_name')" :error="fieldError('last_name')"><template #default="{ id, invalid }"><Input :id="id" v-model="ci.newGuest.last_name" name="new_last_name" :aria-invalid="invalid" /></template></FormField>
        </div>
      </template>

      <template v-else>
        <p v-if="lastVisit" class="m-0 rounded-lg bg-accent px-3 py-2 text-sm text-accent-foreground" data-testid="guest-history">{{ lastVisit }} {{ t('frontDesk.checkIn.justCheck') }}</p>
        <div class="grid gap-4 sm:grid-cols-2">
          <FormField v-for="f in ['first_name', 'last_name', 'id_type', 'id_number', 'phone', 'email', 'nationality'] as const" :key="f" :label="t(`guestFields.${f}`)" :error="fieldError(f)"
            :hint="f === 'nationality' ? t('guestFields.isoHint') : undefined">
            <template #default="{ id, invalid }">
              <Input :id="id" v-model="ci.form[f]" :name="f" :maxlength="f === 'nationality' ? 2 : undefined" :disabled="!ci.guest.value?.can_edit" :aria-invalid="invalid" />
            </template>
          </FormField>
        </div>
        <ul v-if="ci.hints.value.length" class="m-0 flex list-none flex-col gap-1.5 p-0" data-testid="guest-hints">
          <li
            v-for="h in ci.hints.value" :key="h" :data-testid="`hint-${h}`" role="note"
            class="m-0 text-sm" :class="h === 'passport' ? 'rounded-lg border border-destructive bg-destructive/10 px-3 py-2 font-semibold text-destructive' : 'alert warning'"
          >{{ hintText(h) }}</li>
        </ul>
        <p v-if="ci.guest.value && !ci.guest.value.can_edit" class="m-0 text-sm text-muted-foreground" data-testid="guest-readonly">{{ t('guest.readOnly') }}</p>
      </template>
    </section>

    <!-- 2. room -->
    <section v-else-if="ci.step.value === 'room'" class="flex flex-col gap-4" data-testid="step-room">
      <h3 class="m-0 text-lg font-bold">{{ t('frontDesk.checkIn.roomTitle', { type: arrival.room_type_name || arrival.room_type_code }) }}</h3>
      <div class="grid gap-4 sm:grid-cols-2">
        <FormField :label="t('frontDesk.checkIn.roomType')">
          <template #default="{ id }">
            <NativeSelect :id="id" v-model.number="ci.typeId.value" name="room_type" @change="ci.loadRooms">
              <option v-for="ty in ci.activeTypes.value" :key="ty.id" :value="ty.id">{{ ty.code }} {{ ty.id === arrival.room_type_id ? t('frontDesk.checkIn.booked') : t('frontDesk.checkIn.upgrade') }}</option>
            </NativeSelect>
          </template>
        </FormField>
        <FormField :label="t('frontDesk.checkIn.room')" :error="fieldError('room_id')">
          <template #default="{ id, invalid }">
            <Combobox :id="id" v-model="ci.roomId.value" data-autofocus :open-on-focus="false" name="room" :disabled="!ci.rooms.value.length" :aria-invalid="invalid" :options="ci.rooms.value.map((r) => ({ value: r.room_id, label: ci.roomLabel(r) }))" />
            <small v-if="!ci.rooms.value.length" class="text-xs text-muted-foreground" data-testid="no-rooms">{{ t('frontDesk.checkIn.noFreeRoom') }}</small>
          </template>
        </FormField>
        <FormField :label="t('frontDesk.checkIn.adults')" :error="fieldError('adult_count')">
          <template #default="{ id, invalid }"><Input :id="id" v-model.number="ci.party.adults" name="adults" type="number" min="1" :aria-invalid="invalid" /></template>
        </FormField>
        <FormField :label="t('frontDesk.checkIn.children')">
          <template #default="{ id }"><Input :id="id" v-model.number="ci.party.children" name="children" type="number" min="0" /></template>
        </FormField>
      </div>
      <p v-if="ci.isUpgrade.value" class="m-0 text-sm text-muted-foreground" data-testid="upgrade-note">{{ t('frontDesk.checkIn.upgradeNote') }}</p>

      <div v-if="ci.notReady.value" class="alert warning m-0" data-testid="not-ready">
        {{ t('frontDesk.checkIn.notReady', { room: ci.selected.value?.room_number ?? '', status: statusText(ci.selected.value?.housekeeping_status), need: ci.requiresInspection.value ? t('frontDesk.checkIn.needInspected') : t('frontDesk.checkIn.needClean') }) }}
        <template v-if="ci.canOverride.value">
          <label class="mt-2 flex items-center gap-2 text-sm">
            <input v-model="ci.party.override" type="checkbox" name="override" />
            <span>{{ t('frontDesk.checkIn.anyway') }}</span>
          </label>
          <FormField v-if="ci.party.override" class="mt-2" :label="t('frontDesk.checkIn.reason')" :error="fieldError('override_reason')">
            <template #default="{ id, invalid }"><Input :id="id" v-model="ci.party.reason" name="override_reason" maxlength="500" :aria-invalid="invalid" /></template>
          </FormField>
        </template>
      </div>
    </section>

    <!-- 3. deposit -->
    <section v-else-if="ci.step.value === 'deposit'" class="flex flex-col gap-4" data-testid="step-deposit">
      <h3 class="m-0 text-lg font-bold">{{ t('frontDesk.checkIn.depositTitle') }}</h3>
      <div class="grid gap-3 sm:grid-cols-2">
        <div class="rounded-xl border border-border p-3.5" data-testid="deposit-estimate">
          <div class="text-sm text-muted-foreground">{{ ci.estimate.value ? t('frontDesk.checkIn.estimateStay', { n: nights }, nights) : t('frontDesk.checkIn.estimateMissing') }}</div>
          <strong class="text-xl">{{ ci.estimate.value ? formatMoney(ci.estimate.value.total) : '—' }}</strong>
          <div v-if="ci.estimate.value" class="text-xs text-muted-foreground">{{ t('frontDesk.checkIn.estimateTaxed') }}</div>
        </div>
        <div class="rounded-xl border border-border p-3.5" data-testid="deposit-incidental">
          <div class="text-sm text-muted-foreground">{{ t('frontDesk.checkIn.incidental') }}</div>
          <strong class="text-xl">{{ formatMoney(ci.incidental.value) }}</strong>
          <div v-if="Number(ci.incidental.value) === 0" class="text-xs text-muted-foreground">{{ t('frontDesk.checkIn.incidentalNone') }}</div>
        </div>
      </div>
      <p v-if="Number(ci.heldBefore.value) > 0" class="m-0 text-sm text-muted-foreground" data-testid="deposit-held">{{ t('frontDesk.checkIn.alreadyHeld', { amount: formatMoney(ci.heldBefore.value) }) }}</p>

      <div v-if="ci.failedAfterDeposit.value" class="alert warning m-0" role="alert" data-testid="checkin-failed-after-deposit">
        {{ t('frontDesk.checkIn.failedAfterDeposit', { number: ci.posted.value?.number ?? '', amount: formatMoney(ci.posted.value?.amount) }) }}
      </div>
      <p v-else-if="ci.posted.value" class="alert m-0" data-testid="deposit-posted">{{ t('frontDesk.checkIn.depositPosted', { number: ci.posted.value.number, amount: formatMoney(ci.posted.value.amount), method: t(`payMethod.${ci.posted.value.method}`) }) }}</p>

      <p v-if="ci.company.value" class="m-0 rounded-lg bg-accent px-3 py-2 text-sm text-accent-foreground" data-testid="deposit-company">{{ t('frontDesk.checkIn.companyBilled', { company: ci.company.value.name }) }}</p>
      <p v-else-if="ci.covered.value" class="m-0 rounded-lg bg-accent px-3 py-2 text-sm text-accent-foreground" data-testid="deposit-covered">{{ t('frontDesk.checkIn.covered') }}</p>
      <p v-else-if="!ci.canTakeDeposit.value" class="m-0 text-sm text-muted-foreground" data-testid="deposit-nopermission">{{ t('frontDesk.checkIn.noDepositRight') }}</p>

      <template v-if="ci.canTakeDeposit.value && !ci.posted.value">
        <FormField :label="t('frontDesk.checkIn.received')" :error="fieldError('amount')">
          <template #default="{ id, invalid }">
            <Input :id="id" v-model="ci.dep.amount" name="deposit_amount" inputmode="decimal" autocomplete="off" data-autofocus class="h-12 text-xl font-bold" :aria-invalid="invalid" @input="ci.dep.touched = true" />
          </template>
        </FormField>
        <div role="group" :aria-label="t('frontDesk.checkIn.method')" class="grid grid-cols-2 gap-2 sm:grid-cols-4">
          <button
            v-for="m in DEPOSIT_METHODS" :key="m" type="button" :aria-pressed="ci.dep.method === m" :data-testid="`deposit-method-${m}`"
            class="min-h-12 rounded-lg border text-sm font-bold" :class="ci.dep.method === m ? 'border-primary bg-primary text-primary-foreground' : 'border-border bg-card'"
            @click="ci.dep.method = m"
          >{{ t(`payMethod.${m}`) }}</button>
        </div>
        <FormField v-if="ci.dep.method !== 'CASH'" :label="t('frontDesk.checkIn.reference')" :error="fieldError('reference_number')">
          <template #default="{ id }"><Input :id="id" v-model="ci.dep.reference" name="deposit_reference" maxlength="100" /></template>
        </FormField>
      </template>
    </section>

    <!-- 4. done -->
    <section v-else class="flex flex-col items-start gap-4" data-testid="step-done">
      <span class="grid size-14 place-items-center rounded-full bg-success text-white" aria-hidden="true">✓</span>
      <h3 class="m-0 text-xl font-extrabold" data-testid="done-title">{{ t('frontDesk.checkIn.doneTitle', { guest: guestName, room: roomNumber }) }}</h3>
      <p class="m-0 text-sm leading-relaxed" data-testid="done-summary">
        {{ t('frontDesk.checkIn.doneSummary', { stay: ci.result.value?.stay.stay_number ?? '', folio: ci.result.value?.folio?.folio_number ?? '' }) }}
        <template v-if="ci.posted.value"> {{ t('frontDesk.checkIn.doneDeposit', { amount: formatMoney(ci.posted.value.amount), method: t(`payMethod.${ci.posted.value.method}`) }) }}</template>
        <template v-else> {{ t('frontDesk.checkIn.doneNoDeposit') }}</template>
      </p>
      <ErrorNotice :error="docError" inline data-testid="done-error" />
      <p v-if="emailNotice" class="alert m-0" role="status" data-testid="done-email-notice">{{ emailNotice }}</p>
      <div class="flex flex-wrap gap-2">
        <Button v-if="ci.result.value" type="button" variant="outline" data-testid="done-card" @click="print(documentPath.registrationCard(pid ?? 0, ci.result.value.stay.id))">{{ t('frontDesk.checkIn.printCard') }}</Button>
        <Button v-if="ci.posted.value" type="button" variant="outline" data-testid="done-receipt" @click="print(documentPath.receipt(pid ?? 0, ci.posted.value.paymentId))">{{ t('frontDesk.checkIn.printReceipt') }}</Button>
        <Button v-if="canResendEmail" type="button" variant="outline" data-testid="done-email" @click="sendEmail">{{ t('frontDesk.checkIn.sendEmail') }}</Button>
      </div>
    </section>

    <div class="flex flex-wrap justify-end gap-2">
      <template v-if="ci.step.value === 'done'">
        <Button type="button" variant="outline" data-testid="checkin-close" @click="emit('cancel')">{{ t('common.close') }}</Button>
        <Button v-if="upNext" type="button" data-primary data-testid="checkin-next-arrival" @click="emit('next', upNext)">{{ t('frontDesk.checkIn.nextArrival', { guest: upNext.guest_name || upNext.confirmation_number }) }}</Button>
      </template>
      <template v-else>
        <Button v-if="ci.canGoBack.value" type="button" variant="outline" data-testid="checkin-back" @click="ci.back">{{ t('frontDesk.checkIn.back') }}</Button>
        <Button v-else type="button" variant="outline" @click="emit('cancel')">{{ t('common.cancel') }}</Button>
        <Button
          v-if="ci.step.value === 'deposit' && ci.depositRequired.value && !ci.posted.value" type="button" variant="outline" :disabled="ci.busy.value || !ci.roomOk.value"
          data-testid="checkin-no-deposit" @click="ci.finish(true).then(() => ci.result.value && emit('done', ci.result.value))"
        >{{ t('frontDesk.checkIn.withoutDeposit') }}</Button>
        <Button
          v-if="ci.step.value === 'deposit'" type="submit" data-primary data-testid="checkin-submit" :disabled="ci.busy.value || !ci.canFinish.value"
        >{{ ci.failedAfterDeposit.value ? t('frontDesk.checkIn.retryCheckIn') : ci.willTake.value && !ci.posted.value ? t('frontDesk.checkIn.takeAndCheckIn') : t('frontDesk.checkIn.submit') }}</Button>
        <Button
          v-else type="submit" data-primary data-testid="checkin-next"
          :disabled="ci.busy.value || (ci.step.value === 'guest' ? ci.guestId.value === null && !ci.newGuest.last_name.trim() : !ci.roomOk.value)"
        >{{ t('frontDesk.checkIn.next') }}</Button>
      </template>
    </div>
  </form>
</template>
