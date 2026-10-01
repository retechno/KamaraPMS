<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { HousekeepingBoardRoom, HousekeepingStaffMember, MaintenanceRequest } from '@/api/types'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import { addDays } from '@/utils/dates'

const auth = useAuthStore()
const property = usePropertyStore()
const route = useRoute()

const rows = ref<MaintenanceRequest[]>([])
const nextCursor = ref<string | undefined>()
const staff = ref<HousekeepingStaffMember[]>([])
const roomList = ref<HousekeepingBoardRoom[]>([])
const loaded = ref(false)
const error = ref<ApiError | null>(null)
const notice = ref('')
const busy = ref(false)
const filter = reactive({ view: 'open', category: '', priority: '' })
const selectedId = ref<number | null>(null)
const creating = ref(false)
const form = reactive({ room_id: 0, location: '', category: 'OTHER', description: '', priority: 'NORMAL' })
const action = reactive({ assignTo: 0, note: '', releaseBlock: true, blockType: 'OOO', blockEnd: '' })
// What the selected request is being closed with (the note is asked first).
const closing = ref<'resolve' | 'cancel' | null>(null)

const pid = computed(() => property.currentId)
const can = (p: string) => auth.can(p, pid.value)
const businessDate = computed(() => property.clock?.business_date ?? '')
const selected = computed(() => rows.value.find((r) => r.id === selectedId.value) ?? null)
const isOpen = (r: MaintenanceRequest) => r.status === 'OPEN' || r.status === 'IN_PROGRESS'
const fieldError = (field: string) => error.value?.fieldMessage(field)
const LABEL: Record<string, string> = { OPEN: 'Open', IN_PROGRESS: 'In progress', RESOLVED: 'Resolved', CANCELLED: 'Cancelled' }
const CATEGORIES = ['PLUMBING', 'ELECTRICAL', 'AC', 'FURNITURE', 'APPLIANCE', 'OTHER']
const PRIORITIES = ['LOW', 'NORMAL', 'HIGH', 'URGENT']
const where = (r: MaintenanceRequest) => (r.room_number ? `Room ${r.room_number}` : r.location || '')

async function load(more = false): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null || !can('maintenance.report')) return
  try {
    const query: Record<string, unknown> = { limit: 50, cursor: more ? nextCursor.value : undefined }
    if (filter.view === 'open') query.open = true
    else if (filter.view !== 'all') query.status = filter.view
    if (filter.category) query.category = filter.category
    if (filter.priority) query.priority = filter.priority
    const { data } = await api.GET('/api/v1/properties/{propertyId}/maintenance-requests', { params: { path: { propertyId }, query: query as never } })
    rows.value = more ? [...rows.value, ...(data?.data ?? [])] : (data?.data ?? [])
    nextCursor.value = data?.next_cursor
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    loaded.value = true
  }
}

async function loadHelpers(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null) return
  if (can('maintenance.manage')) {
    try {
      const { data } = await api.GET('/api/v1/properties/{propertyId}/maintenance-staff', { params: { path: { propertyId } } })
      staff.value = data?.data ?? []
    } catch {
      staff.value = []
    }
  }
  try {
    const { data } = await api.GET('/api/v1/properties/{propertyId}/housekeeping', { params: { path: { propertyId } } })
    roomList.value = data?.data ?? []
  } catch {
    roomList.value = []
  }
}

// One call, then the list is reloaded also after a refusal (somebody else may have changed the request).
async function run(what: () => Promise<unknown>, done: string): Promise<void> {
  busy.value = true
  error.value = null
  notice.value = ''
  let failure: ApiError | null = null
  try {
    await what()
    notice.value = done
  } catch (e) {
    failure = e instanceof ApiError ? e : null
  } finally {
    busy.value = false
  }
  await load()
  error.value = failure
}

const base = () => ({ path: { propertyId: pid.value as number, id: selectedId.value as number } })

function startNew(room = 0): void {
  Object.assign(form, { room_id: room, location: '', category: 'OTHER', description: '', priority: 'NORMAL' })
  creating.value = true
  error.value = null
}

