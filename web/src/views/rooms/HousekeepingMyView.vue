<script setup lang="ts">
import { BellOff, ChevronsUp, Sparkles, UserCheck } from 'lucide-vue-next'
import { computed, ref, watch } from 'vue'
import { RouterLink } from 'vue-router'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { HousekeepingStaffMember, HousekeepingTask, HousekeepingTaskList } from '@/api/types'
import EmptyState from '@/components/app/EmptyState.vue'
import ErrorNotice from '@/components/app/ErrorNotice.vue'
import PageHeader from '@/components/app/PageHeader.vue'
import StatusBadge from '@/components/app/StatusBadge.vue'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Sheet, SheetContent, SheetDescription, SheetTitle } from '@/components/ui/sheet'
import { Skeleton } from '@/components/ui/skeleton'
import { useAutoRefresh } from '@/composables/useAutoRefresh'
import { t } from '@/i18n'
import { cn } from '@/lib/utils'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'

/**
 * The cleaning work on a phone. A housekeeper sees their own tasks as large cards, in the order the API gives (high priority first, then floor and room) and does the work from the card:
 * start, finish, or skip with a reason. A supervisor (housekeeping.assign) can also see everyone's tasks with the workload of each person, and give tasks to a person. The table of the
 * cleaning list (/housekeeping/tasks) stays for the desk; this page is the one for the corridor. It reads again by itself (once a minute and when the tab comes back), but not while a sheet is open.
 */
const REFRESH_MS = 60 * 1000

const auth = useAuthStore()
const property = usePropertyStore()

const pid = computed(() => property.currentId)
const can = (permission: string): boolean => pid.value !== null && auth.can(permission, pid.value)
const canWork = computed(() => can('housekeeping.update'))
const canAssign = computed(() => can('housekeeping.assign'))
const me = computed(() => auth.me?.user.id ?? 0)

const list = ref<HousekeepingTaskList | null>(null)
const staff = ref<HousekeepingStaffMember[]>([])
const error = ref<ApiError | null>(null)
const notice = ref('')
const busy = ref(false)
const loading = ref(false)

// A housekeeper's page is their own tasks; a supervisor who does no cleaning of their own starts at everyone's.
const scope = ref<'mine' | 'all'>(canWork.value ? 'mine' : 'all')
const tab = ref<'next' | 'done'>('next')
const floor = ref<string | null>(null) // null: every floor; '' is the tasks without a floor
const person = ref<'all' | 'none' | number>('all') // in "everyone": whose tasks
const picked = ref<number[]>([])
const skipping = ref<{ task: HousekeepingTask; reason: string } | null>(null)
const assigning = ref(false)

const open = (task: HousekeepingTask): boolean => task.status === 'PENDING' || task.status === 'IN_PROGRESS'
const typeLabel = (k: string): string => t(`cleaning.type${k}` as never)

async function load(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null || loading.value || (!canWork.value && !canAssign.value)) return
  loading.value = true
  const asked = scope.value
  try {
    const query = asked === 'mine' ? { assigned_to: me.value } : {}
    const { data } = await api.GET('/api/v1/properties/{propertyId}/housekeeping/tasks', { params: { path: { propertyId }, query } })
    if (asked !== scope.value) return // the person switched while this was on its way
    list.value = data ?? null
    picked.value = picked.value.filter((id) => list.value?.data.some((task) => task.id === id && open(task)))
    error.value = null
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    loading.value = false
  }
}

async function loadStaff(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null || !canAssign.value) return
  try {
    const { data } = await api.GET('/api/v1/properties/{propertyId}/housekeeping/staff', { params: { path: { propertyId } } })
    staff.value = data?.data ?? []
  } catch {
    staff.value = []
  }
}

// One call, then the list is read again, also after a refusal (somebody else may have changed the task).
async function run(action: () => Promise<unknown>): Promise<void> {
  busy.value = true
  error.value = null
  notice.value = ''
  try {
    await action()
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    busy.value = false
  }
  const failure = error.value
  await load()
  error.value = failure
}

const taskPath = (task: HousekeepingTask) => ({ path: { propertyId: pid.value as number, taskId: task.id } })
const start = (task: HousekeepingTask) => run(() => api.POST('/api/v1/properties/{propertyId}/housekeeping/tasks/{taskId}/start', { params: taskPath(task) }))
const finish = (task: HousekeepingTask) => run(() => api.POST('/api/v1/properties/{propertyId}/housekeeping/tasks/{taskId}/complete', { params: taskPath(task), body: {} }))

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
const skipOpen = computed({
  get: () => skipping.value !== null,
  set: (v: boolean) => {
    if (!v) skipping.value = null
  },
})

