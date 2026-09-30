<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { api } from '@/api/client'
import { fetchAll } from '@/api/paging'
import { ApiError } from '@/api/problem'
import type { RateGrid, RatePlan, RoomType, Weekday } from '@/api/types'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import { windowDates } from '@/utils/blocks'
import { addDays } from '@/utils/dates'

const auth = useAuthStore()
const property = usePropertyStore()

const plans = ref<RatePlan[]>([])
const types = ref<RoomType[]>([])
const planId = ref(0)
const grid = ref<RateGrid | null>(null)
const days = ref(14)
const windowStart = ref('')
const error = ref<ApiError | null>(null)
const notice = ref('')
const saving = ref(false)

const WEEKDAYS: { key: Weekday; label: string }[] = [
  { key: 'MON', label: 'Mon' }, { key: 'TUE', label: 'Tue' }, { key: 'WED', label: 'Wed' }, { key: 'THU', label: 'Thu' },
  { key: 'FRI', label: 'Fri' }, { key: 'SAT', label: 'Sat' }, { key: 'SUN', label: 'Sun' },
]
const form = reactive({ from: '', to: '', weekdays: [] as Weekday[], amount: '', typeIds: [] as number[] })

const canManage = computed(() => auth.can('rate.manage', property.currentId))
const businessDate = computed(() => property.clock?.business_date ?? '')
const activeTypes = computed(() => [...types.value].filter((t) => t.is_active).sort((a, b) => a.sort_order - b.sort_order || a.code.localeCompare(b.code)))
const plan = computed(() => plans.value.find((p) => p.id === planId.value))
const dates = computed(() => (windowStart.value ? windowDates(windowStart.value, days.value) : []))
const fieldError = (field: string) => error.value?.fieldMessage(field)

// amount by "typeId/date"
const amounts = computed(() => new Map((grid.value?.rates ?? []).map((r) => [`${r.room_type_id}/${r.stay_date}`, r.amount])))
const cell = (typeId: number, date: string) => amounts.value.get(`${typeId}/${date}`)
const dayLabel = (d: string) => `${d.slice(8)}/${d.slice(5, 7)}`
const weekdayOf = (d: string) => WEEKDAYS[(new Date(`${d}T00:00:00Z`).getUTCDay() + 6) % 7]?.label ?? ''

async function loadBase(): Promise<void> {
  const propertyId = property.currentId
  plans.value = []
  types.value = []
  grid.value = null
  planId.value = 0
  if (propertyId === null) return
  try {
    const path = { path: { propertyId } }
    const [p, t] = await Promise.all([
      fetchAll((cursor) => api.GET('/api/v1/properties/{propertyId}/rate-plans', { params: { ...path, query: { limit: 200, cursor } } })),
      fetchAll((cursor) => api.GET('/api/v1/properties/{propertyId}/room-types', { params: { ...path, query: { limit: 200, cursor } } })),
    ])
    plans.value = p
    types.value = t
    planId.value = p.find((x) => x.is_active)?.id ?? p[0]?.id ?? 0
    form.typeIds = activeTypes.value.map((x) => x.id)
    await loadGrid()
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  }
}

async function loadGrid(): Promise<void> {
  const propertyId = property.currentId
  if (propertyId === null || !planId.value) return
  if (!windowStart.value) windowStart.value = businessDate.value
  if (!windowStart.value) return // the business date is still loading; the watcher below retries
  try {
    const { data } = await api.GET('/api/v1/properties/{propertyId}/rates', {
      params: { path: { propertyId }, query: { rate_plan_id: planId.value, from: windowStart.value, to: addDays(windowStart.value, days.value) } },
    })
    grid.value = data ?? null
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  }
}

function shift(n: number): void {
  windowStart.value = addDays(windowStart.value, n)
  void loadGrid()
}

/** Clicking a night prepares a fill for that room type and night, starting from its current amount. */
function pick(typeId: number, date: string): void {
  if (!canManage.value) return
  form.typeIds = [typeId]
  form.from = date
  form.to = addDays(date, 1)
  form.weekdays = []
  form.amount = cell(typeId, date) ?? ''
  notice.value = ''
  error.value = null
}

async function apply(): Promise<void> {
  const propertyId = property.currentId
  if (propertyId === null) return
  saving.value = true
  error.value = null
  notice.value = ''
  try {
    const { data } = await api.PUT('/api/v1/properties/{propertyId}/rates', {
      params: { path: { propertyId } },
      body: { rate_plan_id: planId.value, room_type_ids: form.typeIds, from: form.from, to: form.to, weekdays: form.weekdays, amount: form.amount },
    })
    notice.value = data ? `${data.updated_nights} night(s) written, ${data.created_nights} of them new.` : ''
    await loadGrid()
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    saving.value = false
  }
}

function toggle<T>(list: T[], v: T): void {
  const i = list.indexOf(v)
  if (i >= 0) list.splice(i, 1)
  else list.push(v)
}

watch(() => property.currentId, () => {
  windowStart.value = ''
  void loadBase()
}, { immediate: true })
watch(businessDate, (bd) => {
  if (bd && !windowStart.value) void loadGrid()
})
watch(planId, () => void loadGrid())
</script>

