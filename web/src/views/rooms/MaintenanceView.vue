<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { HousekeepingBoardRoom, HousekeepingStaffMember, MaintenanceRequest } from '@/api/types'
import DataTable, { type Column } from '@/components/app/DataTable.vue'
import EmptyState from '@/components/app/EmptyState.vue'
import FormField from '@/components/app/FormField.vue'
import PageHeader from '@/components/app/PageHeader.vue'
import StatusBadge from '@/components/app/StatusBadge.vue'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Combobox } from '@/components/ui/combobox'
import { NativeSelect } from '@/components/ui/native-select'
import { t } from '@/i18n'
import { cn } from '@/lib/utils'
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
const CATEGORIES = ['PLUMBING', 'ELECTRICAL', 'AC', 'FURNITURE', 'APPLIANCE', 'OTHER']
const PRIORITIES = ['LOW', 'NORMAL', 'HIGH', 'URGENT']
const where = (r: MaintenanceRequest) => (r.room_number ? t('maintenance.roomWord', { number: r.room_number }) : r.location || '')
const categoryLabel = (c: string): string => t(`maintenance.cat${c}` as never)
const priorityLabel = (p: string): string => t(`maintenance.pr${p}` as never)
const priorityVariant = (p: string) => (p === 'URGENT' ? ('destructive' as const) : p === 'HIGH' ? ('warning' as const) : p === 'LOW' ? ('secondary' as const) : ('outline' as const))

const columns = computed<Column<MaintenanceRequest>[]>(() => [
  { key: 'request_number', label: t('maintenance.number'), sortable: true },
  { key: 'where', label: t('maintenance.where') },
  { key: 'category', label: t('maintenance.category'), sortable: true },
  { key: 'priority', label: t('maintenance.priority') },
  { key: 'description', label: t('maintenance.problem') },
  { key: 'assignee_name', label: t('maintenance.assignedTo'), sortable: true },
  { key: 'status', label: t('maintenance.status'), sortable: true },
])

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
    notice.value = t('maintenance.noticeReported', { number: data?.request_number ?? '' })
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

const assign = () => run(() => api.POST('/api/v1/properties/{propertyId}/maintenance-requests/{id}/assign', { params: base(), body: { user_id: action.assignTo || null } }), t('maintenance.noticeSaved'))
const start = () => run(() => api.POST('/api/v1/properties/{propertyId}/maintenance-requests/{id}/start', { params: base() }), t('maintenance.noticeStarted'))
const reopen = () => run(() => api.POST('/api/v1/properties/{propertyId}/maintenance-requests/{id}/reopen', { params: base() }), t('maintenance.noticeReopened'))
const setPriority = (priority: string) =>
  run(() => api.PATCH('/api/v1/properties/{propertyId}/maintenance-requests/{id}', { params: base(), body: { priority: priority as 'NORMAL' } }), t('maintenance.noticePriority'))

async function close(): Promise<void> {
  const r = selected.value
  if (!r || !closing.value) return
  const body = { note: action.note || undefined, release_block: action.releaseBlock && r.block?.status === 'ACTIVE' }
  const verb = closing.value
  await run(
    () => (verb === 'resolve'
      ? api.POST('/api/v1/properties/{propertyId}/maintenance-requests/{id}/resolve', { params: base(), body })
      : api.POST('/api/v1/properties/{propertyId}/maintenance-requests/{id}/cancel', { params: base(), body })),
    verb === 'resolve' ? t('maintenance.noticeResolved') : t('maintenance.noticeCancelled'),
  )
  if (!error.value) closing.value = null
}

