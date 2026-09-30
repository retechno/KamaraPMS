<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { api } from '@/api/client'
import { fetchAll } from '@/api/paging'
import { ApiError } from '@/api/problem'
import type { ChargeCode, MealPlan, RatePlan } from '@/api/types'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'

const auth = useAuthStore()
const property = usePropertyStore()

const plans = ref<RatePlan[]>([])
const roomCodes = ref<ChargeCode[]>([])
const loaded = ref(false)
const error = ref<ApiError | null>(null)
const saving = ref(false)
const editing = ref<RatePlan | 'new' | null>(null)

const canManage = computed(() => auth.can('rate.manage', property.currentId))
const meals: Record<MealPlan, string> = { RO: 'Room only', BB: 'Bed and breakfast', HB: 'Half board', FB: 'Full board', AI: 'All inclusive' }
const blank = () => ({
  code: '', name: '', description: '', meal_plan: 'RO' as MealPlan, cancellation_policy: '', is_refundable: true, room_charge_code_id: 0, is_active: true,
})
const form = reactive(blank())
const fieldError = (field: string) => error.value?.fieldMessage(field)

// The codes a plan can sell through: active ROOM codes, plus the plan's current one if it has since become unusable.
const selectable = computed(() =>
  roomCodes.value.filter((c) => (c.is_active && c.charge_type === 'ROOM') || (editing.value !== 'new' && editing.value?.room_charge_code_id === c.id)),
)
const chosenMode = computed(() => roomCodes.value.find((c) => c.id === Number(form.room_charge_code_id))?.price_mode)

async function load(): Promise<void> {
  const propertyId = property.currentId
  plans.value = []
  loaded.value = false
  if (propertyId === null) return
  try {
    const path = { path: { propertyId } }
    const [p, c] = await Promise.all([
      fetchAll((cursor) => api.GET('/api/v1/properties/{propertyId}/rate-plans', { params: { ...path, query: { limit: 200, cursor } } })),
      fetchAll((cursor) => api.GET('/api/v1/properties/{propertyId}/charge-codes', { params: { ...path, query: { limit: 200, cursor, charge_type: 'ROOM' } } })),
    ])
    plans.value = p
    roomCodes.value = c
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    loaded.value = true
  }
}

function startNew(): void {
  Object.assign(form, blank(), { room_charge_code_id: roomCodes.value.find((c) => c.is_active)?.id ?? 0 })
  error.value = null
  editing.value = 'new'
}

function startEdit(p: RatePlan): void {
  Object.assign(form, blank(), {
    code: p.code, name: p.name, description: p.description ?? '', meal_plan: p.meal_plan, cancellation_policy: p.cancellation_policy ?? '',
    is_refundable: p.is_refundable, room_charge_code_id: p.room_charge_code_id, is_active: p.is_active,
  })
  error.value = null
  editing.value = p
}

async function save(): Promise<void> {
  const propertyId = property.currentId
  if (propertyId === null || editing.value === null) return
  saving.value = true
  error.value = null
  const common = {
    name: form.name, description: form.description, meal_plan: form.meal_plan, cancellation_policy: form.cancellation_policy,
    is_refundable: form.is_refundable, room_charge_code_id: Number(form.room_charge_code_id), is_active: form.is_active,
  }
  try {
    if (editing.value === 'new') {
      await api.POST('/api/v1/properties/{propertyId}/rate-plans', { params: { path: { propertyId } }, body: { code: form.code, ...common } })
    } else {
      await api.PATCH('/api/v1/properties/{propertyId}/rate-plans/{id}', { params: { path: { propertyId, id: editing.value.id } }, body: common })
    }
    editing.value = null
    await load()
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    saving.value = false
  }
}

watch(() => property.currentId, load, { immediate: true })
</script>

<template>
  <div class="page-head">
    <h1 class="page-title">Rate plans</h1>
    <button v-if="canManage && !editing" type="button" class="btn-primary" :disabled="!roomCodes.length" @click="startNew">New rate plan</button>
  </div>

  <p v-if="error" class="alert" role="alert" data-testid="form-error">
    {{ error.message }} <code>{{ error.code }}</code>
    <span v-if="error.code === 'RATE_PLAN_PRICE_MODE_MISMATCH'"> Create a new plan for the other price mode instead.</span>
  </p>
  <p v-if="property.currentId === null" class="muted">Select a property first.</p>
  <p v-else-if="loaded && !roomCodes.length" class="muted">Rate plans sell through a room charge code; there is none yet.</p>

  <form v-if="editing" class="card" novalidate data-testid="plan-form" @submit.prevent="save">
    <h2>{{ editing === 'new' ? 'New rate plan' : `Edit ${form.code}` }}</h2>
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
        <span>Meal plan</span>
        <select v-model="form.meal_plan" name="meal_plan">
          <option v-for="(label, k) in meals" :key="k" :value="k">{{ k }} · {{ label }}</option>
        </select>
      </label>
      <label class="field">
        <span>Room charge code</span>
        <select v-model="form.room_charge_code_id" name="room_charge_code_id" :aria-invalid="!!fieldError('room_charge_code_id')">
          <option v-for="c in selectable" :key="c.id" :value="c.id">{{ c.code }} · {{ c.name }}</option>
        </select>
        <small v-if="chosenMode" class="hint" data-testid="mode-hint">
          Rates are entered as {{ chosenMode === 'INCLUSIVE' ? 'inclusive prices (service and tax are contained)' : 'exclusive prices (service and tax are added)' }}.
        </small>
        <small v-if="fieldError('room_charge_code_id')" class="error-text">{{ fieldError('room_charge_code_id') }}</small>
      </label>
      <label class="field">
        <span>Description</span>
        <input v-model="form.description" name="description" />
      </label>
      <label class="field">
        <span>Cancellation policy</span>
        <input v-model="form.cancellation_policy" name="cancellation_policy" />
      </label>
      <label class="check">
        <input v-model="form.is_refundable" name="is_refundable" type="checkbox" />
        <span>Refundable</span>
      </label>
      <label class="check">
        <input v-model="form.is_active" name="is_active" type="checkbox" />
        <span>Active (can be booked)</span>
      </label>
    </div>
    <div class="form-actions">
      <button type="button" @click="editing = null">Cancel</button>
      <button type="submit" class="btn-primary" :disabled="saving">Save</button>
    </div>
  </form>

  <section class="card">
    <p v-if="loaded && !plans.length" class="muted" data-testid="empty">No rate plans yet.</p>
    <table v-else-if="plans.length" class="list">
      <thead>
        <tr>
          <th>Code</th>
          <th>Name</th>
          <th>Meal plan</th>
          <th>Room charge code</th>
          <th>Prices</th>
          <th>Refundable</th>
          <th>Status</th>
          <th v-if="canManage" />
        </tr>
      </thead>
      <tbody>
        <tr v-for="p in plans" :key="p.id" :data-testid="`plan-${p.code}`">
          <td><b>{{ p.code }}</b></td>
          <td>{{ p.name }}</td>
          <td>{{ p.meal_plan }}</td>
          <td>{{ p.room_charge_code }}</td>
          <td>{{ p.price_mode === 'INCLUSIVE' ? 'Inclusive' : 'Exclusive' }}</td>
          <td>{{ p.is_refundable ? 'Yes' : 'No' }}</td>
          <td>{{ p.is_active ? 'Active' : 'Inactive' }}</td>
          <td v-if="canManage"><button type="button" @click="startEdit(p)">Edit</button></td>
        </tr>
      </tbody>
    </table>
  </section>
</template>
