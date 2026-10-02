<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { api } from '@/api/client'
import { fetchAll } from '@/api/paging'
import { ApiError } from '@/api/problem'
import type { RateQuote, RatePlan, RoomType, YieldRule } from '@/api/types'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'

const auth = useAuthStore()
const property = usePropertyStore()

const rules = ref<YieldRule[]>([])
const plans = ref<RatePlan[]>([])
const types = ref<RoomType[]>([])
const loaded = ref(false)
const error = ref<ApiError | null>(null)
const saving = ref(false)
const editing = ref<YieldRule | 'new' | null>(null)

const pid = computed(() => property.currentId)
const canManage = computed(() => auth.can('rate.manage', pid.value))
const fieldError = (field: string) => error.value?.fieldMessage(field)

const DAYS = ['MON', 'TUE', 'WED', 'THU', 'FRI', 'SAT', 'SUN'] as const
type Day = (typeof DAYS)[number]

const blank = () => ({
  code: '', name: '', rate_plan_id: '' as number | '', room_type_id: '' as number | '', stay_from: '', stay_to: '', weekdays: [] as Day[],
  occupancy_from: '', occupancy_to: '', lead_min: '', lead_max: '', stay_min: '', stay_max: '',
  adjustment_type: 'PERCENT' as 'PERCENT' | 'AMOUNT', adjustment_value: '', floor_amount: '', cap_amount: '', priority: '100', is_active: true,
})
const form = reactive(blank())

const orNull = (s: string): string | null => (s.trim() === '' ? null : s.trim())
const intOrNull = (s: string): number | null => (s.trim() === '' ? null : Number(s))

async function load(): Promise<void> {
  const propertyId = pid.value
  rules.value = []
  loaded.value = false
  if (propertyId === null) return
  try {
    const path = { path: { propertyId } }
    const [r, p, t] = await Promise.all([
      api.GET('/api/v1/properties/{propertyId}/yield-rules', { params: path }),
      fetchAll((cursor) => api.GET('/api/v1/properties/{propertyId}/rate-plans', { params: { ...path, query: { limit: 200, cursor } } })),
      fetchAll((cursor) => api.GET('/api/v1/properties/{propertyId}/room-types', { params: { ...path, query: { limit: 200, cursor } } })),
    ])
    rules.value = r.data?.data ?? []
    plans.value = p
    types.value = t
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    loaded.value = true
  }
}

const planCode = (id: number | null) => (id === null ? 'all plans' : (plans.value.find((p) => p.id === id)?.code ?? `plan ${id}`))
const typeCode = (id: number | null) => (id === null ? 'all room types' : (types.value.find((t) => t.id === id)?.code ?? `type ${id}`))

// The conditions of a rule in a line a person can read.
function conditions(r: YieldRule): string {
  const parts: string[] = [`${planCode(r.rate_plan_id)}, ${typeCode(r.room_type_id)}`]
  if (r.stay_from || r.stay_to) parts.push(`stay ${r.stay_from ?? '…'} to ${r.stay_to ?? '…'}`)
  if (r.weekdays?.length) parts.push(r.weekdays.join(' '))
  if (r.occupancy_from || r.occupancy_to) parts.push(`occupancy ${r.occupancy_from ?? '0'}% to ${r.occupancy_to ?? '100'}%`)
  if (r.lead_days_min !== null || r.lead_days_max !== null) parts.push(`${r.lead_days_min ?? 0} to ${r.lead_days_max ?? '…'} days ahead`)
  if (r.stay_nights_min !== null || r.stay_nights_max !== null) parts.push(`${r.stay_nights_min ?? 1} to ${r.stay_nights_max ?? '…'} nights`)
  return parts.join(' · ')
}

const adjustment = (r: YieldRule): string => {
  const v = Number(r.adjustment_value)
  return `${v > 0 ? '+' : ''}${r.adjustment_value}${r.adjustment_type === 'PERCENT' ? '%' : ''}`
}

function startNew(): void {
  Object.assign(form, blank())
  error.value = null
  editing.value = 'new'
}

function startEdit(r: YieldRule): void {
  Object.assign(form, blank(), {
    code: r.code, name: r.name, rate_plan_id: r.rate_plan_id ?? '', room_type_id: r.room_type_id ?? '', stay_from: r.stay_from ?? '', stay_to: r.stay_to ?? '',
    weekdays: [...(r.weekdays ?? [])], occupancy_from: r.occupancy_from ?? '', occupancy_to: r.occupancy_to ?? '',
    lead_min: r.lead_days_min?.toString() ?? '', lead_max: r.lead_days_max?.toString() ?? '', stay_min: r.stay_nights_min?.toString() ?? '', stay_max: r.stay_nights_max?.toString() ?? '',
    adjustment_type: r.adjustment_type, adjustment_value: r.adjustment_value, floor_amount: r.floor_amount ?? '', cap_amount: r.cap_amount ?? '',
    priority: String(r.priority), is_active: r.is_active,
  })
  error.value = null
  editing.value = r
}

