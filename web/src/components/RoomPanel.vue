<script setup lang="ts">
import { Check, X } from 'lucide-vue-next'
import { computed, reactive, ref, watch } from 'vue'
import { RouterLink, useRouter } from 'vue-router'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { HousekeepingBoardRoom, HousekeepingStaffMember, HousekeepingStatus, RoomSummary } from '@/api/types'
import ErrorNotice from '@/components/app/ErrorNotice.vue'
import FormField from '@/components/app/FormField.vue'
import StatusBadge from '@/components/app/StatusBadge.vue'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { NativeSelect } from '@/components/ui/native-select'
import { Sheet, SheetClose, SheetContent, SheetDescription, SheetTitle } from '@/components/ui/sheet'
import { Skeleton } from '@/components/ui/skeleton'
import { toast } from '@/composables/useToast'
import { t } from '@/i18n'
import { labelOf } from '@/i18n/labels'
import { cn } from '@/lib/utils'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import { formatBusinessDate } from '@/utils/dates'
import { formatBalance, formatDateTime, formatMoney } from '@/utils/format'
import { statusText } from '@/utils/status'

/**
 * The panel of a room: who is in it, what the folio holds, who comes next, what to do now, and what happened today. It is one sheet for the whole application (room status, the quick finder, every
 * room number that is a RoomLink), fed by one call (`/rooms/{id}/summary`). A part the role may not read is not in the answer and not on the screen. Every action is a link to the page that already does
 * it (the folio, the stay) or a form of the module that owns it: the panel decides nothing.
 *
 * `seed` is a row of the room board the caller already has: it is drawn at once, so the sheet is never empty while the summary loads.
 */
const props = defineProps<{ roomId: number; number?: string; seed?: HousekeepingBoardRoom | null }>()
const open = defineModel<boolean>('open', { default: true })
const emit = defineEmits<{ changed: []; close: [] }>()

const auth = useAuthStore()
const property = usePropertyStore()
const router = useRouter()

const summary = ref<RoomSummary | null>(null)
const loading = ref(false)
const busy = ref(false)
const error = ref<ApiError | null>(null)
let seq = 0

// the form that sends housekeeping to this room (below)
const TASK_KINDS = ['DEEP', 'OTHER', 'CHECKOUT', 'STAYOVER', 'ARRIVAL', 'DIRTY'] as const
const hk = reactive({ open: false, task_type: 'DIRTY' as (typeof TASK_KINDS)[number], priority: 'NORMAL' as 'NORMAL' | 'HIGH', assigned_to: 0, notes: '' })
const staff = ref<HousekeepingStaffMember[]>([])
const hkError = ref<ApiError | null>(null)

const pid = computed(() => property.currentId)
const can = (permission: string): boolean => auth.can(permission, pid.value)

async function load(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null) return
  const mine = ++seq
  loading.value = true
  try {
    const { data } = await api.GET('/api/v1/properties/{propertyId}/rooms/{roomId}/summary', { params: { path: { propertyId, roomId: props.roomId } } })
    if (mine !== seq) return
    summary.value = data && 'room' in data ? data : null
    error.value = null
  } catch (e) {
    if (mine !== seq) return
    summary.value = null
    error.value = e instanceof ApiError ? e : null
  } finally {
    if (mine === seq) loading.value = false
  }
}

watch(() => [props.roomId, pid.value], () => {
  summary.value = null
  error.value = null
  hk.open = false
  void load()
}, { immediate: true })

const room = computed<HousekeepingBoardRoom | null>(() => summary.value?.room ?? props.seed ?? null)
const number = computed(() => room.value?.room_number ?? props.number ?? '')
const stay = computed(() => summary.value?.stay ?? null)
const folio = computed(() => summary.value?.folio ?? null)
const arrival = computed(() => summary.value?.next_arrival ?? null)
const access = computed(() => summary.value?.access ?? null)
const history = computed(() => summary.value?.history ?? [])

