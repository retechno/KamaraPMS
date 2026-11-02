<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { RouterLink, useRoute } from 'vue-router'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { BanquetCancelResult, BanquetEvent, BanquetEventType, BanquetItem, BanquetLines, BanquetMoney, BanquetPackage, BanquetPolicyTemplate, BanquetReason, BanquetRoom, BanquetSession } from '@/api/types'
import BanquetCloseDialog from '@/components/banquet/BanquetCloseDialog.vue'
import BanquetBeoTab from '@/components/banquet/BanquetBeoTab.vue'
import BanquetDocsTab from '@/components/banquet/BanquetDocsTab.vue'
import BanquetConfirmDialog from '@/components/banquet/BanquetConfirmDialog.vue'
import BanquetDepositTab from '@/components/banquet/BanquetDepositTab.vue'
import BanquetFolioTab from '@/components/banquet/BanquetFolioTab.vue'
import BanquetLinesTab from '@/components/banquet/BanquetLinesTab.vue'
import BanquetEventDialog from '@/components/banquet/BanquetEventDialog.vue'
import BanquetSessionDialog from '@/components/banquet/BanquetSessionDialog.vue'
import { Card } from '@/components/ui/card'
import EmptyState from '@/components/app/EmptyState.vue'
import ErrorNotice from '@/components/app/ErrorNotice.vue'
import FormField from '@/components/app/FormField.vue'
import PageHeader from '@/components/app/PageHeader.vue'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogTitle } from '@/components/ui/dialog'
import { DateInput } from '@/components/ui/date-input'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { confirm } from '@/composables/useConfirm'
import { toast } from '@/composables/useToast'
import { t } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import { formatDate, formatDateShort, formatMoney } from '@/utils/format'
import { formatHoldRange } from '@/utils/banquet'
import { guardForm } from '@/utils/forms'

/**
 * One event (docs/architecture/36-banquet.md 6.4 to 6.7): the header with what can be done to it, a strip with the money (contract, deposits, bill), and the tabs: Sessions (room, hours, guests and place in the queue of
 * options), Items & packages (what is sold, the estimate), Deposit & policy (the schedule, the down payment, the cancellation policy) and Folio (the bill, the posting of the sessions, the transfer to a group).
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
const tab = ref(['sessions', 'lines', 'deposit', 'folio', 'beo', 'docs', 'details'].includes(String(route.query.tab)) ? String(route.query.tab) : 'sessions')
const money = ref<BanquetMoney | null>(null)
const lines = ref<BanquetLines | null>(null)
const items = ref<BanquetItem[]>([])
const packages = ref<BanquetPackage[]>([])
const templates = ref<BanquetPolicyTemplate[]>([])
const defaultMode = ref<'EXCLUSIVE' | 'INCLUSIVE'>('EXCLUSIVE')
const businessDate = ref<string | null>(null)
const canSeeMoney = computed(() => canManage.value || canConfirm.value || auth.can('banquet.read', pid.value))

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
    await loadMoney()
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    loaded.value = true
  }
}

/** The money of the event: the lines with their estimate, the folio, the deposits and the policy, with the masters the forms choose from. */
async function loadMoney(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null || !event.value || !canSeeMoney.value) return
  const id = event.value.id
  const [mo, li, it, pk, st, td, tp] = await Promise.all([
    api.GET('/api/v1/properties/{propertyId}/banquet/events/{id}/money', { params: { path: { propertyId, id } } }),
    api.GET('/api/v1/properties/{propertyId}/banquet/events/{id}/lines', { params: { path: { propertyId, id } } }),
    api.GET('/api/v1/properties/{propertyId}/banquet/items', { params: { path: { propertyId } } }),
    api.GET('/api/v1/properties/{propertyId}/banquet/packages', { params: { path: { propertyId } } }),
    api.GET('/api/v1/properties/{propertyId}/banquet/settings', { params: { path: { propertyId } } }),
    api.GET('/api/v1/properties/{propertyId}/banquet/today', { params: { path: { propertyId } } }),
    api.GET('/api/v1/properties/{propertyId}/banquet/policy-templates', { params: { path: { propertyId }, query: { active: true } } }),
  ])
  money.value = mo.data ?? null
  lines.value = li.data ?? null
  items.value = it.data?.data ?? []
  packages.value = pk.data?.data ?? []
  defaultMode.value = st.data?.default_price_mode ?? 'EXCLUSIVE'
  businessDate.value = td.data?.date ?? null
  templates.value = tp.data?.data ?? []
}
/** After a change of the lines, the deposits or the folio: the event may have changed too (the estimate is on the page). */
async function reloadMoney(): Promise<void> {
  error.value = null
  try {
    const propertyId = pid.value
    if (propertyId !== null && event.value) {
      const { data } = await api.GET('/api/v1/properties/{propertyId}/banquet/events/{id}', { params: { path: { propertyId, id: event.value.id } } })
      if (data) event.value = data
    }
    await loadMoney()
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  }
}
watch([pid, eventId], load, { immediate: true })