async function report(): Promise<void> {
  busy.value = true
  error.value = null
  try {
    const { data } = await api.POST('/api/v1/properties/{propertyId}/maintenance-requests', {
      params: { path: { propertyId: pid.value as number } },
      body: {
        room_id: form.room_id || null, location: form.location || undefined, category: form.category as 'OTHER', description: form.description,
        priority: form.priority as 'NORMAL',
      },
    })
    creating.value = false
    notice.value = `Request ${data?.request_number ?? ''} reported.`
    selectedId.value = data?.id ?? null
    filter.view = 'open'
    await load()
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    busy.value = false
  }
}

function select(r: MaintenanceRequest): void {
  selectedId.value = r.id
  closing.value = null
  Object.assign(action, { assignTo: r.assigned_to ?? 0, note: '', releaseBlock: true, blockEnd: addDays(businessDate.value || r.business_date, 1) })
  error.value = null
}

const assign = () => run(() => api.POST('/api/v1/properties/{propertyId}/maintenance-requests/{id}/assign', { params: base(), body: { user_id: action.assignTo || null } }), 'Saved.')
const start = () => run(() => api.POST('/api/v1/properties/{propertyId}/maintenance-requests/{id}/start', { params: base() }), 'Started.')
const reopen = () => run(() => api.POST('/api/v1/properties/{propertyId}/maintenance-requests/{id}/reopen', { params: base() }), 'Reopened.')
const setPriority = (priority: string) =>
  run(() => api.PATCH('/api/v1/properties/{propertyId}/maintenance-requests/{id}', { params: base(), body: { priority: priority as 'NORMAL' } }), 'Priority changed.')

async function close(): Promise<void> {
  const r = selected.value
  if (!r || !closing.value) return
  const body = { note: action.note || undefined, release_block: action.releaseBlock && r.block?.status === 'ACTIVE' }
  const verb = closing.value
  await run(
    () => (verb === 'resolve'
      ? api.POST('/api/v1/properties/{propertyId}/maintenance-requests/{id}/resolve', { params: base(), body })
      : api.POST('/api/v1/properties/{propertyId}/maintenance-requests/{id}/cancel', { params: base(), body })),
    verb === 'resolve' ? 'Resolved.' : 'Cancelled.',
  )
  if (!error.value) closing.value = null
}

const block = () => run(() => api.POST('/api/v1/properties/{propertyId}/maintenance-requests/{id}/block', {
  params: base(), body: { block_type: action.blockType as 'OOO', end_date: action.blockEnd },
}), 'The room is out of sale.')

const canBlock = computed(() => !!selected.value?.room_id && isOpen(selected.value) && selected.value.block?.status !== 'ACTIVE' && can('maintenance.manage') && can('room_block.manage'))

watch(() => pid.value, () => {
  rows.value = []
  loaded.value = false
  selectedId.value = null
  void load()
  void loadHelpers()
}, { immediate: true })
watch(() => [filter.view, filter.category, filter.priority], () => void load())
// Coming from the housekeeping board ("Report a problem" on a room).
watch(() => route.query.room, (v) => {
  if (v && can('maintenance.report')) startNew(Number(v))
}, { immediate: true })
</script>

