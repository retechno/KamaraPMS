<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { api } from '@/api/client'
import { fetchAll } from '@/api/paging'
import { ApiError } from '@/api/problem'
import type { RoomType } from '@/api/types'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'

const auth = useAuthStore()
const property = usePropertyStore()

const types = ref<RoomType[]>([])
const loaded = ref(false)
const error = ref<ApiError | null>(null)
const saving = ref(false)
const editing = ref<RoomType | 'new' | null>(null)

const canManage = computed(() => auth.can('room.manage', property.currentId))

const blank = () => ({
  code: '',
  name: '',
  description: '',
  max_adult: 2,
  max_child: 0,
  max_occupancy: 2,
  base_occupancy: 2,
  sort_order: 0,
  is_active: true,
})
const form = reactive(blank())

const sorted = computed(() => [...types.value].sort((a, b) => a.sort_order - b.sort_order || a.code.localeCompare(b.code)))
const fieldError = (field: string) => error.value?.fieldMessage(field)

async function load(): Promise<void> {
  const propertyId = property.currentId
  types.value = []
  loaded.value = false
  if (propertyId === null) return
  try {
    types.value = await fetchAll((cursor) =>
      api.GET('/api/v1/properties/{propertyId}/room-types', { params: { path: { propertyId }, query: { limit: 200, cursor } } }),
    )
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    loaded.value = true
  }
}

function startNew(): void {
  Object.assign(form, blank())
  error.value = null
  editing.value = 'new'
}

function startEdit(t: RoomType): void {
  Object.assign(form, { ...blank(), ...t, description: t.description ?? '' })
  error.value = null
  editing.value = t
}

async function save(): Promise<void> {
  const propertyId = property.currentId
  if (propertyId === null || editing.value === null) return
  saving.value = true
  error.value = null
  const capacity = {
    max_adult: Number(form.max_adult),
    max_child: Number(form.max_child),
    max_occupancy: Number(form.max_occupancy),
    base_occupancy: Number(form.base_occupancy),
    sort_order: Number(form.sort_order),
  }
  try {
    if (editing.value === 'new') {
      await api.POST('/api/v1/properties/{propertyId}/room-types', {
        params: { path: { propertyId } },
        body: { code: form.code, name: form.name, description: form.description || undefined, is_active: form.is_active, ...capacity },
      })
    } else {
      await api.PATCH('/api/v1/properties/{propertyId}/room-types/{id}', {
        params: { path: { propertyId, id: editing.value.id } },
        body: { name: form.name, description: form.description, is_active: form.is_active, ...capacity },
      })
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
    <h1 class="page-title">Room types</h1>
    <button v-if="canManage && !editing" type="button" class="btn-primary" @click="startNew">New room type</button>
  </div>

  <p v-if="error" class="alert" role="alert" data-testid="form-error">{{ error.message }} <code>{{ error.code }}</code></p>
  <p v-if="property.currentId === null" class="muted">Select a property first.</p>

  <form v-if="editing" class="card" novalidate @submit.prevent="save">
    <h2>{{ editing === 'new' ? 'New room type' : `Edit ${form.code}` }}</h2>
    <div class="form-grid">
      <label class="field">
        <span>Code</span>
        <input v-model="form.code" name="code" :disabled="editing !== 'new'" :aria-invalid="!!fieldError('code')" />
        <small class="hint">Permanent, e.g. DLX.</small>
        <small v-if="fieldError('code')" class="error-text">{{ fieldError('code') }}</small>
      </label>
      <label class="field">
        <span>Name</span>
        <input v-model="form.name" name="name" :aria-invalid="!!fieldError('name')" />
        <small v-if="fieldError('name')" class="error-text">{{ fieldError('name') }}</small>
      </label>
      <label class="field">
        <span>Max adults</span>
        <input v-model="form.max_adult" name="max_adult" type="number" min="1" :aria-invalid="!!fieldError('max_adult')" />
        <small v-if="fieldError('max_adult')" class="error-text">{{ fieldError('max_adult') }}</small>
      </label>
      <label class="field">
        <span>Max children</span>
        <input v-model="form.max_child" name="max_child" type="number" min="0" :aria-invalid="!!fieldError('max_child')" />
        <small v-if="fieldError('max_child')" class="error-text">{{ fieldError('max_child') }}</small>
      </label>
      <label class="field">
        <span>Max occupancy</span>
        <input v-model="form.max_occupancy" name="max_occupancy" type="number" min="1" :aria-invalid="!!fieldError('max_occupancy')" />
        <small class="hint">At most adults + children.</small>
        <small v-if="fieldError('max_occupancy')" class="error-text">{{ fieldError('max_occupancy') }}</small>
      </label>
      <label class="field">
        <span>Base occupancy</span>
        <input v-model="form.base_occupancy" name="base_occupancy" type="number" min="1" :aria-invalid="!!fieldError('base_occupancy')" />
        <small class="hint">At most max occupancy.</small>
        <small v-if="fieldError('base_occupancy')" class="error-text">{{ fieldError('base_occupancy') }}</small>
      </label>
      <label class="field">
        <span>Sort order</span>
        <input v-model="form.sort_order" name="sort_order" type="number" />
      </label>
      <label class="field">
        <span>Description</span>
        <input v-model="form.description" name="description" />
      </label>
      <label class="check">
        <input v-model="form.is_active" name="is_active" type="checkbox" />
        <span>Active (sellable)</span>
      </label>
    </div>
    <div class="form-actions">
      <button type="button" @click="editing = null">Cancel</button>
      <button type="submit" class="btn-primary" :disabled="saving">Save</button>
    </div>
  </form>

  <section class="card">
    <p v-if="loaded && !types.length" class="muted" data-testid="empty">No room types yet. Room types group interchangeable rooms; availability is counted per type.</p>
    <table v-else-if="types.length" class="list">
      <thead>
        <tr>
          <th>Code</th>
          <th>Name</th>
          <th>Adults / children</th>
          <th>Occupancy (base / max)</th>
          <th>Status</th>
          <th v-if="canManage" />
        </tr>
      </thead>
      <tbody>
        <tr v-for="t in sorted" :key="t.id" :data-testid="`type-${t.code}`">
          <td>
            <b>{{ t.code }}</b>
          </td>
          <td>{{ t.name }}</td>
          <td>{{ t.max_adult }} / {{ t.max_child }}</td>
          <td>{{ t.base_occupancy }} / {{ t.max_occupancy }}</td>
          <td>{{ t.is_active ? 'Active' : 'Inactive' }}</td>
          <td v-if="canManage"><button type="button" @click="startEdit(t)">Edit</button></td>
        </tr>
      </tbody>
    </table>
  </section>
</template>
