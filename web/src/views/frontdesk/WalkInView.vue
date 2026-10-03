<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import { api } from '@/api/client'
import { fetchAll } from '@/api/paging'
import { ApiError } from '@/api/problem'
import type { Approval, FreeRoom, Guest, RatePlan, RoomType } from '@/api/types'
import ApprovalDialog from '@/components/ApprovalDialog.vue'
import FormField from '@/components/app/FormField.vue'
import PageHeader from '@/components/app/PageHeader.vue'
import RateOverrideSection, { type OverrideNight, type RateChange } from '@/components/RateOverrideSection.vue'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Combobox } from '@/components/ui/combobox'
import { NativeSelect } from '@/components/ui/native-select'
import { t } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import { addDays } from '@/utils/dates'
import { guestLabel, newIdempotencyKey } from '@/utils/reservations'

const auth = useAuthStore()
const property = usePropertyStore()
const router = useRouter()

const types = ref<RoomType[]>([])
const plans = ref<RatePlan[]>([])
const rooms = ref<FreeRoom[]>([])
const guestQuery = ref('')
const guestResults = ref<Guest[]>([])
const guest = ref<Guest | null>(null)
const newGuest = reactive({ first_name: '', last_name: '' })
const form = reactive({ typeId: 0, roomId: null as number | null, planId: 0, departure: '', adults: 1, children: 0, override: false, reason: '', occupancyReason: '' })
const busy = ref(false)
// A rate change: the nights whose price is changed, the reason, and the approval (asked for when the person cannot approve).
const rate = ref<RateChange>({ overrides: [], reason: '' })
const approving = ref(false)
const dialogError = ref<ApiError | null>(null)
const planNights = ref<OverrideNight[]>([])
const error = ref<ApiError | null>(null)
let key = newIdempotencyKey()

const pid = computed(() => property.currentId)
const businessDate = computed(() => property.clock?.business_date ?? '')
const allowed = computed(() => auth.can('frontdesk.checkin', pid.value) && auth.can('reservation.create', pid.value))
const requiresInspection = computed(() => property.current?.require_room_inspection_for_checkin ?? false)
const activeTypes = computed(() => types.value.filter((t) => t.is_active))
// Complimentary and house use plans are offered only to who may book them.
const canGiveFree = computed(() => auth.can('reservation.complimentary', pid.value))
const activePlans = computed(() => plans.value.filter((p) => p.is_active && (p.occupancy_kind === 'PAID' || canGiveFree.value)))
const chosenPlan = computed(() => plans.value.find((p) => p.id === form.planId))
const selected = computed(() => rooms.value.find((r) => r.room_id === form.roomId))
const isReady = (s: string) => (requiresInspection.value ? s === 'INSPECTED' : s === 'CLEAN' || s === 'INSPECTED')
const notReady = computed(() => !!selected.value && !isReady(selected.value.housekeeping_status))
const canOverride = computed(() => auth.can('frontdesk.checkin_unready_room', pid.value))
const canChangeRate = computed(() => auth.can('reservation.override_rate', pid.value))
const canApprove = computed(() => auth.can('reservation.override_rate_approve', pid.value))
const canApproveFree = computed(() => auth.can('reservation.complimentary_approve', pid.value))
const exceedQuota = ref(false)
const isFree = computed(() => !!chosenPlan.value && chosenPlan.value.occupancy_kind !== 'PAID')
/** The monthly quota of free nights this walk-in would go over: what the server says, to show and to confirm. */
const quotaOver = computed(() => {
  const e = error.value
  if (e?.code !== 'FREE_NIGHT_QUOTA_EXCEEDED') return null
  const c = e.context as Record<string, unknown>
  return { kind: String(c.occupancy_kind ?? ''), quota: Number(c.quota), month: String(c.month ?? '').slice(0, 7), used: Number(c.used), requested: Number(c.requested) }
})
const fieldError = (field: string) => error.value?.fieldMessage(field)