const open = computed(() => !!event.value && ['INQUIRY', 'TENTATIVE', 'DEFINITE'].includes(event.value.status))
const sessions = computed(() => event.value?.sessions ?? [])
const variant = (s: string): 'draft' | 'booked' | 'inhouse' | 'closed' | 'cancelled' | 'outline' =>
  ({ INQUIRY: 'draft', TENTATIVE: 'booked', DEFINITE: 'inhouse', COMPLETED: 'closed', CANCELLED: 'cancelled', LOST: 'cancelled' } as const)[s as 'INQUIRY'] ?? 'outline'
const canComplete = computed(() => canManage.value && event.value?.status === 'DEFINITE')
async function completeEvent(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null || !event.value) return
  if (!(await confirm({ title: t('banquet.complete.title'), description: t('banquet.complete.body'), confirmLabel: t('banquet.complete.action') }))) return
  error.value = null
  try {
    const { data } = await api.POST('/api/v1/properties/{propertyId}/banquet/events/{id}/complete', { params: { path: { propertyId, id: event.value.id } } })
    if (data) event.value = data
    toast.success(t('banquet.complete.done'))
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  }
}
const canClose = computed(() => !!event.value && (event.value.status === 'DEFINITE' ? canConfirm.value : canManage.value))
const canTentative = computed(() => canManage.value && event.value?.status === 'INQUIRY' && sessions.value.some((s) => s.status === 'ACTIVE' && s.room_id))

