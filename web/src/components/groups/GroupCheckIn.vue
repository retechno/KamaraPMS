<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { RouterLink } from 'vue-router'
import { api } from '@/api/client'
import { ApiError } from '@/api/problem'
import type { Arrival, GroupCheckInResult, GroupRouting } from '@/api/types'
import AutoAssignDialog from '@/components/AutoAssignDialog.vue'
import DataTable, { type Column } from '@/components/app/DataTable.vue'
import ErrorNotice from '@/components/app/ErrorNotice.vue'
import GroupDepositDialog from '@/components/groups/GroupDepositDialog.vue'
import ReadinessBadge from '@/components/ReadinessBadge.vue'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { NativeSelect } from '@/components/ui/native-select'
import { i18n, t } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { usePropertyStore } from '@/stores/property'
import { DEPOSIT_METHODS, type DepositMethod } from '@/utils/checkin'
import { formatMoney } from '@/utils/format'
import { GROUP_CHECKIN_CHUNK, chunk, depositProposal, identityHint, rowSelectable, takesDeposit } from '@/utils/groupCheckin'
import { newIdempotencyKey } from '@/utils/reservations'
import { lineOfArrival } from '@/utils/roomAssign'

/**
 * Check in a group (docs/architecture/30-group-folio.md part E): the rooms of the group that arrive today, ticked and checked in together. The server checks each room in its own transaction and answers
 * for each: checked in, already checked in, or failed with the reason. Nothing is rolled back for the others. A room that failed stays ticked with its reason; sending it again, in a new request, finds
 * what the first one did: the same stay for a room that went through, and a deposit that was taken is not taken twice (the server keys both by the room, not by the request). The rooms go 20 at a time,
 * one request after the other, so the screen can show how far it is.
 */
const props = defineProps<{ groupId: number; active: boolean; reload?: number }>()
const emit = defineEmits<{ changed: [] }>()

const auth = useAuthStore()
const property = usePropertyStore()
const pid = computed(() => property.currentId)
const can = (permission: string) => auth.can(permission, pid.value)
const decimals = computed(() => property.current?.currency_decimals ?? 0)
const incidental = computed(() => property.current?.checkin_incidental_deposit ?? '0')

const visible = computed(() => props.active && can('frontdesk.checkin') && can('reservation.read'))
const canDeposit = computed(() => can('payment.post'))
const canOverride = computed(() => can('frontdesk.checkin_unready_room'))

const rows = ref<Arrival[]>([])
const routing = ref<GroupRouting | null>(null)
const loaded = ref(false)
const error = ref<ApiError | null>(null)
const notice = ref('')
const selected = ref<number[]>([])
const deposits = reactive<Record<number, string>>({})
const method = ref<DepositMethod>('BANK_TRANSFER')
const overrideNotReady = ref(false)
const overrideReason = ref('')
const running = ref(false)
const progress = ref({ done: 0, total: 0 })
const outcome = reactive<Record<number, GroupCheckInResult['rows'][number]>>({})
const run = ref({ checkedIn: 0, already: 0, failed: 0, deposits: 0 })
const autoOpen = ref(false)
const depositOpen = ref(false)

let latest = 0
async function load(): Promise<void> {
  if (!visible.value || pid.value === null) return
  const mine = ++latest
  error.value = null
  const propertyId = pid.value
  try {
    const [a, r] = await Promise.all([
      api.GET('/api/v1/properties/{propertyId}/arrivals', { params: { path: { propertyId }, query: { booking_group_id: props.groupId, with_estimate: true } } }),
      api.GET('/api/v1/properties/{propertyId}/groups/{id}/routing', { params: { path: { propertyId, id: props.groupId } } }),
    ])
    if (mine !== latest) return
    rows.value = a.data?.data ?? []
    routing.value = r.data ?? null
    const lines = new Set(rows.value.map((x) => x.reservation_room_id))
    selected.value = selected.value.filter((id) => lines.has(id))
    for (const a2 of rows.value) {
      if (deposits[a2.reservation_room_id] === undefined) deposits[a2.reservation_room_id] = depositProposal(a2, routing.value, { incidental: incidental.value, decimals: decimals.value }).amount ?? ''
    }
  } catch (e) {
    if (mine === latest) error.value = e instanceof ApiError ? e : null
  } finally {
    if (mine === latest) loaded.value = true
  }
}
watch([() => props.groupId, pid, () => props.reload, visible], () => void load(), { immediate: true })

