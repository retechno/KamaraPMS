<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { HousekeepingBoardRoom, HousekeepingStaffMember, HousekeepingTask, HousekeepingTaskList } from '@/api/types'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'

const auth = useAuthStore()
const property = usePropertyStore()

const list = ref<HousekeepingTaskList | null>(null)
const staff = ref<HousekeepingStaffMember[]>([])
const error = ref<ApiError | null>(null)
const notice = ref('')
const busy = ref(false)
const picked = ref<number[]>([])
const assignTo = ref(0) // 0: take the tasks back
const filter = reactive({ view: 'all' as 'all' | 'mine' | 'unassigned', status: '' })
// A task being skipped: the reason is asked first.
const skipping = ref<{ task: HousekeepingTask; reason: string } | null>(null)
const adding = ref(false)
const boardRooms = ref<HousekeepingBoardRoom[]>([])
const manual = reactive({ room_id: 0, task_type: 'DEEP', priority: 'NORMAL', assigned_to: 0, notes: '' })

const pid = computed(() => property.currentId)
const can = (p: string) => auth.can(p, pid.value)
const me = computed(() => auth.me?.user.id ?? 0)
const STATUS_LABEL: Record<string, string> = { PENDING: 'To do', IN_PROGRESS: 'In progress', DONE: 'Done', SKIPPED: 'Skipped' }
const TYPE_LABEL: Record<string, string> = { CHECKOUT: 'Check-out', STAYOVER: 'Stayover', ARRIVAL: 'Arrival', DIRTY: 'Dirty room', DEEP: 'Deep clean', OTHER: 'Other' }
const open = (t: HousekeepingTask) => t.status === 'PENDING' || t.status === 'IN_PROGRESS'
const fieldError = (field: string) => error.value?.fieldMessage(field)

async function load(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null) return
  try {
    const query: { status?: 'PENDING' | 'IN_PROGRESS' | 'DONE' | 'SKIPPED'; assigned_to?: number; unassigned?: boolean } = {}
    if (filter.status) query.status = filter.status as 'PENDING'
    if (filter.view === 'mine') query.assigned_to = me.value
    if (filter.view === 'unassigned') query.unassigned = true
    const { data } = await api.GET('/api/v1/properties/{propertyId}/housekeeping/tasks', { params: { path: { propertyId }, query } })
    list.value = data ?? null
    picked.value = picked.value.filter((id) => list.value?.data.some((t) => t.id === id && open(t)))
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  }
}

async function loadStaff(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null || !can('housekeeping.assign')) return
  try {
    const { data } = await api.GET('/api/v1/properties/{propertyId}/housekeeping/staff', { params: { path: { propertyId } } })
    staff.value = data?.data ?? []
  } catch {
    staff.value = []
  }
}

// One call that reloads the list afterwards, also after a refusal (somebody else may have changed the task).
async function run(action: () => Promise<unknown>, done = ''): Promise<void> {
  busy.value = true
  error.value = null
  notice.value = ''
  try {
    await action()
    if (done) notice.value = done
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    busy.value = false
  }
  const failure = error.value
  await load()
  error.value = failure
}

const generate = () => run(async () => {
  const { data } = await api.POST('/api/v1/properties/{propertyId}/housekeeping/tasks/generate', { params: { path: { propertyId: pid.value as number } } })
  notice.value = data?.created ? `${data.created} task(s) added.` : 'Nothing new to add.'
})

const assign = () => run(async () => {
  const { data } = await api.POST('/api/v1/properties/{propertyId}/housekeeping/tasks/assign', {
    params: { path: { propertyId: pid.value as number } },
    body: { task_ids: picked.value, user_id: assignTo.value || null },
  })
  picked.value = []
  notice.value = `${data?.assigned ?? 0} task(s) ${assignTo.value ? 'assigned' : 'taken back'}.`
})

const taskPath = (t: HousekeepingTask) => ({ path: { propertyId: pid.value as number, taskId: t.id } })
const start = (t: HousekeepingTask) => run(() => api.POST('/api/v1/properties/{propertyId}/housekeeping/tasks/{taskId}/start', { params: taskPath(t) }))
const complete = (t: HousekeepingTask) => run(() => api.POST('/api/v1/properties/{propertyId}/housekeeping/tasks/{taskId}/complete', { params: taskPath(t), body: {} }))

function askSkip(t: HousekeepingTask): void {
  skipping.value = { task: t, reason: t.dnd ? 'Do not disturb' : '' }
  error.value = null
}

async function skip(): Promise<void> {
  const s = skipping.value
  if (!s) return
  await run(() => api.POST('/api/v1/properties/{propertyId}/housekeeping/tasks/{taskId}/skip', { params: taskPath(s.task), body: { reason: s.reason } }))
  if (!error.value) skipping.value = null
}