function saved(ev: BanquetEvent): void {
  event.value = ev
  void loadMoney().catch((e) => { error.value = e instanceof ApiError ? e : null })
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

// ---- lost / cancelled: a reason is required, the fee of the policy is shown
const closeOpen = ref(false)
const closeAs = ref<'lost' | 'cancel'>('lost')
const closeResult = ref<BanquetCancelResult | null>(null)
function startClose(as: 'lost' | 'cancel'): void {
  closeAs.value = as
  closeOpen.value = true
}
function closed(result: BanquetCancelResult): void {
  event.value = result.event
  closeResult.value = result
  void loadMoney().catch((e) => { error.value = e instanceof ApiError ? e : null })
}
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
      <Button v-if="canComplete" type="button" variant="outline" data-testid="event-complete" @click="completeEvent">{{ t('banquet.complete.action') }}</Button>
      <Button v-if="canClose && event.status === 'DEFINITE'" type="button" variant="outline" data-testid="event-cancel" @click="startClose('cancel')">{{ t('banquet.event.cancel') }}</Button>
    </template>
  </PageHeader>
  <ErrorNotice v-if="error" :error="error" inline />
  <p v-if="pid === null" class="muted">{{ t('setup.selectProperty') }}</p>
  <EmptyState v-else-if="loaded && !event" :title="t('banquet.event.notFound')" :description="t('emptyState.banquetEvent')" action-label="" />
  <template v-else-if="event">
    <p v-if="event.status === 'TENTATIVE' && event.option_until" class="muted mb-3" data-testid="option-until">{{ t('banquet.event.optionUntil', { date: formatDate(event.option_until) }) }}</p>
    <p v-if="event.reason_name" class="muted mb-3" data-testid="event-reason">{{ t('banquet.event.reason') }}: {{ event.reason_name }}<template v-if="event.reason_note"> · {{ event.reason_note }}</template></p>
    <Card v-if="money && canSeeMoney" class="mb-3 grid grid-cols-2 gap-3 p-3 text-sm sm:grid-cols-4" data-testid="money-strip">
      <div><div class="text-muted-foreground">{{ t('banquet.money.contract') }}</div><div class="font-medium" data-testid="money-contract">{{ formatMoney(money.estimate.total) }}</div></div>
      <div><div class="text-muted-foreground">{{ t('banquet.deposit.held') }}</div><div class="font-medium" data-testid="money-deposit">{{ formatMoney(money.deposit_received) }}</div></div>
      <div><div class="text-muted-foreground">{{ t('banquet.folio.balance') }}</div><div class="font-medium" data-testid="money-balance">{{ money.folio ? formatMoney(money.folio.balance) : '–' }}</div></div>
      <div v-if="money.overdue"><Badge variant="destructive" data-testid="money-overdue">{{ t('banquet.deposit.overdue') }}</Badge></div>
    </Card>
    <Card v-if="closeResult" class="mb-3 p-3 text-sm" data-testid="close-result">
      <p class="m-0">{{ t('banquet.cancel.posted', { amount: formatMoney(closeResult.posted) }) }}</p>
      <p v-if="Number(closeResult.refundable) > 0" class="m-0 mt-1 text-muted-foreground" data-testid="close-result-refund">
        {{ t('banquet.cancel.refundable', { amount: formatMoney(closeResult.refundable) }) }}
        <RouterLink v-if="closeResult.folio_id" :to="`/folios/${closeResult.folio_id}`" class="text-primary hover:underline">{{ t('banquet.cancel.openFolio') }}</RouterLink>
      </p>
    </Card>
    <Tabs :model-value="tab" @update:model-value="tab = String($event)">
      <TabsList data-testid="event-tabs">
        <TabsTrigger value="sessions" data-testid="tab-sessions">{{ t('banquet.event.tabSessions') }}</TabsTrigger>
        <TabsTrigger v-if="canSeeMoney" value="lines" data-testid="tab-lines">{{ t('banquet.event.tabLines') }}</TabsTrigger>
        <TabsTrigger v-if="canSeeMoney" value="deposit" data-testid="tab-deposit">{{ t('banquet.event.tabDeposit') }}</TabsTrigger>
        <TabsTrigger v-if="canSeeMoney" value="folio" data-testid="tab-folio">{{ t('banquet.event.tabFolio') }}</TabsTrigger>
        <TabsTrigger value="beo" data-testid="tab-beo">{{ t('banquet.event.tabBeo') }}</TabsTrigger>
        <TabsTrigger value="docs" data-testid="tab-docs">{{ t('banquet.event.tabDocs') }}</TabsTrigger>
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
      <TabsContent v-if="canSeeMoney && money && lines" value="lines">
        <BanquetLinesTab :property-id="pid!" :event="event" :lines="lines" :money="money" :items="items" :packages="packages" :default-mode="defaultMode" :can-manage="canManage" @changed="reloadMoney" />
      </TabsContent>
      <TabsContent v-if="canSeeMoney && money" value="deposit">
        <BanquetDepositTab :property-id="pid!" :event="event" :money="money" :can-manage="canManage" :can-confirm="canConfirm" @changed="reloadMoney" />
      </TabsContent>
      <TabsContent v-if="canSeeMoney && money" value="folio">
        <BanquetFolioTab :property-id="pid!" :event="event" :money="money" :business-date="businessDate" :can-manage="canManage" @changed="reloadMoney" />
      </TabsContent>
      <TabsContent value="beo">
        <BanquetBeoTab :property-id="pid!" :event="event" :can-manage="canManage" @changed="reloadMoney" />
      </TabsContent>
      <TabsContent value="docs">
        <BanquetDocsTab :property-id="pid!" :event="event" :can-manage="canManage" :can-confirm="canConfirm" @changed="reloadMoney" />
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
    <BanquetConfirmDialog v-if="pid !== null" v-model:open="confirmOpen" :property-id="pid" :event="event" :templates="templates" @saved="saved" />

    <Dialog :open="tentOpen" @update:open="(v) => (tentOpen = v)">
      <DialogContent class="max-w-md p-5" data-testid="tentative-dialog" @interact-outside.prevent>
        <form novalidate class="flex flex-col gap-3" @submit.prevent="guardForm($event) && makeTentative()">
          <DialogTitle>{{ t('banquet.event.makeTentative') }}</DialogTitle>
          <DialogDescription>{{ t('banquet.event.tentativeHint') }}</DialogDescription>
          <ErrorNotice v-if="tentError" :error="tentError" inline />
          <FormField :label="t('banquet.event.optionDate')" required :error="tentError?.fieldMessage('option_until')">
            <template #default="{ id }"><DateInput :id="id" v-model="tentDate" name="option_until" /></template>
          </FormField>
          <div class="flex justify-end gap-2">
            <Button type="button" variant="outline" @click="tentOpen = false">{{ t('common.cancel') }}</Button>
            <Button type="submit" data-testid="tentative-save">{{ t('banquet.event.makeTentative') }}</Button>
          </div>
        </form>
      </DialogContent>
    </Dialog>

    <BanquetCloseDialog v-if="pid !== null" v-model:open="closeOpen" :property-id="pid" :event="event" :as="closeAs" :reasons="reasons" @done="closed" />
  </template>
</template>