const proposals = computed(() => new Map(rows.value.map((a) => [a.reservation_room_id, depositProposal(a, routing.value, { incidental: incidental.value, decimals: decimals.value })])))
const depositColumn = computed(() => canDeposit.value && [...proposals.value.values()].some((p) => p.amount !== null))
const selectable = (a: Arrival): boolean => rowSelectable(a, overrideNotReady.value && canOverride.value)
const readyLines = computed(() => rows.value.filter(selectable).map((a) => a.reservation_room_id))
const isSelected = (a: Arrival): boolean => selected.value.includes(a.reservation_room_id)
const autoLines = computed(() => rows.value.filter((a) => can('reservation.update') && !a.room_id).map(lineOfArrival))
const chosen = computed(() => rows.value.filter((a) => isSelected(a) && selectable(a)))
const needsReason = computed(() => overrideNotReady.value && chosen.value.some((a) => a.readiness.blockers.includes('ROOM_NOT_READY')) && !overrideReason.value.trim())
const canRun = computed(() => chosen.value.length > 0 && !running.value && !needsReason.value)

function toggle(a: Arrival, on: boolean): void {
  const id = a.reservation_room_id
  selected.value = on ? [...new Set([...selected.value, id])] : selected.value.filter((x) => x !== id)
}
const selectReady = (): void => {
  selected.value = [...readyLines.value]
}

function rowBody(a: Arrival) {
  const notReady = a.readiness.blockers.includes('ROOM_NOT_READY')
  const dep = deposits[a.reservation_room_id] ?? ''
  return {
    line_id: a.reservation_room_id,
    version: a.reservation_version,
    room_id: a.room_id ?? undefined,
    guest_id: a.guest_id ?? 0,
    adult_count: a.adult_count,
    child_count: a.child_count,
    override_room_not_ready: notReady && overrideNotReady.value,
    override_reason: notReady && overrideNotReady.value ? overrideReason.value.trim() : undefined,
    deposit: canDeposit.value && takesDeposit(dep, decimals.value) ? { amount: dep.trim(), payment_method: method.value } : undefined,
  }
}

async function checkIn(): Promise<void> {
  const propertyId = pid.value
  if (propertyId === null || !canRun.value) return
  running.value = true
  error.value = null
  notice.value = ''
  run.value = { checkedIn: 0, already: 0, failed: 0, deposits: 0 }
  const parts = chunk([...chosen.value].sort((x, y) => x.reservation_room_id - y.reservation_room_id), GROUP_CHECKIN_CHUNK)
  progress.value = { done: 0, total: chosen.value.length }
  try {
    for (const part of parts) {
      // a request of its own for each part, with a key of its own: the rooms are keyed by the room on the server, so a repeat of a part (or of the whole) is safe
      const { data } = await api.POST('/api/v1/properties/{propertyId}/groups/{id}/check-ins', {
        params: { path: { propertyId, id: props.groupId }, header: { 'Idempotency-Key': newIdempotencyKey() } },
        body: { rows: part.map(rowBody) },
      })
      for (const r of data?.rows ?? []) {
        outcome[r.line_id] = r
        if (r.status === 'FAILED') run.value.failed++
        else if (r.status === 'CHECKED_IN') run.value.checkedIn++
        else run.value.already++
        if (r.deposit?.status === 'RECORDED') run.value.deposits++
      }
      progress.value.done += part.length
      selected.value = selected.value.filter((id) => outcome[id]?.status === 'FAILED' || !outcome[id])
    }
    notice.value = t('groupCheckIn.summary', { n: run.value.checkedIn, already: run.value.already, failed: run.value.failed })
  } catch (e) {
    error.value = e instanceof ApiError ? e : null
  } finally {
    running.value = false
    await load()
    emit('changed')
  }
}