// ---- what is shown ----

// In "everyone" the supervisor may narrow to one person (or to the tasks nobody has).
const ofPerson = computed(() => {
  const all = list.value?.data ?? []
  if (scope.value === 'mine' || person.value === 'all') return all
  return all.filter((task) => (person.value === 'none' ? task.assigned_to === null : task.assigned_to === person.value))
})
const floors = computed(() => {
  const seen = new Set(ofPerson.value.map((task) => task.floor ?? ''))
  return [...seen].sort((a, b) => (a === '' ? 1 : b === '' ? -1 : a.localeCompare(b, undefined, { numeric: true })))
})
const onFloor = computed(() => (floor.value === null ? ofPerson.value : ofPerson.value.filter((task) => (task.floor ?? '') === floor.value)))
const next = computed(() => onFloor.value.filter(open))
const done = computed(() => onFloor.value.filter((task) => !open(task)))
const shown = computed(() => (tab.value === 'next' ? next.value : done.value))
// The progress is of the whole job of the person (all floors): finished and skipped are both "handled".
const handled = computed(() => ofPerson.value.filter((task) => !open(task)).length)
const progressPercent = computed(() => (ofPerson.value.length ? Math.round((handled.value / ofPerson.value.length) * 100) : 0))

// The buttons of the work are on the tasks of the person who is signed in (a supervisor does not start somebody else's task from here; the desk list can).
const mayWork = (task: HousekeepingTask): boolean => canWork.value && open(task) && task.assigned_to === me.value
const mayPick = (task: HousekeepingTask): boolean => canAssign.value && scope.value === 'all' && open(task)
const isPicked = (task: HousekeepingTask): boolean => picked.value.includes(task.id)
function togglePick(task: HousekeepingTask): void {
  picked.value = isPicked(task) ? picked.value.filter((id) => id !== task.id) : [...picked.value, task.id]
}

function pickPerson(p: 'all' | 'none' | number): void {
  person.value = person.value === p ? 'all' : p
  floor.value = null
}

async function assign(userId: number | null): Promise<void> {
  const ids = [...picked.value]
  await run(async () => {
    const { data } = await api.POST('/api/v1/properties/{propertyId}/housekeeping/tasks/assign', { params: { path: { propertyId: pid.value as number } }, body: { task_ids: ids, user_id: userId } })
    const n = data?.assigned ?? ids.length
    notice.value = userId === null ? t('myTasks.takenBackDone', { n }, n) : t('myTasks.assignedDone', { n }, n)
    picked.value = []
    assigning.value = false
  })
}

