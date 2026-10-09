<script setup lang="ts">
import { Search } from 'lucide-vue-next'
import { computed, reactive, ref, watch } from 'vue'
import { RouterLink, useRoute, useRouter } from 'vue-router'
import { api } from '@/api/client'
import { fetchAll } from '@/api/paging'
import { ApiError } from '@/api/problem'
import type { Approval, AvailabilitySearch, BedType, Company, Group, Guest, PlanOffer, ReservationSource, TypeOffer, Violation } from '@/api/types'
import ApprovalDialog from '@/components/ApprovalDialog.vue'
import DataTable, { type Column } from '@/components/app/DataTable.vue'
import FormField from '@/components/app/FormField.vue'
import PageHeader from '@/components/app/PageHeader.vue'
import RateOverrideSection, { type OverrideNight, type RateChange } from '@/components/RateOverrideSection.vue'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Combobox } from '@/components/ui/combobox'
import { NativeSelect } from '@/components/ui/native-select'
import { vAutofocus } from '@/directives/autofocus'
import { t } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import { addDays } from '@/utils/dates'
import { guestLabel, newIdempotencyKey } from '@/utils/reservations'

const auth = useAuthStore()
const property = usePropertyStore()
const router = useRouter()
const route = useRoute()

const SOURCES: ReservationSource[] = ['PHONE', 'EMAIL', 'WALK_IN', 'WEBSITE', 'OTA', 'AGENT', 'OTHER']

const search = reactive({ arrival: '', departure: '', adults: 2, children: 0 })
const result = ref<AvailabilitySearch | null>(null)
const searching = ref(false)
const picked = ref<{ type: TypeOffer; plan: PlanOffer } | null>(null)
const guestQuery = ref('')
const guestResults = ref<Guest[]>([])
const guest = ref<Guest | null>(null)
const beds = ref<BedType[]>([])
const form = reactive({ source: 'PHONE' as ReservationSource, bedTypeId: 0, bedLocked: false, occupancyReason: '', confirm: true, remarks: '', companyId: 0, groupId: Number(route.query.group) || 0 })
const companies = ref<Company[]>([])
const groups = ref<Group[]>([])
/** The variants the picked room type offers, with what is left and the supplement of the picked plan; falls back to the catalogue of beds. */
interface BedChoice { id: number; label: string; left: number | null; soldOut: boolean; supplement: string }
const bedChoices = computed<BedChoice[]>(() => {
  const pk = picked.value
  if (!pk) return []
  const variants = pk.type.beds ?? []
  if (!variants.length) return beds.value.map((b) => ({ id: b.id, label: b.name, left: null, soldOut: false, supplement: '0' }))
  return variants.map((v) => {
    const plan = v.rate_plans.find((p) => p.id === pk.plan.id)
    const adj = (plan?.nightly ?? []).reduce((sum, n) => sum + Number(n.bed_adjustment), 0)
    return { id: v.bed_type_id, label: v.name, left: v.available_min, soldOut: v.available_min < 1, supplement: String(adj) }
  })
})
const chosenBed = computed(() => bedChoices.value.find((b) => b.id === form.bedTypeId))
watch(() => form.bedTypeId, (id) => {
  if (!id) form.bedLocked = false
})
const chosenGroup = computed(() => groups.value.find((g) => g.id === form.groupId))
const saving = ref(false)
const error = ref<ApiError | null>(null)
// One key per booking attempt: submitting twice (a double click, a retry) returns the same reservation.
let idempotencyKey = newIdempotencyKey()