async function loadBase(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null || !allowed.value) return
  try {
    const path = { path: { propertyId } }
    ;[types.value, plans.value] = await Promise.all([
      fetchAll((cursor) => api.GET('/api/v1/properties/{propertyId}/room-types', { params: { ...path, query: { limit: 200, cursor } } })),
      fetchAll((cursor) => api.GET('/api/v1/properties/{propertyId}/rate-plans', { params: { ...path, query: { limit: 200, cursor } } })),
    ])
    form.typeId ||= activeTypes.value[0]?.id ?? 0
    form.planId ||= activePlans.value[0]?.id ?? 0
    await loadRooms()
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  }
}

async function loadRooms(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null || !form.typeId || !businessDate.value || !form.departure) return
  try {
    const { data } = await api.GET('/api/v1/properties/{propertyId}/availability/rooms', {
      params: { path: { propertyId }, query: { room_type_id: form.typeId, arrival: businessDate.value, departure: form.departure } },
    })
    rooms.value = data?.data ?? []
    if (!rooms.value.some((r) => r.room_id === form.roomId)) form.roomId = rooms.value[0]?.room_id ?? null
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  }
}

async function findGuests(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null || !guestQuery.value.trim()) return
  try {
    const { data } = await api.GET('/api/v1/guests', { params: { query: { q: guestQuery.value.trim(), property_id: propertyId, limit: 10 } } })
    guestResults.value = data?.data ?? []
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  }
}

/** The standard price of each night of the chosen room type and plan, for the rate editor. */
async function loadNights(): Promise<void> {
  planNights.value = []
  const propertyId = pid.value
  if (propertyId === null || !canChangeRate.value || !form.typeId || !form.planId || !businessDate.value || !form.departure || form.departure <= businessDate.value) return
  try {
    const { data } = await api.GET('/api/v1/properties/{propertyId}/availability', {
      params: { path: { propertyId }, query: { arrival: businessDate.value, departure: form.departure, adults: Math.max(1, form.adults), children: form.children } },
    })
    const offer = data?.room_types.find((ty) => ty.room_type_id === form.typeId)?.rate_plans.find((p) => p.id === form.planId)
    planNights.value = (offer?.nightly ?? []).map((n) => ({ date: n.date, standard: n.amount, current: n.amount }))
  } catch {
    planNights.value = [] // the editor is optional: without prices it stays away
  }
}

/** The button: a price change that the person cannot approve asks for an approver first. */
function onSubmit(): void {
  if ((rate.value.overrides.length && !canApprove.value) || (isFree.value && !canApproveFree.value)) {
    dialogError.value = null
    approving.value = true
    return
  }
  void submit()
}

async function submit(approval?: Approval): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null || form.roomId === null) return
  busy.value = true
  error.value = null
  dialogError.value = null
  try {
    const { data } = await api.POST('/api/v1/properties/{propertyId}/walk-ins', {
      params: { path: { propertyId }, header: { 'Idempotency-Key': key } },
      body: {
        guest_id: guest.value?.id, new_guest: guest.value ? undefined : { first_name: newGuest.first_name || undefined, last_name: newGuest.last_name },
        room_id: form.roomId, rate_plan_id: form.planId, departure_date: form.departure, adult_count: form.adults, child_count: form.children,
        override_room_not_ready: form.override, override_reason: form.override ? form.reason : undefined,
        occupancy_reason: chosenPlan.value && chosenPlan.value.occupancy_kind !== 'PAID' ? form.occupancyReason : undefined,
        nightly_overrides: rate.value.overrides.length ? rate.value.overrides : undefined,
        rate_override_reason: rate.value.overrides.length ? rate.value.reason : undefined,
        rate_override_approval: rate.value.overrides.length ? approval : undefined,
        occupancy_approval: isFree.value ? approval : undefined,
        exceed_free_quota: exceedQuota.value || undefined,
      },
    })
    if (data) await router.push(`/stays/${data.stay.id}`)
  } catch (e) {
    if (e instanceof ApiError && e.code === 'FREE_NIGHT_QUOTA_EXCEEDED') {
      approving.value = false // the page shows the quota and asks whether to go over it
      error.value = e
    } else if (approval) {
      dialogError.value = e instanceof ApiError ? e : null
    } else {
      error.value = e instanceof ApiError ? e : null
    }
    if (e instanceof ApiError) key = newIdempotencyKey()
  } finally {
    busy.value = false
  }
}