const owes = computed(() => !!folio.value && Number(folio.value.balance) > 0)
const guestFolio = computed(() => folio.value?.folios.find((f) => f.folio_type === 'GUEST') ?? folio.value?.folios[0] ?? null)
const hidden = computed(() => !!access.value && (!access.value.stay || (!!stay.value && !access.value.folio)))

const initials = (name: string): string => name.split(/\s+/).filter(Boolean).slice(0, 2).map((w) => w[0]!.toUpperCase()).join('')
const stayLine = computed(() => {
  const s = stay.value
  if (!s) return ''
  return [
    `${formatBusinessDate(s.arrival_date)} → ${formatBusinessDate(s.departure_date)}`,
    t('roomPanel.nightOf', { n: s.night, total: s.nights }),
    t('roomPanel.adults', { n: s.adult_count }, s.adult_count) + (s.child_count ? `, ${t('roomPanel.children', { n: s.child_count }, s.child_count)}` : ''),
    s.source ?? '',
  ].filter(Boolean).join(' · ')
})
const arrivalLine = computed(() => {
  const a = arrival.value
  return a ? t('roomPanel.nextArrivalLine', { arrival: formatBusinessDate(a.arrival_date), nights: a.nights }, a.nights) : ''
})

// ---- cleaning: the next statuses are buttons for the role that may change them, and plain chips for the others
const canChange = computed(() => can('housekeeping.update'))
const mayMark = (to: HousekeepingStatus): boolean => canChange.value && (to !== 'INSPECTED' || can('housekeeping.inspect'))
async function mark(to: HousekeepingStatus): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null || busy.value) return
  busy.value = true
  error.value = null
  try {
    await api.POST('/api/v1/properties/{propertyId}/rooms/{id}/housekeeping', { params: { path: { propertyId, id: props.roomId } }, body: { status: to } })
    await load()
    emit('changed')
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    busy.value = false
  }
}

// ---- quick actions: links to the flows that exist (at most six)
interface Action {
  id: string
  label: string
  to?: string
  key?: string
  primary?: boolean
}
const actions = computed<Action[]>(() => {
  const out: Action[] = []
  const s = stay.value
  const f = guestFolio.value
  if (s && f && f.status === 'OPEN') {
    if (can('payment.post')) out.push({ id: 'pay', label: t('roomPanel.pay'), to: `/folios/${f.id}?tab=payment`, key: 'p', primary: true })
    if (can('folio.post_charge')) out.push({ id: 'charge', label: t('roomPanel.charge'), to: `/folios/${f.id}?tab=charge`, key: 't' })
  }
  if (s && can('reservation.update')) out.push({ id: 'extend', label: t('roomPanel.extend'), to: `/stays/${s.id}?action=extend` })
  if (s && can('frontdesk.room_move')) out.push({ id: 'move', label: t('roomPanel.move'), to: `/stays/${s.id}?action=move` })
  if (can('housekeeping.assign')) out.push({ id: 'housekeeping', label: t('roomPanel.housekeeping') })
  if (s && can('frontdesk.checkout')) out.push({ id: 'checkout', label: t('roomPanel.checkOut'), to: `/stays/${s.id}?action=checkout`, key: 'o' })
  return out
})

function leave(): void {
  open.value = false
  emit('close')
}
function onKey(event: KeyboardEvent): void {
  if (event.ctrlKey || event.metaKey || event.altKey || hk.open) return
  const el = event.target
  if (el instanceof HTMLElement && (['INPUT', 'TEXTAREA', 'SELECT'].includes(el.tagName) || el.isContentEditable)) return
  const action = actions.value.find((a) => a.to && a.key === event.key.toLowerCase())
  if (!action?.to) return
  event.preventDefault()
  leave()
  void router.push(action.to)
}

// ---- send housekeeping: a manual cleaning task for this room, through the endpoint the cleaning list uses
const hkField = (field: string): string | undefined => hkError.value?.fieldMessage(field)

