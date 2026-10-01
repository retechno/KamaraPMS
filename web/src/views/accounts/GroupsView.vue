<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { api } from '@/api/client'
import { fetchAll } from '@/api/paging'
import { ApiError } from '@/api/problem'
import type { Company, Group } from '@/api/types'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'

const auth = useAuthStore()
const property = usePropertyStore()

const groups = ref<Group[]>([])
const companies = ref<Company[]>([])
const nextCursor = ref<string | undefined>()
const loaded = ref(false)
const error = ref<ApiError | null>(null)
const saving = ref(false)
const creating = ref(false)
const activeOnly = ref(true)
const missingDates = ref(false)

const canRead = computed(() => auth.can('reservation.read', property.currentId))
const canManage = computed(() => auth.can('group.manage', property.currentId))
const businessDate = computed(() => property.clock?.business_date ?? '')
const blank = () => ({ code: '', name: '', company_id: 0, contact_name: '', contact_email: '', contact_phone: '', arrival_date: businessDate.value, departure_date: '', notes: '' })
const form = reactive(blank())
const fieldError = (field: string) => error.value?.fieldMessage(field)

async function load(more = false): Promise<void> {
  const propertyId = property.currentId
  if (propertyId === null || !canRead.value) return
  error.value = null
  try {
    const { data } = await api.GET('/api/v1/properties/{propertyId}/groups', {
      params: { path: { propertyId }, query: { limit: 50, cursor: more ? nextCursor.value : undefined, active: activeOnly.value ? true : undefined } },
    })
    groups.value = more ? [...groups.value, ...(data?.data ?? [])] : (data?.data ?? [])
    nextCursor.value = data?.next_cursor
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    loaded.value = true
  }
}

async function loadCompanies(): Promise<void> {
  const propertyId = property.currentId
  if (propertyId === null || !canRead.value) return
  try {
    companies.value = await fetchAll((cursor) => api.GET('/api/v1/properties/{propertyId}/companies', { params: { path: { propertyId }, query: { limit: 200, cursor, active: true } } }))
  } catch {
    companies.value = [] // a role that cannot see companies can still make a group without one
  }
}

function startNew(): void {
  Object.assign(form, blank())
  error.value = null
  missingDates.value = false
  creating.value = true
  void loadCompanies()
}

async function save(): Promise<void> {
  const propertyId = property.currentId
  if (propertyId === null) return
  error.value = null
  // An empty date cannot be sent (the server cannot read "" as a date): ask for it here.
  if (!form.arrival_date || !form.departure_date) {
    missingDates.value = true
    return
  }
  missingDates.value = false
  saving.value = true
  try {
    await api.POST('/api/v1/properties/{propertyId}/groups', {
      params: { path: { propertyId } },
      body: {
        code: form.code, name: form.name, company_id: form.company_id || null, contact_name: form.contact_name, contact_email: form.contact_email,
        contact_phone: form.contact_phone, arrival_date: form.arrival_date, departure_date: form.departure_date, notes: form.notes, is_active: true,
      },
    })
    creating.value = false
    await load()
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    saving.value = false
  }
}

watch(() => property.currentId, () => {
  groups.value = []
  loaded.value = false
  void load()
}, { immediate: true })
watch(activeOnly, () => void load())
</script>

<template>
  <div class="page-head">
    <h1 class="page-title">Groups</h1>
    <button v-if="canManage && !creating" type="button" class="btn-primary" @click="startNew">New group</button>
  </div>

  <p v-if="error" class="alert" role="alert" data-testid="form-error">{{ error.message }} <code>{{ error.code }}</code></p>
  <p v-if="property.currentId === null" class="muted">Select a property first.</p>
  <p v-else-if="!canRead" class="muted" data-testid="no-access">Your role at this property cannot see groups.</p>

  <form v-if="creating" class="card" novalidate data-testid="group-form" @submit.prevent="save">
    <h2>New group</h2>
    <div class="form-grid">
      <label class="field">
        <span>Code</span>
        <input v-model="form.code" name="code" :aria-invalid="!!fieldError('code')" />
        <small v-if="fieldError('code')" class="error-text">{{ fieldError('code') }}</small>
      </label>
      <label class="field">
        <span>Name</span>
        <input v-model="form.name" name="name" :aria-invalid="!!fieldError('name')" />
        <small v-if="fieldError('name')" class="error-text">{{ fieldError('name') }}</small>
      </label>
      <label class="field">
        <span>Arrival</span>
        <input v-model="form.arrival_date" name="arrival_date" type="date" :min="businessDate" :aria-invalid="!!fieldError('arrival_date')" />
        <small v-if="fieldError('arrival_date')" class="error-text">{{ fieldError('arrival_date') }}</small>
      </label>
      <label class="field">
        <span>Departure</span>
        <input v-model="form.departure_date" name="departure_date" type="date" :min="form.arrival_date" :aria-invalid="!!fieldError('departure_date')" />
        <small v-if="fieldError('departure_date')" class="error-text">{{ fieldError('departure_date') }}</small>
      </label>
      <label class="field">
        <span>Company that is billed</span>
        <select v-model.number="form.company_id" name="company_id">
          <option :value="0">None (guests pay)</option>
          <option v-for="c in companies" :key="c.id" :value="c.id">{{ c.code }} · {{ c.name }}</option>
        </select>
      </label>
      <label class="field">
        <span>Contact person</span>
        <input v-model="form.contact_name" name="contact_name" />
      </label>
      <label class="field">
        <span>Contact email</span>
        <input v-model="form.contact_email" name="contact_email" type="email" :aria-invalid="!!fieldError('contact_email')" />
        <small v-if="fieldError('contact_email')" class="error-text">{{ fieldError('contact_email') }}</small>
      </label>
      <label class="field">
        <span>Contact phone</span>
        <input v-model="form.contact_phone" name="contact_phone" />
      </label>
      <label class="field">
        <span>Notes</span>
        <input v-model="form.notes" name="notes" />
      </label>
    </div>
    <p v-if="missingDates" class="error-text" role="alert" data-testid="dates-required">Arrival and departure dates are required.</p>
    <div class="form-actions">
      <button type="button" @click="creating = false">Cancel</button>
      <button type="submit" class="btn-primary" :disabled="saving">Save</button>
    </div>
  </form>

  <section v-if="canRead" class="card">
    <label class="check">
      <input v-model="activeOnly" type="checkbox" name="active_only" />
      <span>Active groups only</span>
    </label>
    <p v-if="loaded && !groups.length" class="muted" data-testid="empty">No groups yet.</p>
    <table v-else-if="groups.length" class="list">
      <thead>
        <tr>
          <th>Code</th>
          <th>Name</th>
          <th>Company</th>
          <th>Dates</th>
          <th class="num">Reservations</th>
          <th class="num">Rooms</th>
          <th>Status</th>
        </tr>
      </thead>
      <tbody>
        <tr v-for="g in groups" :key="g.id" :data-testid="`group-${g.code}`">
          <td><RouterLink :to="`/groups/${g.id}`"><b>{{ g.code }}</b></RouterLink></td>
          <td>{{ g.name }}</td>
          <td>{{ g.company_name }}</td>
          <td>{{ g.arrival_date }} to {{ g.departure_date }}</td>
          <td class="num">{{ g.reservation_count }}</td>
          <td class="num">{{ g.room_count }}</td>
          <td>{{ g.is_active ? 'Active' : 'Inactive' }}</td>
        </tr>
      </tbody>
    </table>
    <button v-if="nextCursor" type="button" data-testid="more" @click="load(true)">Load more</button>
  </section>
</template>