// The reason of a row that failed, in the language of the page when there is a text for its code (the answer of the server is in English)
function reason(code?: string, message?: string): string {
  const key = `errors.${code}`
  return i18n.global.locale.value !== 'en' && code && i18n.global.te(key, i18n.global.locale.value) ? t(key as never) : (message ?? '')
}

const failed = computed(() => Object.values(outcome).filter((r) => r.status === 'FAILED'))
const inRoom = computed(() => Object.values(outcome).filter((r) => r.status === 'CHECKED_IN' && r.stay))

const columns = computed<Column<Arrival>[]>(() => [
  { key: 'select', label: '' },
  { key: 'room_number', label: t('groupCheckIn.room'), card: 'primary' as const },
  { key: 'guest_name', label: t('groupCheckIn.guest'), card: 'secondary' as const },
  { key: 'room_type_code', label: t('groupCheckIn.type') },
  { key: 'party', label: t('groupCheckIn.party') },
  { key: 'state', label: t('groupCheckIn.readiness'), card: 'badge' as const },
  ...(depositColumn.value ? [{ key: 'deposit', label: t('groupCheckIn.deposit'), align: 'right' as const }] : []),
])

const instructionText = computed(() => {
  const list = routing.value?.instructions ?? []
  if (!list.length) return t('groupCheckIn.payNone')
  const what = list.map((i) => (i.scope === 'ALL' ? t('groupCheckIn.payAll') : i.scope === 'ROOM' ? t('groupCheckIn.payRoom') : (i.charge_code ?? ''))).filter(Boolean).join(', ')
  return t('groupCheckIn.payRoutes', { what })
})

async function depositDone(message: string): Promise<void> {
  depositOpen.value = false
  notice.value = message
  await load()
  emit('changed')
}
</script>

