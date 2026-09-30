<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { api } from '@/api/client'
import { fetchAll } from '@/api/paging'
import { ApiError } from '@/api/problem'
import type { BlockType, Room, RoomBlock } from '@/api/types'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import { blockColumns, windowDates } from '@/utils/blocks'
import { addDays, formatBusinessDate } from '@/utils/dates'

const auth = useAuthStore()
const property = usePropertyStore()

const rooms = ref<Room[]>([])
const blocks = ref<RoomBlock[]>([])
const loaded = ref(false)
const error = ref<ApiError | null>(null)
const saving = ref(false)
const days = ref(14)
const windowStart = ref('')
const cancelling = ref<RoomBlock | null>(null)
const cancelReason = ref('')

const canManage = computed(() => auth.can('room_block.manage', property.currentId))
const businessDate = computed(() => property.clock?.business_date ?? '')
const dates = computed(() => (windowStart.value ? windowDates(windowStart.value, days.value) : []))
const sortedRooms = computed(() =>
  rooms.value.filter((r) => r.is_active).sort((a, b) => a.room_number.localeCompare(b.room_number, undefined, { numeric: true })),
)
const roomNumber = (id: number) => rooms.value.find((r) => r.id === id)?.room_number ?? String(id)

const blank = () => ({ room_id: 0, block_type: 'OOO' as BlockType, start_date: '', end_date: '', reason: '' })
const form = reactive(blank())
const fieldError = (field: string) => error.value?.fieldMessage(field)
const conflicts = computed(() => {
  const c = error.value?.context.conflicts
  return Array.isArray(c) ? (c as { type: string; id: number; reference?: string; from: string; to: string }[]) : []
})

// Blocks per room that are visible in the window, with their grid columns (one extra column holds the room label).
const rows = computed(() =>
  sortedRooms.value.map((room) => ({
    room,
    bars: blocks.value
      .filter((b) => b.room_id === room.id)
      .flatMap((b) => {
        const cols = windowStart.value ? blockColumns(b, windowStart.value, days.value) : null
        return cols ? [{ block: b, start: cols.start + 1, end: cols.end + 1 }] : []
      }),
  })),
)

async function load(): Promise<void> {
  const propertyId = property.currentId
  rooms.value = []
  blocks.value = []
  loaded.value = false
  if (propertyId === null) return
  if (!windowStart.value) windowStart.value = businessDate.value
  if (!windowStart.value) return // the business date is still loading; the watcher below retries
  try {
    const from = windowStart.value
    const to = addDays(from, days.value)
    const [r, b] = await Promise.all([
      fetchAll((cursor) =>
        api.GET('/api/v1/properties/{propertyId}/rooms', { params: { path: { propertyId }, query: { limit: 200, cursor } } }),
      ),
      fetchAll((cursor) =>
        api.GET('/api/v1/properties/{propertyId}/room-blocks', {
          params: { path: { propertyId }, query: { status: 'ACTIVE', from, to, limit: 200, cursor } },
        }),
      ),
    ])
    rooms.value = r
    blocks.value = b.sort((x, y) => x.start_date.localeCompare(y.start_date) || x.id - y.id)
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    loaded.value = true
  }
}

function shift(n: number): void {
  windowStart.value = addDays(windowStart.value, n)
  void load()
}

function backToToday(): void {
  windowStart.value = businessDate.value
  void load()
}

function prefill(roomId: number, date: string): void {
  if (!canManage.value) return
  Object.assign(form, blank(), { room_id: roomId, start_date: date, end_date: addDays(date, 1) })
  error.value = null
}

async function create(): Promise<void> {
  const propertyId = property.currentId
  if (propertyId === null) return
  saving.value = true
  error.value = null
  try {
    await api.POST('/api/v1/properties/{propertyId}/room-blocks', {
      params: { path: { propertyId } },
      body: { ...form, room_id: Number(form.room_id) },
    })
    Object.assign(form, blank())
    await load()
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    saving.value = false
  }
}

async function cancel(): Promise<void> {
  const propertyId = property.currentId
  if (propertyId === null || !cancelling.value) return
  saving.value = true
  error.value = null
  try {
    await api.POST('/api/v1/properties/{propertyId}/room-blocks/{id}/cancel', {
      params: { path: { propertyId, id: cancelling.value.id } },
      body: { reason: cancelReason.value },
    })
    cancelling.value = null
    cancelReason.value = ''
    await load()
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    saving.value = false
  }
}

