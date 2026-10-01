<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { Group, GroupMember } from '@/api/types'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import { statusLabel } from '@/utils/reservations'

const props = defineProps<{ id: string }>()
const auth = useAuthStore()
const property = usePropertyStore()

const group = ref<Group | null>(null)
const members = ref<GroupMember[]>([])
const error = ref<ApiError | null>(null)
const editing = ref(false)
const saving = ref(false)
const form = reactive({ name: '', arrival_date: '', departure_date: '', contact_name: '', contact_email: '', contact_phone: '', notes: '', is_active: true })

const pid = computed(() => property.currentId)
const canManage = computed(() => auth.can('group.manage', pid.value))
const canBook = computed(() => auth.can('reservation.create', pid.value) && !!group.value?.is_active)
const fieldError = (field: string) => error.value?.fieldMessage(field)
const path = () => ({ path: { propertyId: pid.value as number, id: Number(props.id) } })

async function load(): Promise<void> {
  if (pid.value === null || !auth.can('reservation.read', pid.value)) return
  error.value = null
  try {
    const [g, m] = await Promise.all([
      api.GET('/api/v1/properties/{propertyId}/groups/{id}', { params: path() }),
      api.GET('/api/v1/properties/{propertyId}/groups/{id}/reservations', { params: path() }),
    ])
    group.value = g.data ?? null
    members.value = m.data?.data ?? []
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  }
}

function startEdit(): void {
  if (!group.value) return
  const g = group.value
  Object.assign(form, {
    name: g.name, arrival_date: g.arrival_date, departure_date: g.departure_date, contact_name: g.contact_name ?? '', contact_email: g.contact_email ?? '',
    contact_phone: g.contact_phone ?? '', notes: g.notes ?? '', is_active: g.is_active,
  })
  error.value = null
  editing.value = true
}

async function save(): Promise<void> {
  saving.value = true
  error.value = null
  try {
    await api.PATCH('/api/v1/properties/{propertyId}/groups/{id}', { params: path(), body: { ...form } })
    editing.value = false
    await load()
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    saving.value = false
  }
}

watch(() => [pid.value, props.id], () => void load(), { immediate: true })
</script>

<template>
  <div class="page-head">
    <h1 class="page-title">{{ group ? `${group.code} · ${group.name}` : 'Group' }}</h1>
    <RouterLink to="/groups">Groups</RouterLink>
  </div>

  <p v-if="error" class="alert" role="alert" data-testid="form-error">
    {{ error.message }} <code>{{ error.code }}</code>
    <span v-if="error.code === 'GROUP_HAS_ROOMS_OUTSIDE_DATES'"> Move or cancel those rooms first.</span>
  </p>

  <section v-if="group && !editing" class="card" data-testid="group-summary">
    <dl class="facts">
      <div><dt>Dates</dt><dd>{{ group.arrival_date }} to {{ group.departure_date }}</dd></div>
      <div><dt>Company billed</dt><dd>{{ group.company_name || 'None (guests pay)' }}</dd></div>
      <div><dt>Contact</dt><dd>{{ group.contact_name }} {{ group.contact_email }} {{ group.contact_phone }}</dd></div>
      <div><dt>Reservations</dt><dd>{{ group.reservation_count }} ({{ group.room_count }} rooms)</dd></div>
      <div><dt>Status</dt><dd>{{ group.is_active ? 'Active' : 'Inactive: takes no more rooms' }}</dd></div>
    </dl>
    <p v-if="group.notes" class="muted">{{ group.notes }}</p>
    <div class="form-actions">
      <button v-if="canManage" type="button" data-testid="edit-group" @click="startEdit">Edit</button>
      <RouterLink v-if="canBook" class="btn-primary" :to="{ path: '/reservations/new', query: { group: group.id } }" data-testid="add-rooms">Add rooms</RouterLink>
    </div>
  </section>

  <form v-if="group && editing" class="card" novalidate data-testid="group-form" @submit.prevent="save">
    <h2>Edit {{ group.code }}</h2>
    <div class="form-grid">
      <label class="field">
        <span>Name</span>
        <input v-model="form.name" name="name" :aria-invalid="!!fieldError('name')" />
        <small v-if="fieldError('name')" class="error-text">{{ fieldError('name') }}</small>
      </label>
      <label class="field">
        <span>Arrival</span>
        <input v-model="form.arrival_date" name="arrival_date" type="date" :aria-invalid="!!fieldError('arrival_date')" />
        <small v-if="fieldError('arrival_date')" class="error-text">{{ fieldError('arrival_date') }}</small>
      </label>
      <label class="field">
        <span>Departure</span>
        <input v-model="form.departure_date" name="departure_date" type="date" :aria-invalid="!!fieldError('departure_date')" />
        <small v-if="fieldError('departure_date')" class="error-text">{{ fieldError('departure_date') }}</small>
      </label>
      <label class="field">
        <span>Contact person</span>
        <input v-model="form.contact_name" name="contact_name" />
      </label>
      <label class="field">
        <span>Contact email</span>
        <input v-model="form.contact_email" name="contact_email" type="email" :aria-invalid="!!fieldError('contact_email')" />
      </label>
      <label class="field">
        <span>Contact phone</span>
        <input v-model="form.contact_phone" name="contact_phone" />
      </label>
      <label class="field">
        <span>Notes</span>
        <input v-model="form.notes" name="notes" />
      </label>
      <label class="check">
        <input v-model="form.is_active" name="is_active" type="checkbox" />
        <span>Active (takes rooms)</span>
      </label>
    </div>
    <div class="form-actions">
      <button type="button" @click="editing = false">Cancel</button>
      <button type="submit" class="btn-primary" :disabled="saving">Save</button>
    </div>
  </form>

  <section v-if="group" class="card">
    <h2>Reservations</h2>
    <p v-if="!members.length" class="muted" data-testid="no-members">No room is booked into this group yet.</p>
    <table v-else class="list">
      <thead>
        <tr><th>Confirmation</th><th>Booker</th><th>Dates</th><th class="num">Rooms</th><th>Status</th></tr>
      </thead>
      <tbody>
        <tr v-for="m in members" :key="m.reservation_id" :data-testid="`member-${m.confirmation_number}`">
          <td><RouterLink :to="`/reservations/${m.reservation_id}`">{{ m.confirmation_number }}</RouterLink></td>
          <td>{{ m.guest_name }}</td>
          <td>{{ m.arrival_date }} to {{ m.departure_date }}</td>
          <td class="num">{{ m.room_count }}</td>
          <td>{{ statusLabel(m.status) }}</td>
        </tr>
      </tbody>
    </table>
  </section>
</template>