const block = () => run(() => api.POST('/api/v1/properties/{propertyId}/maintenance-requests/{id}/block', {
  params: base(), body: { block_type: action.blockType as 'OOO', end_date: action.blockEnd },
}), t('maintenance.noticeRoomOut'))

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
  <PageHeader :title="t('maintenance.title')">
    <template #actions>
      <Button v-if="can('maintenance.report') && !creating" size="sm" data-testid="new-request" @click="startNew()">{{ t('maintenance.report') }}</Button>
    </template>
  </PageHeader>

  <p v-if="error" class="alert" role="alert" data-testid="mt-error">{{ error.message }} <code>{{ error.code }}</code></p>
  <p v-if="notice" class="alert warning" role="status" data-testid="notice">{{ notice }}</p>
  <p v-if="pid === null" class="muted">{{ t('maintenance.selectProperty') }}</p>
  <p v-else-if="!can('maintenance.report')" class="muted" data-testid="no-access">{{ t('maintenance.noAccess') }}</p>

  <template v-else>
    <Card v-if="creating" class="mb-4 border-primary/50">
      <form novalidate data-testid="request-form" @submit.prevent="report">
        <CardHeader><CardTitle>{{ t('maintenance.report') }}</CardTitle></CardHeader>
        <CardContent>
          <div class="grid gap-4 sm:grid-cols-2">
            <FormField :label="t('maintenance.room')">
              <template #default="{ id }">
                <Combobox :id="id" v-model="form.room_id" name="room_id" :options="[{ value: 0, label: `${t('maintenance.notRoom')}` }, ...roomList.map((r) => ({ value: r.room_id, label: `${r.room_number} · ${r.room_type_code}` }))]" />
              </template>
            </FormField>
            <FormField :label="t('maintenance.place')" :error="fieldError('location')">
              <template #default="{ id, invalid }"><Input :id="id" v-model="form.location" name="location" maxlength="150" :placeholder="t('maintenance.placePlaceholder')" :aria-invalid="invalid" /></template>
            </FormField>
            <FormField :label="t('maintenance.category')">
              <template #default="{ id }">
                <NativeSelect :id="id" v-model="form.category" name="category">
                  <option v-for="c in CATEGORIES" :key="c" :value="c">{{ categoryLabel(c) }}</option>
                </NativeSelect>
              </template>
            </FormField>
            <FormField :label="t('maintenance.priority')">
              <template #default="{ id }">
                <NativeSelect :id="id" v-model="form.priority" name="priority">
                  <option v-for="p in PRIORITIES" :key="p" :value="p">{{ priorityLabel(p) }}</option>
                </NativeSelect>
              </template>
            </FormField>
            <FormField class="sm:col-span-2" :label="t('maintenance.whatWrong')" :error="fieldError('description')">
              <template #default="{ id, invalid }"><Input :id="id" v-model="form.description" name="description" maxlength="1000" :aria-invalid="invalid" /></template>
            </FormField>
          </div>
          <div class="mt-4 flex justify-end gap-2">
            <Button type="button" variant="outline" @click="creating = false">{{ t('common.cancel') }}</Button>
            <Button type="submit" :disabled="busy">{{ t('maintenance.reportButton') }}</Button>
          </div>
        </CardContent>
      </form>
    </Card>

    <div :class="cn('grid items-start gap-4', selected && 'xl:grid-cols-[minmax(0,1fr)_26rem]')">
      <div class="min-w-0">
        <form class="mb-3 flex flex-wrap items-end gap-3" novalidate @submit.prevent="load()">
          <FormField class="w-52" :label="t('maintenance.show')">
            <template #default="{ id }">
              <NativeSelect :id="id" v-model="filter.view" name="view">
                <option value="open">{{ t('maintenance.openInProgress') }}</option>
                <option value="all">{{ t('maintenance.everything') }}</option>
                <option value="RESOLVED">{{ t('status.RESOLVED') }}</option>
                <option value="CANCELLED">{{ t('status.CANCELLED') }}</option>
              </NativeSelect>
            </template>
          </FormField>
          <FormField class="w-44" :label="t('maintenance.category')">
            <template #default="{ id }">
              <NativeSelect :id="id" v-model="filter.category" name="filter_category">
                <option value="">{{ t('maintenance.any') }}</option>
                <option v-for="c in CATEGORIES" :key="c" :value="c">{{ categoryLabel(c) }}</option>
              </NativeSelect>
            </template>
          </FormField>
          <FormField class="w-40" :label="t('maintenance.priority')">
            <template #default="{ id }">
              <NativeSelect :id="id" v-model="filter.priority" name="filter_priority">
                <option value="">{{ t('maintenance.any') }}</option>
                <option v-for="p in PRIORITIES" :key="p" :value="p">{{ priorityLabel(p) }}</option>
              </NativeSelect>
            </template>
          </FormField>
        </form>

        <DataTable
          :columns="columns"
          :rows="rows"
          row-key="id"
          :loading="!loaded"
          :row-test-id="(r) => `request-${r.request_number}`"
          :row-class="(r) => cn(r.id === selectedId && 'bg-accent', !isOpen(r) && 'closed text-muted-foreground')"
          :caption="t('maintenance.title')"
          data-testid="requests"
        >
          <template #cell-request_number="{ row: r }">
            <button type="button" class="cursor-pointer border-0 bg-transparent p-0 text-primary underline-offset-2 hover:underline" :data-testid="`select-${r.request_number}`" @click="select(r)">{{ r.request_number }}</button>
          </template>
          <template #cell-where="{ row: r }">
            {{ where(r) }}
            <Badge v-if="r.block && r.block.status === 'ACTIVE'" variant="warning" class="ml-1" data-testid="blocked">{{ t('maintenance.blockUntil', { type: r.block.block_type, date: $date(r.block.end_date) }) }}</Badge>
          </template>
          <template #cell-category="{ row: r }">{{ categoryLabel(r.category) }}</template>
          <template #cell-priority="{ row: r }"><Badge :variant="priorityVariant(r.priority)">{{ priorityLabel(r.priority) }}</Badge></template>
          <template #cell-assignee_name="{ row: r }">{{ r.assignee_name || '—' }}</template>
          <template #cell-status="{ row: r }"><StatusBadge domain="work" :status="r.status" /></template>
          <template #empty><EmptyState :title="t('maintenance.empty')" data-testid="empty" /></template>
          <template #footer>
            <div v-if="nextCursor" class="flex justify-center p-3"><Button variant="outline" size="sm" data-testid="more" @click="load(true)">{{ t('maintenance.loadMore') }}</Button></div>
          </template>
        </DataTable>
      </div>

      <Card v-if="selected" data-testid="detail">
        <CardHeader><CardTitle>{{ selected.request_number }} · {{ where(selected) }}</CardTitle></CardHeader>
        <CardContent class="flex flex-col gap-4">
          <div>
            <p class="m-0 text-sm">{{ selected.description }}</p>
            <p class="m-0 mt-1 text-sm text-muted-foreground">
              {{ t('maintenance.reported', { date: $date(selected.business_date) }) }}{{ selected.reporter_name ? ` ${t('maintenance.byReporter', { name: selected.reporter_name })}` : '' }}.
              <template v-if="selected.resolution_note"> {{ t('maintenance.noteLabel', { note: selected.resolution_note }) }}</template>
            </p>
          </div>

          <template v-if="can('maintenance.manage')">
            <div v-if="isOpen(selected)" class="flex flex-wrap items-end gap-3">
              <FormField class="w-36" :label="t('maintenance.priority')">
                <template #default="{ id }">
                  <NativeSelect :id="id" :model-value="selected.priority" name="detail_priority" @change="setPriority(($event.target as HTMLSelectElement).value)">
                    <option v-for="p in PRIORITIES" :key="p" :value="p">{{ priorityLabel(p) }}</option>
                  </NativeSelect>
                </template>
              </FormField>
              <FormField class="w-44" :label="t('maintenance.assignedTo')">
                <template #default="{ id }">
                  <Combobox :id="id" v-model="action.assignTo" name="assign_to" :options="[{ value: 0, label: `${t('maintenance.nobody')}` }, ...staff.map((s) => ({ value: s.id, label: `${s.full_name}` }))]" />
                </template>
              </FormField>
              <Button variant="outline" size="sm" :disabled="busy" data-testid="assign" @click="assign">{{ t('maintenance.saveAssignment') }}</Button>
            </div>

            <div class="flex flex-wrap gap-2">
              <Button v-if="selected.status === 'OPEN'" variant="outline" size="sm" :disabled="busy" data-testid="start" @click="start">{{ t('maintenance.start') }}</Button>
              <Button v-if="isOpen(selected)" size="sm" :disabled="busy" data-testid="resolve" @click="closing = 'resolve'">{{ t('maintenance.resolve') }}</Button>
              <Button v-if="isOpen(selected)" variant="outline" size="sm" class="text-destructive" :disabled="busy" data-testid="cancel" @click="closing = 'cancel'">{{ t('maintenance.cancelRequest') }}</Button>
              <Button v-if="selected.status === 'RESOLVED'" variant="outline" size="sm" :disabled="busy" data-testid="reopen" @click="reopen">{{ t('maintenance.reopen') }}</Button>
            </div>

            <form v-if="closing" class="flex flex-col gap-3 rounded-lg border border-border bg-muted/40 p-3" novalidate data-testid="close-form" @submit.prevent="close">
              <FormField :label="closing === 'cancel' ? t('maintenance.whyCancelled') : t('maintenance.whatDone')" :error="fieldError('note')">
                <template #default="{ id, invalid }"><Input :id="id" v-model="action.note" name="note" maxlength="1000" :aria-invalid="invalid" /></template>
              </FormField>
              <label v-if="selected.block?.status === 'ACTIVE'" class="flex items-center gap-2 text-sm">
                <input v-model="action.releaseBlock" name="release_block" type="checkbox" />
                <span>{{ t('maintenance.putBack', { type: selected.block.block_type, date: $date(selected.block.end_date) }) }}</span>
              </label>
              <div class="flex justify-end gap-2">
                <Button type="button" variant="outline" size="sm" @click="closing = null">{{ t('maintenance.back') }}</Button>
                <Button type="submit" size="sm" :disabled="busy || (closing === 'cancel' && !action.note.trim())" data-testid="close-submit">
                  {{ closing === 'cancel' ? t('maintenance.cancelTheRequest') : t('maintenance.markResolved') }}
                </Button>
              </div>
            </form>

            <form v-if="canBlock" class="flex flex-wrap items-end gap-3 rounded-lg border border-border p-3" novalidate data-testid="block-form" @submit.prevent="block">
              <FormField class="w-44" :label="t('maintenance.takeOut')">
                <template #default="{ id }">
                  <NativeSelect :id="id" v-model="action.blockType" name="block_type">
                    <option value="OOO">{{ t('maintenance.ooo') }}</option>
                    <option value="OOS">{{ t('maintenance.oos') }}</option>
                  </NativeSelect>
                </template>
              </FormField>
              <FormField class="w-40" :label="t('maintenance.until')" :error="fieldError('end_date')">
                <template #default="{ id, invalid }"><Input :id="id" v-model="action.blockEnd" name="block_end" type="date" :min="businessDate" :aria-invalid="invalid" /></template>
              </FormField>
              <Button type="submit" variant="outline" size="sm" :disabled="busy || !action.blockEnd" data-testid="block-room">{{ t('maintenance.blockRoom') }}</Button>
            </form>
          </template>
        </CardContent>
      </Card>
    </div>
  </template>
</template>