watch(() => property.currentId, () => {
  windowStart.value = ''
  void load()
}, { immediate: true })
// The business date arrives after the property is selected.
watch(businessDate, (bd) => {
  if (bd && !windowStart.value) void load()
})
</script>

<template>
  <div class="page-head">
    <h1 class="page-title">Room blocks</h1>
    <div class="nav">
      <button type="button" @click="shift(-7)">&larr; Week</button>
      <button type="button" @click="backToToday">Business date</button>
      <button type="button" @click="shift(7)">Week &rarr;</button>
      <select v-model.number="days" aria-label="Days shown" @change="load">
        <option :value="14">14 days</option>
        <option :value="28">28 days</option>
      </select>
    </div>
  </div>

  <div v-if="error" class="alert" role="alert" data-testid="form-error">
    {{ error.message }} <code>{{ error.code }}</code>
    <ul v-if="conflicts.length" class="conflicts" data-testid="conflicts">
      <li v-for="c in conflicts" :key="`${c.type}-${c.id}`">
        {{ c.type === 'STAY' ? `In-house stay ${c.reference ?? c.id}` : `Reservation line ${c.id}` }}: {{ c.from }} to {{ c.to }}
      </li>
    </ul>
  </div>
  <p v-if="property.currentId === null" class="muted">Select a property first.</p>

  <section v-else class="card calendar-card">
    <p v-if="loaded && !sortedRooms.length" class="muted" data-testid="empty">No active rooms yet.</p>
    <div v-else class="calendar" :style="{ '--days': days }" role="grid" aria-label="Room block calendar">
      <div class="row head">
        <div class="label" />
        <div v-for="d in dates" :key="d" class="day" :class="{ today: d === businessDate }" role="columnheader">
          {{ d.slice(8) }}<small>{{ d.slice(5, 7) }}</small>
        </div>
      </div>
      <div v-for="row in rows" :key="row.room.id" class="row" role="row" :data-testid="`cal-${row.room.room_number}`">
        <div class="label" role="rowheader">{{ row.room.room_number }}</div>
        <button
          v-for="(d, i) in dates"
          :key="d"
          type="button"
          class="cell"
          :class="{ today: d === businessDate }"
          :style="{ gridColumn: i + 2, gridRow: 1 }"
          :disabled="!canManage"
          :aria-label="`Block room ${row.room.room_number} from ${d}`"
          @click="prefill(row.room.id, d)"
        />
        <div
          v-for="bar in row.bars"
          :key="bar.block.id"
          class="bar"
          :class="bar.block.block_type.toLowerCase()"
          :style="{ gridColumn: `${bar.start} / ${bar.end}`, gridRow: 1 }"
          :title="`${bar.block.block_type}: ${bar.block.reason} (${bar.block.start_date} to ${bar.block.end_date})`"
          data-testid="bar"
        >
          {{ bar.block.block_type }}
        </div>
      </div>
    </div>
  </section>

  <form v-if="canManage && property.currentId !== null" class="card" novalidate data-testid="block-form" @submit.prevent="create">
    <h2>New block</h2>
    <div class="form-grid">
      <label class="field">
        <span>Room</span>
        <select v-model="form.room_id" name="room_id" :aria-invalid="!!fieldError('room_id')">
          <option :value="0" disabled>Select a room</option>
          <option v-for="r in sortedRooms" :key="r.id" :value="r.id">{{ r.room_number }}</option>
        </select>
      </label>
      <label class="field">
        <span>Type</span>
        <select v-model="form.block_type" name="block_type">
          <option value="OOO">Out of order (OOO)</option>
          <option value="OOS">Out of service (OOS)</option>
        </select>
        <small class="hint">Both make the room unsellable.</small>
      </label>
      <label class="field">
        <span>From</span>
        <input v-model="form.start_date" name="start_date" type="date" :aria-invalid="!!fieldError('start_date')" />
        <small v-if="fieldError('start_date')" class="error-text">{{ fieldError('start_date') }}</small>
      </label>
      <label class="field">
        <span>Until (not included)</span>
        <input v-model="form.end_date" name="end_date" type="date" :aria-invalid="!!fieldError('end_date')" />
        <small v-if="fieldError('end_date')" class="error-text">{{ fieldError('end_date') }}</small>
      </label>
      <label class="field wide">
        <span>Reason</span>
        <input v-model="form.reason" name="reason" maxlength="500" :aria-invalid="!!fieldError('reason')" />
        <small v-if="fieldError('reason')" class="error-text">{{ fieldError('reason') }}</small>
      </label>
    </div>
    <div class="form-actions">
      <button type="submit" class="btn-primary" :disabled="saving">Create block</button>
    </div>
  </form>

  <section class="card">
    <h2>Active blocks in view</h2>
    <p v-if="loaded && !blocks.length" class="muted">No blocks in this period.</p>
    <table v-else-if="blocks.length" class="list">
      <thead>
        <tr>
          <th>Room</th>
          <th>Type</th>
          <th>From</th>
          <th>Until</th>
          <th>Reason</th>
          <th v-if="canManage" />
        </tr>
      </thead>
      <tbody>
        <tr v-for="b in blocks" :key="b.id" :data-testid="`block-${b.id}`">
          <td><b>{{ roomNumber(b.room_id) }}</b></td>
          <td>{{ b.block_type }}</td>
          <td>{{ formatBusinessDate(b.start_date) }}</td>
          <td>{{ formatBusinessDate(b.end_date) }}</td>
          <td>{{ b.reason }}</td>
          <td v-if="canManage"><button type="button" @click="cancelling = b; cancelReason = ''">Release</button></td>
        </tr>
      </tbody>
    </table>
  </section>

  <form v-if="cancelling" class="card" novalidate data-testid="cancel-form" @submit.prevent="cancel">
    <h2>Release block on room {{ roomNumber(cancelling.room_id) }}</h2>
    <label class="field">
      <span>Reason</span>
      <input v-model="cancelReason" name="cancel_reason" maxlength="500" :aria-invalid="!!fieldError('reason')" />
      <small v-if="fieldError('reason')" class="error-text">{{ fieldError('reason') }}</small>
    </label>
    <div class="form-actions">
      <button type="button" @click="cancelling = null">Keep block</button>
      <button type="submit" class="btn-primary" :disabled="saving">Release block</button>
    </div>
  </form>
