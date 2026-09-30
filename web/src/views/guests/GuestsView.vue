<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { RouterLink } from 'vue-router'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { CreateGuestRequest, CreatedGuest, Guest } from '@/api/types'
import GuestFields from '@/components/GuestFields.vue'
import { blankGuestForm } from '@/components/guestForm'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'

const auth = useAuthStore()
const property = usePropertyStore()

const query = ref('')
const results = ref<Guest[]>([])
const nextCursor = ref<string | undefined>()
const searched = ref(false)
const loading = ref(false)
const error = ref<ApiError | null>(null)

const creating = ref(false)
const form = reactive(blankGuestForm())
const saving = ref(false)
const created = ref<CreatedGuest | null>(null)

const canRead = computed(() => auth.can('guest.read', property.currentId))
const canWrite = computed(() => auth.can('guest.write', property.currentId))

async function search(more = false): Promise<void> {
  if (property.currentId === null) return
  loading.value = true
  error.value = null
  try {
    const { data } = await api.GET('/api/v1/guests', {
      params: { query: { q: query.value, property_id: property.currentId, limit: 50, cursor: more ? nextCursor.value : undefined } },
    })
    results.value = more ? [...results.value, ...(data?.data ?? [])] : (data?.data ?? [])
    nextCursor.value = data?.next_cursor
    searched.value = true
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    loading.value = false
  }
}

function startCreate(): void {
  Object.assign(form, blankGuestForm())
  created.value = null
  error.value = null
  creating.value = true
}

/** Sends only the fields that were filled in. */
function body(): CreateGuestRequest {
  const b: Record<string, unknown> = { origin_property_id: property.currentId }
  for (const [k, v] of Object.entries(form)) if (v !== '') b[k] = v
  return b as unknown as CreateGuestRequest
}

async function create(): Promise<void> {
  saving.value = true
  error.value = null
  try {
    const { data } = await api.POST('/api/v1/guests', { body: body() })
    created.value = data ?? null
    creating.value = false
    await search()
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    saving.value = false
  }
}

function fullName(g: Guest): string {
  return [g.first_name, g.last_name].filter(Boolean).join(' ')
}

watch(() => property.currentId, () => {
  results.value = []
  searched.value = false
  created.value = null
  if (property.currentId !== null && canRead.value) void search()
}, { immediate: true })
</script>

<template>
  <div class="page-head">
    <h1 class="page-title">Guests</h1>
    <button v-if="canWrite && !creating" type="button" class="btn-primary" @click="startCreate">New guest</button>
  </div>

  <p v-if="error" class="alert" role="alert" data-testid="form-error">{{ error.message }} <code>{{ error.code }}</code></p>
  <p v-if="property.currentId === null" class="muted">Select a property first.</p>
  <p v-else-if="!canRead" class="muted" data-testid="no-access">Your role at this property does not allow viewing guests.</p>

  <template v-else>
    <div v-if="created" class="alert warning" role="status" data-testid="created">
      Created <RouterLink :to="`/guests/${created.id}`">{{ created.code }}</RouterLink>.
      <template v-if="created.possible_duplicates.length || created.hidden_duplicate_count">
        <b>This may be a duplicate:</b>
        <ul data-testid="duplicates">
          <li v-for="d in created.possible_duplicates" :key="d.guest.id">
            <RouterLink :to="`/guests/${d.guest.id}`">{{ d.guest.code }} {{ fullName(d.guest) }}</RouterLink>
            <small> ({{ d.reasons.map((r) => r.toLowerCase().replaceAll('_', ' ')).join(', ') }})</small>
          </li>
          <li v-if="created.hidden_duplicate_count" data-testid="hidden-duplicates">
            {{ created.hidden_duplicate_count }} similar profile(s) exist that you cannot see.
          </li>
        </ul>
      </template>
    </div>

    <form v-if="creating" class="card" novalidate data-testid="create-form" @submit.prevent="create">
      <h2>New guest</h2>
      <GuestFields v-model="form" :error="error" />
      <div class="form-actions">
        <button type="button" @click="creating = false">Cancel</button>
        <button type="submit" class="btn-primary" :disabled="saving">Save</button>
      </div>
    </form>

    <form class="card search" role="search" @submit.prevent="search()">
      <label class="field grow">
        <span>Search</span>
        <input v-model="query" name="q" type="search" placeholder="Name, email, phone, ID number or guest code" />
      </label>
      <button type="submit" :disabled="loading">Search</button>
    </form>

    <section class="card">
      <p v-if="searched && !results.length" class="muted" data-testid="empty">No guests found.</p>
      <table v-else-if="results.length" class="list">
        <thead>
          <tr>
            <th>Code</th>
            <th>Name</th>
            <th>Email</th>
            <th>Phone</th>
            <th>Nationality</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="g in results" :key="g.id" :data-testid="`guest-${g.code}`">
            <td><RouterLink :to="`/guests/${g.id}`">{{ g.code }}</RouterLink></td>
            <td>{{ fullName(g) }}</td>
            <td>{{ g.email }}</td>
            <td>{{ g.phone }}</td>
            <td>{{ g.nationality }}</td>
          </tr>
        </tbody>
      </table>
      <div v-if="nextCursor" class="form-actions">
        <button type="button" :disabled="loading" data-testid="more" @click="search(true)">Load more</button>
      </div>
    </section>
  </template>
</template>

<style scoped>
.search {
  display: flex;
  gap: 12px;
  align-items: flex-end;
}
.grow {
  flex: 1;
}
</style>
