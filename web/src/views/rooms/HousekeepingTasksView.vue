<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { HousekeepingBoardRoom, HousekeepingStaffMember, HousekeepingTask, HousekeepingTaskList } from '@/api/types'
import DataTable, { type Column } from '@/components/app/DataTable.vue'
import EmptyState from '@/components/app/EmptyState.vue'
import FormField from '@/components/app/FormField.vue'
import PageHeader from '@/components/app/PageHeader.vue'
import StatusBadge from '@/components/app/StatusBadge.vue'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { NativeSelect } from '@/components/ui/native-select'
import { t } from '@/i18n'
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
const TASK_STATUSES = ['PENDING', 'IN_PROGRESS', 'DONE', 'SKIPPED'] as const
const typeLabel = (k: string): string => t(`cleaning.type${k}` as never)
const open = (task: HousekeepingTask) => task.status === 'PENDING' || task.status === 'IN_PROGRESS'
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
    picked.value = picked.value.filter((id) => list.value?.data.some((task) => task.id === id && open(task)))
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
  notice.value = data?.created ? t('cleaning.added', { n: data.created }) : t('cleaning.nothingNew')
})

const assign = () => run(async () => {
  const { data } = await api.POST('/api/v1/properties/{propertyId}/housekeeping/tasks/assign', {
    params: { path: { propertyId: pid.value as number } },
    body: { task_ids: picked.value, user_id: assignTo.value || null },
  })
  picked.value = []
  notice.value = t(assignTo.value ? 'cleaning.assigned' : 'cleaning.takenBack', { n: data?.assigned ?? 0 })
})

const taskPath = (task: HousekeepingTask) => ({ path: { propertyId: pid.value as number, taskId: task.id } })
const start = (task: HousekeepingTask) => run(() => api.POST('/api/v1/properties/{propertyId}/housekeeping/tasks/{taskId}/start', { params: taskPath(task) }))
const complete = (task: HousekeepingTask) => run(() => api.POST('/api/v1/properties/{propertyId}/housekeeping/tasks/{taskId}/complete', { params: taskPath(task), body: {} }))

function askSkip(task: HousekeepingTask): void {
  skipping.value = { task, reason: task.dnd ? t('cleaning.dndReason') : '' }
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
    notice.value = t('cleaning.taskAdded')
  })
}

function toggleAll(on: boolean): void {
  picked.value = on ? (list.value?.data ?? []).filter(open).map((task) => task.id) : []
}

const openCount = computed(() => (list.value?.data ?? []).filter(open).length)
const mayWork = (task: HousekeepingTask) => can('housekeeping.update') && open(task)

const workloadColumns = computed<Column<HousekeepingTaskList['workload'][number]>[]>(() => [
  { key: 'name', label: t('cleaning.housekeeper') },
  { key: 'total', label: t('cleaning.tasks'), align: 'right', format: 'money' as const },
  { key: 'pending', label: t('cleaning.todo'), align: 'right' },
  { key: 'in_progress', label: t('cleaning.inProgress'), align: 'right' },
  { key: 'done', label: t('cleaning.done'), align: 'right' },
  { key: 'skipped', label: t('cleaning.skipped'), align: 'right' },
])
const taskColumns = computed<Column<HousekeepingTask>[]>(() => [
  ...(can('housekeeping.assign') ? [{ key: 'pick', label: '', class: 'w-8' }] : []),
  { key: 'room_number', label: t('cleaning.room'), sortable: true },
  { key: 'task_type', label: t('cleaning.kind'), sortable: true },
  { key: 'room_status', label: t('cleaning.roomIs'), sortable: true },
  { key: 'assignee_name', label: t('cleaning.housekeeper'), sortable: true },
  { key: 'status', label: t('cleaning.status'), sortable: true },
  { key: 'actions', label: '', align: 'right' },
])

watch(() => pid.value, () => {
  list.value = null
  void load()
  void loadStaff()
}, { immediate: true })
watch(() => [filter.view, filter.status], () => void load())
</script>

