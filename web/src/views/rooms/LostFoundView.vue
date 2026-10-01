<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { HousekeepingBoardRoom, LostFoundItem, LostFoundOwner } from '@/api/types'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'

const auth = useAuthStore()
const property = usePropertyStore()

const rows = ref<LostFoundItem[]>([])
const nextCursor = ref<string | undefined>()
const roomList = ref<HousekeepingBoardRoom[]>([])
const owners = ref<LostFoundOwner[]>([])
const loaded = ref(false)
const error = ref<ApiError | null>(null)
const notice = ref('')
const busy = ref(false)
const creating = ref(false)
const selectedId = ref<number | null>(null)
const filter = reactive({ status: 'STORED', category: '', q: '' })
const form = reactive({ room_id: 0, location: '', category: 'OTHER', description: '', storage_location: '', possible_owner: '', notes: '' })
const closing = ref<'return' | 'dispose' | null>(null)
const handBack = reactive({ claimant_name: '', claimant_proof: '', note: '' })
const disposeReason = ref('')
const storage = ref('')

const pid = computed(() => property.currentId)
const can = (p: string) => auth.can(p, pid.value)
const selected = computed(() => rows.value.find((r) => r.id === selectedId.value) ?? null)
const fieldError = (field: string) => error.value?.fieldMessage(field)
const CATEGORIES = ['ELECTRONICS', 'CLOTHING', 'DOCUMENTS', 'JEWELRY', 'BAGS', 'OTHER']
const STATUS_LABEL: Record<string, string> = { STORED: 'Stored', RETURNED: 'Returned', DISPOSED: 'Disposed' }
const where = (i: LostFoundItem) => (i.room_number ? `Room ${i.room_number}` : i.location || '')

async function load(more = false): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null || !can('lostfound.report')) return
  try {
    const query: Record<string, unknown> = { limit: 50, cursor: more ? nextCursor.value : undefined }
    if (filter.status) query.status = filter.status
    if (filter.category) query.category = filter.category
    if (filter.q.trim()) query.q = filter.q.trim()
    const { data } = await api.GET('/api/v1/properties/{propertyId}/lost-found', { params: { path: { propertyId }, query: query as never } })
    rows.value = more ? [...rows.value, ...(data?.data ?? [])] : (data?.data ?? [])
    nextCursor.value = data?.next_cursor
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    loaded.value = true
  }
}

async function loadRooms(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null) return
  try {
    const { data } = await api.GET('/api/v1/properties/{propertyId}/housekeeping', { params: { path: { propertyId } } })
    roomList.value = data?.data ?? []
  } catch {
    roomList.value = []
  }
}

// One call; the list is reloaded also after a refusal (another desk may have closed the item).
async function run(what: () => Promise<unknown>, done: string): Promise<boolean> {
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
  return !failure
}

const base = () => ({ path: { propertyId: pid.value as number, id: selectedId.value as number } })

function startNew(): void {
  Object.assign(form, { room_id: 0, location: '', category: 'OTHER', description: '', storage_location: '', possible_owner: '', notes: '' })
  creating.value = true
  error.value = null
}

async function record(): Promise<void> {
  const ok = await run(async () => {
    await api.POST('/api/v1/properties/{propertyId}/lost-found', {
      params: { path: { propertyId: pid.value as number } },
      body: {
        room_id: form.room_id || null, location: form.location || undefined, category: form.category as 'OTHER', description: form.description,
        storage_location: form.storage_location || undefined, possible_owner: form.possible_owner || undefined, notes: form.notes || undefined,
      },
    })
  }, 'Item recorded.')
  if (ok) creating.value = false
}

async function select(i: LostFoundItem): Promise<void> {
  selectedId.value = i.id
  closing.value = null
  storage.value = i.storage_location ?? ''
  Object.assign(handBack, { claimant_name: i.possible_owner ?? '', claimant_proof: '', note: '' })
  disposeReason.value = ''
  owners.value = []
  error.value = null
  if (i.room_id && can('reservation.read') && pid.value !== null) {
    try {
      const { data } = await api.GET('/api/v1/properties/{propertyId}/lost-found/{id}/possible-owners', { params: base() })
      owners.value = data?.data ?? []
    } catch {
      owners.value = []
    }
  }
}

const saveStorage = () => run(() => api.PATCH('/api/v1/properties/{propertyId}/lost-found/{id}', { params: base(), body: { storage_location: storage.value } }), 'Saved.')