<template>
  <div class="page-head">
    <h1 class="page-title">Rate grid</h1>
    <div class="nav">
      <button type="button" @click="shift(-7)">&larr; Week</button>
      <button type="button" @click="windowStart = businessDate; loadGrid()">Business date</button>
      <button type="button" @click="shift(7)">Week &rarr;</button>
      <select v-model.number="days" aria-label="Days shown" @change="loadGrid">
        <option :value="14">14 days</option>
        <option :value="28">28 days</option>
      </select>
    </div>
  </div>

  <p v-if="error" class="alert" role="alert" data-testid="form-error">{{ error.message }} <code>{{ error.code }}</code></p>
  <p v-if="property.currentId === null" class="muted">Select a property first.</p>
  <p v-else-if="!plans.length" class="muted" data-testid="no-plans">Create a rate plan first (Setup, Rate plans).</p>

  <template v-else>
    <label class="field plan-pick">
      <span>Rate plan</span>
      <select v-model.number="planId" name="rate_plan">
        <option v-for="p in plans" :key="p.id" :value="p.id">{{ p.code }} · {{ p.name }}{{ p.is_active ? '' : ' (inactive)' }}</option>
      </select>
      <small v-if="plan" class="hint" data-testid="price-mode">
        Amounts are {{ plan.price_mode === 'INCLUSIVE' ? 'inclusive: service and tax are contained' : 'exclusive: service and tax are added' }} ({{ plan.room_charge_code }}).
      </small>
    </label>

    <section class="card grid-card">
      <table class="grid" data-testid="grid">
        <thead>
          <tr>
            <th />
            <th v-for="d in dates" :key="d" :class="{ today: d === businessDate }">
              {{ weekdayOf(d) }}<br />{{ dayLabel(d) }}
            </th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="t in activeTypes" :key="t.id" :data-testid="`row-${t.code}`">
            <th scope="row">{{ t.code }}</th>
            <td v-for="d in dates" :key="d" :class="{ today: d === businessDate }">
              <button type="button" class="cell" :class="{ empty: cell(t.id, d) === undefined }" :disabled="!canManage" :data-testid="`cell-${t.code}-${d}`" @click="pick(t.id, d)">
                {{ cell(t.id, d) ?? '—' }}
              </button>
            </td>
          </tr>
        </tbody>
      </table>
    </section>

    <form v-if="canManage" class="card" novalidate data-testid="fill-form" @submit.prevent="apply">
      <h2>Set rates</h2>
      <p v-if="notice" class="muted" role="status" data-testid="notice">{{ notice }}</p>
      <div class="form-grid">
        <label class="field">
          <span>From</span>
          <input v-model="form.from" name="from" type="date" :aria-invalid="!!fieldError('from')" />
          <small v-if="fieldError('from')" class="error-text">{{ fieldError('from') }}</small>
        </label>
        <label class="field">
          <span>Until (not included)</span>
          <input v-model="form.to" name="to" type="date" :aria-invalid="!!fieldError('to')" />
          <small v-if="fieldError('to')" class="error-text">{{ fieldError('to') }}</small>
        </label>
        <label class="field">
          <span>Amount</span>
          <input v-model="form.amount" name="amount" inputmode="decimal" :aria-invalid="!!fieldError('amount')" />
          <small v-if="fieldError('amount')" class="error-text">{{ fieldError('amount') }}</small>
        </label>
      </div>
      <fieldset class="checks">
        <legend>Room types</legend>
        <label v-for="t in activeTypes" :key="t.id" class="check">
          <input type="checkbox" :checked="form.typeIds.includes(t.id)" :name="`type-${t.code}`" @change="toggle(form.typeIds, t.id)" />
          <span>{{ t.code }}</span>
        </label>
        <small v-if="fieldError('room_type_ids')" class="error-text">{{ fieldError('room_type_ids') }}</small>
      </fieldset>
      <fieldset class="checks">
        <legend>Only these days (all when none is ticked)</legend>
        <label v-for="w in WEEKDAYS" :key="w.key" class="check">
          <input type="checkbox" :checked="form.weekdays.includes(w.key)" :name="`weekday-${w.key}`" @change="toggle(form.weekdays, w.key)" />
          <span>{{ w.label }}</span>
        </label>
        <small v-if="fieldError('weekdays')" class="error-text">{{ fieldError('weekdays') }}</small>
      </fieldset>
      <div class="form-actions">
        <button type="submit" class="btn-primary" :disabled="saving">Apply</button>
      </div>
    </form>
  </template>
</template>

<style scoped>
.nav {
  display: flex;
  gap: 8px;
  align-items: center;
}
.nav select,
.plan-pick select {
  font: inherit;
  padding: 6px 8px;
  border-radius: 8px;
  border: 1px solid var(--border);
  background: var(--surface);
  color: var(--text);
}
.plan-pick {
  max-width: 420px;
  margin-bottom: 16px;
}
.grid-card {
  overflow-x: auto;
  padding: 8px;
}
.grid {
  border-collapse: collapse;
  font-size: 12px;
  min-width: 640px;
  width: 100%;
}
.grid th,
.grid td {
  border: 1px solid var(--border);
  padding: 0;
  text-align: center;
  font-weight: 500;
}
.grid thead th {
  color: var(--text-muted);
  padding: 4px;
  line-height: 1.2;
}
.grid tbody th {
  padding: 4px 8px;
  text-align: left;
}
.today {
  background: var(--accent-soft);
}
.cell {
  width: 100%;
  border: 0;
  border-radius: 0;
  background: transparent;
  padding: 6px 4px;
  font-variant-numeric: tabular-nums;
}
.cell.empty {
  color: var(--text-muted);
}
.cell:disabled {
  cursor: default;
}
.checks {
  border: 1px solid var(--border);
  border-radius: 8px;
  margin: 12px 0 0;
  display: flex;
  flex-wrap: wrap;
  gap: 4px 16px;
  padding: 8px 12px;
}
.checks legend {
  font-size: 13px;
  font-weight: 500;
  padding: 0 4px;
}
</style>