async function startHk(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null) return
  Object.assign(hk, { open: true, task_type: room.value?.occupancy === 'OCCUPIED' ? 'STAYOVER' : 'DIRTY', priority: 'NORMAL', assigned_to: 0, notes: '' })
  hkError.value = null
  try {
    const { data } = await api.GET('/api/v1/properties/{propertyId}/housekeeping/staff', { params: { path: { propertyId } } })
    staff.value = data?.data ?? []
  } catch {
    staff.value = []
  }
}
async function sendHk(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null || busy.value) return
  busy.value = true
  hkError.value = null
  try {
    await api.POST('/api/v1/properties/{propertyId}/housekeeping/tasks', {
      params: { path: { propertyId } },
      body: { room_id: props.roomId, task_type: hk.task_type, priority: hk.priority, assigned_to: hk.assigned_to || null, notes: hk.notes || undefined },
    })
    hk.open = false
    toast.success(t('roomPanel.taskSent'))
    await load()
    emit('changed')
  } catch (e) {
    hkError.value = e instanceof ApiError ? e : null
  } finally {
    busy.value = false
  }
}

// ---- today
const timeOf = (at: string): string => formatDateTime(at).slice(-5)
function describe(item: RoomSummary['history'][number]): string {
  if (item.kind === 'CLEANING') {
    const text = t('roomPanel.cleaningChanged', { from: statusText(item.from), to: statusText(item.to) })
    return item.notes ? `${text} · ${item.notes}` : text
  }
  return [labelOf('auditAction', item.action ?? ''), item.label, item.user].filter(Boolean).join(' · ')
}

const updatedAt = (iso: string): string => formatDateTime(iso)
</script>