<template>
  <Card v-if="visible" class="mt-4" data-testid="group-checkin">
    <CardHeader>
      <CardTitle>{{ t('groupCheckIn.title') }}</CardTitle>
    </CardHeader>
    <CardContent>
      <p class="m-0 mb-3 text-sm text-muted-foreground">{{ t('groupCheckIn.hint') }}</p>
      <ErrorNotice v-if="error" :error="error" inline data-testid="group-checkin-error" />
      <p v-if="notice" :class="run.failed > 0 ? 'alert warning' : 'notice'" role="status" data-testid="group-checkin-notice">{{ notice }}</p>

      <div class="mb-4 rounded-md border border-border p-3 text-sm" data-testid="group-payment">
        <p class="m-0 font-medium">{{ t('groupCheckIn.payTitle') }}</p>
        <p class="m-0 mt-1 text-muted-foreground" data-testid="group-pay-routing">{{ instructionText }}</p>
        <p v-if="routing?.master" class="m-0 mt-1" data-testid="group-pay-master">
          <RouterLink :to="`/folios/${routing.master.id}`" class="text-primary hover:underline">{{ routing.master.folio_number }}</RouterLink>
          · {{ routing.master.status === 'OPEN' ? t('groupMaster.statusOpen') : t('groupMaster.statusClosed') }}
          <template v-if="routing.master.balance !== ''"> · {{ t('groupMaster.balance') }} <span class="tabular-nums">{{ formatMoney(routing.master.balance) }}</span></template>
        </p>
        <p v-else class="m-0 mt-1 text-muted-foreground" data-testid="group-pay-nomaster">{{ t('groupCheckIn.payNoMaster') }}</p>
        <div v-if="routing?.master?.status === 'OPEN' && canDeposit" class="mt-2">
          <Button type="button" variant="outline" size="sm" data-testid="group-deposit-open" @click="depositOpen = true">{{ t('groupCheckIn.depositButton') }}</Button>
        </div>
      </div>

      <p v-if="loaded && !rows.length" class="m-0 text-sm text-muted-foreground" data-testid="group-checkin-empty">{{ t('groupCheckIn.empty') }}</p>
      <template v-else>
        <div class="mb-3 flex flex-wrap items-center gap-2" data-testid="group-checkin-bar">
          <Button type="button" variant="outline" size="sm" :disabled="!readyLines.length || running" data-testid="select-ready" @click="selectReady">{{ t('groupCheckIn.selectReady', { n: readyLines.length }) }}</Button>
          <Button v-if="selected.length" type="button" variant="ghost" size="sm" data-testid="select-none" @click="selected = []">{{ t('groupCheckIn.clear') }}</Button>
          <Button type="button" variant="ghost" size="sm" :disabled="running" data-testid="group-checkin-reload" @click="load">{{ t('groupCheckIn.reload') }}</Button>
          <Button v-if="autoLines.length" type="button" variant="outline" size="sm" :disabled="running" data-testid="group-auto-assign" @click="autoOpen = true">{{ t('groupCheckIn.autoAssign', { n: autoLines.length }) }}</Button>
          <label v-if="depositColumn" class="ml-auto inline-flex items-center gap-2 text-sm">
            <span>{{ t('groupCheckIn.depositMethod') }}</span>
            <NativeSelect v-model="method" class="w-auto" name="group_checkin_method" data-testid="group-checkin-method">
              <option v-for="m in DEPOSIT_METHODS" :key="m" :value="m">{{ t(`cashier.${m}` as never) }}</option>
            </NativeSelect>
          </label>
        </div>
        <div v-if="canOverride && rows.some((a) => a.readiness.blockers.includes('ROOM_NOT_READY'))" class="mb-3 flex flex-wrap items-center gap-3 text-sm" data-testid="group-checkin-override">
          <label class="inline-flex items-center gap-2"><input v-model="overrideNotReady" type="checkbox" name="override_not_ready" class="size-4 accent-primary" /> {{ t('groupCheckIn.overrideNotReady') }}</label>
          <Input v-if="overrideNotReady" v-model="overrideReason" name="override_reason" class="max-w-xs" maxlength="500" :placeholder="t('groupCheckIn.overrideReason')" />
        </div>

        <DataTable :columns="columns" :rows="rows" row-key="reservation_room_id" :loading="!loaded" cards>
          <template #cell-select="{ row: a }">
            <input type="checkbox" class="size-4 accent-primary" :checked="isSelected(a)" :disabled="!selectable(a) || running" :aria-label="t('groupCheckIn.pick')" :name="`pick_${a.reservation_room_id}`" :data-testid="`pick-${a.reservation_room_id}`" @change="toggle(a, ($event.target as HTMLInputElement).checked)" />
          </template>
          <template #cell-room_number="{ row: a }">
            <span :data-testid="`room-${a.reservation_room_id}`">{{ a.room_number || t('groupCheckIn.noRoom') }}</span>
            <span class="ml-2 text-xs text-muted-foreground">{{ a.confirmation_number }}</span>
          </template>
          <template #cell-guest_name="{ row: a }">
            <span :class="a.guest_is_placeholder ? 'text-muted-foreground' : ''">{{ a.guest_name || t('groupCheckIn.noGuest') }}</span>
            <Badge v-if="a.guest_is_placeholder" variant="outline" class="ml-2" :data-testid="`placeholder-${a.reservation_room_id}`">{{ t('rooming.placeholder') }}</Badge>
            <Badge v-if="identityHint(a)" variant="warning" class="ml-2" :title="t(`checkin.hint.${identityHint(a)}` as never)" :data-testid="`identity-${a.reservation_room_id}`">{{ t(`groupCheckIn.identity.${identityHint(a)}` as never) }}</Badge>
          </template>
          <template #cell-party="{ row: a }">{{ a.adult_count }}+{{ a.child_count }}</template>
          <template #cell-state="{ row: a }">
            <ReadinessBadge :readiness="a.readiness" />
            <p v-if="outcome[a.reservation_room_id]?.status === 'FAILED'" class="m-0 mt-1 text-xs text-destructive" :data-testid="`failed-${a.reservation_room_id}`">
              {{ outcome[a.reservation_room_id]?.code }}: {{ reason(outcome[a.reservation_room_id]?.code, outcome[a.reservation_room_id]?.message) }}
            </p>
            <p v-if="outcome[a.reservation_room_id]?.deposit && outcome[a.reservation_room_id]?.deposit?.status !== 'FAILED'" class="m-0 mt-1 text-xs text-muted-foreground" :data-testid="`deposit-note-${a.reservation_room_id}`">
              {{ t('groupCheckIn.depositNote', { number: outcome[a.reservation_room_id]?.deposit?.payment_number ?? '', amount: formatMoney(outcome[a.reservation_room_id]?.deposit?.amount ?? '0') }) }}
            </p>
          </template>
          <template #cell-deposit="{ row: a }">
            <span v-if="proposals.get(a.reservation_room_id)?.amount === null" class="text-muted-foreground" :title="t(`groupCheckIn.basis.${proposals.get(a.reservation_room_id)?.basis}` as never)" :data-testid="`deposit-none-${a.reservation_room_id}`">—</span>
            <Input v-else v-model="deposits[a.reservation_room_id]" class="w-28 text-right tabular-nums" inputmode="decimal" autocomplete="off" :name="`deposit_${a.reservation_room_id}`" :aria-label="t('groupCheckIn.deposit')" :data-testid="`deposit-${a.reservation_room_id}`" />
          </template>
        </DataTable>

        <div class="mt-3 flex flex-wrap items-center justify-between gap-2">
          <p class="m-0 text-sm text-muted-foreground" data-testid="group-checkin-progress">
            <template v-if="running">{{ t('groupCheckIn.progress', { done: progress.done, total: progress.total }) }}</template>
            <template v-else-if="needsReason">{{ t('groupCheckIn.reasonMissing') }}</template>
            <template v-else>{{ t('groupCheckIn.chosen', { n: chosen.length }) }}</template>
          </p>
          <Button type="button" :disabled="!canRun" data-testid="group-checkin-run" @click="checkIn">{{ t('groupCheckIn.run', { n: chosen.length }) }}</Button>
        </div>

      </template>
      <ul v-if="inRoom.length" class="m-0 mt-3 list-disc pl-5 text-sm" data-testid="group-checkin-done">
        <li v-for="r in inRoom" :key="r.line_id">
          {{ t('groupCheckIn.checkedIn', { room: r.room_number ?? '' }) }}
          <RouterLink v-if="r.stay" :to="`/stays/${r.stay.id}`" class="text-primary hover:underline">{{ r.stay.stay_number }}</RouterLink>
          <template v-if="r.deposit && r.deposit.status !== 'FAILED'"> · {{ t('groupCheckIn.depositNote', { number: r.deposit.payment_number ?? '', amount: formatMoney(r.deposit.amount ?? '0') }) }}<template v-if="r.deposit.status === 'REPLAYED'"> ({{ t('groupCheckIn.depositReplayed') }})</template></template>
        </li>
      </ul>
      <p v-if="failed.length" class="m-0 mt-2 text-sm text-muted-foreground" data-testid="group-checkin-failed">{{ t('groupCheckIn.failedHint', { n: failed.length }) }}</p>
    </CardContent>
  </Card>
  <AutoAssignDialog v-if="visible" v-model:open="autoOpen" :lines="autoLines" @changed="load" />
  <GroupDepositDialog v-if="depositOpen && routing?.master" :master="routing.master" @done="depositDone" @close="depositOpen = false" />
</template>