function body(isActive = form.is_active) {
  return {
    code: form.code, name: form.name, rate_plan_id: form.rate_plan_id === '' ? null : Number(form.rate_plan_id),
    room_type_id: form.room_type_id === '' ? null : Number(form.room_type_id), stay_from: orNull(form.stay_from), stay_to: orNull(form.stay_to),
    weekdays: form.weekdays.length ? form.weekdays : null, occupancy_from: orNull(form.occupancy_from), occupancy_to: orNull(form.occupancy_to),
    lead_days_min: intOrNull(form.lead_min), lead_days_max: intOrNull(form.lead_max), stay_nights_min: intOrNull(form.stay_min), stay_nights_max: intOrNull(form.stay_max),
    adjustment_type: form.adjustment_type, adjustment_value: form.adjustment_value, floor_amount: orNull(form.floor_amount), cap_amount: orNull(form.cap_amount),
    priority: Number(form.priority), is_active: isActive,
  }
}

async function save(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null || editing.value === null) return
  saving.value = true
  error.value = null
  try {
    if (editing.value === 'new') {
      await api.POST('/api/v1/properties/{propertyId}/yield-rules', { params: { path: { propertyId } }, body: body() })
    } else {
      await api.PUT('/api/v1/properties/{propertyId}/yield-rules/{id}', { params: { path: { propertyId, id: editing.value.id } }, body: body() })
    }
    editing.value = null
    await load()
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    saving.value = false
  }
}

async function toggle(r: YieldRule): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null) return
  startEdit(r)
  error.value = null
  try {
    await api.PUT('/api/v1/properties/{propertyId}/yield-rules/{id}', { params: { path: { propertyId, id: r.id } }, body: body(!r.is_active) })
    editing.value = null
    await load()
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  }
}

async function remove(r: YieldRule): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null || !window.confirm(`Delete the rule ${r.code}? Reservations already made keep their prices.`)) return
  error.value = null
  try {
    await api.DELETE('/api/v1/properties/{propertyId}/yield-rules/{id}', { params: { path: { propertyId, id: r.id } } })
    await load()
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  }
}

// ---- price check: what a stay costs today, rule by rule
const check = reactive({ plan: '' as number | '', type: '' as number | '', arrival: '', departure: '' })
const quote = ref<RateQuote | null>(null)
const quoteError = ref<ApiError | null>(null)
const quoting = ref(false)

async function runQuote(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null || check.plan === '' || check.type === '') return
  quoting.value = true
  quoteError.value = null
  try {
    const { data } = await api.GET('/api/v1/properties/{propertyId}/rate-quotes', {
      params: { path: { propertyId }, query: { rate_plan_id: Number(check.plan), room_type_id: Number(check.type), arrival_date: check.arrival, departure_date: check.departure } },
    })
    quote.value = data ?? null
  } catch (e) {
    quote.value = null
    quoteError.value = e instanceof ApiError ? e : null
  } finally {
    quoting.value = false
  }
}

watch(pid, load, { immediate: true })
</script>