<template>
  <Sheet v-model:open="open" @update:open="(v: boolean) => !v && emit('close')">
    <SheetContent
      class="flex w-[35rem] max-w-full flex-col overflow-hidden p-0 max-sm:w-screen max-sm:max-w-none"
      data-testid="room-sheet"
      @keydown="onKey"
    >
      <header class="flex flex-col gap-3 border-b border-border px-6 pb-4 pt-5">
        <div class="flex items-start justify-between gap-3">
          <div class="flex min-w-0 items-center gap-3.5">
            <SheetTitle class="m-0 text-4xl font-extrabold leading-none tracking-tight" data-testid="rp-number">
              <span aria-hidden="true">{{ number }}</span>
              <span class="sr-only">{{ t('roomStatus.details', { number }) }}</span>
            </SheetTitle>
            <SheetDescription v-if="room" as="div" class="m-0 flex min-w-0 flex-col gap-0.5 text-sm text-muted-foreground">
              <strong class="truncate text-base text-foreground">{{ room.room_type_code }} · {{ room.room_type_name }}</strong>
              <span v-if="room.floor || room.building || room.bed_type_code" class="truncate" data-testid="rp-place">{{ [room.floor ? t('roomPanel.floorOf', { floor: room.floor }) : '', room.building, room.bed_type_code].filter(Boolean).join(' · ') }}</span>
            </SheetDescription>
            <SheetDescription v-else class="sr-only">{{ t('roomPanel.loading') }}</SheetDescription>
          </div>
          <SheetClose as-child>
            <button type="button" class="grid size-11 shrink-0 cursor-pointer place-items-center rounded-lg border-0 bg-muted text-foreground hover:bg-accent" :aria-label="t('roomPanel.close')" data-testid="rp-close" @click="emit('close')">
              <X class="size-[18px]" aria-hidden="true" />
            </button>
          </SheetClose>
        </div>
        <div v-if="room" class="flex flex-wrap gap-2" data-testid="rp-chips">
          <StatusBadge domain="occupancy" :status="room.occupancy" />
          <StatusBadge domain="housekeeping" :status="room.status" />
          <StatusBadge v-if="room.block" domain="block" :status="room.block.type" :label="t(`roomStatus.${room.block.type}` as never)" />
          <Badge v-if="room.dnd" variant="outline">{{ t('roomStatus.dnd') }}</Badge>
          <Badge v-if="room.make_up_requested" variant="outline">{{ t('roomStatus.makeUp') }}</Badge>
          <Badge v-if="room.priority === 'HIGH'" variant="outline">{{ t('roomStatus.highPriority') }}</Badge>
          <Badge v-if="owes && folio" variant="warning" class="tabular-nums" data-testid="rp-owes-chip">{{ t('roomPanel.owesChip', { amount: formatMoney(folio.balance) }) }}</Badge>
        </div>
        <Skeleton v-else class="h-6 w-48" />
      </header>

      <div class="flex min-h-0 flex-1 flex-col gap-5 overflow-y-auto px-6 py-4">
        <ErrorNotice v-if="error" :error="error" inline data-testid="rp-error" />
        <div v-if="error && !summary" class="flex justify-end"><Button variant="outline" size="sm" data-testid="rp-reload" @click="load">{{ t('roomPanel.reload') }}</Button></div>

        <!-- who is in the room -->
        <section v-if="stay" class="flex items-center gap-3.5" data-testid="rp-stay">
          <span class="grid size-12 shrink-0 place-items-center rounded-full bg-accent font-extrabold text-accent-foreground" aria-hidden="true">{{ initials(stay.guest.name) }}</span>
          <div class="flex min-w-0 flex-1 flex-col gap-0.5">
            <RouterLink :to="`/guests/${stay.guest.id}`" class="truncate text-[17px] font-bold text-foreground no-underline hover:underline" data-testid="rp-guest" @click="leave">{{ stay.guest.name }}</RouterLink>
            <span class="text-sm text-muted-foreground" data-testid="rp-stay-line">{{ stayLine }}</span>
          </div>
        </section>
        <p v-if="stay?.special_request" class="m-0 rounded-lg bg-muted px-3 py-2.5 text-sm leading-snug" data-testid="rp-request"><strong>{{ t('roomPanel.request') }}:</strong> {{ stay.special_request }}</p>

        <!-- nobody in the room: who comes next -->
        <section v-else-if="access?.stay && !stay" class="flex flex-col gap-2" data-testid="rp-empty">
          <p class="m-0 text-sm text-muted-foreground">{{ t('roomPanel.empty') }}</p>
          <div v-if="arrival" class="flex flex-col gap-1 rounded-lg border border-border p-3" data-testid="rp-next-arrival">
            <h3 class="m-0 text-sm font-bold">{{ t('roomPanel.nextArrival') }}</h3>
            <strong class="text-base">{{ arrival.guest_name || arrival.confirmation_number }}</strong>
            <span class="text-sm text-muted-foreground">{{ arrivalLine }} · {{ arrival.room_type_code }}</span>
            <RouterLink :to="`/reservations/${arrival.reservation_id}`" class="text-sm font-semibold" data-testid="rp-reservation" @click="leave">{{ arrival.confirmation_number }} · {{ t('roomPanel.openReservation') }}</RouterLink>
          </div>
        </section>

        <!-- the folio -->
        <section v-if="stay && folio" class="flex flex-col gap-2" data-testid="rp-folio">
          <div class="flex items-baseline justify-between gap-3">
            <h3 class="m-0 text-[15px] font-bold">{{ folio.folios.length === 1 ? t('roomPanel.folioTitle', { number: folio.folios[0]?.folio_number }) : t('roomPanel.folioMany') }}</h3>
            <RouterLink v-if="guestFolio" :to="`/folios/${guestFolio.id}`" class="text-sm font-semibold" data-testid="rp-open-folio" @click="leave">{{ t('roomPanel.openFolio') }}</RouterLink>
          </div>
          <div class="grid grid-cols-3 gap-2.5">
            <div class="rounded-lg border border-border p-3"><div class="text-xs text-muted-foreground">{{ t('roomPanel.charges') }}</div><strong class="text-[17px] tabular-nums" data-testid="rp-charges">{{ formatMoney(folio.charges) }}</strong></div>
            <div class="rounded-lg border border-border p-3"><div class="text-xs text-muted-foreground">{{ t('roomPanel.paid') }}</div><strong class="text-[17px] tabular-nums" data-testid="rp-paid">{{ formatMoney(folio.paid) }}</strong></div>
            <div :class="cn('rounded-lg border p-3', owes ? 'border-warning bg-warning/15' : 'border-border')" :data-owes="owes">
              <div :class="cn('text-xs', owes ? 'font-medium text-warning-text' : 'text-muted-foreground')">{{ owes ? t('roomPanel.toPay') : t('roomPanel.balance') }}</div>
              <strong :class="cn('text-[17px] tabular-nums', owes && 'text-warning-text')" data-testid="rp-balance">{{ formatBalance(folio.balance) }}</strong>
            </div>
          </div>
        </section>
        <p v-else-if="stay && access?.folio" class="m-0 text-sm text-muted-foreground" data-testid="rp-no-folio">{{ t('roomPanel.noFolio') }}</p>

        <!-- cleaning: where the room can go from here -->
        <section v-if="room && room.allowed_next.length" class="flex flex-col gap-2" data-testid="rp-cleaning">
          <h3 class="m-0 text-[15px] font-bold">{{ t('roomStatus.canBe') }}</h3>
          <div class="flex flex-wrap gap-2">
            <template v-for="s in room.allowed_next" :key="s">
              <Button v-if="mayMark(s)" variant="outline" class="min-h-11" :disabled="busy" :data-testid="`rp-mark-${s}`" @click="mark(s)">
                <Check aria-hidden="true" />{{ t('roomPanel.markAs', { status: statusText(s) }) }}
              </Button>
              <StatusBadge v-else domain="housekeeping" :status="s" />
            </template>
          </div>
        </section>

        <!-- quick actions -->
        <section v-if="actions.length" class="flex flex-col gap-2" data-testid="rp-actions">
          <h3 class="m-0 text-[15px] font-bold">{{ t('roomPanel.actions') }}</h3>
          <div class="grid grid-cols-3 gap-2 max-sm:grid-cols-2">
            <template v-for="a in actions" :key="a.id">
              <Button v-if="a.to" as-child :variant="a.primary ? 'default' : 'outline'" class="min-h-14 whitespace-normal text-center font-semibold">
                <RouterLink :to="a.to" :data-testid="`rp-action-${a.id}`" @click="leave">{{ a.label }}</RouterLink>
              </Button>
              <Button v-else variant="outline" class="min-h-14 whitespace-normal text-center font-semibold" :data-testid="`rp-action-${a.id}`" @click="startHk">{{ a.label }}</Button>
            </template>
          </div>
          <form v-if="hk.open" novalidate class="mt-2 flex flex-col gap-3 rounded-lg border border-primary/50 p-3" data-testid="rp-hk-form" @submit.prevent="sendHk">
            <h4 class="m-0 text-sm font-bold">{{ t('roomPanel.hkTitle', { number }) }}</h4>
            <ErrorNotice :error="hkError" />
            <div class="grid grid-cols-2 gap-3 max-sm:grid-cols-1">
              <FormField :label="t('cleaning.kind')" :error="hkField('task_type')">
                <template #default="{ id }">
                  <NativeSelect :id="id" v-model="hk.task_type" name="task_type">
                    <option v-for="k in TASK_KINDS" :key="k" :value="k">{{ t(`cleaning.type${k}` as never) }}</option>
                  </NativeSelect>
                </template>
              </FormField>
              <FormField :label="t('cleaning.priority')" :error="hkField('priority')">
                <template #default="{ id }">
                  <NativeSelect :id="id" v-model="hk.priority" name="priority">
                    <option value="NORMAL">{{ t('cleaning.normal') }}</option>
                    <option value="HIGH">{{ t('cleaning.high') }}</option>
                  </NativeSelect>
                </template>
              </FormField>
              <FormField :label="t('cleaning.assignTo')" :error="hkField('assigned_to')">
                <template #default="{ id }">
                  <NativeSelect :id="id" v-model.number="hk.assigned_to" name="assigned_to">
                    <option :value="0">{{ t('cleaning.nobodyYet') }}</option>
                    <option v-for="s in staff" :key="s.id" :value="s.id">{{ s.full_name }}</option>
                  </NativeSelect>
                </template>
              </FormField>
              <FormField :label="t('cleaning.note')" :error="hkField('notes')">
                <template #default="{ id }"><Input :id="id" v-model="hk.notes" name="notes" maxlength="500" /></template>
              </FormField>
            </div>
            <div class="flex justify-end gap-2">
              <Button type="button" variant="outline" @click="hk.open = false">{{ t('common.cancel') }}</Button>
              <Button type="submit" :disabled="busy" data-testid="rp-hk-send">{{ t('roomPanel.hkSend') }}</Button>
            </div>
          </form>
        </section>

        <p v-if="hidden" class="m-0 text-xs text-muted-foreground" data-testid="rp-hidden">{{ t('roomPanel.hiddenForRole') }}</p>

        <!-- the details the room board has always shown -->
        <dl v-if="room" class="m-0 divide-y divide-border text-sm">
          <div class="flex justify-between gap-3 py-2"><dt class="font-medium">{{ t('roomStatus.updated') }}</dt><dd class="m-0">{{ updatedAt(room.status_updated_at) }}</dd></div>
          <div v-if="room.block" class="py-2" data-testid="sheet-block">{{ t('roomStatus.blockedUntil', { date: formatBusinessDate(room.block.end_date) }) }}</div>
          <div v-if="room.flag_note" class="py-2"><dt class="font-medium">{{ t('roomStatus.note') }}</dt><dd class="m-0 mt-1 text-muted-foreground">{{ room.flag_note }}</dd></div>
        </dl>
        <Button as-child variant="outline" class="self-start">
          <RouterLink to="/housekeeping" data-testid="sheet-housekeeping" @click="leave">{{ t('roomStatus.openHousekeeping') }}</RouterLink>
        </Button>

        <!-- today -->
        <section v-if="summary" class="flex flex-col gap-2.5" data-testid="rp-history">
          <h3 class="m-0 text-[15px] font-bold">{{ t('roomPanel.history') }}</h3>
          <p v-if="!history.length" class="m-0 text-sm text-muted-foreground" data-testid="rp-history-empty">{{ t('roomPanel.historyEmpty') }}</p>
          <ol v-else class="m-0 flex list-none flex-col gap-2.5 p-0 text-sm">
            <li v-for="(h, i) in history" :key="i" class="flex gap-3" :data-kind="h.kind">
              <span class="w-12 shrink-0 tabular-nums text-muted-foreground">{{ timeOf(h.at) }}</span>
              <span>{{ describe(h) }}</span>
            </li>
          </ol>
        </section>
        <div v-else-if="loading" class="flex flex-col gap-2" data-testid="rp-loading"><Skeleton class="h-12 w-full" /><Skeleton class="h-20 w-full" /></div>
      </div>

      <footer class="flex flex-wrap gap-x-4 gap-y-1 border-t border-border px-6 py-3 text-xs text-muted-foreground">
        <template v-for="a in actions.filter((x) => x.key)" :key="a.id">
          <span class="max-sm:hidden pointer-coarse:hidden" data-testid="rp-key-hint"><kbd class="rounded border border-border px-1.5 font-[inherit] uppercase">{{ a.key }}</kbd> {{ a.id === 'pay' ? t('roomPanel.keyPay') : a.id === 'charge' ? t('roomPanel.keyCharge') : t('roomPanel.keyCheckOut') }}</span>
        </template>
        <span class="ml-auto">{{ t('roomPanel.footerHint') }}</span>
      </footer>
    </SheetContent>
  </Sheet>
</template>