const time = (iso: string | null | undefined): string => (iso ? new Date(iso).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' }) : '')
const workloadDone = (w: HousekeepingTaskList['workload'][number]): number => w.done + w.skipped
const workloadKey = (w: HousekeepingTaskList['workload'][number]): 'none' | number => w.user_id ?? 'none'
const workloadPercent = (w: HousekeepingTaskList['workload'][number]): number => (w.total ? Math.round((workloadDone(w) / w.total) * 100) : 0)

watch(() => pid.value, () => {
  list.value = null
  picked.value = []
  notice.value = ''
  scope.value = canWork.value ? 'mine' : 'all'
  void load()
  void loadStaff()
}, { immediate: true })
watch(scope, () => {
  list.value = null
  picked.value = []
  person.value = 'all'
  floor.value = null
  void load()
})
// A floor that no task has any more (the list changed) is not kept as the filter.
watch(floors, (f) => {
  if (floor.value !== null && !f.includes(floor.value)) floor.value = null
})

useAutoRefresh(() => void load(), { everyMs: REFRESH_MS, paused: () => skipping.value !== null || assigning.value || busy.value, busy: () => loading.value })
</script>

<template>
  <div class="mx-auto w-full max-w-xl">
    <PageHeader :title="t('myTasks.title')" :description="t('myTasks.description')">
      <template #actions>
        <Button v-if="canAssign" as-child variant="outline" size="sm">
          <RouterLink to="/housekeeping/tasks" data-testid="open-full">{{ t('myTasks.openFull') }}</RouterLink>
        </Button>
      </template>
    </PageHeader>

    <ErrorNotice v-if="error" :error="error" inline data-testid="hk-error" />
    <p v-if="notice" class="alert warning" role="status" data-testid="notice">{{ notice }}</p>
    <p v-if="pid === null" class="muted">{{ t('myTasks.selectProperty') }}</p>
    <p v-else-if="!canWork && !canAssign" class="muted" data-testid="no-access">{{ t('myTasks.noAccess') }}</p>

    <template v-else>
      <div v-if="canAssign && canWork" class="mb-3 flex gap-1.5 rounded-lg bg-muted p-1" role="tablist" :aria-label="t('myTasks.scopeLabel')" data-testid="scope">
        <button
          v-for="s in (['mine', 'all'] as const)"
          :key="s"
          type="button"
          role="tab"
          :aria-selected="scope === s"
          :data-testid="`scope-${s}`"
          :class="cn('min-h-10 flex-1 cursor-pointer rounded-md border-0 px-3 text-sm font-semibold', scope === s ? 'bg-card text-foreground shadow-sm' : 'bg-transparent text-muted-foreground')"
          @click="scope = s"
        >{{ s === 'mine' ? t('myTasks.scopeMine') : t('myTasks.scopeAll') }}</button>
      </div>

      <div v-if="list === null && !error" class="flex flex-col gap-2.5" data-testid="loading">
        <Skeleton v-for="n in 3" :key="n" class="h-32 rounded-xl" />
      </div>

      <template v-else-if="list">
        <section v-if="scope === 'all' && list.workload.length" class="mb-4" :aria-label="t('myTasks.workload')" data-testid="workload">
          <h2 class="mb-2 text-sm font-semibold text-muted-foreground">{{ t('myTasks.workload') }}</h2>
          <ul class="m-0 flex list-none flex-col gap-2 p-0">
            <li v-for="w in list.workload" :key="workloadKey(w)">
              <button
                type="button"
                :aria-pressed="person === workloadKey(w)"
                :data-testid="`person-${workloadKey(w)}`"
                :class="cn('flex w-full cursor-pointer flex-col gap-1.5 rounded-xl border border-border bg-card p-3 text-left', person === workloadKey(w) && 'ring-2 ring-primary')"
                @click="pickPerson(workloadKey(w))"
              >
                <span class="flex items-baseline justify-between gap-3">
                  <strong class="text-base">{{ w.user_id === null ? t('myTasks.unassigned') : w.name }}</strong>
                  <span class="text-sm text-muted-foreground">{{ t('myTasks.workloadOf', { done: workloadDone(w), total: w.total }) }}</span>
                </span>
                <span class="h-2 rounded-full bg-muted" aria-hidden="true"><span class="block h-2 rounded-full bg-primary" :style="{ width: `${workloadPercent(w)}%` }" /></span>
              </button>
            </li>
          </ul>
        </section>

        <div v-if="ofPerson.length" class="mb-3 flex flex-col gap-1.5" data-testid="progress">
          <p class="m-0 text-sm font-semibold">{{ t('myTasks.progress', { done: handled, total: ofPerson.length }) }}</p>
          <div class="h-2 rounded-full bg-muted" role="progressbar" :aria-valuenow="progressPercent" aria-valuemin="0" aria-valuemax="100"><div class="h-2 rounded-full bg-primary" :style="{ width: `${progressPercent}%` }" /></div>
        </div>

        <div v-if="floors.length > 1" class="mb-3 flex flex-wrap gap-1.5" role="group" :aria-label="t('myTasks.floorsLabel')" data-testid="floors">
          <Button type="button" size="sm" class="min-h-9" :variant="floor === null ? 'default' : 'outline'" :aria-pressed="floor === null" data-testid="floor-all" @click="floor = null">{{ t('myTasks.allFloors') }}</Button>
          <Button
            v-for="f in floors"
            :key="f"
            type="button"
            size="sm"
            class="min-h-9"
            :variant="floor === f ? 'default' : 'outline'"
            :aria-pressed="floor === f"
            :data-testid="`floor-${f || 'none'}`"
            @click="floor = f"
          >{{ f ? t('myTasks.floorChip', { floor: f }) : t('myTasks.noFloorChip') }}</Button>
        </div>

        <div v-if="ofPerson.length" class="mb-3 flex gap-1.5 rounded-lg bg-muted p-1" role="tablist" :aria-label="t('myTasks.tabsLabel')" data-testid="tabs">
          <button type="button" role="tab" :aria-selected="tab === 'next'" data-testid="tab-next" :class="cn('min-h-10 flex-1 cursor-pointer rounded-md border-0 px-3 text-sm font-semibold', tab === 'next' ? 'bg-card text-foreground shadow-sm' : 'bg-transparent text-muted-foreground')" @click="tab = 'next'">{{ t('myTasks.tabNext', { n: next.length }) }}</button>
          <button type="button" role="tab" :aria-selected="tab === 'done'" data-testid="tab-done" :class="cn('min-h-10 flex-1 cursor-pointer rounded-md border-0 px-3 text-sm font-semibold', tab === 'done' ? 'bg-card text-foreground shadow-sm' : 'bg-transparent text-muted-foreground')" @click="tab = 'done'">{{ t('myTasks.tabDone', { n: done.length }) }}</button>
        </div>

        <EmptyState
          v-if="!list.data.length"
          :title="scope === 'mine' ? t('myTasks.noTasks') : t('myTasks.noTasksAll')"
          :description="scope === 'mine' ? t('myTasks.emptyMine') : t('myTasks.emptyAll')"
          data-testid="empty"
        />
        <EmptyState
          v-else-if="!shown.length"
          :title="tab === 'next' ? t('myTasks.nothingNext') : t('myTasks.nothingDone')"
          :description="tab === 'next' ? t('myTasks.emptyNext') : t('myTasks.emptyDone')"
          data-testid="empty-tab"
        />

        <ul class="m-0 flex list-none flex-col gap-2.5 p-0" :class="picked.length ? 'pb-24' : undefined" data-testid="cards">
          <li
            v-for="task in shown"
            :key="task.id"
            :data-testid="`card-${task.room_number}`"
            :data-status="task.status"
            :class="cn('flex flex-col gap-3 rounded-xl border border-border bg-card p-3.5', (task.arrival_today || task.priority === 'HIGH') && open(task) && 'border-2 border-warning', task.status === 'IN_PROGRESS' && 'border-2 border-primary', !open(task) && 'text-muted-foreground')"
          >
            <div class="flex items-start justify-between gap-3">
              <div class="min-w-0">
                <strong class="block text-[1.65rem] font-extrabold leading-none">{{ task.room_number }}</strong>
                <span class="mt-1 block text-sm text-muted-foreground">
                  {{ [task.room_type_code, task.floor ? t('cleaning.floorWord', { floor: task.floor }) : '', typeLabel(task.task_type)].filter(Boolean).join(' · ') }}
                </span>
              </div>
              <div class="flex shrink-0 flex-col items-end gap-1.5">
                <StatusBadge domain="task" :status="task.status" />
                <StatusBadge domain="housekeeping" :status="task.room_status" />
              </div>
            </div>

            <div class="flex flex-wrap items-center gap-1.5 empty:hidden">
              <Badge v-if="task.arrival_today" variant="warning" data-testid="arrival-today">{{ t('myTasks.arrivalToday') }}</Badge>
              <Badge v-if="task.priority === 'HIGH'" variant="destructive"><ChevronsUp class="size-3.5" aria-hidden="true" />{{ t('cleaning.highShort') }}</Badge>
              <Badge v-if="task.dnd" variant="warning" data-testid="dnd"><BellOff class="size-3.5" aria-hidden="true" />{{ t('cleaning.dndShort') }}</Badge>
              <Badge v-if="task.make_up_requested" variant="secondary"><Sparkles class="size-3.5" aria-hidden="true" />{{ t('cleaning.makeUpShort') }}</Badge>
              <Badge v-if="task.status === 'IN_PROGRESS' && task.started_at" variant="outline">{{ t('myTasks.startedAt', { time: time(task.started_at) }) }}</Badge>
            </div>
            <p v-if="task.flag_note" class="m-0 text-sm text-muted-foreground">{{ task.flag_note }}</p>
            <p v-if="task.status === 'SKIPPED' && task.notes" class="m-0 text-sm">{{ task.notes }}</p>
            <p v-if="scope === 'all'" class="m-0 flex items-center gap-1.5 text-sm text-muted-foreground" data-testid="assignee">
              <UserCheck class="size-4" aria-hidden="true" />{{ task.assignee_name ? t('myTasks.assignedTo', { name: task.assignee_name }) : t('myTasks.unassigned') }}
            </p>

            <div v-if="mayWork(task)" class="flex gap-2">
              <Button v-if="task.status === 'PENDING'" class="min-h-[52px] flex-1 text-base font-bold" :disabled="busy" :data-testid="`start-${task.room_number}`" @click="start(task)">{{ t('myTasks.startAction') }}</Button>
              <Button v-else class="min-h-[52px] flex-1 text-base font-bold" :disabled="busy" :data-testid="`finish-${task.room_number}`" @click="finish(task)">{{ t('myTasks.finishAction') }}</Button>
              <Button variant="outline" class="min-h-[52px] px-4" :disabled="busy" :data-testid="`skip-${task.room_number}`" @click="askSkip(task)">{{ t('myTasks.skipAction') }}</Button>
            </div>
            <label v-if="mayPick(task)" class="flex min-h-11 cursor-pointer items-center gap-3 text-sm font-medium">
              <input type="checkbox" class="size-5 accent-primary" :checked="isPicked(task)" :data-testid="`pick-${task.room_number}`" :aria-label="t('cleaning.tickRoom', { number: task.room_number })" @change="togglePick(task)" />
              {{ t('cleaning.tickRoom', { number: task.room_number }) }}
            </label>
          </li>
        </ul>
      </template>
    </template>
  </div>

  <div
    v-if="canAssign && picked.length"
    role="region"
    :aria-label="t('myTasks.assignAction')"
    class="fixed inset-x-3 bottom-3 z-30 mx-auto flex max-w-xl items-center gap-2 rounded-xl bg-foreground p-3 text-background shadow-xl"
    data-testid="assign-bar"
  >
    <strong class="flex-1 px-1.5 text-sm" data-testid="picked-count">{{ t('myTasks.selectedCount', { n: picked.length }) }}</strong>
    <Button class="min-h-11" variant="secondary" :disabled="busy" data-testid="assign-open" @click="assigning = true">{{ t('myTasks.assignAction') }}</Button>
    <Button class="min-h-11 text-background/80 hover:bg-background/10 hover:text-background" variant="ghost" data-testid="assign-clear" @click="picked = []">{{ t('myTasks.clearSelection') }}</Button>
  </div>

  <Sheet v-model:open="skipOpen">
    <SheetContent v-if="skipping" side="bottom" class="p-5" data-testid="skip-sheet">
      <SheetTitle class="text-lg">{{ t('cleaning.skipTitle', { number: skipping.task.room_number }) }}</SheetTitle>
      <SheetDescription class="sr-only">{{ t('cleaning.reason') }}</SheetDescription>
      <form class="mt-3 flex flex-col gap-3" novalidate data-testid="skip-form" @submit.prevent="skip">
        <label class="flex flex-col gap-1.5 text-sm font-medium">
          {{ t('cleaning.reason') }}
          <Input v-model="skipping.reason" name="skip_reason" maxlength="500" class="h-11" :aria-invalid="error?.fieldMessage('reason') ? true : undefined" />
        </label>
        <Button type="button" variant="outline" class="min-h-11 self-start" data-testid="reason-dnd" @click="skipping.reason = t('cleaning.dndReason')">{{ t('cleaning.dndReason') }}</Button>
        <ErrorNotice v-if="error" :error="error" inline data-testid="skip-error" />
        <Button type="submit" class="min-h-[52px] text-base font-bold" :disabled="busy || !skipping.reason.trim()" data-testid="skip-confirm">{{ t('cleaning.skip') }}</Button>
      </form>
    </SheetContent>
  </Sheet>

  <Sheet v-model:open="assigning">
    <SheetContent side="bottom" class="max-h-[80vh] overflow-y-auto p-5" data-testid="assign-sheet">
      <SheetTitle class="text-lg">{{ t('myTasks.assignTitle') }}</SheetTitle>
      <SheetDescription class="sr-only">{{ t('myTasks.selectedCount', { n: picked.length }) }}</SheetDescription>
      <ul class="m-0 mt-3 flex list-none flex-col gap-2 p-0">
        <li v-for="s in staff" :key="s.id">
          <Button variant="outline" class="min-h-12 w-full justify-start text-base" :disabled="busy" :data-testid="`assign-to-${s.id}`" @click="assign(s.id)">{{ s.full_name }}</Button>
        </li>
        <li>
          <Button variant="ghost" class="min-h-12 w-full justify-start text-base" :disabled="busy" data-testid="assign-take-back" @click="assign(null)">{{ t('cleaning.nobodyTakeBack') }}</Button>
        </li>
      </ul>
    </SheetContent>
  </Sheet>
</template>