// A rate change: the nights whose price is changed, the reason, and the approval (asked for when the person cannot approve).
const rate = ref<RateChange>({ overrides: [], reason: '' })
const approving = ref(false)
const dialogError = ref<ApiError | null>(null)
const canOverride = computed(() => auth.can('reservation.override_rate', property.currentId))
const canApproveFree = computed(() => auth.can('reservation.complimentary_approve', property.currentId))
const exceedQuota = ref(false)
const isFree = computed(() => !!picked.value && picked.value.plan.occupancy_kind !== 'PAID')
/** The monthly quota of free nights this booking would go over: what the server says, to show and to confirm. */
const quotaOver = computed(() => {
  const e = error.value
  if (e?.code !== 'FREE_NIGHT_QUOTA_EXCEEDED') return null
  const c = e.context as Record<string, unknown>
  return { kind: String(c.occupancy_kind ?? ''), quota: Number(c.quota), month: String(c.month ?? '').slice(0, 7), used: Number(c.used), requested: Number(c.requested) }
})
const canApprove = computed(() => auth.can('reservation.override_rate_approve', property.currentId))
// A stay that breaks a sales restriction: staff who hold reservation.override_restriction can still book it, with a reason and an approval.
const canOverrideRestriction = computed(() => auth.can('reservation.override_restriction', property.currentId))
const canApproveRestriction = computed(() => auth.can('reservation.restriction_approve', property.currentId))
const restrictionReason = ref('')
const restrictions = (p: { restrictions?: Violation[] } | null | undefined): Violation[] => p?.restrictions ?? []
const isRestricted = computed(() => restrictions(picked.value?.plan).length > 0)
const violationText = (v: Violation): string => t(`restrictions.violation_${v.type}`, { date: v.date, value: v.value ?? '', nights: v.nights ?? '' })
const overrideNights = computed<OverrideNight[]>(() => (picked.value?.plan.nightly ?? []).map((n) => ({ date: n.date, standard: n.amount, current: n.amount })))
const canCreate = computed(() => auth.can('reservation.create', property.currentId))
const canRead = computed(() => auth.can('reservation.read', property.currentId))
/** Why a Book button is disabled (empty when it is enabled). */
function whyNot(ty: { available_min: number; fits_occupancy: boolean }, p: { estimate?: unknown; missing_nights?: number; restrictions?: Violation[] }): string {
  if (ty.available_min < 1) return t('newReservation.whyNoRooms')
  if (restrictions(p).length && !canOverrideRestriction.value) return t('restrictions.whyRestricted')
  if (!ty.fits_occupancy) return t('newReservation.whyTooSmall')
  if (!p.estimate) return t('newReservation.whyNoRate', { n: p.missing_nights ?? 0 })
  return ''
}

// One row per plan of a room type (a type without a plan has a row of its own), so the offers read as a table.
interface Offer {
  key: string
  type: TypeOffer
  plan: PlanOffer | null
}
const offers = computed<Offer[]>(() =>
  (result.value?.room_types ?? []).flatMap((ty): Offer[] =>
    ty.rate_plans.length ? ty.rate_plans.map((plan) => ({ key: `${ty.room_type_id}/${plan.id}`, type: ty, plan })) : [{ key: `${ty.room_type_id}/none`, type: ty, plan: null }],
  ),
)
const offerTestId = (o: Offer): string => (o.plan ? `offer-${o.type.code}-${o.plan.code}` : `type-${o.type.code}`)
const offerColumns = computed<Column<Offer>[]>(() => [
  { key: 'type', label: t('newReservation.roomType') },
  { key: 'left', label: t('newReservation.roomsLeft') },
  { key: 'plan', label: t('newReservation.ratePlan') },
  { key: 'estimate', label: t('newReservation.estimate'), align: 'right', class: 'tabular-nums', format: 'money' as const },
  { key: 'action', label: '', align: 'right' },
])
const businessDate = computed(() => property.clock?.business_date ?? '')
const fieldError = (field: string) => error.value?.fieldMessage(field)

watch(businessDate, (bd) => {
  if (bd && !search.arrival) {
    search.arrival = bd
    search.departure = addDays(bd, 1)
  }
}, { immediate: true })
watch(() => property.currentId, () => {
  result.value = null
  picked.value = null
})

// Coming from a group ("Add rooms"): start the search on the group's dates.
async function presetFromGroup(): Promise<void> {
  const propertyId = property.currentId
  if (propertyId === null || !form.groupId) return
  try {
    const { data } = await api.GET('/api/v1/properties/{propertyId}/groups/{id}', { params: { path: { propertyId, id: form.groupId } } })
    if (data) {
      search.arrival = data.arrival_date < businessDate.value ? businessDate.value : data.arrival_date
      search.departure = data.departure_date
    }
  } catch {
    // the group is shown in the form anyway; the dates stay at the defaults
  }
}
void presetFromGroup()

async function runSearch(): Promise<void> {
  const propertyId = property.currentId
  if (propertyId === null) return
  searching.value = true
  error.value = null
  picked.value = null
  try {
    const { data } = await api.GET('/api/v1/properties/{propertyId}/availability', {
      params: { path: { propertyId }, query: { arrival: search.arrival, departure: search.departure, adults: search.adults, children: search.children } },
    })
    result.value = data ?? null
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
    result.value = null
  } finally {
    searching.value = false
  }
}