async function startAdding(): Promise<void> {
  adding.value = true
  error.value = null
  const propertyId = pid.value
  if (propertyId === null) return
  try {
    const { data } = await api.GET('/api/v1/properties/{propertyId}/housekeeping', { params: { path: { propertyId } } })
    boardRooms.value = data?.data ?? []
    manual.room_id ||= boardRooms.value[0]?.room_id ?? 0
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  }
}

async function addManual(): Promise<void> {
  await run(async () => {
    await api.POST('/api/v1/properties/{propertyId}/housekeeping/tasks', {
      params: { path: { propertyId: pid.value as number } },
      body: {
        room_id: Number(manual.room_id), task_type: manual.task_type as 'DEEP', priority: manual.priority as 'NORMAL',
        assigned_to: manual.assigned_to || null, notes: manual.notes || undefined,
      },
    })
    adding.value = false
    notice.value = 'Task added.'
  })
}

function toggleAll(on: boolean): void {
  picked.value = on ? (list.value?.data ?? []).filter(open).map((t) => t.id) : []
}

const openCount = computed(() => (list.value?.data ?? []).filter(open).length)
const mayWork = (t: HousekeepingTask) => can('housekeeping.update') && open(t)

watch(() => pid.value, () => {
  list.value = null
  void load()
  void loadStaff()
}, { immediate: true })
watch(() => [filter.view, filter.status], () => void load())
</script>