</template>

<style scoped>
.nav {
  display: flex;
  gap: 8px;
  align-items: center;
}
.nav select {
  font: inherit;
  padding: 6px 8px;
  border-radius: 8px;
  border: 1px solid var(--border);
  background: var(--surface);
  color: var(--text);
}
.calendar-card {
  overflow-x: auto;
}
.calendar {
  display: grid;
  min-width: 640px;
  font-size: 12px;
}
.row {
  display: grid;
  grid-template-columns: 72px repeat(var(--days), minmax(28px, 1fr));
  grid-auto-rows: 26px;
  border-bottom: 1px solid var(--border);
}
.row.head {
  grid-auto-rows: 36px;
  color: var(--text-muted);
}
.label {
  grid-column: 1;
  grid-row: 1;
  display: flex;
  align-items: center;
  padding-left: 6px;
  font-weight: 600;
}
.day {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  line-height: 1.1;
}
.day.today,
.cell.today {
  background: var(--accent-soft);
}
.cell {
  border: 0;
  border-left: 1px solid var(--border);
  border-radius: 0;
  background: transparent;
  padding: 0;
  cursor: pointer;
}
.cell:disabled {
  cursor: default;
}
.cell:hover:not(:disabled) {
  background: var(--hover);
}
.bar {
  z-index: 1;
  margin: 3px 1px;
  border-radius: 6px;
  display: flex;
  align-items: center;
  padding: 0 6px;
  font-weight: 700;
  color: var(--text);
  pointer-events: none;
}
.bar.ooo {
  background: color-mix(in srgb, var(--danger) 30%, transparent);
  border: 1px solid var(--danger);
}
.bar.oos {
  background: color-mix(in srgb, var(--warning) 30%, transparent);
  border: 1px solid var(--warning);
}
.wide {
  grid-column: 1 / -1;
}
.conflicts {
  margin: 6px 0 0;
  padding-left: 18px;
}
</style>
