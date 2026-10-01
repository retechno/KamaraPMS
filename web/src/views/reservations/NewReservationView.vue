<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { AvailabilitySearch, Guest, PlanOffer, ReservationSource, TypeOffer } from '@/api/types'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import { addDays } from '@/utils/dates'
import { guestLabel, newIdempotencyKey } from '@/utils/reservations'

const auth = useAuthStore()
const property = usePropertyStore()
const router = useRouter()

const SOURCES: ReservationSource[] = ['PHONE', 'EMAIL', 'WALK_IN', 'WEBSITE', 'OTA', 'AGENT', 'OTHER']

const search = reactive({ arrival: '', departure: '', adults: 2, children: 0 })
const result = ref<AvailabilitySearch | null>(null)
const searching = ref(false)
const picked = ref<{ type: TypeOffer; plan: PlanOffer } | null>(null)
const guestQuery = ref('')
const guestResults = ref<Guest[]>([])
const guest = ref<Guest | null>(null)
const form = reactive({ source: 'PHONE' as ReservationSource, confirm: true, remarks: '' })
const saving = ref(false)
const error = ref<ApiError | null>(null)
// One key per booking attempt: submitting twice (a double click, a retry) returns the same reservation.
let idempotencyKey = newIdempotencyKey()

const canCreate = computed(() => auth.can('reservation.create', property.currentId))
const canRead = computed(() => auth.can('reservation.read', property.currentId))
/** Why a Book button is disabled (empty when it is enabled). */
function whyNot(t: { available_min: number; fits_occupancy: boolean }, p: { estimate?: unknown; missing_nights?: number }): string {
  if (t.available_min < 1) return 'No room of this type is left for these dates (add rooms under Setup → Rooms, or change the dates).'
  if (!t.fits_occupancy) return 'The room type is too small for this party.'
  if (!p.estimate) return `${p.missing_nights ?? 'Some'} night(s) have no rate: fill them in the Rate grid first.`
  return ''
}
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