<template>
  <PageHeader :title="t('cleaning.title')">
    <template #actions>
      <Button v-if="can('housekeeping.assign') && !adding" variant="outline" size="sm" data-testid="add-task" @click="startAdding">{{ t('cleaning.addTask') }}</Button>
      <Button v-if="can('housekeeping.assign')" size="sm" :disabled="busy" data-testid="generate" @click="generate">{{ t('cleaning.generate') }}</Button>
    </template>
  </PageHeader>

  <p v-if="error" class="alert" role="alert" data-testid="hk-error">{{ error.message }} <code>{{ error.code }}</code></p>
  <p v-if="notice" class="alert warning" role="status" data-testid="notice">{{ notice }}</p>
  <p v-if="pid === null" class="muted">{{ t('cleaning.selectProperty') }}</p>

  <template v-else>
    <Card v-if="adding" class="mb-4 border-primary/50">
      <form novalidate data-testid="task-form" @submit.prevent="addManual">
        <CardHeader><CardTitle>{{ t('cleaning.addTitle') }}</CardTitle></CardHeader>
        <CardContent>
          <div class="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
            <FormField :label="t('cleaning.room')" :error="fieldError('room_id')">
              <template #default="{ id, invalid }">
                <NativeSelect :id="id" v-model.number="manual.room_id" name="room_id" :aria-invalid="invalid">
                  <option v-for="r in boardRooms" :key="r.room_id" :value="r.room_id">{{ r.room_number }} · {{ r.room_type_code }}</option>
                </NativeSelect>
              </template>
            </FormField>
            <FormField :label="t('cleaning.kind')">
              <template #default="{ id }">
                <NativeSelect :id="id" v-model="manual.task_type" name="task_type">
                  <option v-for="k in ['DEEP', 'OTHER', 'CHECKOUT', 'STAYOVER', 'ARRIVAL', 'DIRTY']" :key="k" :value="k">{{ typeLabel(k) }}</option>
                </NativeSelect>
              </template>
            </FormField>
            <FormField :label="t('cleaning.priority')">
              <template #default="{ id }">
                <NativeSelect :id="id" v-model="manual.priority" name="priority">
                  <option value="NORMAL">{{ t('cleaning.normal') }}</option>
                  <option value="HIGH">{{ t('cleaning.high') }}</option>
                </NativeSelect>
              </template>
            </FormField>
            <FormField :label="t('cleaning.assignTo')" :error="fieldError('assigned_to')">
              <template #default="{ id, invalid }">
                <NativeSelect :id="id" v-model.number="manual.assigned_to" name="assigned_to" :aria-invalid="invalid">
                  <option :value="0">{{ t('cleaning.nobodyYet') }}</option>
                  <option v-for="s in staff" :key="s.id" :value="s.id">{{ s.full_name }}</option>
                </NativeSelect>
              </template>
            </FormField>
            <FormField :label="t('cleaning.note')">
              <template #default="{ id }"><Input :id="id" v-model="manual.notes" name="notes" maxlength="500" /></template>
            </FormField>
          </div>
          <div class="mt-4 flex justify-end gap-2">
            <Button type="button" variant="outline" @click="adding = false">{{ t('common.cancel') }}</Button>
            <Button type="submit" :disabled="busy">{{ t('cleaning.add') }}</Button>
          </div>
        </CardContent>
      </form>
    </Card>

    <Card v-if="list" class="mb-4" data-testid="workload">
      <CardHeader><CardTitle>{{ $date(list.date) }}</CardTitle></CardHeader>
      <CardContent>
        <p v-if="!list.workload.length" class="m-0 text-sm text-muted-foreground" data-testid="empty">
          {{ t('cleaning.noTasks') }} {{ can('housekeeping.assign') ? t('cleaning.generateToStart') : t('cleaning.supervisorGenerates') }}
        </p>
        <DataTable v-else :columns="workloadColumns" :rows="list.workload" row-key="name" :row-test-id="(w) => `workload-${w.user_id ?? 'none'}`" :caption="t('cleaning.housekeeper')" />
      </CardContent>
    </Card>

    <Card v-if="(list && list.data.length) || filter.view !== 'all' || filter.status" class="mb-4">
      <CardContent class="flex flex-col gap-4 pt-5">
        <form class="flex flex-wrap items-end gap-3" novalidate @submit.prevent="load()">
          <FormField class="w-44" :label="t('cleaning.show')">
            <template #default="{ id }">
              <NativeSelect :id="id" v-model="filter.view" name="view">
                <option value="all">{{ t('cleaning.everything') }}</option>
                <option value="mine">{{ t('cleaning.mine') }}</option>
                <option value="unassigned">{{ t('cleaning.notAssigned') }}</option>
              </NativeSelect>
            </template>
          </FormField>
          <FormField class="w-44" :label="t('cleaning.status')">
            <template #default="{ id }">
              <NativeSelect :id="id" v-model="filter.status" name="status">
                <option value="">{{ t('cleaning.any') }}</option>
                <option v-for="k in TASK_STATUSES" :key="k" :value="k">{{ t(`status.${k}` as never) }}</option>
              </NativeSelect>
            </template>
          </FormField>
        </form>

        <form v-if="can('housekeeping.assign') && openCount" class="flex flex-wrap items-end gap-3" novalidate data-testid="assign-form" @submit.prevent="assign">
          <FormField class="w-64" :label="t('cleaning.giveTicked', { n: picked.length })">
            <template #default="{ id }">
              <NativeSelect :id="id" v-model.number="assignTo" name="assign_to">
                <option :value="0">{{ t('cleaning.nobodyTakeBack') }}</option>
                <option v-for="s in staff" :key="s.id" :value="s.id">{{ s.full_name }}</option>
              </NativeSelect>
            </template>
          </FormField>
          <Button type="submit" variant="outline" :disabled="busy || !picked.length" data-testid="assign">{{ t('cleaning.assign') }}</Button>
        </form>

        <DataTable
          v-if="list && list.data.length"
          :columns="taskColumns"
          :rows="list.data"
          row-key="id"
          :row-test-id="(task) => `task-${task.room_number}-${task.task_type}`"
          :row-class="(task) => (open(task) ? undefined : 'closed text-muted-foreground')"
          :caption="t('cleaning.title')"
          data-testid="tasks"
        >
          <template #header-pick>
            <input type="checkbox" :aria-label="t('cleaning.tickAll')" data-testid="pick-all" :checked="openCount > 0 && picked.length === openCount" @change="toggleAll(($event.target as HTMLInputElement).checked)" />
          </template>
          <template #cell-pick="{ row: task }">
            <input v-model="picked" type="checkbox" :value="task.id" :disabled="!open(task)" :aria-label="t('cleaning.tickRoom', { number: task.room_number })" />
          </template>
          <template #cell-room_number="{ row: task }">
            <b>{{ task.room_number }}</b>
            <small v-if="task.floor" class="ml-1 text-muted-foreground">{{ t('cleaning.floorWord', { floor: task.floor }) }}</small>
            <Badge v-if="task.priority === 'HIGH'" variant="destructive" class="ml-1.5">{{ t('cleaning.highShort') }}</Badge>
            <Badge v-if="task.dnd" variant="warning" class="ml-1.5" data-testid="dnd">{{ t('cleaning.dndShort') }}</Badge>
            <Badge v-if="task.make_up_requested" variant="secondary" class="ml-1.5">{{ t('cleaning.makeUpShort') }}</Badge>
            <small v-if="task.flag_note" class="ml-1 text-muted-foreground">{{ task.flag_note }}</small>
          </template>
          <template #cell-task_type="{ row: task }">{{ typeLabel(task.task_type) }}</template>
          <template #cell-room_status="{ row: task }"><StatusBadge domain="housekeeping" :status="task.room_status" /></template>
          <template #cell-assignee_name="{ row: task }">{{ task.assignee_name || '—' }}</template>
          <template #cell-status="{ row: task }">
            <StatusBadge domain="task" :status="task.status" />
            <small v-if="task.status === 'SKIPPED' && task.notes" class="ml-1 text-muted-foreground">{{ task.notes }}</small>
          </template>
          <template #cell-actions="{ row: task }">
            <span class="inline-flex flex-wrap justify-end gap-1.5">
              <Button v-if="mayWork(task) && task.status === 'PENDING'" variant="outline" size="sm" :disabled="busy" :data-testid="`start-${task.room_number}`" @click="start(task)">{{ t('cleaning.start') }}</Button>
              <Button v-if="mayWork(task)" size="sm" :disabled="busy" :data-testid="`done-${task.room_number}`" @click="complete(task)">{{ t('cleaning.doneButton') }}</Button>
              <Button v-if="mayWork(task)" variant="outline" size="sm" :disabled="busy" :data-testid="`skip-${task.room_number}`" @click="askSkip(task)">{{ t('cleaning.skip') }}</Button>
            </span>
          </template>
        </DataTable>
        <EmptyState v-else :title="t('cleaning.noMatch')" data-testid="no-match" />
      </CardContent>
    </Card>

    <Card v-if="skipping" class="mb-4 border-primary/50">
      <form novalidate data-testid="skip-form" @submit.prevent="skip">
        <CardHeader><CardTitle>{{ t('cleaning.skipTitle', { number: skipping.task.room_number }) }}</CardTitle></CardHeader>
        <CardContent>
          <FormField :label="t('cleaning.reason')" :error="fieldError('reason')">
            <template #default="{ id, invalid }"><Input :id="id" v-model="skipping.reason" name="skip_reason" maxlength="500" :aria-invalid="invalid" /></template>
          </FormField>
          <div class="mt-4 flex justify-end gap-2">
            <Button type="button" variant="outline" @click="skipping = null">{{ t('common.cancel') }}</Button>
            <Button type="submit" :disabled="busy || !skipping.reason.trim()">{{ t('cleaning.skip') }}</Button>
          </div>
        </CardContent>
      </form>
    </Card>
  </template>
</template>
