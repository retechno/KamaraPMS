<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { GuestHistoryItem, GuestView, PatchGuestRequest } from '@/api/types'
import GuestFields from '@/components/GuestFields.vue'
import { blankGuestForm } from '@/components/guestForm'
import { formatBusinessDate } from '@/utils/dates'

const props = defineProps<{ id: string }>()

const guest = ref<GuestView | null>(null)
const history = ref<GuestHistoryItem[]>([])
const hidden = ref(0)
const nextCursor = ref<string | undefined>()
const form = reactive(blankGuestForm())
const error = ref<ApiError | null>(null)
const notFound = ref(false)
const saving = ref(false)
const saved = ref(false)

const guestId = computed(() => Number(props.id))
const title = computed(() => (guest.value ? [guest.value.first_name, guest.value.last_name].filter(Boolean).join(' ') : 'Guest'))

function fill(g: GuestView): void {
  guest.value = g
  for (const k of Object.keys(form) as (keyof typeof form)[]) form[k] = (g[k] as string | undefined) ?? ''
}

async function load(): Promise<void> {
  try {
    const { data } = await api.GET('/api/v1/guests/{id}', { params: { path: { id: guestId.value } } })
    if (data) fill(data)
    await loadHistory()
  } catch (e) {
    if (e instanceof ApiError && e.code === 'GUEST_NOT_FOUND') notFound.value = true
    else error.value = e instanceof ApiError ? e : null
  }
}

async function loadHistory(more = false): Promise<void> {
  const { data } = await api.GET('/api/v1/guests/{id}/history', {
    params: { path: { id: guestId.value }, query: { limit: 50, cursor: more ? nextCursor.value : undefined } },
  })
  history.value = more ? [...history.value, ...(data?.data ?? [])] : (data?.data ?? [])
  hidden.value = data?.hidden_count ?? 0
  nextCursor.value = data?.next_cursor
}

/** Every field is sent: an empty string clears it on the server. */
async function save(): Promise<void> {
  saving.value = true
  saved.value = false
  error.value = null
  try {
    const { data } = await api.PATCH('/api/v1/guests/{id}', {
      params: { path: { id: guestId.value } },
      body: { ...form } as PatchGuestRequest,
    })
    if (data) fill(data)
    saved.value = true
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    saving.value = false
  }
}

onMounted(load)
</script>

<template>
  <p v-if="notFound" class="muted" data-testid="not-found">This guest does not exist or is not accessible.</p>
  <template v-else>
    <div class="page-head">
      <h1 class="page-title">{{ title }}</h1>
      <code v-if="guest">{{ guest.code }}</code>
    </div>

    <p v-if="error" class="alert" role="alert" data-testid="form-error">{{ error.message }} <code>{{ error.code }}</code></p>
    <p v-if="saved" class="muted" role="status" data-testid="saved">Saved.</p>

    <form v-if="guest" class="card" novalidate @submit.prevent="save">
      <h2>Profile</h2>
      <GuestFields v-model="form" :error="error" :disabled="!guest.can_edit" />
      <p v-if="!guest.can_edit" class="muted" data-testid="read-only">
        You can view this profile but not edit it: editing needs guest.write at a property where the guest has stayed or booked.
      </p>
      <div v-else class="form-actions">
        <button type="submit" class="btn-primary" :disabled="saving">Save</button>
      </div>
    </form>

    <section class="card">
      <h2>History</h2>
      <p v-if="!history.length" class="muted" data-testid="no-history">No reservations or stays yet.</p>
      <table v-else class="list">
        <thead>
          <tr>
            <th>Property</th>
            <th>Type</th>
            <th>Number</th>
            <th>Role</th>
            <th>Dates</th>
            <th>Status</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="h in history" :key="`${h.type}-${h.id}`">
            <td>{{ h.property_code }}</td>
            <td>{{ h.type === 'STAY' ? 'Stay' : 'Reservation' }}</td>
            <td>{{ h.number }}</td>
            <td>{{ h.role.toLowerCase() }}</td>
            <td>{{ formatBusinessDate(h.arrival_date) }} to {{ formatBusinessDate(h.departure_date) }}</td>
            <td>{{ h.status }}</td>
          </tr>
        </tbody>
      </table>
      <p v-if="hidden" class="muted" data-testid="hidden">
        {{ hidden }} more {{ hidden === 1 ? 'entry' : 'entries' }} at other properties {{ hidden === 1 ? 'is' : 'are' }} not shown.
      </p>
      <div v-if="nextCursor" class="form-actions">
        <button type="button" @click="loadHistory(true)">Load more</button>
      </div>
    </section>
  </template>
</template>