<template>
  <div class="page-head">
    <h1 class="page-title">Cleaning list</h1>
    <div class="head-actions">
      <button v-if="can('housekeeping.assign')" type="button" class="btn-primary" :disabled="busy" data-testid="generate" @click="generate">Generate today's list</button>
      <button v-if="can('housekeeping.assign') && !adding" type="button" data-testid="add-task" @click="startAdding">Add a task</button>
    </div>
  </div>

  <p v-if="error" class="alert" role="alert" data-testid="hk-error">{{ error.message }} <code>{{ error.code }}</code></p>
  <p v-if="notice" class="notice" role="status" data-testid="notice">{{ notice }}</p>
  <p v-if="pid === null" class="muted">Select a property first.</p>

  <template v-else>
    <form v-if="adding" class="card" novalidate data-testid="task-form" @submit.prevent="addManual">
      <h2>Add a task for today</h2>
      <div class="form-grid">
        <label class="field">
          <span>Room</span>
          <select v-model.number="manual.room_id" name="room_id" :aria-invalid="!!fieldError('room_id')">
            <option v-for="r in boardRooms" :key="r.room_id" :value="r.room_id">{{ r.room_number }} · {{ r.room_type_code }}</option>
          </select>
          <small v-if="fieldError('room_id')" class="error-text">{{ fieldError('room_id') }}</small>
        </label>
        <label class="field">
          <span>Kind</span>
          <select v-model="manual.task_type" name="task_type">
            <option value="DEEP">Deep clean</option>
            <option value="OTHER">Other</option>
            <option value="CHECKOUT">Check-out</option>
            <option value="STAYOVER">Stayover</option>
            <option value="ARRIVAL">Arrival</option>
            <option value="DIRTY">Dirty room</option>
          </select>
        </label>
        <label class="field">
          <span>Priority</span>
          <select v-model="manual.priority" name="priority">
            <option value="NORMAL">Normal</option>
            <option value="HIGH">High</option>
          </select>
        </label>
        <label class="field">
          <span>Assign to</span>
          <select v-model.number="manual.assigned_to" name="assigned_to" :aria-invalid="!!fieldError('assigned_to')">
            <option :value="0">Nobody yet</option>
            <option v-for="s in staff" :key="s.id" :value="s.id">{{ s.full_name }}</option>
          </select>
        </label>
        <label class="field">
          <span>Note</span>
          <input v-model="manual.notes" name="notes" maxlength="500" />
        </label>
      </div>
      <div class="form-actions">
        <button type="button" @click="adding = false">Cancel</button>
        <button type="submit" class="btn-primary" :disabled="busy">Add</button>
      </div>
    </form>

    <section v-if="list" class="card" data-testid="workload">
      <h2>{{ list.date }}</h2>
      <p v-if="!list.workload.length" class="muted" data-testid="empty">
        No tasks yet. {{ can('housekeeping.assign') ? 'Generate the list to start.' : 'A supervisor generates the list.' }}
      </p>
      <table v-else class="list">
        <thead><tr><th>Housekeeper</th><th class="num">Tasks</th><th class="num">To do</th><th class="num">In progress</th><th class="num">Done</th><th class="num">Skipped</th></tr></thead>
        <tbody>
          <tr v-for="w in list.workload" :key="w.user_id ?? 0" :data-testid="`workload-${w.user_id ?? 'none'}`">
            <td>{{ w.name }}</td><td class="num">{{ w.total }}</td><td class="num">{{ w.pending }}</td><td class="num">{{ w.in_progress }}</td><td class="num">{{ w.done }}</td><td class="num">{{ w.skipped }}</td>
          </tr>
        </tbody>
      </table>
    </section>

    <section v-if="list && list.data.length || filter.view !== 'all' || filter.status" class="card">
      <form class="filters" novalidate @submit.prevent="load()">
        <label class="field">
          <span>Show</span>
          <select v-model="filter.view" name="view">
            <option value="all">Everything</option>
            <option value="mine">My tasks</option>
            <option value="unassigned">Not assigned</option>
          </select>
        </label>
        <label class="field">
          <span>Status</span>
          <select v-model="filter.status" name="status">
            <option value="">Any</option>
            <option v-for="(label, k) in STATUS_LABEL" :key="k" :value="k">{{ label }}</option>
          </select>
        </label>
      </form>

      <form v-if="can('housekeeping.assign') && openCount" class="filters" novalidate data-testid="assign-form" @submit.prevent="assign">
        <label class="field">
          <span>Give the {{ picked.length }} ticked task(s) to</span>
          <select v-model.number="assignTo" name="assign_to">
            <option :value="0">Nobody (take back)</option>
            <option v-for="s in staff" :key="s.id" :value="s.id">{{ s.full_name }}</option>
          </select>
        </label>
        <button type="submit" :disabled="busy || !picked.length" data-testid="assign">Assign</button>
      </form>

      <table v-if="list && list.data.length" class="list" data-testid="tasks">
        <thead>
          <tr>
            <th v-if="can('housekeeping.assign')"><input type="checkbox" aria-label="Tick all open tasks" data-testid="pick-all" :checked="openCount > 0 && picked.length === openCount" @change="toggleAll(($event.target as HTMLInputElement).checked)" /></th>
            <th>Room</th><th>Kind</th><th>Room is</th><th>Housekeeper</th><th>Status</th><th />
          </tr>
        </thead>
        <tbody>
          <tr v-for="t in list.data" :key="t.id" :class="{ closed: !open(t) }" :data-testid="`task-${t.room_number}-${t.task_type}`">
            <td v-if="can('housekeeping.assign')"><input v-model="picked" type="checkbox" :value="t.id" :disabled="!open(t)" :aria-label="`Tick room ${t.room_number}`" /></td>
            <td>
              <b>{{ t.room_number }}</b>
              <small v-if="t.floor" class="muted"> floor {{ t.floor }}</small>
              <span v-if="t.priority === 'HIGH'" class="pill high">High</span>
              <span v-if="t.dnd" class="pill dnd" data-testid="dnd">DND</span>
              <span v-if="t.make_up_requested" class="pill makeup">Make-up</span>
              <small v-if="t.flag_note" class="muted"> {{ t.flag_note }}</small>
            </td>
            <td>{{ TYPE_LABEL[t.task_type] }}</td>
            <td>{{ t.room_status.toLowerCase() }}</td>
            <td>{{ t.assignee_name || '—' }}</td>
            <td>{{ STATUS_LABEL[t.status] }}<small v-if="t.status === 'SKIPPED' && t.notes" class="muted"> {{ t.notes }}</small></td>
            <td class="row-actions">
              <button v-if="mayWork(t) && t.status === 'PENDING'" type="button" :disabled="busy" :data-testid="`start-${t.room_number}`" @click="start(t)">Start</button>
              <button v-if="mayWork(t)" type="button" class="btn-primary" :disabled="busy" :data-testid="`done-${t.room_number}`" @click="complete(t)">Done</button>
              <button v-if="mayWork(t)" type="button" :disabled="busy" :data-testid="`skip-${t.room_number}`" @click="askSkip(t)">Skip</button>
            </td>
          </tr>
        </tbody>
      </table>
      <p v-else class="muted" data-testid="no-match">No task matches.</p>
    </section>

    <form v-if="skipping" class="card" novalidate data-testid="skip-form" @submit.prevent="skip">
      <h2>Skip room {{ skipping.task.room_number }}</h2>
      <label class="field">
        <span>Reason</span>
        <input v-model="skipping.reason" name="skip_reason" maxlength="500" :aria-invalid="!!fieldError('reason')" />
        <small v-if="fieldError('reason')" class="error-text">{{ fieldError('reason') }}</small>
      </label>
      <div class="form-actions">
        <button type="button" @click="skipping = null">Cancel</button>
        <button type="submit" class="btn-primary" :disabled="busy || !skipping.reason.trim()">Skip</button>
      </div>
    </form>
  </template>
</template>

<style scoped>
.head-actions {
  display: flex;
  gap: 8px;
}
.closed td {
  color: var(--muted, #6b7280);
}
.pill {
  display: inline-block;
  margin-left: 6px;
  padding: 1px 8px;
  border-radius: 999px;
  border: 1px solid var(--border);
  font-size: 12px;
  font-weight: 600;
}
.pill.high {
  border-color: var(--danger);
}
.pill.dnd {
  border-color: var(--warning);
}
.pill.makeup {
  border-color: var(--accent);
}
</style>