async function finish(): Promise<void> {
  const how = closing.value
  if (!how) return
  const ok = await run(
    () => (how === 'return'
      ? api.POST('/api/v1/properties/{propertyId}/lost-found/{id}/return', { params: base(), body: { claimant_name: handBack.claimant_name, claimant_proof: handBack.claimant_proof || undefined, note: handBack.note || undefined } })
      : api.POST('/api/v1/properties/{propertyId}/lost-found/{id}/dispose', { params: base(), body: { reason: disposeReason.value } })),
    how === 'return' ? 'Handed back.' : 'Disposed of.',
  )
  if (ok) closing.value = null
}

function useOwner(o: LostFoundOwner): void {
  handBack.claimant_name = o.guest_name
  handBack.claimant_proof = `Stay ${o.stay_number}`
  closing.value = 'return'
}

watch(() => pid.value, () => {
  rows.value = []
  loaded.value = false
  selectedId.value = null
  void load()
  void loadRooms()
}, { immediate: true })
watch(() => [filter.status, filter.category], () => void load())
</script>

<template>
  <div class="page-head">
    <h1 class="page-title">Lost &amp; found</h1>
    <button v-if="can('lostfound.report') && !creating" type="button" class="btn-primary" data-testid="new-item" @click="startNew">Record an item</button>
  </div>

  <p v-if="error" class="alert" role="alert" data-testid="lf-error">{{ error.message }} <code>{{ error.code }}</code></p>
  <p v-if="notice" class="notice" role="status" data-testid="notice">{{ notice }}</p>
  <p v-if="pid === null" class="muted">Select a property first.</p>
  <p v-else-if="!can('lostfound.report')" class="muted" data-testid="no-access">Your role at this property cannot see lost and found: the <code>lostfound.report</code> permission is needed.</p>

  <template v-else>
    <form v-if="creating" class="card" novalidate data-testid="item-form" @submit.prevent="record">
      <h2>Record an item</h2>
      <div class="form-grid">
        <label class="field">
          <span>Found in room</span>
          <select v-model.number="form.room_id" name="room_id">
            <option :value="0">Not in a room (give the place)</option>
            <option v-for="r in roomList" :key="r.room_id" :value="r.room_id">{{ r.room_number }} · {{ r.room_type_code }}</option>
          </select>
        </label>
        <label class="field">
          <span>Place</span>
          <input v-model="form.location" name="location" maxlength="150" placeholder="Pool, restaurant, lobby…" :aria-invalid="!!fieldError('location')" />
          <small v-if="fieldError('location')" class="error-text">{{ fieldError('location') }}</small>
        </label>
        <label class="field">
          <span>Category</span>
          <select v-model="form.category" name="category">
            <option v-for="c in CATEGORIES" :key="c" :value="c">{{ c }}</option>
          </select>
        </label>
        <label class="field">
          <span>Kept at</span>
          <input v-model="form.storage_location" name="storage_location" maxlength="100" placeholder="Shelf A, safe…" />
        </label>
        <label class="field wide">
          <span>What was found</span>
          <input v-model="form.description" name="description" maxlength="500" :aria-invalid="!!fieldError('description')" />
          <small v-if="fieldError('description')" class="error-text">{{ fieldError('description') }}</small>
        </label>
        <label class="field">
          <span>Possible owner</span>
          <input v-model="form.possible_owner" name="possible_owner" maxlength="150" />
        </label>
        <label class="field">
          <span>Notes</span>
          <input v-model="form.notes" name="notes" maxlength="500" />
        </label>
      </div>
      <div class="form-actions">
        <button type="button" @click="creating = false">Cancel</button>
        <button type="submit" class="btn-primary" :disabled="busy">Record</button>
      </div>
    </form>

    <section class="card">
      <form class="filters" novalidate data-testid="filters" @submit.prevent="load()">
        <label class="field">
          <span>Show</span>
          <select v-model="filter.status" name="status">
            <option value="STORED">Stored</option>
            <option value="">Everything</option>
            <option value="RETURNED">Returned</option>
            <option value="DISPOSED">Disposed</option>
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
          <span>Search</span>
          <input v-model="filter.q" name="q" type="search" placeholder="Number, item, place, owner" />
        </label>
        <button type="submit">Search</button>
      </form>
      <p v-if="loaded && !rows.length" class="muted" data-testid="empty">No items.</p>
      <table v-else-if="rows.length" class="list" data-testid="items">
        <thead><tr><th>Number</th><th>Found</th><th>Where</th><th>Item</th><th>Kept at</th><th>Status</th></tr></thead>
        <tbody>
          <tr v-for="i in rows" :key="i.id" :class="{ chosen: i.id === selectedId, closed: i.status !== 'STORED' }" :data-testid="`item-${i.item_number}`">
            <td><button type="button" class="link" :data-testid="`select-${i.item_number}`" @click="select(i)">{{ i.item_number }}</button></td>
            <td>{{ i.found_on }}</td>
            <td>{{ where(i) }}</td>
            <td>{{ i.description }} <small class="muted">{{ i.category.toLowerCase() }}</small></td>
            <td>{{ i.storage_location }}</td>
            <td>{{ STATUS_LABEL[i.status] }}<small v-if="i.claimant_name" class="muted"> {{ i.claimant_name }}</small></td>
          </tr>
        </tbody>
      </table>
      <button v-if="nextCursor" type="button" data-testid="more" @click="load(true)">Load more</button>
    </section>

    <section v-if="selected" class="card" data-testid="detail">
      <h2>{{ selected.item_number }} · {{ selected.description }}</h2>
      <p class="muted">
        Found {{ selected.found_on }} {{ where(selected) ? `in ${where(selected)}` : '' }}<template v-if="selected.finder_name"> by {{ selected.finder_name }}</template>.
        <template v-if="selected.possible_owner"> Possible owner: {{ selected.possible_owner }}.</template>
        <template v-if="selected.status === 'RETURNED'"> Handed to {{ selected.claimant_name }} on {{ selected.closed_on }}<template v-if="selected.claimant_proof"> ({{ selected.claimant_proof }})</template>.</template>
        <template v-if="selected.status === 'DISPOSED'"> Disposed of on {{ selected.closed_on }}: {{ selected.close_note }}.</template>
      </p>

      <div v-if="owners.length" data-testid="owners">
        <h3>Who had the room</h3>
        <ul class="plain">
          <li v-for="o in owners" :key="o.stay_id">
            {{ o.guest_name }} <small class="muted">stay {{ o.stay_number }}, {{ o.arrival_date }} to {{ o.departure_date }} {{ [o.phone, o.email].filter(Boolean).join(' · ') }}</small>
            <button v-if="selected.status === 'STORED' && can('lostfound.manage')" type="button" :data-testid="`owner-${o.stay_number}`" @click="useOwner(o)">Hand to this guest</button>
          </li>
        </ul>
      </div>

      <template v-if="selected.status === 'STORED' && can('lostfound.manage')">
        <div class="filters">
          <label class="field">
            <span>Kept at</span>
            <input v-model="storage" name="storage" maxlength="100" />
          </label>
          <button type="button" :disabled="busy" data-testid="save-storage" @click="saveStorage">Save</button>
        </div>
        <div class="form-actions">
          <button type="button" class="btn-primary" data-testid="return" @click="closing = 'return'">Hand back</button>
          <button type="button" data-testid="dispose" @click="closing = 'dispose'">Dispose</button>
        </div>

        <form v-if="closing === 'return'" class="filters" novalidate data-testid="return-form" @submit.prevent="finish">
          <label class="field">
            <span>Taken by</span>
            <input v-model="handBack.claimant_name" name="claimant_name" maxlength="150" :aria-invalid="!!fieldError('claimant_name')" />
            <small v-if="fieldError('claimant_name')" class="error-text">{{ fieldError('claimant_name') }}</small>
          </label>
          <label class="field">
            <span>What was checked (ID, booking, contents)</span>
            <input v-model="handBack.claimant_proof" name="claimant_proof" maxlength="150" />
          </label>
          <label class="field">
            <span>Note</span>
            <input v-model="handBack.note" name="note" maxlength="500" />
          </label>
          <button type="button" @click="closing = null">Back</button>
          <button type="submit" class="btn-primary" :disabled="busy || !handBack.claimant_name.trim()" data-testid="return-submit">Confirm hand-over</button>
        </form>

        <form v-if="closing === 'dispose'" class="filters" novalidate data-testid="dispose-form" @submit.prevent="finish">
          <label class="field grow">
            <span>Reason</span>
            <input v-model="disposeReason" name="reason" maxlength="500" :aria-invalid="!!fieldError('reason')" />
            <small v-if="fieldError('reason')" class="error-text">{{ fieldError('reason') }}</small>
          </label>
          <button type="button" @click="closing = null">Back</button>
          <button type="submit" class="btn-primary" :disabled="busy || !disposeReason.trim()" data-testid="dispose-submit">Confirm disposal</button>
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
.plain {
  list-style: none;
  padding: 0;
  margin: 4px 0 12px;
}
.plain li {
  display: flex;
  gap: 8px;
  align-items: center;
  flex-wrap: wrap;
}
</style>