function pick(type: TypeOffer, plan: PlanOffer): void {
  picked.value = { type, plan }
  idempotencyKey = newIdempotencyKey()
  error.value = null
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

async function book(): Promise<void> {
  const propertyId = property.currentId
  if (propertyId === null || !picked.value) return
  saving.value = true
  error.value = null
  try {
    const { data } = await api.POST('/api/v1/properties/{propertyId}/reservations', {
      params: { path: { propertyId }, header: { 'Idempotency-Key': idempotencyKey } },
      body: {
        guest_id: guest.value?.id,
        source: form.source,
        remarks: form.remarks || undefined,
        confirm: form.confirm,
        rooms: [{
          room_type_id: picked.value.type.room_type_id,
          rate_plan_id: picked.value.plan.id,
          arrival_date: search.arrival,
          departure_date: search.departure,
          adult_count: search.adults,
          child_count: search.children,
        }],
      },
    })
    if (data) await router.push(`/reservations/${data.id}`)
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
    if (e instanceof ApiError) idempotencyKey = newIdempotencyKey() // the server answered: the next submit is a new attempt (a network failure keeps the key, so a retry replays)
  } finally {
    saving.value = false
  }
}
</script>

<template>
  <div class="page-head">
    <h1 class="page-title">New reservation</h1>
    <RouterLink to="/reservations">Reservations</RouterLink>
  </div>

  <p v-if="error" class="alert" role="alert" data-testid="form-error">{{ error.message }} <code>{{ error.code }}</code></p>
  <p v-if="property.currentId === null" class="muted">Select a property first.</p>
  <p v-else-if="!canRead || !canCreate" class="muted" data-testid="no-access">Your role at this property does not allow creating reservations.</p>

  <template v-else>
    <form class="card search" role="search" novalidate data-testid="search-form" @submit.prevent="runSearch">
      <label class="field">
        <span>Arrival</span>
        <input v-model="search.arrival" name="arrival" type="date" :min="businessDate" :aria-invalid="!!fieldError('arrival_date')" />
      </label>
      <label class="field">
        <span>Departure</span>
        <input v-model="search.departure" name="departure" type="date" :min="search.arrival" :aria-invalid="!!fieldError('departure_date')" />
        <small v-if="fieldError('departure_date')" class="error-text">{{ fieldError('departure_date') }}</small>
      </label>
      <label class="field small">
        <span>Adults</span>
        <input v-model.number="search.adults" name="adults" type="number" min="1" />
      </label>
      <label class="field small">
        <span>Children</span>
        <input v-model.number="search.children" name="children" type="number" min="0" />
      </label>
      <button type="submit" class="btn-primary" :disabled="searching">Search</button>
    </form>

    <section v-if="result" class="card" data-testid="results">
      <h2>{{ result.nights.length }} night(s)</h2>
      <table class="list">
        <thead>
          <tr>
            <th>Room type</th>
            <th>Rooms left</th>
            <th>Rate plan</th>
            <th class="num">Estimate (incl. service and tax)</th>
            <th />
          </tr>
        </thead>
        <tbody>
          <template v-for="t in result.room_types" :key="t.room_type_id">
            <tr v-if="!t.rate_plans.length" :data-testid="`type-${t.code}`">
              <td>{{ t.code }} · {{ t.name }}</td>
              <td>{{ t.available_min }}</td>
              <td colspan="3" class="muted">No active rate plan.</td>
            </tr>
            <tr v-for="p in t.rate_plans" :key="`${t.room_type_id}/${p.id}`" :data-testid="`offer-${t.code}-${p.code}`">
              <td>
                {{ t.code }} · {{ t.name }}
                <small v-if="!t.fits_occupancy" class="error-text" data-testid="no-fit">does not fit {{ search.adults }} adult(s) and {{ search.children }} child(ren)</small>
              </td>
              <td :class="{ soldout: t.available_min < 1 }">{{ t.available_min }}</td>
              <td>{{ p.code }} <small class="muted">{{ p.price_mode === 'INCLUSIVE' ? 'inclusive' : 'exclusive' }}</small></td>
              <td class="num">
                <template v-if="p.estimate">{{ p.estimate.total }}</template>
                <small v-else class="muted" data-testid="missing">{{ p.missing_nights }} night(s) without a rate</small>
              </td>
              <td>
                <button type="button" :disabled="t.available_min < 1 || !t.fits_occupancy || !p.estimate" :title="whyNot(t, p)" :data-testid="`pick-${t.code}-${p.code}`" @click="pick(t, p)">Book</button>
              </td>
            </tr>
          </template>
        </tbody>
      </table>
    </section>

    <form v-if="picked" class="card" novalidate data-testid="book-form" @submit.prevent="book">
      <h2>Book {{ picked.type.code }} on {{ picked.plan.code }}</h2>
      <p class="muted">{{ search.arrival }} to {{ search.departure }}, {{ search.adults }} adult(s), {{ search.children }} child(ren)</p>

      <div class="guest-pick">
        <label class="field grow">
          <span>Booker</span>
          <input v-model="guestQuery" name="guest_q" type="search" placeholder="Search a guest by name, email, phone or code" @keydown.enter.prevent="findGuests" />
        </label>
        <button type="button" data-testid="find-guest" @click="findGuests">Find</button>
      </div>
      <ul v-if="guestResults.length" class="picks">
        <li v-for="g in guestResults" :key="g.id">
          <button type="button" :data-testid="`guest-${g.code}`" @click="guest = g; guestResults = []">{{ g.code }} · {{ guestLabel(g) }}</button>
        </li>
      </ul>
      <p v-if="guest" data-testid="chosen-guest">Booker: <strong>{{ guestLabel(guest) }}</strong> ({{ guest.code }})
        <button type="button" class="link" @click="guest = null">change</button></p>
      <small v-if="fieldError('guest_id')" class="error-text">{{ fieldError('guest_id') }}</small>

      <div class="form-grid">
        <label class="field">
          <span>Source</span>
          <select v-model="form.source" name="source">
            <option v-for="s in SOURCES" :key="s" :value="s">{{ s }}</option>
          </select>
        </label>
        <label class="field">
          <span>Remarks</span>
          <input v-model="form.remarks" name="remarks" />
        </label>
      </div>
      <label class="check">
        <input v-model="form.confirm" type="checkbox" name="confirm" />
        <span>Confirm now (holds the room; a draft holds nothing)</span>
      </label>
      <div class="form-actions">
        <button type="submit" class="btn-primary" :disabled="saving">{{ form.confirm ? 'Book and confirm' : 'Save draft' }}</button>
      </div>
    </form>
  </template>
</template>

<style scoped>
.search {
  display: flex;
  gap: 12px;
  align-items: flex-end;
  flex-wrap: wrap;
}
.field.small input {
  width: 80px;
}
.grow {
  flex: 1;
}
.list {
  width: 100%;
  border-collapse: collapse;
  font-size: 14px;
}
.list th,
.list td {
  text-align: left;
  padding: 6px 8px;
  border-bottom: 1px solid var(--border);
}
.num {
  text-align: right !important;
  font-variant-numeric: tabular-nums;
}
.soldout {
  color: var(--danger, #b42318);
  font-weight: 600;
}
.guest-pick {
  display: flex;
  gap: 12px;
  align-items: flex-end;
}
.picks {
  list-style: none;
  padding: 0;
  margin: 8px 0;
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
}
.link {
  border: 0;
  background: none;
  color: var(--accent);
  padding: 0;
  text-decoration: underline;
}
</style>