<template>
  <div class="page-head">
    <h1 class="page-title">Yield rules</h1>
    <button v-if="canManage && !editing" type="button" class="btn-primary" data-testid="new-rule" @click="startNew">New rule</button>
  </div>
  <p class="muted">
    A rule changes the price a night is sold at when its conditions hold, for example +20% when the hotel is 70% full, or -10% for bookings made two days ahead.
    The rate grid stays the base price. Rules apply in priority order, each to the price the one before left. A booking keeps the price it was sold at.
  </p>

  <p v-if="error" class="alert" role="alert" data-testid="form-error">{{ error.message }} <code>{{ error.code }}</code></p>
  <p v-if="pid === null" class="muted">Select a property first.</p>
  <p v-else-if="!canManage" class="muted" data-testid="read-only">Your role at this property can view the rules but not change them: the <code>rate.manage</code> permission is needed.</p>

  <form v-if="editing" class="card" novalidate data-testid="rule-form" @submit.prevent="save">
    <h2>{{ editing === 'new' ? 'New yield rule' : `Edit ${form.code}` }}</h2>
    <div class="form-grid">
      <label class="field">
        <span>Code</span>
        <input v-model="form.code" name="code" :disabled="editing !== 'new'" :aria-invalid="!!fieldError('code')" />
        <small v-if="fieldError('code')" class="error-text">{{ fieldError('code') }}</small>
      </label>
      <label class="field">
        <span>Name</span>
        <input v-model="form.name" name="name" :aria-invalid="!!fieldError('name')" />
        <small v-if="fieldError('name')" class="error-text">{{ fieldError('name') }}</small>
      </label>
      <label class="field">
        <span>Priority</span>
        <input v-model="form.priority" name="priority" inputmode="numeric" />
        <small class="hint">Lower applies first.</small>
        <small v-if="fieldError('priority')" class="error-text">{{ fieldError('priority') }}</small>
      </label>
    </div>

    <h3>When</h3>
    <div class="form-grid">
      <label class="field">
        <span>Rate plan</span>
        <select v-model="form.rate_plan_id" name="rate_plan_id">
          <option value="">All plans</option>
          <option v-for="p in plans" :key="p.id" :value="p.id">{{ p.code }} · {{ p.name }}</option>
        </select>
      </label>
      <label class="field">
        <span>Room type</span>
        <select v-model="form.room_type_id" name="room_type_id">
          <option value="">All room types</option>
          <option v-for="t in types" :key="t.id" :value="t.id">{{ t.code }} · {{ t.name }}</option>
        </select>
      </label>
      <label class="field">
        <span>Stay nights from</span>
        <input v-model="form.stay_from" name="stay_from" type="date" />
      </label>
      <label class="field">
        <span>Stay nights to</span>
        <input v-model="form.stay_to" name="stay_to" type="date" :aria-invalid="!!fieldError('stay_to')" />
        <small v-if="fieldError('stay_to')" class="error-text">{{ fieldError('stay_to') }}</small>
      </label>
      <label class="field">
        <span>Occupancy from (%)</span>
        <input v-model="form.occupancy_from" name="occupancy_from" inputmode="decimal" :aria-invalid="!!fieldError('occupancy_from')" />
        <small class="hint">How full the hotel is that night, counting the rooms held by reservations and stays. Included.</small>
        <small v-if="fieldError('occupancy_from')" class="error-text">{{ fieldError('occupancy_from') }}</small>
      </label>
      <label class="field">
        <span>Occupancy to (%)</span>
        <input v-model="form.occupancy_to" name="occupancy_to" inputmode="decimal" :aria-invalid="!!fieldError('occupancy_to')" />
        <small class="hint">Excluded, except 100, which includes a full house.</small>
        <small v-if="fieldError('occupancy_to')" class="error-text">{{ fieldError('occupancy_to') }}</small>
      </label>
      <label class="field">
        <span>Booked at least (days ahead)</span>
        <input v-model="form.lead_min" name="lead_min" inputmode="numeric" />
      </label>
      <label class="field">
        <span>Booked at most (days ahead)</span>
        <input v-model="form.lead_max" name="lead_max" inputmode="numeric" :aria-invalid="!!fieldError('lead_days_max')" />
        <small v-if="fieldError('lead_days_max')" class="error-text">{{ fieldError('lead_days_max') }}</small>
      </label>
      <label class="field">
        <span>Stay of at least (nights)</span>
        <input v-model="form.stay_min" name="stay_min" inputmode="numeric" />
      </label>
      <label class="field">
        <span>Stay of at most (nights)</span>
        <input v-model="form.stay_max" name="stay_max" inputmode="numeric" />
      </label>
    </div>
    <fieldset class="field" data-testid="weekdays">
      <legend>Weekdays (none ticked means every day)</legend>
      <label v-for="d in DAYS" :key="d" class="check"><input v-model="form.weekdays" type="checkbox" name="weekdays" :value="d" /><span>{{ d }}</span></label>
    </fieldset>

    <h3>Then</h3>
    <div class="form-grid">
      <label class="field">
        <span>Adjust by</span>
        <select v-model="form.adjustment_type" name="adjustment_type">
          <option value="PERCENT">A percentage of the price</option>
          <option value="AMOUNT">An amount</option>
        </select>
      </label>
      <label class="field">
        <span>Value (negative lowers the price)</span>
        <input v-model="form.adjustment_value" name="adjustment_value" inputmode="decimal" :aria-invalid="!!fieldError('adjustment_value')" />
        <small v-if="fieldError('adjustment_value')" class="error-text">{{ fieldError('adjustment_value') }}</small>
      </label>
      <label class="field">
        <span>Never below (optional)</span>
        <input v-model="form.floor_amount" name="floor_amount" inputmode="decimal" :aria-invalid="!!fieldError('floor_amount')" />
        <small v-if="fieldError('floor_amount')" class="error-text">{{ fieldError('floor_amount') }}</small>
      </label>
      <label class="field">
        <span>Never above (optional)</span>
        <input v-model="form.cap_amount" name="cap_amount" inputmode="decimal" :aria-invalid="!!fieldError('cap_amount')" />
        <small v-if="fieldError('cap_amount')" class="error-text">{{ fieldError('cap_amount') }}</small>
      </label>
    </div>
    <label class="check"><input v-model="form.is_active" type="checkbox" name="is_active" /><span>Active</span></label>
    <div class="form-actions">
      <button type="button" @click="editing = null">Cancel</button>
      <button type="submit" class="btn-primary" :disabled="saving">{{ saving ? 'Saving…' : 'Save' }}</button>
    </div>
  </form>

  <section class="card">
    <p v-if="loaded && !rules.length" class="muted" data-testid="empty">No yield rule yet: every night is sold at its grid price.</p>
    <table v-else-if="rules.length" class="list" data-testid="rules">
      <thead><tr><th class="num">Priority</th><th>Rule</th><th>When</th><th class="num">Adjusts</th><th>Status</th><th></th></tr></thead>
      <tbody>
        <tr v-for="r in rules" :key="r.id" :data-testid="`rule-${r.code}`" :class="{ off: !r.is_active }">
          <td class="num">{{ r.priority }}</td>
          <td><b>{{ r.code }}</b> <small class="muted">{{ r.name }}</small></td>
          <td><small>{{ conditions(r) }}</small></td>
          <td class="num"><b>{{ adjustment(r) }}</b><small v-if="r.floor_amount || r.cap_amount" class="muted"> ({{ r.floor_amount ?? '–' }} to {{ r.cap_amount ?? '–' }})</small></td>
          <td>{{ r.is_active ? 'Active' : 'Off' }}</td>
          <td class="actions">
            <template v-if="canManage">
              <button type="button" :data-testid="`edit-${r.code}`" @click="startEdit(r)">Edit</button>
              <button type="button" :data-testid="`toggle-${r.code}`" @click="toggle(r)">{{ r.is_active ? 'Turn off' : 'Turn on' }}</button>
              <button type="button" :data-testid="`delete-${r.code}`" @click="remove(r)">Delete</button>
            </template>
          </td>
        </tr>
      </tbody>
    </table>
  </section>

  <form class="card" novalidate data-testid="quote-form" @submit.prevent="runQuote">
    <h2>Price check</h2>
    <p class="muted">What a booking made now would cost, night by night, with the rules that moved each price.</p>
    <div class="form-grid">
      <label class="field">
        <span>Rate plan</span>
        <select v-model="check.plan" name="quote_plan"><option v-for="p in plans" :key="p.id" :value="p.id">{{ p.code }} · {{ p.name }}</option></select>
      </label>
      <label class="field">
        <span>Room type</span>
        <select v-model="check.type" name="quote_type"><option v-for="t in types" :key="t.id" :value="t.id">{{ t.code }} · {{ t.name }}</option></select>
      </label>
      <label class="field"><span>Arrival</span><input v-model="check.arrival" name="quote_arrival" type="date" /></label>
      <label class="field"><span>Departure</span><input v-model="check.departure" name="quote_departure" type="date" /></label>
    </div>
    <div class="form-actions"><button type="submit" :disabled="quoting || check.plan === '' || check.type === ''" data-testid="quote-run">Check the price</button></div>
    <p v-if="quoteError" class="alert" role="alert" data-testid="quote-error">{{ quoteError.message }} <code>{{ quoteError.code }}</code></p>
    <template v-if="quote">
      <table class="list" data-testid="quote">
        <thead><tr><th>Night</th><th class="num">Grid</th><th class="num">Full</th><th>Rules</th><th class="num">Price</th></tr></thead>
        <tbody>
          <tr v-for="n in quote.nights" :key="n.date">
            <td>{{ n.date }}</td>
            <td class="num">{{ n.grid_rate ?? 'not set' }}</td>
            <td class="num">{{ n.occupancy_percent }}%</td>
            <td><small v-for="s in n.steps" :key="s.code" class="step">{{ s.code }} {{ s.before }} → {{ s.after }}</small><small v-if="!n.steps.length" class="muted">none</small></td>
            <td class="num"><b>{{ n.amount ?? '–' }}</b></td>
          </tr>
        </tbody>
        <tfoot><tr><td colspan="4">Total ({{ quote.price_mode === 'INCLUSIVE' ? 'inclusive' : 'exclusive' }}; grid {{ quote.grid_total }})</td><td class="num"><b data-testid="quote-total">{{ quote.total }}</b></td></tr></tfoot>
      </table>
      <p v-if="quote.missing_nights" class="muted">{{ quote.missing_nights }} night(s) have no grid price.</p>
    </template>
  </form>
</template>

<style scoped>
.num {
  text-align: right;
  white-space: nowrap;
}
.off {
  opacity: 0.55;
}
.actions {
  display: flex;
  gap: 6px;
  justify-content: flex-end;
}
.step {
  display: block;
}
h3 {
  margin: 16px 0 8px;
  font-size: 14px;
}
</style>