watch(() => property.currentId, () => void loadBase(), { immediate: true })
watch(() => [form.typeId, form.planId, form.departure, form.adults, form.children, businessDate.value, canChangeRate.value], () => void loadNights(), { immediate: true })
watch(businessDate, (bd) => {
  if (bd && !form.departure) {
    form.departure = addDays(bd, 1)
    void loadRooms()
  }
}, { immediate: true })
</script>

<template>
  <PageHeader :title="t('walkIn.title')">
    <template #actions><RouterLink to="/arrivals" class="text-sm text-primary hover:underline">{{ t('walkIn.back') }}</RouterLink></template>
  </PageHeader>

  <p v-if="error" class="alert" role="alert" data-testid="form-error">{{ error.message }} <code>{{ error.code }}</code></p>
  <p v-if="pid === null" class="muted">{{ t('setup.selectProperty') }}</p>
  <p v-else-if="!allowed" class="muted" data-testid="no-access">{{ t('walkIn.noAccess') }}</p>

  <Card v-else>
    <form novalidate data-testid="walkin-form" @submit.prevent="onSubmit">
      <CardHeader><CardTitle>{{ t('walkIn.stay') }}</CardTitle></CardHeader>
      <CardContent>
        <div class="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
          <FormField :label="t('walkIn.departure')" :error="fieldError('departure_date')">
            <template #default="{ id, invalid }"><Input :id="id" v-model="form.departure" name="departure" type="date" :min="businessDate" :aria-invalid="invalid" @change="loadRooms" /></template>
          </FormField>
          <FormField :label="t('walkIn.roomType')">
            <template #default="{ id }">
              <NativeSelect :id="id" v-model.number="form.typeId" name="room_type" @change="loadRooms">
                <option v-for="rt in activeTypes" :key="rt.id" :value="rt.id">{{ rt.code }} · {{ rt.name }}</option>
              </NativeSelect>
            </template>
          </FormField>
          <FormField :label="t('walkIn.room')" :error="fieldError('room_id')">
            <template #default="{ id, invalid }">
              <Combobox :id="id" v-model="form.roomId" name="room" :disabled="!rooms.length" :aria-invalid="invalid" :options="[...rooms.map((r) => ({ value: r.room_id, label: `${r.room_number} · ${r.housekeeping_status}${r.bed_type_name ? ` · ${r.bed_type_name}` : ''}${isReady(r.housekeeping_status) ? '' : t('walkIn.notReadyTag')}` }))]" />
              <small v-if="!rooms.length" class="text-xs text-muted-foreground" data-testid="no-rooms">{{ t('walkIn.noRooms') }}</small>
            </template>
          </FormField>
          <FormField :label="t('walkIn.ratePlan')">
            <template #default="{ id }">
              <NativeSelect :id="id" v-model.number="form.planId" name="rate_plan">
                <option v-for="p in activePlans" :key="p.id" :value="p.id">{{ p.code }} · {{ p.name }}{{ p.occupancy_kind !== 'PAID' ? ` (${t(`occupancy.kind_${p.occupancy_kind}`)})` : '' }}</option>
              </NativeSelect>
            </template>
          </FormField>
          <FormField v-if="chosenPlan && chosenPlan.occupancy_kind !== 'PAID'" :label="t('occupancy.reason')" :error="fieldError('rooms[0].occupancy_reason')">
            <template #default="{ id, invalid }"><Input :id="id" v-model="form.occupancyReason" name="occupancy_reason" :placeholder="t('occupancy.reasonHint')" :aria-invalid="invalid" /></template>
          </FormField>
          <FormField :label="t('walkIn.adults')" :error="fieldError('adult_count')">
            <template #default="{ id, invalid }"><Input :id="id" v-model.number="form.adults" name="adults" type="number" min="1" :aria-invalid="invalid" /></template>
          </FormField>
          <FormField :label="t('walkIn.children')">
            <template #default="{ id }"><Input :id="id" v-model.number="form.children" name="children" type="number" min="0" /></template>
          </FormField>
        </div>

        <div v-if="notReady" class="alert warning mt-4" data-testid="not-ready">
          {{ t('walkIn.roomIs', { room: selected?.room_number ?? '', status: selected?.housekeeping_status ?? '' }) }}
          <template v-if="canOverride">
            <label class="mt-2 flex items-center gap-2 text-sm"><input v-model="form.override" type="checkbox" name="override" class="size-4 accent-primary" /><span>{{ t('walkIn.useAnyway') }}</span></label>
            <FormField v-if="form.override" class="mt-2 max-w-md" :label="t('walkIn.reason')">
              <template #default="{ id }"><Input :id="id" v-model="form.reason" name="override_reason" maxlength="500" /></template>
            </FormField>
          </template>
        </div>

        <div v-if="quotaOver" class="alert warning" role="alert" data-testid="quota-warning">
          {{ t('freeQuotas.over', { kind: t(`occupancy.kind_${quotaOver.kind}`), quota: quotaOver.quota, month: quotaOver.month, used: quotaOver.used, requested: quotaOver.requested }) }}
          <label class="mt-2 flex items-center gap-2 text-sm"><input v-model="exceedQuota" type="checkbox" name="exceed_free_quota" class="size-4 accent-primary" /><span>{{ t('freeQuotas.goOver') }}</span></label>
        </div>

        <RateOverrideSection v-if="canChangeRate && chosenPlan?.occupancy_kind === 'PAID' && planNights.length" v-model="rate" class="mt-4" :nights="planNights" />

        <h2 class="mb-2 mt-6 text-base font-semibold">{{ t('walkIn.guest') }}</h2>
        <div class="flex flex-wrap items-end gap-3">
          <FormField class="min-w-64 flex-1" :label="t('walkIn.findGuest')">
            <template #default="{ id }"><Input :id="id" v-model="guestQuery" name="guest_q" type="search" :placeholder="t('walkIn.findPlaceholder')" @keydown.enter.prevent="findGuests" /></template>
          </FormField>
          <Button type="button" variant="outline" data-testid="find-guest" @click="findGuests">{{ t('walkIn.find') }}</Button>
        </div>
        <ul v-if="guestResults.length" class="m-0 mt-2 flex list-none flex-wrap gap-2 p-0">
          <li v-for="g in guestResults" :key="g.id"><Button type="button" variant="outline" size="sm" :data-testid="`guest-${g.code}`" @click="guest = g; guestResults = []">{{ g.code }} · {{ guestLabel(g) }}</Button></li>
        </ul>
        <p v-if="guest" class="mb-0 mt-3 text-sm" data-testid="chosen-guest">{{ t('walkIn.chosen') }} <strong>{{ guestLabel(guest) }}</strong> <button type="button" class="cursor-pointer border-0 bg-transparent p-0 text-primary underline" @click="guest = null">{{ t('walkIn.change') }}</button></p>
        <div v-else class="mt-3 grid gap-4 sm:grid-cols-2">
          <FormField :label="t('walkIn.firstName')">
            <template #default="{ id }"><Input :id="id" v-model="newGuest.first_name" name="first_name" /></template>
          </FormField>
          <FormField :label="t('walkIn.lastName')" :error="fieldError('new_guest.last_name')">
            <template #default="{ id, invalid }"><Input :id="id" v-model="newGuest.last_name" name="last_name" :aria-invalid="invalid" /></template>
          </FormField>
        </div>
        <small v-if="fieldError('guest_id')" role="alert" class="text-xs text-destructive">{{ fieldError('guest_id') }}</small>

        <div class="mt-4 flex justify-end">
          <Button type="submit" :disabled="busy || form.roomId === null || (notReady && !form.override) || (!guest && !newGuest.last_name.trim())" data-testid="walkin-submit">{{ t('walkIn.checkIn') }}</Button>
        </div>
      </CardContent>
    </form>
  </Card>
  <ApprovalDialog v-if="approving" :title="isFree ? t('freeQuotas.approvalTitle') : t('rateOverride.approvalTitle')" :message="isFree ? t('freeQuotas.approvalMessage') : t('rateOverride.approvalMessage')" :busy="busy" :error="dialogError" @approve="(a) => submit(a)" @cancel="approving = false" />
</template>