// Companies and groups are optional: a role that cannot list them books without.
async function loadLinks(): Promise<void> {
  const propertyId = property.currentId
  if (propertyId === null || companies.value.length || groups.value.length) return
  try {
    const path = { path: { propertyId } }
    ;[companies.value, groups.value] = await Promise.all([
      fetchAll((cursor) => api.GET('/api/v1/properties/{propertyId}/companies', { params: { ...path, query: { limit: 200, cursor, active: true } } })),
      fetchAll((cursor) => api.GET('/api/v1/properties/{propertyId}/groups', { params: { ...path, query: { limit: 100, cursor, active: true } } })),
    ])
  } catch {
    companies.value = []
    groups.value = []
  }
}

// The beds a guest can ask for. Optional: a role that cannot list them books without.
async function loadBeds(): Promise<void> {
  const propertyId = property.currentId
  if (propertyId === null || beds.value.length) return
  try {
    const { data } = await api.GET('/api/v1/properties/{propertyId}/bed-types', { params: { path: { propertyId }, query: { active: true } } })
    beds.value = data?.data ?? []
  } catch {
    beds.value = []
  }
}

function pick(type: TypeOffer, plan: PlanOffer): void {
  picked.value = { type, plan }
  form.occupancyReason = ''
  rate.value = { overrides: [], reason: '' }
  approving.value = false
  exceedQuota.value = false
  idempotencyKey = newIdempotencyKey()
  error.value = null
  void loadLinks()
  void loadBeds()
}

async function findGuests(): Promise<void> {
  const propertyId = property.currentId
  if (propertyId === null || !guestQuery.value.trim()) return
  try {
    const { data } = await api.GET('/api/v1/guests', { params: { query: { q: guestQuery.value.trim(), property_id: propertyId, limit: 10 } } })
    guestResults.value = data?.data ?? []
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  }
}

/** The button: a price change that the person cannot approve asks for an approver first. */
function submit(): void {
  if ((rate.value.overrides.length && !canApprove.value) || (isFree.value && !canApproveFree.value) || (isRestricted.value && !canApproveRestriction.value)) {
    dialogError.value = null
    approving.value = true
    return
  }
  void book()
}

async function book(approval?: Approval): Promise<void> {
  const propertyId = property.currentId
  if (propertyId === null || !picked.value) return
  saving.value = true
  error.value = null
  dialogError.value = null
  try {
    const { data } = await api.POST('/api/v1/properties/{propertyId}/reservations', {
      params: { path: { propertyId }, header: { 'Idempotency-Key': idempotencyKey } },
      body: {
        guest_id: guest.value?.id,
        source: form.source,
        remarks: form.remarks || undefined,
        confirm: form.confirm,
        company_id: form.groupId ? undefined : form.companyId || undefined, // a group brings its own company
        booking_group_id: form.groupId || undefined,
        rate_override_reason: rate.value.overrides.length ? rate.value.reason : undefined,
        rate_override_approval: rate.value.overrides.length ? approval : undefined,
        occupancy_approval: isFree.value ? approval : undefined,
        exceed_free_quota: exceedQuota.value || undefined,
        restriction_override: isRestricted.value ? { reason: restrictionReason.value, approval } : undefined,
        rooms: [{
          room_type_id: picked.value.type.room_type_id,
          rate_plan_id: picked.value.plan.id,
          arrival_date: search.arrival,
          departure_date: search.departure,
          adult_count: search.adults,
          child_count: search.children,
          bed_type_id: form.bedTypeId || undefined,
          bed_locked: form.bedTypeId && form.bedLocked ? true : undefined,
          occupancy_reason: picked.value.plan.occupancy_kind !== 'PAID' ? form.occupancyReason : undefined,
          nightly_overrides: rate.value.overrides.length ? rate.value.overrides : undefined,
        }],
      },
    })
    if (data) await router.push(`/reservations/${data.id}`)
  } catch (e) {
    if (e instanceof ApiError && e.code === 'FREE_NIGHT_QUOTA_EXCEEDED') {
      approving.value = false // the page shows the quota and asks whether to go over it
      error.value = e
    } else if (approval) {
      dialogError.value = e instanceof ApiError ? e : null // the dialog shows what the approver got wrong
    } else {
      error.value = e instanceof ApiError ? e : null
    }
    if (e instanceof ApiError) idempotencyKey = newIdempotencyKey() // the server answered: the next submit is a new attempt (a network failure keeps the key, so a retry replays)
  } finally {
    saving.value = false
  }
}
</script>

