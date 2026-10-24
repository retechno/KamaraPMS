<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { RouterLink, useRoute } from 'vue-router'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { BanquetEvent, BanquetEventType, BanquetReason, BanquetRoom, BanquetSession } from '@/api/types'
import BanquetConfirmDialog from '@/components/banquet/BanquetConfirmDialog.vue'
import BanquetEventDialog from '@/components/banquet/BanquetEventDialog.vue'
import BanquetSessionDialog from '@/components/banquet/BanquetSessionDialog.vue'
import EmptyState from '@/components/app/EmptyState.vue'
import ErrorNotice from '@/components/app/ErrorNotice.vue'
import FormField from '@/components/app/FormField.vue'
import PageHeader from '@/components/app/PageHeader.vue'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { NativeSelect } from '@/components/ui/native-select'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { confirm } from '@/composables/useConfirm'
import { toast } from '@/composables/useToast'
import { t } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import { formatDate, formatDateShort } from '@/utils/format'
import { formatHoldRange } from '@/utils/banquet'

/**
 * One event (docs/architecture/36-banquet.md 6.4): the header with what can be done to it, and the Sessions tab: each session with its room, hours, guests and place in the queue of options.
 * No money yet; the other tabs come with the packages and the bill.
 */
const route = useRoute()
const property = usePropertyStore()
const auth = useAuthStore()
const pid = computed(() => property.currentId)
const eventId = computed(() => Number(route.params.id))
const canManage = computed(() => auth.can('banquet.manage', pid.value))
const canConfirm = computed(() => auth.can('banquet.confirm', pid.value))

const event = ref<BanquetEvent | null>(null)
const rooms = ref<BanquetRoom[]>([])
const types = ref<BanquetEventType[]>([])
const reasons = ref<BanquetReason[]>([])
const error = ref<ApiError | null>(null)
const loaded = ref(false)
const tab = ref('sessions')

async function load(): Promise<void> {
  const propertyId = pid.value
  error.value = null
  if (propertyId === null || !eventId.value) {
    loaded.value = true
    return
  }
  try {
    const path = { params: { path: { propertyId } } }
    const [ev, ro, ty, re] = await Promise.all([
      api.GET('/api/v1/properties/{propertyId}/banquet/events/{id}', { params: { path: { propertyId, id: eventId.value } } }),
      api.GET('/api/v1/properties/{propertyId}/banquet/rooms', path),
      api.GET('/api/v1/properties/{propertyId}/banquet/event-types', path),
      api.GET('/api/v1/properties/{propertyId}/banquet/reasons', path),
    ])
    event.value = ev.data ?? null
    rooms.value = ro.data?.data ?? []
    types.value = ty.data?.data ?? []
    reasons.value = re.data?.data ?? []
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    loaded.value = true
  }
}
watch([pid, eventId], load, { immediate: true })

const open = computed(() => !!event.value && ['INQUIRY', 'TENTATIVE', 'DEFINITE'].includes(event.value.status))
const sessions = computed(() => event.value?.sessions ?? [])
const variant = (s: string): 'draft' | 'booked' | 'inhouse' | 'closed' | 'cancelled' | 'outline' =>
  ({ INQUIRY: 'draft', TENTATIVE: 'booked', DEFINITE: 'inhouse', COMPLETED: 'closed', CANCELLED: 'cancelled', LOST: 'cancelled' } as const)[s as 'INQUIRY'] ?? 'outline'
const canClose = computed(() => !!event.value && (event.value.status === 'DEFINITE' ? canConfirm.value : canManage.value))
const canTentative = computed(() => canManage.value && event.value?.status === 'INQUIRY' && sessions.value.some((s) => s.status === 'ACTIVE' && s.room_id))

function saved(ev: BanquetEvent): void {
  event.value = ev
}

// ---- dialogs
const editOpen = ref(false)
const confirmOpen = ref(false)
const sessionOpen = ref(false)
const editing = ref<BanquetSession | null>(null)
function addSession(): void {
  editing.value = null
  sessionOpen.value = true
}
function editSession(s: BanquetSession): void {
  editing.value = s
  sessionOpen.value = true
}
async function cancelSession(s: BanquetSession): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null || !event.value) return
  if (!(await confirm({ title: t('banquet.session.cancelTitle', { name: s.name }), description: t('banquet.session.cancelBody'), confirmLabel: t('banquet.session.cancelAction'), destructive: true }))) return
  try {
    const { data } = await api.DELETE('/api/v1/properties/{propertyId}/banquet/events/{id}/sessions/{sid}', { params: { path: { propertyId, id: event.value.id, sid: s.id } } })
    if (data) event.value = data
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  }
}