<template>
  <div class="page-head">
    <h1 class="page-title">Maintenance</h1>
    <button v-if="can('maintenance.report') && !creating" type="button" class="btn-primary" data-testid="new-request" @click="startNew()">Report a problem</button>
  </div>

  <p v-if="error" class="alert" role="alert" data-testid="mt-error">{{ error.message }} <code>{{ error.code }}</code></p>
  <p v-if="notice" class="notice" role="status" data-testid="notice">{{ notice }}</p>
  <p v-if="pid === null" class="muted">Select a property first.</p>
  <p v-else-if="!can('maintenance.report')" class="muted" data-testid="no-access">Your role at this property cannot see maintenance requests: the <code>maintenance.report</code> permission is needed.</p>

  <template v-else>
    <form v-if="creating" class="card" novalidate data-testid="request-form" @submit.prevent="report">
      <h2>Report a problem</h2>
      <div class="form-grid">
        <label class="field">
          <span>Room</span>
          <select v-model.number="form.room_id" name="room_id">
            <option :value="0">Not a room (give the place)</option>
            <option v-for="r in roomList" :key="r.room_id" :value="r.room_id">{{ r.room_number }} · {{ r.room_type_code }}</option>
          </select>
        </label>
        <label class="field">
          <span>Place</span>
          <input v-model="form.location" name="location" maxlength="150" placeholder="Lobby, pool, kitchen…" :aria-invalid="!!fieldError('location')" />
          <small v-if="fieldError('location')" class="error-text">{{ fieldError('location') }}</small>
        </label>
        <label class="field">
          <span>Category</span>
          <select v-model="form.category" name="category">
            <option v-for="c in CATEGORIES" :key="c" :value="c">{{ c }}</option>
          </select>
        </label>
        <label class="field">
          <span>Priority</span>
          <select v-model="form.priority" name="priority">
            <option v-for="p in PRIORITIES" :key="p" :value="p">{{ p }}</option>
          </select>
        </label>
        <label class="field wide">
          <span>What is wrong</span>
          <input v-model="form.description" name="description" maxlength="1000" :aria-invalid="!!fieldError('description')" />
          <small v-if="fieldError('description')" class="error-text">{{ fieldError('description') }}</small>
        </label>
      </div>
      <div class="form-actions">
        <button type="button" @click="creating = false">Cancel</button>
        <button type="submit" class="btn-primary" :disabled="busy">Report</button>
      </div>
    </form>

    <section class="card">
      <form class="filters" novalidate @submit.prevent="load()">
        <label class="field">
          <span>Show</span>
          <select v-model="filter.view" name="view">
            <option value="open">Open and in progress</option>
            <option value="all">Everything</option>
            <option value="RESOLVED">Resolved</option>
            <option value="CANCELLED">Cancelled</option>
          </select>
        </label>
        <label class="field">
          <span>Category</span>
          <select v-model="filter.category" name="filter_category">
            <option value="">Any</option>
            <option v-for="c in CATEGORIES" :key="c" :value="c">{{ c }}</option>
          </select>
        </label>
        <label class="field">
          <span>Priority</span>
          <select v-model="filter.priority" name="filter_priority">
            <option value="">Any</option>
            <option v-for="p in PRIORITIES" :key="p" :value="p">{{ p }}</option>
          </select>
        </label>
      </form>
      <p v-if="loaded && !rows.length" class="muted" data-testid="empty">No requests.</p>
      <table v-else-if="rows.length" class="list" data-testid="requests">
        <thead><tr><th>Number</th><th>Where</th><th>Category</th><th>Priority</th><th>Problem</th><th>Assigned to</th><th>Status</th></tr></thead>
        <tbody>
          <tr v-for="r in rows" :key="r.id" :class="{ chosen: r.id === selectedId, closed: !isOpen(r) }" :data-testid="`request-${r.request_number}`">
            <td><button type="button" class="link" :data-testid="`select-${r.request_number}`" @click="select(r)">{{ r.request_number }}</button></td>
            <td>{{ where(r) }}<small v-if="r.block && r.block.status === 'ACTIVE'" class="pill blocked" data-testid="blocked"> {{ r.block.block_type }} until {{ r.block.end_date }}</small></td>
            <td>{{ r.category }}</td>
            <td><span class="pill" :class="`pr-${r.priority.toLowerCase()}`">{{ r.priority }}</span></td>
            <td>{{ r.description }}</td>
            <td>{{ r.assignee_name || '—' }}</td>
            <td>{{ LABEL[r.status] }}</td>
          </tr>
        </tbody>
      </table>
      <button v-if="nextCursor" type="button" data-testid="more" @click="load(true)">Load more</button>
    </section>

    <section v-if="selected" class="card" data-testid="detail">
      <h2>{{ selected.request_number }} · {{ where(selected) }}</h2>
      <p>{{ selected.description }}</p>
      <p class="muted">
        Reported {{ selected.business_date }}<template v-if="selected.reporter_name"> by {{ selected.reporter_name }}</template>.
        <template v-if="selected.resolution_note"> Note: {{ selected.resolution_note }}</template>
      </p>

      <template v-if="can('maintenance.manage')">
        <div v-if="isOpen(selected)" class="filters">
          <label class="field">
            <span>Priority</span>
            <select :value="selected.priority" name="detail_priority" @change="setPriority(($event.target as HTMLSelectElement).value)">
              <option v-for="p in PRIORITIES" :key="p" :value="p">{{ p }}</option>
            </select>
          </label>
          <label class="field">
            <span>Assigned to</span>
            <select v-model.number="action.assignTo" name="assign_to">
              <option :value="0">Nobody</option>
              <option v-for="s in staff" :key="s.id" :value="s.id">{{ s.full_name }}</option>
            </select>
          </label>
          <button type="button" :disabled="busy" data-testid="assign" @click="assign">Save assignment</button>
        </div>

        <div class="form-actions">
          <button v-if="selected.status === 'OPEN'" type="button" :disabled="busy" data-testid="start" @click="start">Start work</button>
          <button v-if="isOpen(selected)" type="button" class="btn-primary" :disabled="busy" data-testid="resolve" @click="closing = 'resolve'">Resolve</button>
          <button v-if="isOpen(selected)" type="button" :disabled="busy" data-testid="cancel" @click="closing = 'cancel'">Cancel request</button>
          <button v-if="selected.status === 'RESOLVED'" type="button" :disabled="busy" data-testid="reopen" @click="reopen">Reopen</button>
        </div>

        <form v-if="closing" class="filters" novalidate data-testid="close-form" @submit.prevent="close">
          <label class="field grow">
            <span>{{ closing === 'cancel' ? 'Why is it cancelled?' : 'What was done?' }}</span>
            <input v-model="action.note" name="note" maxlength="1000" :aria-invalid="!!fieldError('note')" />
            <small v-if="fieldError('note')" class="error-text">{{ fieldError('note') }}</small>
          </label>
          <label v-if="selected.block?.status === 'ACTIVE'" class="check">
            <input v-model="action.releaseBlock" name="release_block" type="checkbox" />
            <span>Put the room back on sale ({{ selected.block.block_type }} until {{ selected.block.end_date }})</span>
          </label>
          <button type="button" @click="closing = null">Back</button>
          <button type="submit" class="btn-primary" :disabled="busy || (closing === 'cancel' && !action.note.trim())" data-testid="close-submit">
            {{ closing === 'cancel' ? 'Cancel the request' : 'Mark resolved' }}
          </button>
        </form>

        <form v-if="canBlock" class="filters" novalidate data-testid="block-form" @submit.prevent="block">
          <label class="field">
            <span>Take the room out of sale</span>
            <select v-model="action.blockType" name="block_type">
              <option value="OOO">Out of order</option>
              <option value="OOS">Out of service</option>
            </select>
          </label>
          <label class="field">
            <span>Until (not including)</span>
            <input v-model="action.blockEnd" name="block_end" type="date" :min="businessDate" :aria-invalid="!!fieldError('end_date')" />
            <small v-if="fieldError('end_date')" class="error-text">{{ fieldError('end_date') }}</small>
          </label>
          <button type="submit" :disabled="busy || !action.blockEnd" data-testid="block-room">Block room</button>
        </form>
      </template>
    </section>
  </template>
</template>

<style scoped>
.chosen td {
  background: var(--accent-soft);
}
.closed td {
  color: var(--muted, #6b7280);
}
.pill {
  display: inline-block;
  padding: 1px 8px;
  border-radius: 999px;
  border: 1px solid var(--border);
  font-size: 12px;
  font-weight: 600;
}
.pill.blocked {
  margin-left: 6px;
  border-color: var(--warning);
}
.pr-urgent {
  border-color: var(--danger);
  font-weight: 700;
}
.pr-high {
  border-color: var(--warning);
}
.link {
  border: 0;
  background: none;
  color: var(--accent);
  padding: 0;
  text-decoration: underline;
}
.wide {
  grid-column: 1 / -1;
}
.grow {
  flex: 1;
}
</style>