<template>
  <PageHeader :title="t('newReservation.title')">
    <template #actions>
      <Button as-child variant="ghost" size="sm"><RouterLink to="/reservations">{{ t('newReservation.reservations') }}</RouterLink></Button>
    </template>
  </PageHeader>

  <p v-if="error" class="alert" role="alert" data-testid="form-error">{{ error.message }} <code>{{ error.code }}</code></p>
  <p v-if="property.currentId === null" class="muted">{{ t('newReservation.selectProperty') }}</p>
  <p v-else-if="!canRead || !canCreate" class="muted" data-testid="no-access">{{ t('newReservation.noAccess') }}</p>

  <template v-else>
    <Card class="mb-4">
      <form v-autofocus class="flex flex-wrap items-end gap-3 p-4" role="search" novalidate data-testid="search-form" @submit.prevent="runSearch">
        <FormField class="w-44" :label="t('newReservation.arrival')">
          <template #default="{ id, invalid }"><Input :id="id" v-model="search.arrival" name="arrival" type="date" :min="businessDate" :aria-invalid="!!fieldError('arrival_date') || invalid" /></template>
        </FormField>
        <FormField class="w-44" :label="t('newReservation.departure')" :error="fieldError('departure_date')">
          <template #default="{ id, invalid }"><Input :id="id" v-model="search.departure" name="departure" type="date" :min="search.arrival" :aria-invalid="invalid" /></template>
        </FormField>
        <FormField class="w-24" :label="t('newReservation.adults')">
          <template #default="{ id }"><Input :id="id" v-model.number="search.adults" name="adults" type="number" min="1" /></template>
        </FormField>
        <FormField class="w-24" :label="t('newReservation.children')">
          <template #default="{ id }"><Input :id="id" v-model.number="search.children" name="children" type="number" min="0" /></template>
        </FormField>
        <Button type="submit" :disabled="searching"><Search />{{ t('newReservation.search') }}</Button>
      </form>
    </Card>

    <Card v-if="result" class="mb-4" data-testid="results">
      <CardHeader><CardTitle>{{ t('newReservation.nightsHeading', { n: result.nights.length }) }}</CardTitle></CardHeader>
      <CardContent>
        <DataTable :columns="offerColumns" :rows="offers" row-key="key" :row-test-id="offerTestId" :caption="t('newReservation.title')">
          <template #cell-type="{ row: o }">
            {{ o.type.code }} · {{ o.type.name }}
            <small v-if="o.plan && !o.type.fits_occupancy" class="ml-1 text-destructive" data-testid="no-fit">{{ t('newReservation.noFit', { adults: search.adults, children: search.children }) }}</small>
          </template>
          <template #cell-left="{ row: o }"><span :class="o.type.available_min < 1 && 'font-semibold text-destructive'">{{ o.type.available_min }}</span></template>
          <template #cell-plan="{ row: o }">
            <small v-if="!o.plan" class="text-muted-foreground">{{ t('newReservation.noPlan') }}</small>
            <template v-else><Badge v-for="(v, i) in restrictions(o.plan)" :key="i" variant="warning" class="mr-1" :title="violationText(v)" :data-testid="`restricted-${o.type.code}-${o.plan.code}`">{{ t(`restrictions.badge_${v.type}`) }}</Badge>{{ o.plan.code }} <Badge v-if="o.plan.occupancy_kind !== 'PAID'" variant="warning" :data-testid="`kind-${o.plan.code}`">{{ t(`occupancy.kind_${o.plan.occupancy_kind}`) }}</Badge> <small class="text-muted-foreground">{{ o.plan.price_mode === 'INCLUSIVE' ? t('newReservation.inclusive') : t('newReservation.exclusive') }}</small></template>
          </template>
          <template #cell-estimate="{ row: o }">
            <template v-if="o.plan">
              <template v-if="o.plan.estimate">{{ $money(o.plan.estimate.total) }}</template>
              <small v-else class="text-muted-foreground" data-testid="missing">{{ t('newReservation.missing', { n: o.plan.missing_nights ?? 0 }) }}</small>
            </template>
          </template>
          <template #cell-action="{ row: o }">
            <Button v-if="o.plan" size="sm" :disabled="o.type.available_min < 1 || !o.type.fits_occupancy || !o.plan.estimate || (restrictions(o.plan).length > 0 && !canOverrideRestriction)" :title="whyNot(o.type, o.plan)" :data-testid="`pick-${o.type.code}-${o.plan.code}`" @click="pick(o.type, o.plan)">{{ t('newReservation.book') }}</Button>
          </template>
        </DataTable>
      </CardContent>
    </Card>

    <Card v-if="picked" class="mb-4 border-primary/50">
      <form novalidate data-testid="book-form" @submit.prevent="submit">
        <CardHeader>
          <CardTitle>{{ t('newReservation.bookTitle', { type: picked.type.code, plan: picked.plan.code }) }}</CardTitle>
          <p class="m-0 text-sm text-muted-foreground">{{ t('newReservation.bookSummary', { arrival: search.arrival, departure: search.departure, adults: search.adults, children: search.children }) }}</p>
        </CardHeader>
        <CardContent class="flex flex-col gap-4">
          <div v-if="isRestricted" class="rounded-md border border-warning/60 bg-warning/10 p-3 text-sm" role="alert" data-testid="restriction-notice">
            <p class="m-0 font-medium">{{ t('restrictions.overrideTitle') }}</p>
            <ul class="m-0 mt-1 list-disc pl-5"><li v-for="(v, i) in restrictions(picked?.plan)" :key="i">{{ violationText(v) }}</li></ul>
            <FormField class="mt-3 max-w-md" :label="t('restrictions.overrideReason')" :hint="t('restrictions.overrideHint')" :error="fieldError('restriction_override.reason')">
              <template #default="{ id, invalid }"><Input :id="id" v-model="restrictionReason" name="restriction_reason" :aria-invalid="invalid" /></template>
            </FormField>
          </div>
          <div>
            <div class="flex items-end gap-3">
              <FormField class="flex-1" :label="t('newReservation.booker')" :error="fieldError('guest_id')">
                <template #default="{ id }">
                  <Input :id="id" v-model="guestQuery" name="guest_q" type="search" :placeholder="t('newReservation.bookerPlaceholder')" @keydown.enter.prevent="findGuests" />
                </template>
              </FormField>
              <Button type="button" variant="outline" data-testid="find-guest" @click="findGuests">{{ t('newReservation.find') }}</Button>
            </div>
            <ul v-if="guestResults.length" class="m-0 mt-2 flex list-none flex-wrap gap-2 p-0">
              <li v-for="g in guestResults" :key="g.id">
                <Button type="button" variant="outline" size="sm" :data-testid="`guest-${g.code}`" @click="guest = g; guestResults = []">{{ g.code }} · {{ guestLabel(g) }}</Button>
              </li>
            </ul>
            <p v-if="guest" class="mb-0 mt-2 text-sm" data-testid="chosen-guest">
              {{ t('newReservation.chosenBooker') }} <strong>{{ guestLabel(guest) }}</strong> ({{ guest.code }})
              <button type="button" class="cursor-pointer border-0 bg-transparent p-0 text-primary underline" @click="guest = null">{{ t('newReservation.change') }}</button>
            </p>
          </div>

          <div class="grid gap-4 sm:grid-cols-2">
            <FormField :label="t('newReservation.source')">
              <template #default="{ id }">
                <NativeSelect :id="id" v-model="form.source" name="source">
                  <option v-for="s in SOURCES" :key="s" :value="s">{{ s }}</option>
                </NativeSelect>
              </template>
            </FormField>
            <FormField v-if="bedChoices.length" :label="t('bedTypes.requested')">
              <template #default="{ id }">
                <NativeSelect :id="id" v-model.number="form.bedTypeId" name="bed_type_id">
                  <option :value="0">{{ t('bedTypes.noPreference') }}</option>
                  <option v-for="b in bedChoices" :key="b.id" :value="b.id" :disabled="b.soldOut && form.bedLocked">{{ b.label }}{{ b.left === null ? '' : b.soldOut ? ` · ${t('bedTypes.soldOut')}` : ` · ${t('bedTypes.left', { n: b.left })}` }}</option>
                </NativeSelect>
                <label v-if="form.bedTypeId" class="mt-2 flex items-center gap-2 text-sm">
                  <input v-model="form.bedLocked" type="checkbox" name="bed_locked" class="size-4 accent-primary" />
                  <span>{{ t('bedTypes.keepBed') }}</span>
                </label>
                <small v-if="form.bedTypeId && form.bedLocked" class="text-xs text-muted-foreground" data-testid="bed-supplement">
                  {{ chosenBed && Number(chosenBed.supplement) !== 0 ? t('bedTypes.supplement', { amount: $money(chosenBed.supplement) }) : t('bedTypes.noSupplement') }}
                  <template v-if="chosenBed?.soldOut"> · {{ t('bedTypes.soldOut') }}</template>
                </small>
              </template>
            </FormField>
            <FormField v-if="picked.plan.occupancy_kind !== 'PAID'" :label="t('occupancy.reason')" :error="fieldError('rooms[0].occupancy_reason')">
              <template #default="{ id, invalid }">
                <Input :id="id" v-model="form.occupancyReason" name="occupancy_reason" :placeholder="t('occupancy.reasonHint')" :aria-invalid="invalid" />
              </template>
            </FormField>
            <FormField :label="t('newReservation.remarks')">
              <template #default="{ id }"><Input :id="id" v-model="form.remarks" name="remarks" /></template>
            </FormField>
            <FormField v-if="groups.length || form.groupId" :label="t('newReservation.group')" :error="fieldError('booking_group_id')">
              <template #default="{ id, invalid }">
                <Combobox :id="id" v-model="form.groupId" name="booking_group_id" :aria-invalid="invalid" :options="[{ value: 0, label: `${t('newReservation.groupNone')}` }, ...groups.map((g) => ({ value: g.id, label: `${g.code} · ${g.name} (${$date(g.arrival_date)} - ${$date(g.departure_date)})` }))]" />
                <small v-if="chosenGroup" class="text-xs text-muted-foreground" data-testid="group-hint">{{ t('newReservation.groupHint', { from: $date(chosenGroup.arrival_date), to: $date(chosenGroup.departure_date) }) }}{{ chosenGroup.company_name ? t('newReservation.groupHintCompany', { company: chosenGroup.company_name }) : '' }}.</small>
              </template>
            </FormField>
            <FormField v-if="companies.length && !form.groupId" :label="t('newReservation.company')" :error="fieldError('company_id')">
              <template #default="{ id, invalid }">
                <Combobox :id="id" v-model="form.companyId" name="company_id" :aria-invalid="invalid" :options="[{ value: 0, label: `${t('newReservation.companyNone')}` }, ...companies.map((c) => ({ value: c.id, label: `${c.code} · ${c.name}` }))]" />
              </template>
            </FormField>
          </div>

          <div v-if="quotaOver" class="alert warning" role="alert" data-testid="quota-warning">
            {{ t('freeQuotas.over', { kind: t(`occupancy.kind_${quotaOver.kind}`), quota: quotaOver.quota, month: quotaOver.month, used: quotaOver.used, requested: quotaOver.requested }) }}
            <label class="mt-2 flex items-center gap-2 text-sm"><input v-model="exceedQuota" type="checkbox" name="exceed_free_quota" class="size-4 accent-primary" /><span>{{ t('freeQuotas.goOver') }}</span></label>
          </div>
          <RateOverrideSection v-if="canOverride && picked.plan.occupancy_kind === 'PAID' && overrideNights.length" v-model="rate" :nights="overrideNights" />
          <label class="flex items-center gap-2 text-sm">
            <input v-model="form.confirm" type="checkbox" name="confirm" />
            <span>{{ t('newReservation.confirmNow') }}</span>
          </label>
          <div class="flex justify-end">
            <Button type="submit" :disabled="saving">{{ form.confirm ? t('newReservation.bookAndConfirm') : t('newReservation.saveDraft') }}</Button>
          </div>
        </CardContent>
      </form>
    </Card>
    <ApprovalDialog v-if="approving" :title="isRestricted ? t('restrictions.approvalTitle') : isFree ? t('freeQuotas.approvalTitle') : t('rateOverride.approvalTitle')" :message="isRestricted ? t('restrictions.approvalMessage') : isFree ? t('freeQuotas.approvalMessage') : t('rateOverride.approvalMessage')" :busy="saving" :error="dialogError" @approve="(a) => book(a)" @cancel="approving = false" />
  </template>
</template>