// ---- tentative: the option date
const tentOpen = ref(false)
const tentDate = ref('')
const tentError = ref<ApiError | null>(null)
async function makeTentative(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null || !event.value) return
  tentError.value = null
  try {
    const { data } = await api.POST('/api/v1/properties/{propertyId}/banquet/events/{id}/tentative', { params: { path: { propertyId, id: event.value.id } }, body: { option_until: tentDate.value } })
    if (data) event.value = data
    tentOpen.value = false
    toast.success(t('banquet.event.madeTentative'))
  } catch (e) {
    tentError.value = e instanceof ApiError ? e : null
  }
}

// ---- lost / cancelled: a reason is required
const closeOpen = ref(false)
const closeForm = reactive({ reason_id: 0, note: '' })
const closeError = ref<ApiError | null>(null)
const closeAs = ref<'lost' | 'cancel'>('lost')
function startClose(as: 'lost' | 'cancel'): void {
  closeAs.value = as
  closeForm.reason_id = 0
  closeForm.note = ''
  closeError.value = null
  closeOpen.value = true
}
async function doClose(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null || !event.value) return
  closeError.value = null
  const params = { path: { propertyId, id: event.value.id } }
  const body = { reason_id: closeForm.reason_id, reason_note: closeForm.note.trim() || null }
  try {
    const { data } = closeAs.value === 'lost'
      ? await api.POST('/api/v1/properties/{propertyId}/banquet/events/{id}/lost', { params, body })
      : await api.POST('/api/v1/properties/{propertyId}/banquet/events/{id}/cancel', { params, body })
    if (data) event.value = data
    closeOpen.value = false
  } catch (e) {
    closeError.value = e instanceof ApiError ? e : null
  }
}
const reasonOptions = computed(() => reasons.value.filter((r) => r.is_active && r.kind === (closeAs.value === 'lost' ? 'LOST' : 'CANCEL')))
const placeOf = (s: BanquetSession): string => (event.value?.status === 'TENTATIVE' && s.status === 'ACTIVE' ? (s.rank === 1 ? t('banquet.session.first') : t('banquet.session.place', { n: s.rank ?? 1 })) : '')
const range = (s: BanquetSession): string => (s.start_time ? `${s.start_time.slice(0, 5)}–${(s.end_time ?? '').slice(0, 5)}${s.ends_next_day ? ' +1' : ''}` : '–')
</script>

<template>
  <PageHeader :title="event ? `${event.number} · ${event.name}` : t('banquet.event.title')" :description="event ? event.booker_name : undefined">
    <template v-if="event" #actions>
      <Badge :variant="variant(event.status)" data-testid="event-status">{{ t(`banquet.status.${event.status}`) }}</Badge>
      <Badge v-if="event.option_overdue" variant="destructive" data-testid="option-overdue">{{ t('banquet.event.optionOverdue') }}</Badge>
      <Button v-if="canManage && open" type="button" variant="outline" data-testid="event-edit" @click="editOpen = true">{{ t('common.edit') }}</Button>
      <Button v-if="canTentative" type="button" variant="outline" data-testid="event-tentative" @click="tentOpen = true">{{ t('banquet.event.makeTentative') }}</Button>
      <Button v-if="canConfirm && event.status === 'TENTATIVE'" type="button" data-testid="event-confirm" @click="confirmOpen = true">{{ t('banquet.confirm.action') }}</Button>
      <Button v-if="canClose && event.status !== 'DEFINITE' && open" type="button" variant="outline" data-testid="event-lost" @click="startClose('lost')">{{ t('banquet.event.markLost') }}</Button>
      <Button v-if="canClose && event.status === 'DEFINITE'" type="button" variant="outline" data-testid="event-cancel" @click="startClose('cancel')">{{ t('banquet.event.cancel') }}</Button>
    </template>
  </PageHeader>
  <ErrorNotice v-if="error" :error="error" inline />
  <p v-if="pid === null" class="muted">{{ t('setup.selectProperty') }}</p>
  <EmptyState v-else-if="loaded && !event" :title="t('banquet.event.notFound')" :description="t('emptyState.banquetEvent')" action-label="" />
  <template v-else-if="event">
    <p v-if="event.status === 'TENTATIVE' && event.option_until" class="muted mb-3" data-testid="option-until">{{ t('banquet.event.optionUntil', { date: formatDate(event.option_until) }) }}</p>
    <p v-if="event.reason_name" class="muted mb-3" data-testid="event-reason">{{ t('banquet.event.reason') }}: {{ event.reason_name }}<template v-if="event.reason_note"> · {{ event.reason_note }}</template></p>
    <Tabs :model-value="tab" @update:model-value="tab = String($event)">
      <TabsList data-testid="event-tabs">
        <TabsTrigger value="sessions" data-testid="tab-sessions">{{ t('banquet.event.tabSessions') }}</TabsTrigger>
        <TabsTrigger value="details" data-testid="tab-details">{{ t('banquet.event.tabDetails') }}</TabsTrigger>
      </TabsList>
      <TabsContent value="sessions">
        <div class="mb-3 flex justify-end"><Button v-if="canManage && open" type="button" data-testid="session-add" @click="addSession">{{ t('banquet.session.new') }}</Button></div>
        <EmptyState v-if="!sessions.length" :title="t('banquet.session.empty')" :description="t('emptyState.banquetSessions')" />
        <ul v-else class="m-0 flex list-none flex-col gap-2 p-0" data-testid="session-list">
          <li v-for="s in sessions" :key="s.id" class="flex flex-wrap items-start justify-between gap-2 rounded-md border border-border p-3" :class="s.status === 'CANCELLED' ? 'opacity-60' : ''" :data-testid="`session-${s.id}`">
            <div class="min-w-0 flex-1 basis-60">
              <p class="m-0 font-medium">{{ s.name }}<Badge v-if="s.status === 'CANCELLED'" variant="cancelled" class="ml-2">{{ t('banquet.session.cancelled') }}</Badge><Badge v-if="s.room_taken" variant="destructive" class="ml-2">{{ t('banquet.tone.taken') }}</Badge></p>
              <p class="m-0 text-sm text-muted-foreground">
                {{ formatDateShort(s.session_date) }} · {{ range(s) }} · {{ s.room_name ?? t('banquet.session.noRoom') }}<template v-if="s.setup_style"> · {{ t(`banquet.style.${s.setup_style}`) }}</template> · {{ t('banquet.session.paxCount', { n: s.pax_estimated }) }}
              </p>
              <p v-if="s.hold_from" class="m-0 text-xs text-muted-foreground" data-testid="hold-range">{{ t('banquet.session.held', { range: formatHoldRange(s.hold_from, s.hold_to ?? s.hold_from) }) }}</p>
            </div>
            <Badge v-if="placeOf(s)" :variant="s.rank === 1 ? 'booked' : 'outline'" :data-testid="`rank-${s.id}`">{{ placeOf(s) }}</Badge>
            <div v-if="canManage && open && s.status === 'ACTIVE'" class="flex gap-2">
              <Button type="button" variant="outline" size="sm" :data-testid="`session-edit-${s.id}`" @click="editSession(s)">{{ event.status === 'TENTATIVE' ? t('banquet.session.move') : t('common.edit') }}</Button>
              <Button type="button" variant="outline" size="sm" :data-testid="`session-cancel-${s.id}`" @click="cancelSession(s)">{{ t('banquet.session.cancelAction') }}</Button>
            </div>
          </li>
        </ul>
      </TabsContent>
      <TabsContent value="details">
        <dl class="m-0 grid gap-x-6 gap-y-2 text-sm sm:grid-cols-2" data-testid="event-details">
          <div><dt class="text-muted-foreground">{{ t('banquet.event.type') }}</dt><dd class="m-0">{{ event.event_type_name ?? '–' }}</dd></div>
          <div><dt class="text-muted-foreground">{{ t('banquet.event.booker') }}</dt><dd class="m-0">{{ event.booker_name }}</dd></div>
          <div><dt class="text-muted-foreground">{{ t('banquet.event.contactName') }}</dt><dd class="m-0">{{ event.contact_name ?? '–' }} {{ event.contact_phone ?? '' }} {{ event.contact_email ?? '' }}</dd></div>
          <div><dt class="text-muted-foreground">{{ t('banquet.event.sales') }}</dt><dd class="m-0">{{ event.sales_name }}</dd></div>
          <div v-if="event.booking_group_id"><dt class="text-muted-foreground">{{ t('banquet.event.group') }}</dt><dd class="m-0"><RouterLink :to="`/groups/${event.booking_group_id}`" class="text-primary hover:underline">{{ event.booking_group_name }}</RouterLink></dd></div>
          <div class="sm:col-span-2"><dt class="text-muted-foreground">{{ t('banquet.event.notes') }}</dt><dd class="m-0">{{ event.notes ?? '–' }}</dd></div>
        </dl>
      </TabsContent>
    </Tabs>

    <BanquetEventDialog v-if="pid !== null" v-model:open="editOpen" :property-id="pid" :event="event" :types="types" @saved="saved" />
    <BanquetSessionDialog v-if="pid !== null" v-model:open="sessionOpen" :property-id="pid" :event="event" :rooms="rooms" :session="editing" @saved="saved" />
    <BanquetConfirmDialog v-if="pid !== null" v-model:open="confirmOpen" :property-id="pid" :event="event" @saved="saved" />

    <Dialog :open="tentOpen" @update:open="(v) => (tentOpen = v)">
      <DialogContent class="max-w-md p-5" data-testid="tentative-dialog" @interact-outside.prevent>
        <form novalidate class="flex flex-col gap-3" @submit.prevent="makeTentative">
          <DialogTitle>{{ t('banquet.event.makeTentative') }}</DialogTitle>
          <DialogDescription>{{ t('banquet.event.tentativeHint') }}</DialogDescription>
          <ErrorNotice v-if="tentError" :error="tentError" inline />
          <FormField :label="t('banquet.event.optionDate')" required :error="tentError?.fieldMessage('option_until')">
            <template #default="{ id }"><Input :id="id" v-model="tentDate" name="option_until" type="date" /></template>
          </FormField>
          <div class="flex justify-end gap-2">
            <Button type="button" variant="outline" @click="tentOpen = false">{{ t('common.cancel') }}</Button>
            <Button type="submit" data-testid="tentative-save">{{ t('banquet.event.makeTentative') }}</Button>
          </div>
        </form>
      </DialogContent>
    </Dialog>

    <Dialog :open="closeOpen" @update:open="(v) => (closeOpen = v)">
      <DialogContent class="max-w-md p-5" data-testid="close-dialog" @interact-outside.prevent>
        <form novalidate class="flex flex-col gap-3" @submit.prevent="doClose">
          <DialogTitle>{{ closeAs === 'lost' ? t('banquet.event.markLost') : t('banquet.event.cancel') }}</DialogTitle>
          <DialogDescription>{{ t('banquet.event.closeHint') }}</DialogDescription>
          <ErrorNotice v-if="closeError" :error="closeError" inline />
          <FormField :label="t('banquet.event.reason')" required :error="closeError?.fieldMessage('reason_id')">
            <template #default="{ id }">
              <NativeSelect :id="id" v-model.number="closeForm.reason_id" name="reason_id">
                <option :value="0">{{ t('banquet.event.choose') }}</option>
                <option v-for="r in reasonOptions" :key="r.id" :value="r.id">{{ r.name }}</option>
              </NativeSelect>
            </template>
          </FormField>
          <FormField :label="t('banquet.event.reasonNote')"><template #default="{ id }"><Input :id="id" v-model="closeForm.note" name="reason_note" maxlength="500" /></template></FormField>
          <div class="flex justify-end gap-2">
            <Button type="button" variant="outline" @click="closeOpen = false">{{ t('common.cancel') }}</Button>
            <Button type="submit" variant="destructive" data-testid="close-save">{{ closeAs === 'lost' ? t('banquet.event.markLost') : t('banquet.event.cancel') }}</Button>
          </div>
        </form>
      </DialogContent>
    </Dialog>
  </template>
</template>
